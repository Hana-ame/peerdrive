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
	"net"
	"os"
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
	Nonce   string     `json:"nonce,omitempty"` // fwd-challenge：一次性质询（forward.go）
	Hmac    string     `json:"hmac,omitempty"`  // fwd-auth：HMAC-SHA256(key, nonce)
	Port    int        `json:"port,omitempty"`  // fwd-open：客户端声明的目标端口
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

	// adminUp 管理面二进制上传收集槽（单槽，admin.go）：浏览器发 admin 帧
	// binary=true 声明后，泵内把后续二进制帧路由到这里（写临时文件），收齐
	// 后触发 serveAdminUploadComplete（multipart 内部转发）。与 pendingUpload
	// （文件索引上传）互斥独立：同一连接同时最多一个上传收集者。
	adminUp *adminUploadState

	// forward 转发隧道（单槽，forward.go）：同一连接同时一条活跃转发流。
	// fwdHandshake 是握手等待状态（fwd-open 发出 → ok/err 到达前占位）。
	fwd   *fwdStream
	fwdHs *fwdHandshake
	fwdCh chan fwdChunk // fwd 块 → 连接级 worker 写隧道（有界背压，同 binCh）
}

// fwdChunk 一块待写入隧道的转发数据（归属随块携带——隧道可能已换/已关，
// worker 写已关 out 报错即丢弃，符合「转发是尽力而为的流」语义）。
type fwdChunk struct {
	fw   *fwdStream
	data []byte
}

// binaryChunk 一个待落盘的上传分片（路由已在消息泵确定，worker 只做 IO）。
// up 为 fileIndex 上传（inbound.go uploadWorker 写 UploadSession）；
// au 为 admin 管理面上传（admin.go 写临时文件，multipart 转发前收集）。
type binaryChunk struct {
	up     *uploadState
	au     *adminUploadState // admin 上传分片（与 up 互斥，一帧只归一类）
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
		fwdCh:   make(chan fwdChunk, 16),
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
			case "admin":
				// 管理面 verb（admin.go）：仅本地 WS 会话（浏览器）使用，
				// 内部转发 gin engine 复用全部 HTTP controller。WebRTC 连接
				// 收到 admin 帧在 serveAdmin 内被拒绝（ID!="local"）。
				// 同步执行：binary 上传声明的 adminUp 占槽必须在泵内完成，
				// 否则后续二进制帧先到泵时 adminUp 仍为空 → 数据块丢失
				// （发现背景：初版 go 异步，上传分片全部丢失）。
				s.serveAdmin(c, st, msg.Data)
			case "fwd-open":
				go s.serveForwardOpen(c, st, r)
			case "fwd-auth":
				go s.serveForwardAuth(c, st, r)
			case "fwd-data":
				// 转发数据头（forward.go）：声明「下一个二进制块归转发隧道」。
				// 头-块连续约束与文件传输一致（SendFrame 原子发送），泵内按帧序
				// 处理故无竞态；非法（无隧道/已关）时静默丢弃并清 pending。
				st.mu.Lock()
				fw := st.fwd
				if fw != nil && !fw.closed {
					fw.pending = true
				}
				st.mu.Unlock()
			case "fwd-close":
				go s.serveForwardClose(c, st, r)
			case "fwd-challenge", "fwd-ok", "fwd-err":
				// 客户端侧握手响应（OpenForward 等待中）——与文件拉取响应同槽路由
				s.routeForwardResponse(st, r)
			default:
				s.routeResponse(st, r)
			}
			return
		}
		// 二进制数据块：路由决策在泵内（廉价、保持与文本帧的顺序一致性），
		// 落盘 IO（WriteAt/Complete）交给连接级 worker（H5）
		st.mu.Lock()
		fw := st.fwd
		if fw != nil && fw.pending && !fw.closed {
			// 转发块：投递到 fwdCh（有界背压，worker 写隧道；连接关闭放行）
			fw.pending = false
			select {
			case st.fwdCh <- fwdChunk{fw: fw, data: msg.Data}:
			case <-st.binDone:
			}
			st.mu.Unlock()
			return
		}
		up := st.pendingUpload
		if up != nil {
			up.got += int64(len(msg.Data))
			if up.got >= up.size {
				st.pendingUpload = nil
			}
		}
		// 管理面上传收集（admin.go）：pendingUpload 之后的第二优先级。
		// 块投递到 binCh（复用 H5 worker，写盘移出泵）；收齐（got>=size）
		// 触发内部 multipart 转发。
		au := st.adminUp
		if au != nil {
			au.got += int64(len(msg.Data))
			// 防御：声明 size 与实际不符 / 对端多发 → 中止并清理
			abort := au.got > au.size || time.Since(au.created) > adminUploadTimeout
			last := false
			if abort {
				st.adminUp = nil
				au.aborted = true
				last = true // 空块也投递：worker 收到 aborted 即清理回 err
			} else if au.got >= au.size {
				st.adminUp = nil
				last = true
			}
			select {
			case st.binCh <- binaryChunk{au: au, data: msg.Data, last: last}:
			case <-st.binDone:
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
			var fwdOut net.Conn
			st.mu.Lock()
			// 管理面上传收集（admin.go）：连接关闭 → 中止并清理临时文件
			if au := st.adminUp; au != nil {
				st.adminUp = nil
				os.Remove(au.path)
				au.f.Close()
			}
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
			// forward：隧道随之死亡——关 out 释放读侧（forwardPump 退出），
			// 调用方（OpenForward 返回的 net.Conn）读侧随即 EOF
			if st.fwd != nil {
				fwdOut = st.fwd.out
			}
			st.mu.Unlock()
			// H5：通知上传 worker 退出（未消费的分片直接丢弃——连接已死，
			// 会话残留由 file_index 的 10 分钟 reap 清理）
			close(st.binDone)
			if fwdOut != nil {
				fwdOut.Close()
			}
		}
		log.LogInfo("peerjs: connection closed from %s", c.ID())
	})
}
