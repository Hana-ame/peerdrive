package source

// peer.go：PeerSource——p2p 透传源（经 PeerJS/WebRTC 从在线对端拉取）。
// 语义：
//   - Open 枚举在线对端（PeerJSService.Connections），依次尝试 OpenStream，
//     第一个成功返回——透传语义：本节点没有就从对端拉
//   - 同一对端同时只允许一个流：连接级 expect 单槽（帧协议约束，见
//     transport/conn.go bindConn 注释）——并发向同一 peer 发起两个流会
//     数据交错。用 TryLock 跳过忙碌 peer 而不是等待（大文件流会阻塞很久）
//   - 全量请求的 sha256 校验由 transport 的 fetchReader 完成（H5 兜底）
//   - Available = 有在线对端
//
// 说明：当前为「串行尝试」（依次试每个 peer），未来可升级为多 peer 并发
// 竞速（不同连接并发安全，同一连接仍需互斥）。

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"peerdrive/internal/transport"
)

// PeerSource p2p 透传文件源。
type PeerSource struct {
	name string
	svc  *transport.PeerJSService

	mu       sync.RWMutex
	priority int

	// peerLocks 每个对端的流互斥：同 peer 同时只一个 OpenStream。
	// TryLock 语义：忙则跳过该 peer（去下一个），不等待——等大文件流
	// 结束会阻塞整个路由。
	peerLocks sync.Map // peerID → *sync.Mutex
}

// NewPeerSource 创建 p2p 透传源。name 默认 "peer"。
func NewPeerSource(svc *transport.PeerJSService) *PeerSource {
	return &PeerSource{name: "peer", svc: svc}
}

func (s *PeerSource) Name() string { return s.name }
func (s *PeerSource) Type() string { return "peer" }

// Capabilities 对端流式分片（req 帧支持 offset/size range）。
func (s *PeerSource) Capabilities() Capability { return CapStream }

func (s *PeerSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *PeerSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available 有在线对端（排除自身）。
func (s *PeerSource) Available(ctx context.Context) bool {
	conns := s.svc.Connections()
	for pid := range conns {
		if pid != s.svc.ID() {
			return true
		}
	}
	return false
}

// Open 从在线对端拉取内容：多对端并发竞速，首个成功返回。
// 为什么要竞速：串行尝试时第一个慢对端（远端磁盘慢/网络抖）会卡住整个
// 回源直到失败/超时——透传场景多个对端在线时应选最快路径。竞速只到
// 「首个流建立成功」，不等待其余对端返回（迟到/失败的流由收割 goroutine
// Close，防 peer 流互斥锁泄漏和本端 fetch 状态悬挂）。
// ctx 可携带回源链路（transport.TraceKey，serveFile 回源时注入）——
// 透传给 OpenStreamFrom 防环（A←→B 互连回源死循环，2026-08-18 第 3 项
// 优化）。根请求（HTTP 下载等）ctx 无该值 → trace 为 nil。
func (s *PeerSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	var trace []string
	if t, ok := ctx.Value(transport.TraceKey).([]string); ok {
		trace = t
	}
	conns := s.svc.Connections()
	var pids []string
	var locks []*sync.Mutex
	for pid := range conns {
		if pid == s.svc.ID() {
			continue
		}
		muI, _ := s.peerLocks.LoadOrStore(pid, &sync.Mutex{})
		mu := muI.(*sync.Mutex)
		if !mu.TryLock() {
			// 该 peer 已有流在进行（连接级 expect 单槽）——跳过，不等待
			continue
		}
		pids = append(pids, pid)
		locks = append(locks, mu)
	}
	if len(pids) == 0 {
		return nil, fmt.Errorf("no online peer available")
	}
	if len(pids) == 1 {
		// 单对端：走原串行路径（无并发开销）
		r, err := s.svc.OpenStreamFrom(pids[0], hash, offset, size, trace)
		if err != nil {
			locks[0].Unlock()
			return nil, fmt.Errorf("peer %s: %w", pids[0], err)
		}
		return &peerReadCloser{r: r, mu: locks[0]}, nil
	}
	// 多对端并发竞速：channel 容量 = 候选数，输家 goroutine 永不阻塞
	type res struct {
		r   io.ReadCloser
		mu  *sync.Mutex
		err error // 失败原因（全失败时聚合进最终报错，可诊断）
	}
	ch := make(chan res, len(pids))
	for i := range pids {
		go func(pid string, mu *sync.Mutex) {
			r, err := s.svc.OpenStreamFrom(pid, hash, offset, size, trace)
			if err != nil {
				mu.Unlock() // 失败：立即释放该 peer 槽位
				ch <- res{err: err}
				return
			}
			ch <- res{r: r, mu: mu} // 成功：解锁权交给胜者/收割者
		}(pids[i], locks[i])
	}
	// 首个成功立即返回（真竞速：慢对端不阻塞本调用）；
	// 其余未决结果由后台收割 goroutine 关闭（r 泄漏 → 该对端流互斥锁
	// 永久占用 + 本端 fetch 状态悬挂，慢对端晚到数秒即泄漏）
	got := 0
	var errs []string
	for got < len(pids) {
		rr := <-ch
		got++
		if rr.r == nil {
			if rr.err != nil {
				errs = append(errs, rr.err.Error())
			}
			continue
		}
		if got < len(pids) {
			go func(n int) {
				for j := n; j < len(pids); j++ {
					x := <-ch
					if x.r != nil {
						x.r.Close()
						x.mu.Unlock()
					}
				}
			}(got)
		}
		return &peerReadCloser{r: rr.r, mu: rr.mu}, nil
	}
	// 全失败：聚合每个对端的原因（回退到上层 source 时的可诊断报错）
	if len(errs) > 0 {
		return nil, fmt.Errorf("all peers failed: %s", strings.Join(errs, "; "))
	}
	return nil, fmt.Errorf("all peers failed")
}

// Fetch 整体获取（CapStream 已覆盖，防御性实现）。
func (s *PeerSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	r, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Info p2p 源不做元数据查询（对端 info verb 未在拉取侧实现——第一版不支持）。
func (s *PeerSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	return nil, nil
}

// peerReadCloser 读完后释放 peer 流互斥锁。
type peerReadCloser struct {
	r  io.ReadCloser
	mu *sync.Mutex
}

func (p *peerReadCloser) Read(b []byte) (int, error) { return p.r.Read(b) }

func (p *peerReadCloser) Close() error {
	err := p.r.Close()
	p.mu.Unlock() // 流结束释放该 peer 槽位（TryLock 持有者才走到这）
	return err
}
