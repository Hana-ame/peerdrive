package transport

// outbound.go：出站角色 = 本端发起 verb 并收集响应的全集（「我问别人」）。
// 归属：FetchFromPeer/requestFile（发起 req 帧）、routeResponse（按 reqId 路由
// meta/data/done/err）、stateFor（连接状态查找）。与 inbound.go（应答对端）
// 相对，两条路径在同一连接上双工并发复用——连接关闭时 routeResponse 的清理
// 在 conn.go 的 bindConn OnClose 里统一做，本文件只管发起与收集。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	hashutil "peerdrive/pkg/hashutil"
)

// maxPeerFetchSize 远端声明上限（H6 修复）：data 帧声明的块大小/文件大小
// 无上限 → 恶意对端声明 1<<62 并持续发 data 帧 → f.got 无界增长 OOM。
// 与上传上限（8GB）一致。
const maxPeerFetchSize = 8 * 1024 * 1024 * 1024

// OpenStream 通过直连 peer 流式拉取 sha256 文件内容（分片/流式读取，
// source 体系的 peerSource 适配入口）。返回的 reader 在全量请求读完时
// 自动校验 sha256（内容寻址兜底）；Close 可提前取消（本地丢弃，不断连）。
func (s *PeerJSService) OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error) {
	return s.OpenStreamFrom(peerID, hash, offset, size, nil)
}

// OpenStreamFrom 与 OpenStream 同语义，额外携带回源链路 trace（防环，
// 2026-08-18 第 3 项优化，见 dcReq.Trace 注释）。根请求 trace 为 nil；
// serveFile 回源时由 PeerSource 从 ctx 取出传入（source/peer.go）。
func (s *PeerJSService) OpenStreamFrom(peerID, hash string, offset, size int64, trace []string) (io.ReadCloser, error) {
	s.mu.Lock()
	conn := s.conns[peerID]
	s.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("peerjs: no connection to %s", peerID)
	}
	return s.openStream(conn, hash, offset, size, trace)
}

// FetchFromPeer 兼容封装：[]byte 整体拉取（现有调用方/集成测试用）。
// 内部走流式 OpenStream + io.ReadAll——全量请求的 sha256 校验由 reader
// 在 EOF 时完成，语义与旧实现一致。
func (s *PeerJSService) FetchFromPeer(peerID, hash string, offset, size int64) ([]byte, error) {
	r, err := s.OpenStream(peerID, hash, offset, size)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// openStream 发送 req 帧并返回流式 reader。数据块经连接级 pump 投递到
// fetchState.q（有界队列），reader 消费；done/err 经 done/errCh 通知。
// 清理（cleanup）：EOF/错误/取消时从 fetches 路由表删除 + close(f.closed)
// 放行 pump 投递阻塞——只执行一次。
func (s *PeerJSService) openStream(c Session, hash string, offset, size int64, trace []string) (*fetchReader, error) {
	// 指令 UUID：reqId 是响应路由键，UUID v4 保证跨连接唯一（randHex8 仅 32bit，并发高时可能碰撞）
	reqID := uuid.NewString()
	f := &fetchState{
		reqID:  reqID,
		q:      make(chan []byte, 8),
		done:   make(chan struct{}),
		errCh:  make(chan error, 1),
		closed: make(chan struct{}),
	}
	st := s.stateFor(c)
	if st == nil {
		return nil, fmt.Errorf("peerjs: connection not bound")
	}
	st.mu.Lock()
	st.fetches[reqID] = f
	st.mu.Unlock()
	cleanOnce := sync.Once{}
	cleanup := func() {
		cleanOnce.Do(func() {
			st.mu.Lock()
			delete(st.fetches, reqID)
			st.mu.Unlock()
			// 幂等 close：bindConn OnClose 也可能已关闭（连接先断开场景）——
			// 重复 close(f.closed) 会 panic
			select {
			case <-f.closed:
			default:
				close(f.closed)
			}
		})
	}

	if err := c.SendJSON(dcReq{Type: "req", Hash: hash, Offset: offset, Size: size, ReqID: reqID, Trace: trace}); err != nil {
		cleanup()
		return nil, err
	}
	return &fetchReader{
		f:       f,
		ctx:     s.ctx,
		hash:    hash,
		verify:  offset == 0 && size < 0, // 全量请求 → 读完校验 sha256（H5）
		cleanup: cleanup,
	}, nil
}

// fetchIdleTimeout 流式读取的块间隔超时。旧实现是 5 分钟总超时——流式下
// 总超时对大文件（8GB 多块）无意义，改为「块间隔」超时：对端 5 分钟不
// 发任何数据块视为卡死（原总超时语义由对端连接保活覆盖）。
const fetchIdleTimeout = 5 * time.Minute

// fetchReader 流式读取对端响应的数据块（出站角色）。
// Read 语义：
//   - 从 f.q 消费数据块，块间阻塞等待
//   - done 帧（close(f.done)）→ 先 drain q 剩余块（pump 顺序保证块先于
//     done 入队），再 EOF——EOF 前非阻塞检查 errCh，防 err 帧被 done 掩盖
//   - err 帧 / 连接关闭（errCh）→ 返回错误
//   - 块间隔超时（fetchIdleTimeout）→ 报错
//   - 全量请求读完校验 sha256（内容寻址兜底，H5）——校验失败以错误返回
type fetchReader struct {
	f       *fetchState
	ctx     context.Context
	hash    string
	verify  bool
	cleanup func()

	buf []byte
	h   hash.Hash
	eof bool
	err error
}

func (r *fetchReader) Read(p []byte) (int, error) {
	// 空闲超时定时器：只创建一次并复用（Reset），不能每次循环 time.After——
	// 每消费一个块就创建 1 个 timer，8GB 大文件 = 13 万个 timer 常驻 runtime
	// timer 堆直到 5 分钟到期（发现背景：代码审阅，内存+GC 双浪费）。
	idle := time.NewTimer(fetchIdleTimeout)
	defer idle.Stop()
	idleReset := func() {
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(fetchIdleTimeout)
	}
	for {
		if len(r.buf) > 0 {
			n := copy(p, r.buf)
			r.buf = r.buf[n:]
			return n, nil
		}
		if r.eof {
			return 0, r.err
		}
		select {
		case chunk := <-r.f.q:
			idleReset() // 有数据活跃：重置空闲计时
			if r.verify {
				if r.h == nil {
					r.h = sha256.New()
				}
				r.h.Write(chunk)
			}
			r.buf = chunk
		case <-r.f.done:
			// 传输完成：done 后 q 仍可能有块（pump 顺序：块先入队、done 后 close）。
			// 一次只取一块——buf 消费完外层 for 会回到 select（done 一直 ready，
			// 再次进此分支取下一块），直到 q 空才 EOF。不可循环取块：
			// 循环里 buf 赋值会覆盖未消费的块（块1 丢失 bug，流式测试复现）。
			select {
			case chunk := <-r.f.q:
				idleReset()
				if r.verify {
					if r.h == nil {
						r.h = sha256.New()
					}
					r.h.Write(chunk)
				}
				r.buf = chunk
			default:
				// q 空：EOF。EOF 前非阻塞检查 errCh，防 err 帧被 done 掩盖
				select {
				case err := <-r.f.errCh:
					r.finish(err)
					return 0, err
				default:
				}
				r.finish(nil)
				return 0, io.EOF
			}
		case err := <-r.f.errCh:
			r.finish(err)
			return 0, err
		case <-idle.C:
			r.finish(fmt.Errorf("peerjs: fetch %s idle timeout", r.hash))
			return 0, r.err
		case <-r.ctx.Done():
			r.finish(r.ctx.Err())
			return 0, r.err
		}
	}
}

// finish 幂等结束：标记 eof、记录错误、执行清理（delete 路由表 + close closed）。
// 全量请求 EOF 时校验 sha256（H5 内容寻址兜底）。
func (r *fetchReader) finish(err error) {
	if r.eof {
		return
	}
	r.eof = true
	if err == nil && r.verify && r.h != nil {
		sum := hex.EncodeToString(r.h.Sum(nil))
		if sum != r.hash {
			err = fmt.Errorf("peerjs: content hash mismatch for %s", r.hash)
		}
	}
	r.err = err
	if r.cleanup != nil {
		r.cleanup()
	}
}

// Close 提前取消：本地丢弃后续块（pump 经 f.closed 停止投递），不断连。
func (r *fetchReader) Close() error {
	r.finish(io.ErrClosedPipe)
	return nil
}

// hashMatchesSHA256 校验 data 的 sha256 是否等于期望哈希。
// 注意：hash 是用户输入，必须确保本身是合法 64 位 hex（否则比较恒失败）；
// 调用方（FetchFromPeer 入口）已保证，防御性再判一次。
// 空文件不做特判：sha256(空) 是合法内容寻址值，len(data)==0 不应被直接拒绝。
func hashMatchesSHA256(hash string, data []byte) bool {
	if len(hash) != 64 || !hashutil.IsValidSHA256(hash) {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == hash
}

func (s *PeerJSService) stateFor(c Session) *connState {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.pending[c]
}

// failFetch 给 fetch 状态投递失败（错误/上限/完整性拒绝）。闭包戒律：
// 每来一个 data 帧调用一次 routeResponse，内联闭包 = 每 64KB 一次堆分配
// （8GB 传输 13 万次），抽成包级函数零分配。
// 已完成（done 已 close）后的迟到 err 帧忽略。
func failFetch(f *fetchState, format string, args ...any) {
	select {
	case <-f.done:
		return
	default:
	}
	select {
	case f.errCh <- fmt.Errorf(format, args...):
	default:
	}
}

// routeResponse 把响应帧路由到对应的 fetch 状态（出站角色的收集端）。
// 持有 st.mu 期间完成（数据帧高频：泵内直接调用，不额外加锁层次）。
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
	switch r.Type {
	case "meta":
		// total 为文件全量大小（range 请求时 ≠ 本次接收量），只做上限校验
		if r.Total > maxPeerFetchSize {
			failFetch(f, "peerjs: declared file size %d exceeds limit", r.Total)
		}
	case "data":
		// H6：块大小设上限且必须为正——恶意对端声明超大 size → 无界分配
		if r.Size <= 0 || r.Size > maxPeerFetchSize {
			failFetch(f, "peerjs: invalid data size %d", r.Size)
			return
		}
		f.size = r.Size
		st.expect = f
	case "done":
		// done 幂等：重复 done（或 err 后到达）不处理——重复 close(done) 会 panic
		select {
		case <-f.done:
			return
		default:
		}
		// H6 完整性校验：对端提前 done（只发 meta+done）会把截断文件
		// 当成功返回 → 静默数据损坏。done.Size = 对端声明的实际发送字节，
		// 必须与已投递字节（received）一致才放行
		if r.Size >= 0 && f.received != r.Size {
			failFetch(f, "peerjs: incomplete transfer: got %d bytes, peer sent %d", f.received, r.Size)
			return
		}
		close(f.done)
	case "err":
		failFetch(f, "peerjs: %s", r.Msg)
	}
}
