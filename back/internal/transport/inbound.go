package transport

// inbound.go：入站角色 = 应答对端发来的 verb 全集（「别人问我答」）。
// 归属：req（serveFile）、create/upload/list/info/delete/sync（serve* 索引 verb）、
// 上传分片落盘 worker（uploadWorker）。共享连接机制在 conn.go，本文件只放
// 对端驱动的处理逻辑——与 outbound.go（本端发起）相对，两条路径在同一连接上
// 双工并发复用，不共享任何可变状态（除 connState 内各自的槽位）。

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"
	hashutil "peerdrive/pkg/hashutil"
)

// chunkSize DataChannel 单块传输大小（pion SCTP 单消息上限约 256KB，64KB 兼顾流控粒度）。
// 写缓冲流控已下沉到 peerjs.Connection.SendFrame（连接级全局回调），此处只定块大小。
const chunkSize = 64 * 1024

// chunkPool 64KB 块缓冲池：并发 serveFile 各自 make 会重复分配 64KB
// （GC 压力 + 内存峰值），池化复用（每请求一块，SendFrame 同步复制后归还
// ——SendFrame 内部 marshal 与落线均不持有 buf）。
var chunkPool = sync.Pool{
	New: func() any {
		b := make([]byte, chunkSize)
		return &b
	},
}

func getChunk() []byte  { return *chunkPool.Get().(*[]byte) }
func putChunk(b []byte) { chunkPool.Put(&b) }

// ---- 文件服务（对端请求本节点文件） ----

// FileRouter 文件路由接口（source.Manager 实现；传输层只依赖接口避免
// import 环——source 包引 transport，transport 不能再引 source）。
// serveFile 经此路由「本地 → 对端 → URL 模板」多源获取（第 3 项优化，
// 2026-08-18）；未装配时（nil）退回本地语义（openFile）。
type FileRouter interface {
	// OpenRange 流式打开 hash 的分片（offset<0→0；size<0→到文件尾）。
	OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error)
	// InfoSize 查询文件总大小（meta 帧用；不支持元数据的源返回 err）。
	InfoSize(ctx context.Context, hash string) (int64, error)
}

// serveFile 打开文件（内容寻址/file_index）并按请求发送分块，带写缓冲流控。
// 安全（H1/H2 修复）：
//   - hash 必须 64 位 hex 才切片——之前直接 req.Hash[:2]，对端发空/短 hash
//     越界 panic，serveFile 在 goroutine 里 panic 直接杀死整个进程
//     （公共信令网络上任意节点一行 JSON 就能打崩全节点）
//   - file_index 命中的路径必须落在允许根目录内——之前直接 os.Open(fi.Path)，
//     对端 create 任意绝对路径后可 req 读取（/etc/shadow 攻击链）
//
// 优先查 file_index 映射（外部登记/上传的文件），其次内容寻址存储。
// 2026-08-18（第 3 项优化）：打开改为多源路由（装配了 FileRouter 时）——
// 本地权威优先，未命中回源对端/URL 模板（原 openFile 语义与
// source.LocalSource.resolvePath 一致，收敛到一处）。回源环由 Trace
// 防环（本节点 ID 已在链路中 → 拒绝，见 dcReq.Trace 注释）。
func (s *PeerJSService) serveFile(c Session, req dcReq) {
	if !hashutil.IsStrictSHA256(req.Hash) {
		_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "invalid hash", ReqID: req.ReqID})
		return
	}
	// trace 防环：本节点已在请求链路上 → 拒绝（防 A←→B 互连回源死循环）
	if s.ID() != "" {
		for _, id := range req.Trace {
			if id == s.ID() {
				_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "loop detected", ReqID: req.ReqID})
				return
			}
		}
	}
	// 转发给下游的链路：追加本节点（本节点不在链中才走到这）
	fwdTrace := append(append([]string{}, req.Trace...), s.ID())
	ctx := context.WithValue(context.Background(), TraceKey, fwdTrace)

	total := int64(-1) // -1 = 未知（对端 fetchReader 的 total>0 上限校验会跳过）
	var r io.Reader
	if s.router != nil {
		// 元数据：local 源可查（file_index/CAS stat）；peer/url 源无 Info → -1
		if size, err := s.router.InfoSize(ctx, req.Hash); err == nil && size >= 0 {
			total = size
		}
		f, err := s.router.OpenRange(ctx, req.Hash, req.Offset, req.Size)
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "not found", ReqID: req.ReqID})
			return
		}
		defer f.Close()
		r = f
	} else {
		// 未装配 router（测试/独立模式）：本地语义（file_index 优先 + CAS 兜底）
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
		total = st.Size()
		// 原 clamp 语义（LocalSource 内部等价 clamp，此处只为对齐旧行为）
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
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "seek failed", ReqID: req.ReqID})
			return
		}
		r = io.LimitReader(f, limit)
	}

	if err := c.SendJSON(dcResp{Type: "meta", Hash: req.Hash, Total: total, ReqID: req.ReqID}); err != nil {
		return
	}

	// 写缓冲流控已下沉到 peerjs.Connection.SendFrame（连接级、全局回调一次）：
	// 并发 serveFile 经 sendMu 串行发送 + lowWater 广播，不再各自注册
	// OnBufferedAmountLow（替换式回调会被覆盖 → 死等，曾导致并发请求卡死）。

	buf := getChunk()
	defer putChunk(buf)
	sent := int64(0)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if err := c.SendFrame(dcResp{Type: "data", Hash: req.Hash, Offset: req.Offset + sent, Size: int64(n), ReqID: req.ReqID}, buf[:n]); err != nil {
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
	_ = c.SendJSON(dcResp{Type: "done", Hash: req.Hash, Offset: req.Offset, Size: sent, ReqID: req.ReqID})
}

func (s *PeerJSService) openFile(hash string) (*os.File, int64, error) {
	if !hashutil.IsStrictSHA256(hash) {
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

// ---- 文件索引 verb 服务端处理（create/upload/list/info/delete/sync） ----
// 全部经 Session 传输（WS/WebRTC 同一套），reqId 回显保持请求-响应配对。
// 请求字段由 dcResp 通用结构承载（Hash/Offset/Size/ReqID/Path/Name/Seq）。

// redactDisallowedPath 对外的文件信息做路径脱敏：create 已强制根目录内，
// 但历史库/旧版本可能残留根目录外的 Path；serveFile 已回退 CAS，list/info/sync
// 也不能再把这类绝对路径泄露给对端。
func (s *PeerJSService) redactDisallowedPath(fi FileInfo) FileInfo {
	if fi.Path != "" && !s.fileIndex.IsPathAllowed(fi.Path) {
		fi.Path = ""
	}
	return fi
}

// serveCreate 处理 create：登记外部文件（sha256 → 绝对路径，不复制文件）。
// 请求 {type:"create", path} → 响应 {type:"created", hash,size,name,path,seq} | err
func (s *PeerJSService) serveCreate(c Session, r dcResp) {
	if r.Path == "" {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "path required", ReqID: r.ReqID})
		return
	}
	fi, err := s.fileIndex.Create(r.Path)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "created", Hash: fi.Hash, Total: fi.Size, Path: fi.Path, Name: fi.Name, Seq: fi.Seq, ReqID: r.ReqID})
}

// serveUploadBegin 处理 upload：开始流式接收（后续二进制帧写入 UploadSink）。
// 请求 {type:"upload", name, size, reqId} → 响应 meta{total}；完成后 uploaded{hash,path}。
// 注意：同一连接同时只有一个 upload 接收流（连接级 pendingUpload）。
func (s *PeerJSService) serveUploadBegin(c Session, st *connState, r dcResp) {
	// 允许 size==0：空文件是合法内容寻址值（sha256(空)），与拉取侧空文件校验对称
	if r.Size < 0 || r.Size > 8*1024*1024*1024 {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "invalid upload size", ReqID: r.ReqID})
		return
	}
	offset := r.Offset
	if offset < 0 || offset%uploadChunkSize != 0 {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "offset must be chunk-aligned", ReqID: r.ReqID})
		return
	}
	sess, err := s.fileIndex.BeginUpload(r.Name, r.Size)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	if r.Size == 0 {
		// 空文件没有 data 帧可发，这里直接完成，避免连接级 pendingUpload 卡到
		// 30s stale 清理（且旧实现 size<=0 直接把空文件上传拒之门外）
		if offset != 0 {
			_ = c.SendJSON(dcResp{Type: "err", Msg: "offset beyond size", ReqID: r.ReqID})
			return
		}
		done, fi, err := sess.Complete()
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
			return
		}
		if !done {
			_ = c.SendJSON(dcResp{Type: "err", Msg: "empty upload failed to complete", ReqID: r.ReqID})
			return
		}
		_ = c.SendJSON(dcResp{Type: "uploaded", Hash: fi.Hash, Total: fi.Size, Path: fi.Path, ReqID: r.ReqID})
		return
	}
	// 分片长度：单次 data 块 ≤ 一个 chunk（64KB）；尾部块自动截断
	length := r.Size - offset
	if length > uploadChunkSize {
		length = uploadChunkSize
	}
	if length <= 0 {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "offset beyond size", ReqID: r.ReqID})
		return
	}
	st.mu.Lock()
	if st.pendingUpload != nil {
		// M6 修复：对端发 upload 头后不发数据块会永久占用槽位——之后该连接
		// 所有 upload 全部 "already in progress"（连接级 DoS，重连才恢复）。
		// 超时自动清空（会话本身留待 file_index 10 分钟 reap，不影响多 source）
		if time.Since(st.pendingUpload.created) > 30*time.Second {
			log.LogWarn("peerjs: stale pending upload cleared (reqId=%s)", st.pendingUpload.reqID)
			st.pendingUpload = nil
		} else {
			// 上一个分片未完成（连接复用异常）：拒绝重入（多 source 走多连接）
			_ = c.SendJSON(dcResp{Type: "err", Msg: "upload already in progress", ReqID: r.ReqID})
			st.mu.Unlock()
			return
		}
	}
	st.pendingUpload = &uploadState{reqID: r.ReqID, offset: offset, size: length, sess: sess, created: time.Now()}
	st.mu.Unlock()
	_ = c.SendJSON(dcResp{Type: "meta", Total: r.Size, Offset: sess.ContiguousOffset(), ReqID: r.ReqID})
}

// serveList 处理 list：列出全部未删除映射。
// 请求 {type:"list", offset?, size?} → 响应 {type:"list-resp", files, total}
func (s *PeerJSService) serveList(c Session, r dcResp) {
	files, err := s.fileIndex.List(int(r.Offset), int(r.Size))
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	if files == nil {
		files = []FileInfo{}
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, s.redactDisallowedPath(f))
	}
	_ = c.SendJSON(dcResp{Type: "list-resp", Files: out, Total: int64(len(out)), ReqID: r.ReqID})
}

// serveInfo 处理 info：按 hash 返回文件信息（download 前先查）。
// 请求 {type:"info", hash} → 响应 {type:"info-resp", hash,size,name,path,seq} | err
func (s *PeerJSService) serveInfo(c Session, r dcResp) {
	fi, err := s.fileIndex.Info(r.Hash)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	safe := s.redactDisallowedPath(*fi)
	_ = c.SendJSON(dcResp{Type: "info-resp", Hash: safe.Hash, Total: safe.Size, Name: safe.Name, Path: safe.Path, Seq: safe.Seq, ReqID: r.ReqID})
}

// serveDelete 处理 delete：逻辑删除映射（同步用 tombstone）。
// 请求 {type:"delete", hash} → 响应 {type:"deleted", hash, seq}
// L6：响应补 seq（REFACTOR §4 协议要求 deleted{hash,seq}），对端 sync 游标跟踪删除。
func (s *PeerJSService) serveDelete(c Session, r dcResp) {
	seq, err := s.fileIndex.Delete(r.Hash)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "deleted", Hash: r.Hash, Seq: seq, ReqID: r.ReqID})
}

// serveSync 处理 sync：metadata 增量同步（seq 游标之后的所有变更含 tombstone）。
// 请求 {type:"sync", seq} → 响应 {type:"sync-resp", files, lastSeq}
func (s *PeerJSService) serveSync(c Session, r dcResp) {
	files, last, err := s.fileIndex.SyncSince(r.Seq)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	if files == nil {
		files = []FileInfo{}
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, s.redactDisallowedPath(f))
	}
	_ = c.SendJSON(dcResp{Type: "sync-resp", Files: out, LastSeq: last, ReqID: r.ReqID})
}

// ---- 上传落盘 worker（入站角色） ----

// uploadWorker 连接级上传落盘 worker（H5 修复）：消费消息泵投递的分片，
// 执行 WriteAt/Complete（fsync + 全文件 hashFile 是慢操作，之前同步跑在
// pion 消息泵上，一个 8GB 上传完成的瞬间整条连接的其他帧全部冻结）。
// 单 worker 保序（分片按到达顺序落盘，与帧协议顺序一致）；连接关闭经
// binDone 退出（不悬挂）；写失败回 err 帧。
// 注意：转发块（fwdCh）2026-08-18 起由独立 fwdWorker 消费（见下），
// 上传 Complete 不再阻塞同连接的转发隧道。
func (s *PeerJSService) uploadWorker(c Session, st *connState) {
	for {
		select {
		case ch := <-st.binCh:
			if ch.au != nil {
				// admin 管理面上传（admin.go）：块写临时文件（收集），
				// 收齐/中止 → 清理 + 回帧（复用 H5 架构：IO 移出消息泵）。
				s.adminUploadChunk(c, ch.au, ch.data, ch.last)
				continue
			}
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

// fwdWorker 连接级转发写 worker（2026-08-18 拆出，发现背景：代码审阅——
// 转发块原本由 uploadWorker 的 select 共用消费，一个 8GB 上传的 Complete
// （fsync + 全文件 hashFile，慢磁盘可达秒级）期间同连接所有转发隧道冻结，
// 交互式隧道（SSH 等）整条卡死）。独立 worker 让转发 IO 不受上传影响。
// 单 worker 保序；与 uploadWorker 共用 binDone 退出信号。
func (s *PeerJSService) fwdWorker(c Session, st *connState) {
	for {
		select {
		case ch := <-st.fwdCh:
			// 写失败（隧道已关/对端断开）静默丢弃：转发是尽力而为的流。
			if _, err := ch.fw.out.Write(ch.data); err != nil {
				log.LogDebug("peerjs: fwd write drop: %v", err)
				ch.fw.out.Close()
			}
		case <-st.binDone:
			return
		}
	}
}
