package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"

	"github.com/Hana-ame/go-peerjs"
	"peerdrive/internal/config"
	"peerdrive/internal/log"
)

// chunkSize DataChannel 单块传输大小（pion SCTP 单消息上限约 256KB，64KB 兼顾流控粒度）。
// 写缓冲流控已下沉到 peerjs.Connection.SendFrame（连接级全局回调），此处只定块大小。
const chunkSize = 64 * 1024

// PeerJSService 通过 PeerJS 公共信令 + pion/webrtc DataChannel 提供双向文件服务：
//   - 被动接收：浏览器或其它节点连接本节点请求 sha256 内容（服务端）
//   - 主动发起：连接其它节点拉取文件（客户端），节点间互联
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
type PeerJSService struct {
	cfg        *config.Config
	storageDir string
	peer       *peerjs.Peer
	id         string

	iceServers []webrtc.ICEServer

	mu     sync.Mutex
	conns  map[string]Session // key: remote peer id / "local"（WS 本地会话）
	closed chan struct{}

	pendingMu sync.Mutex
	pending   map[Session]*connState // 连接级请求/响应状态（Session 动态类型为指针，可作 map key）

	// peerMu 保护 peer/httpDisc/discovery 的读写（低危 2 修复）：
	// startLoop 写（重连时替换指针），Close 读——之前无锁，关停期 data race
	peerMu   sync.Mutex
	httpDisc *HTTPDiscovery
	discovery *MQTTDiscovery

	// connecting 去重：同一 peerID 可能被多个来源（配置 PEERS、MQTT/HTTP 发现、
	// 被动连接）触发 connectLoop——双连接浪费资源，靠 conns map 覆盖兜底但
	// 有重复握手开销（低危 3 修复）
	connectingMu sync.Mutex
	connecting   map[string]struct{}

	fileIndex *FileIndexService // sha256 → 绝对路径 索引（create/upload/list/info/sync）

	ctx    context.Context
	cancel context.CancelFunc
}

// connState 记录一条连接上的请求状态机与响应路由。
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

// fetchState 一次文件拉取的收集状态。
type fetchState struct {
	reqID  string
	size   int64 // 期待中的 data 块大小（上限校验见 maxPeerFetchSize）
	got    []byte
	done   chan []byte
	errCh  chan error
	closed bool
}

// NewPeerJSService 创建 PeerJS 文件服务。节点 ID 默认 <prefix>-<随机hex>，
// 常驻在线后其他 peer（浏览器或节点）可通过该 ID 直连。
func NewPeerJSService(cfg *config.Config, storageDir string) *PeerJSService {
	id := cfg.PeerJSID
	if id == "" {
		id = "peerdrive-" + randHex8()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &PeerJSService{
		cfg:         cfg,
		storageDir:  storageDir,
		id:          id,
		iceServers:  parseICEServers(cfg.WebRTCSTUNServer, cfg.WebRTCTURNServer),
		conns:       make(map[string]Session),
		pending:     make(map[Session]*connState),
		connecting:  make(map[string]struct{}),
		fileIndex:   NewFileIndexService(cfg.DownloadDir),
		closed:      make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// ID 返回本节点在信令网络中的 peer id。
func (s *PeerJSService) ID() string { return s.id }

// Start 注册到信令服务器；断线自动重连。异步，不阻塞调用方。
func (s *PeerJSService) Start() {
	go s.startLoop()
}

// Close 关闭信令连接并释放所有 WebRTC 连接。
func (s *PeerJSService) Close() {
	s.cancel()
	s.mu.Lock()
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	conns := make([]Session, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.conns = make(map[string]Session)
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	s.peerMu.Lock()
	peer := s.peer
	httpDisc := s.httpDisc
	s.peer = nil
	s.httpDisc = nil
	s.discovery = nil
	s.peerMu.Unlock()
	if peer != nil {
		peer.Close()
	}
	if httpDisc != nil {
		httpDisc.Stop()
	}
}

func (s *PeerJSService) startLoop() {
	backoff := 2 * time.Second
	for {
		if s.ctx.Err() != nil {
			return
		}
		opts := peerjs.DefaultOptions()
		opts.Host = s.cfg.PeerJSHost
		opts.Port = s.cfg.PeerJSPort
		opts.Secure = s.cfg.PeerJSSecure
		opts.Key = s.cfg.PeerJSKey
		if opts.Host == "" {
			opts.Host = "0.peerjs.com"
		}
		if opts.Port == "" {
			opts.Port = "443"
		}
		opts.ICEServers = s.iceServers
		p := peerjs.NewPeer(s.id, opts)
		p.OnConnection(s.onIncomingConnection)
		s.peerMu.Lock()
		s.peer = p
		s.peerMu.Unlock()
		if err := p.Dial(s.ctx); err != nil {
			log.LogWarn("peerjs: connect failed (retry in %s): %v", backoff, err)
			select {
			case <-time.After(backoff):
			case <-s.ctx.Done():
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 2 * time.Second
		log.LogInfo("peerjs: connected, id=%s host=%s", p.ID(), opts.Host)

		// 连接配置的对端节点
		peers := strings.Split(s.cfg.PeerJSPeers, ",")
		for _, pid := range peers {
			pid = strings.TrimSpace(pid)
			if pid == "" || pid == s.id {
				continue
			}
			go s.connectLoop(pid)
		}

		// 房间发现（多路并取）：自托管信令服务器的发现 API 优先，否则 MQTT
		if s.httpDisc == nil && s.cfg.DiscoverURL != "" {
			cols := s.collectionHashes()
			s.httpDisc = NewHTTPDiscovery(s.cfg.DiscoverURL, s.id, cols, s.onDiscoveredPeer)
			s.httpDisc.Start()
			log.LogInfo("peerjs: http discovery enabled url=%s collections=%d", s.cfg.DiscoverURL, len(cols))
		} else if s.discovery == nil && s.cfg.MQTTEnable {
			cols := s.collectionHashes()
			s.discovery = NewMQTTDiscovery(s.cfg.MQTTBroker, s.cfg.MQTTTopicPref,
				"pd-node-"+s.id, s.onDiscoveredPeer)
			s.discovery.Start(cols)
			s.discovery.Announce(s.id, cols)
			log.LogInfo("peerjs: mqtt discovery enabled broker=%s collections=%d", s.cfg.MQTTBroker, len(cols))
		}

		select {
		case <-s.ctx.Done():
			p.Close()
			return
		case <-s.closed:
			p.Close()
			return
		case <-p.Done():
			// H7 修复：信令 WS 意外断开（公共云掉线/代理抖动常见）——之前
			// readLoop 静默退出后 startLoop 只 select ctx/closed 两个永不触发的
			// 信号，节点永久失聪直到重启（心跳空转、connectLoop 永远退避打转）。
			// 现在 Signaller.Done() 在断线时关闭，触发整轮重连（复用 backoff）。
			log.LogWarn("peerjs: signalling connection lost, reconnecting in %s", backoff)
			p.Close()
			select {
			case <-time.After(backoff):
			case <-s.ctx.Done():
				return
			case <-s.closed:
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
		}
	}
}

// BindLocal 注册本地 WebSocket 会话（浏览器直连本节点，帧协议与远端
// DataChannel 完全一致）。本地会话以 "local" 注册，FetchFromPeer("local", ...)
// 即可复用同一拉取路径。
func (s *PeerJSService) BindLocal(sess Session) {
	s.bindConn(sess)
	log.LogInfo("peerjs: local session bound (id=%s)", sess.ID())
}

// onDiscoveredPeer MQTT 发现回调：对端节点在线，发起互联（已连接则跳过）。
func (s *PeerJSService) onDiscoveredPeer(peerID string) {
	if peerID == "" || peerID == s.id {
		return
	}
	s.mu.Lock()
	_, ok := s.conns[peerID]
	s.mu.Unlock()
	if ok {
		return
	}
	go s.connectLoop(peerID)
}

// collectionHashes 返回本节点关注的集合 hash 分片（配置 + 本地存储）。
func (s *PeerJSService) collectionHashes() []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range strings.Split(s.cfg.MQTTCollections, ",") {
		h = strings.TrimSpace(h)
		if isValidHash(h) && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// connectLoop 保持与远端节点的连接（断开自动重连），直到服务关闭。
// 为什么不用一次性 Connect：对端可能尚未上线（OFFER 入队过期 → EXPIRE），
// 或 ICE 协商失败（failed → Close），必须循环重试直到真正 OnOpen。
// 注意 opened 用一次性 close 而不是带缓冲 channel，防止 OnOpen 重复触发时泄漏。
// 低危 3 修复：connecting 去重——同一 peerID 可能被配置 PEERS 与发现回调
// 同时触发，双 connectLoop 会开两条重复连接（之前靠 conns map 覆盖兜底）。
func (s *PeerJSService) connectLoop(peerID string) {
	s.connectingMu.Lock()
	if _, ok := s.connecting[peerID]; ok {
		s.connectingMu.Unlock()
		return
	}
	s.connecting[peerID] = struct{}{}
	s.connectingMu.Unlock()
	defer func() {
		s.connectingMu.Lock()
		delete(s.connecting, peerID)
		s.connectingMu.Unlock()
	}()

	backoff := 2 * time.Second
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.peerMu.Lock()
		peer := s.peer
		s.peerMu.Unlock()
		if peer == nil {
			return
		}
		conn, err := peer.Connect(s.ctx, peerID, "peerdrive")
		if err != nil {
			log.LogWarn("peerjs: connect to %s failed (retry in %s): %v", peerID, backoff, err)
			if !sleepCtx(s.ctx, backoff) {
				return
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		opened := make(chan struct{})
		conn.OnOpen(func(c *peerjs.Connection) {
			s.bindConn(newRTCSession(c))
			select {
			case <-opened:
			default:
				close(opened)
			}
		})
		select {
		case <-opened:
			backoff = 2 * time.Second
			log.LogInfo("peerjs: connected to %s (conn=%s)", peerID, conn.ID)
			// 等待连接关闭（EXPIRE / ICE 失败 / 对端断开），随后重连
			select {
			case <-conn.Done():
			case <-s.ctx.Done():
				return
			}
		case <-time.After(30 * time.Second):
			log.LogWarn("peerjs: connect to %s timed out", peerID)
			conn.Close()
		}
		if !sleepCtx(s.ctx, backoff) {
			return
		}
	}
}

// onIncomingConnection 被动连接（浏览器或其它节点发起）就绪后绑定消息处理。
// 注意：必须等 OnOpen 再 bindConn——answerer 侧回调在 handleOffer 时立即触发，
// 此时 DataChannel 尚未 open，过早注册会让 FetchFromPeer 拿到未就绪连接
// （发现背景：双向发现时双端同时发起连接，B 侧 answerer 连接未 open 即被使用，
// 报 "connection not open"）。
func (s *PeerJSService) onIncomingConnection(c *peerjs.Connection) {
	c.OnOpen(func(c *peerjs.Connection) {
		s.bindConn(newRTCSession(c))
		log.LogInfo("peerjs: incoming connection from %s (conn=%s)", c.PeerID, c.ID)
	})
}

// bindConn 绑定连接的消息分派：解析 JSON 头按 reqId 路由，二进制块追加到 expect 状态。
// 坑：这条连接是「全双工复用」的——既服务对端的 req（serveFile），也接收
// 自己发起请求的响应（routeResponse）。两者靠帧类型 + reqId 区分：
//   - 文本帧 type=="req" → 对端请求，go serveFile（独立 goroutine，可并发）
//   - 文本帧其它 type → 本端请求的响应，按 reqId 路由
//   - 二进制帧 → 数据块，路由决策（归 upload 还是 expect）在泵内完成，
//     落盘 IO 交给连接级 worker（H5，见 uploadWorker）
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
			f.got = append(f.got, msg.Data...)
			if int64(len(f.got)) >= f.size {
				st.expect = nil
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
				if !f.closed {
					f.closed = true
					select {
					case f.errCh <- fmt.Errorf("peerjs: connection closed"):
					default:
					}
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

// uploadWorker 连接级上传落盘 worker（H5 修复）：消费消息泵投递的分片，
// 执行 WriteAt/Complete（fsync + 全文件 hashFile 是慢操作，之前同步跑在
// pion 消息泵上，一个 8GB 上传完成的瞬间整条连接的其他帧全部冻结）。
// 单 worker 保序（分片按到达顺序落盘，与帧协议顺序一致）；连接关闭经
// binDone 退出（不悬挂）；写失败回 err 帧。
func (s *PeerJSService) uploadWorker(c Session, st *connState) {
	for {
		select {
		case ch := <-st.binCh:
			if err := ch.up.sess.WriteAt(ch.offset, ch.data); err != nil {
				_ = c.SendJSON(dcResp{Type: "err", Msg: "upload write failed: " + err.Error(), ReqID: ch.up.reqID})
				continue
			}
			if ch.last {
				done, fi, err := ch.up.sess.Complete()
				if err != nil {
					_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: ch.up.reqID})
					continue
				}
				if done {
					_ = c.SendJSON(dcResp{Type: "uploaded", Hash: fi.Hash, Total: fi.Size, Path: fi.Path, ReqID: ch.up.reqID})
				} else {
					// 分片已写，整体未完成：ack 告知客户端可继续下一分片
					_ = c.SendJSON(dcResp{Type: "ack", Offset: ch.offset, ReqID: ch.up.reqID})
				}
			}
		case <-st.binDone:
			return
		}
	}
}

// maxPeerFetchSize 远端声明上限（H6 修复）：data 帧声明的块大小/文件大小
// 无上限 → 恶意对端声明 1<<62 并持续发 data 帧 → f.got 无界增长 OOM。
// 与上传上限（8GB）一致。
const maxPeerFetchSize = 8 * 1024 * 1024 * 1024

// routeResponse 把响应帧路由到对应的 fetch 状态。
func (s *PeerJSService) routeResponse(st *connState, r dcResp) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if r.ReqID == "" {
		return
	}
	f := st.fetches[r.ReqID]
	if f == nil {
		return
	}
	reject := func(format string, args ...any) {
		if f.closed {
			return
		}
		f.closed = true
		select {
		case f.errCh <- fmt.Errorf(format, args...):
		default:
		}
	}
	switch r.Type {
	case "meta":
		// total 为文件全量大小（range 请求时 ≠ 本次接收量），只做上限校验
		if r.Total > maxPeerFetchSize {
			reject("peerjs: declared file size %d exceeds limit", r.Total)
		}
	case "data":
		// H6：块大小设上限且必须为正——恶意对端声明超大 size → 无界分配
		if r.Size <= 0 || r.Size > maxPeerFetchSize {
			reject("peerjs: invalid data size %d", r.Size)
			return
		}
		f.size = r.Size
		st.expect = f
	case "done":
		if !f.closed {
			f.closed = true
			// H6 完整性校验：对端提前 done（只发 meta+done）会把截断文件
			// 当成功返回 → 静默数据损坏。done.Size = 对端声明的实际发送字节，
			// 必须与已收字节一致才放行
			if r.Size >= 0 && int64(len(f.got)) != r.Size {
				select {
				case f.errCh <- fmt.Errorf("peerjs: incomplete transfer: got %d bytes, peer sent %d", len(f.got), r.Size):
				default:
				}
			} else {
				select {
				case f.done <- f.got:
				default:
				}
			}
		}
	case "err":
		if !f.closed {
			f.closed = true
			select {
			case f.errCh <- fmt.Errorf("peerjs: %s", r.Msg):
			default:
			}
		}
	}
}

// Connections 返回当前活跃的节点连接（按远端 peer id）。
func (s *PeerJSService) Connections() map[string]Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Session, len(s.conns))
	for k, v := range s.conns {
		out[k] = v
	}
	return out
}

// FetchFromPeer 通过直连 peer 拉取 sha256 文件内容（node↔node / 浏览器直连）。
func (s *PeerJSService) FetchFromPeer(peerID, hash string, offset, size int64) ([]byte, error) {
	s.mu.Lock()
	conn := s.conns[peerID]
	s.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("peerjs: no connection to %s", peerID)
	}
	return s.requestFile(conn, hash, offset, size)
}

// requestFile 发送 req 帧并收集 meta/data/done 直到完成。
func (s *PeerJSService) requestFile(c Session, hash string, offset, size int64) ([]byte, error) {
	// 指令 UUID：reqId 是响应路由键，UUID v4 保证跨连接唯一（randHex8 仅 32bit，并发高时可能碰撞）
	reqID := uuid.NewString()
	f := &fetchState{
		reqID: reqID,
		done:  make(chan []byte, 1),
		errCh: make(chan error, 1),
	}
	st := s.stateFor(c)
	if st == nil {
		return nil, fmt.Errorf("peerjs: connection not bound")
	}
	st.mu.Lock()
	st.fetches[reqID] = f
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		delete(st.fetches, reqID)
		st.mu.Unlock()
	}()

	if err := c.SendJSON(dcReq{Type: "req", Hash: hash, Offset: offset, Size: size, ReqID: reqID}); err != nil {
		return nil, err
	}

	select {
	case data := <-f.done:
		return data, nil
	case err := <-f.errCh:
		return nil, err
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("peerjs: fetch %s timed out", hash)
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *PeerJSService) stateFor(c Session) *connState {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.pending[c]
}

// ---- 文件服务（对端请求本节点文件） ----

type dcReq struct {
	Type   string `json:"type"`
	Hash   string `json:"hash"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	ReqID  string `json:"reqId,omitempty"`
}

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

// serveFile 打开内容寻址文件并按请求发送分块，带写缓冲流控。
// 安全（H1/H2 修复）：
//   - hash 必须 64 位 hex 才切片——之前直接 req.Hash[:2]，对端发空/短 hash
//     越界 panic，serveFile 在 goroutine 里 panic 直接杀死整个进程
//     （公共信令网络上任意节点一行 JSON 就能打崩全节点）
//   - file_index 命中的路径必须落在允许根目录内——之前直接 os.Open(fi.Path)，
//     对端 create 任意绝对路径后可 req 读取（/etc/shadow 攻击链）
// 优先查 file_index 映射（外部登记/上传的文件），其次内容寻址存储。
func (s *PeerJSService) serveFile(c Session, req dcReq) {
	if !isValidHash(req.Hash) {
		_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "invalid hash", ReqID: req.ReqID})
		return
	}
	path := filepath.Join(s.storageDir, req.Hash[:2], req.Hash)
	if fi, err := s.fileIndex.Info(req.Hash); err == nil && fi.Path != "" {
		if s.fileIndex.IsPathAllowed(fi.Path) {
			path = fi.Path
		} else {
			// 索引命中但路径越权（历史脏数据/恶意登记）：回退内容寻址存储，
			// 不回传根目录外文件（H2）
			log.LogWarn("peerjs: index path outside allowed root, serving content-addressed: %s", fi.Path)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "not found", ReqID: req.ReqID})
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "stat failed", ReqID: req.ReqID})
		return
	}
	total := st.Size()

	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := req.Size
	if limit < 0 || offset+limit > total {
		limit = total - offset
	}

	if err := c.SendJSON(dcResp{Type: "meta", Hash: req.Hash, Total: total, ReqID: req.ReqID}); err != nil {
		return
	}

	// 写缓冲流控已下沉到 peerjs.Connection.SendFrame（连接级、全局回调一次）：
	// 并发 serveFile 经 sendMu 串行发送 + lowWater 广播，不再各自注册
	// OnBufferedAmountLow（替换式回调会被覆盖 → 死等，曾导致并发请求卡死）。

	buf := make([]byte, chunkSize)
	sent := int64(0)
	for sent < limit {
		n, err := f.ReadAt(buf[:min64(chunkSize, limit-sent)], offset+sent)
		if n > 0 {
			if err := c.SendFrame(dcResp{Type: "data", Hash: req.Hash, Offset: offset + sent, Size: int64(n), ReqID: req.ReqID}, buf[:n]); err != nil {
				return
			}
			sent += int64(n)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			log.LogWarn("peerjs: read file %s: %v", req.Hash, err)
			_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "read failed", ReqID: req.ReqID})
			return
		}
	}
	_ = c.SendJSON(dcResp{Type: "done", Hash: req.Hash, Offset: offset, Size: sent, ReqID: req.ReqID})
}

func (s *PeerJSService) openFile(hash string) (*os.File, int64, error) {
	if !isValidHash(hash) {
		return nil, 0, fmt.Errorf("invalid hash %q", hash)
	}
	path := filepath.Join(s.storageDir, hash[:2], hash)
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func randHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func parseICEServers(stun, turn string) []webrtc.ICEServer {
	var out []webrtc.ICEServer
	if stun != "" {
		out = append(out, webrtc.ICEServer{URLs: []string{stun}})
	}
	if turn != "" {
		urls := strings.Split(turn, ",")
		out = append(out, webrtc.ICEServer{URLs: urls})
	}
	return out
}
