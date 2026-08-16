package transport

// conn.go：一条连接的共享核心（两角色共用的机制，禁止复制）。
//
// 角色划分——inbound/outbound 是「帧角色」，不是连接方向。WebRTC 连接全双工
// 对称，同一条 Session 同时承载两角色（可一边 serve 对端的 req，一边收集自己
// 发起请求的响应），因此：
//   - inbound.go：入站角色 = 应答对端发来的 verb（req/create/upload/list/info/delete/sync）
//   - outbound.go：出站角色 = 本端发起 verb（req）并收集响应（meta/data/done/err）
//   - conn.go：两角色共享的连接级机制只此一份——帧类型、reqId 状态机、二进制块
//     路由、流控。拆角色时若复制这些会直接破坏协议一致性（见 bindConn 注释）。
//
// 帧协议（DataChannel 上 JSON 文本帧 + 二进制块，go↔go 与 go↔web 共用）：
//
//	请求: {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<optional>"}
//	响应: {"type":"meta","hash","total","reqId"}          文件信息
//	      {"type":"data","hash","offset","size","reqId"}  + 紧随 size 字节原始数据
//	      {"type":"done","hash","offset","size","reqId"}  传输完成
//	      {"type":"err","msg","reqId"}                    失败
//
// 关键约束（协议正确性依赖，勿破坏）：
//  1. data 头必须是文本帧、数据块必须是二进制帧（pion dc.Send 发二进制，
//     SendText 发文本——发反了对端会把 JSON 头当数据块吞掉）
//  2. data 头与数据块必须连续（Connection.SendFrame 原子发送），接收端按
//     「连接级 expect」状态机把二进制块挂到最近的 data 头所属请求上
//  3. reqId 路由：浏览器端可以不传 reqId（向后兼容），Go 端始终携带

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"

	"peerdrive/internal/log"
)

// dcReq 文件拉取请求帧（出站角色发起，入站角色应答）。
type dcReq struct {
	Type   string `json:"type"`
	Hash   string `json:"hash"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	ReqID  string `json:"reqId,omitempty"`
}

// dcResp 通用响应帧：拉取响应（meta/data/done/err）与文件索引 verb 响应
// （created/uploaded/ack/list-resp/info-resp/deleted/sync-resp）共用。
type dcResp struct {
	Type    string     `json:"type"`
	Hash    string     `json:"hash,omitempty"`
	Total   int64      `json:"total,omitempty"`
	Offset  int64      `json:"offset"`
	Size    int64      `json:"size,omitempty"`
	Msg     string     `json:"msg,omitempty"`
	ReqID   string     `json:"reqId,omitempty"`
	Path    string     `json:"path,omitempty"`
	Name    string     `json:"name,omitempty"`
	Seq     int64      `json:"seq,omitempty"`
	Files   []FileInfo `json:"files"`
	LastSeq int64      `json:"lastSeq,omitempty"`
}

// connState 记录一条连接上的请求状态机与响应路由。
// 状态归属标注（同一条连接双工并发复用，平铺共享不拆两份）：
//   - fetches/expect：出站角色（outbound.go 的 requestFile/routeResponse）
//   - pendingUpload/binCh/binDone：入站角色（inbound.go 的 serveUploadBegin/uploadWorker）
type connState struct {
	mu            sync.Mutex
	expect        *fetchState            // 当前期待二进制数据块的下载请求
	fetches       map[string]*fetchState // reqId → 下载请求
	pendingUpload *uploadState           // 当前接收中的流式上传（同一连接同时只有一个）

	// H5 修复：二进制数据块（上传分片）投递到连接级 worker（binCh/binDone），
	// WriteAt/Complete（fsync + 全文件 hashFile）移出 pion 消息泵——之前 8GB
	// 上传完成的瞬间，这条连接上的所有其他帧全部冻结到 Complete 结束
	// （头-of-line 阻塞，慢磁盘直接卡死整条连接）。路由决策（归谁）在消息泵
	// 内完成（廉价），worker 只做 IO，保序由单 worker 保证。
	binCh   chan binaryChunk
	binDone chan struct{}
}

// binaryChunk 一个待落盘的上传分片（路由已在消息泵确定，worker 只做 IO）。
type binaryChunk struct {
	up     *uploadState
	offset int64
	data   []byte
	last   bool // 本分片收齐 → 触发 Complete（位图全满才登记）
}

// uploadState 分片上传接收状态（连接级单流：一次 upload 请求 → 一个 data 块）。
// 多 source 并发 = 多连接并行 WriteAt 不同分片；同连接内分片串行（请求-响应配对）。
type uploadState struct {
	reqID   string
	offset  int64 // 本分片起点（chunk 对齐）
	size    int64 // 本分片长度
	got     int64
	sess    *UploadSession
	created time.Time // M6：创建时间——对端发 upload 头后不发数据块会永久占位
}

// fetchState 一次文件拉取的流式收集状态（出站角色）。
// 数据块不驻留 state（流式：投递到有界队列 q 由 fetchReader 消费），
// received 只做字节计数——用于 done 帧的完整性校验。
type fetchState struct {
	reqID    string
	size     int64         // 期待中的 data 块大小（上限校验见 maxPeerFetchSize）
	received int64         // 已投递队列的字节数
	q        chan []byte   // 数据块队列（有界 8，消息泵投递 / fetchReader 消费）
	done     chan struct{} // close → 对端 done 帧（传输完成；q 中剩余块仍可消费）
	errCh    chan error    // 错误（含连接关闭）
	closed   chan struct{} // 本地取消（reader.Close）：pump 停止投递，块丢弃
}

// bindConn 绑定连接的消息分派：解析 JSON 头按 reqId 路由，二进制块追加到 expect 状态。
// 坑：这条连接是「全双工复用」的——既服务对端的 req（serveFile），也接收
// 自己发起请求的响应（routeResponse）。两者靠帧类型 + reqId 区分：
//   - 文本帧 type 为 verb（req/create/upload/list/info/delete/sync）→ 入站角色应答
//   - 文本帧其它 type → 出站角色的响应，按 reqId 路由
//   - 二进制帧 → 数据块，路由决策（归 upload 还是 expect）在泵内完成，
//     落盘 IO 交给连接级 worker（H5，见 inbound.go 的 uploadWorker）
func (s *PeerJSService) bindConn(c Session) {
	s.mu.Lock()
	s.conns[c.ID()] = c
	s.mu.Unlock()
	st := &connState{
		fetches: make(map[string]*fetchState),
		binCh:   make(chan binaryChunk, 16),
		binDone: make(chan struct{}),
	}
	s.pendingMu.Lock()
	s.pending[c] = st
	s.pendingMu.Unlock()
	go s.uploadWorker(c, st)

	c.OnMessage(func(msg peerjs.Frame) {
		if msg.IsText {
			var r dcResp
			if err := json.Unmarshal(msg.Data, &r); err != nil || r.Type == "" {
				return
			}
			switch r.Type {
			case "req":
				// 对端请求本节点文件（download）
				req := dcReq{
					Type:   r.Type,
					Hash:   r.Hash,
					Offset: r.Offset,
					Size:   r.Size,
					ReqID:  r.ReqID,
				}
				go s.serveFile(c, req)
			case "create":
				// 对端登记外部文件（sha256 → 绝对路径）
				go s.serveCreate(c, r)
			case "upload":
				// 对端流式上传：开始接收（后续 data 帧写入 UploadSink）
				go s.serveUploadBegin(c, st, r)
			case "list":
				go s.serveList(c, r)
			case "info":
				go s.serveInfo(c, r)
			case "delete":
				go s.serveDelete(c, r)
			case "sync":
				go s.serveSync(c, r)
			default:
				s.routeResponse(st, r)
			}
			return
		}
		// 二进制数据块：路由决策在泵内（廉价、保持与文本帧的顺序一致性），
		// 落盘 IO（WriteAt/Complete）交给连接级 worker（H5）
		st.mu.Lock()
		up := st.pendingUpload
		if up != nil {
			up.got += int64(len(msg.Data))
			if up.got >= up.size {
				st.pendingUpload = nil
			}
		}
		f := st.expect
		if up == nil && f != nil {
			// 数据块投递到 fetch 队列（有界背压）；本地已取消（closed）则丢弃。
			// 投递成功才计数（取消后不计数，expect 由调用方清理）。
			select {
			case f.q <- msg.Data:
				f.received += int64(len(msg.Data))
				if f.received >= f.size {
					st.expect = nil
				}
			case <-f.closed:
			}
		}
		last := up != nil && up.got >= up.size
		st.mu.Unlock()
		if up != nil {
			select {
			case st.binCh <- binaryChunk{up: up, offset: up.offset, data: msg.Data, last: last}:
			case <-st.binDone: // 连接关闭：不再投递
				return
			}
		}
	})
	c.OnClose(func() {
		s.mu.Lock()
		if s.conns[c.ID()] == c {
			delete(s.conns, c.ID())
		}
		s.mu.Unlock()
		s.pendingMu.Lock()
		st := s.pending[c]
		delete(s.pending, c)
		s.pendingMu.Unlock()
		if st != nil {
			st.mu.Lock()
			for _, f := range st.fetches {
				// 连接关闭：通知 fetch reader 退出（errCh），并放行 pump 投递阻塞
				// （close(f.closed) 幂等检查——reader 可能已自行清理）
				select {
				case f.errCh <- fmt.Errorf("peerjs: connection closed"):
				default:
				}
				select {
				case <-f.closed:
				default:
					close(f.closed)
				}
			}
			st.mu.Unlock()
			// H5：通知上传 worker 退出（未消费的分片直接丢弃——连接已死，
			// 会话残留由 file_index 的 10 分钟 reap 清理）
			close(st.binDone)
		}
		log.LogInfo("peerjs: connection closed from %s", c.ID())
	})
}
