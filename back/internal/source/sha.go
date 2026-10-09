package source

// sha.go: ShaSource — 把「同一个内容地址的多条获取路径」合并成一个 sha 数据源。
//
// sha 是地址（描述内容是什么），获取路径有两条：
//   - 本地：file_index（sha256 → 绝对路径）+ CAS（storageDir/<h[:2]>/<h>），见 LocalSource；
//   - peer：PeerJS req 帧通道（{type:"req", hash, offset, size, reqId, trace}），见 PeerSource。
// 「本地优先、peer 兜底」的语义完整保留——只是从「Manager 里两条独立注册」收敛成
// 「一个源内的多条路径」，对外暴露一个统一的 sha 源。
//
// 为什么组合而不是重写：LocalSource 已经收敛了 serveFile 的路径判定逻辑（file_index 优先 +
// CAS 兜底 + IsPathReadable 读取边界 + pathutil.SafeOpen 防 TOCTOU），PeerSource 已经收敛了
// 多对端并发 race + 连接级单槽 + trace 防环。ShaSource 复用两者而不复制安全逻辑——多写一份
// 就会长出「清单列得出、一拉 read failed」的分叉（AGENTS.md 的路径边界硬约束）。
//
// 组合方式：paths 是 []Source，按序尝试（先 local 后 peer）。这样「再加一条路径」
// （比如远端 URL 兜底）不需要改 ShaSource，只需要往 paths 里加一个 Source——
// 这也是本接口的可扩展性基座。
//
// 装配示例（保持路由统计名与 /sources/local/* 控制面可用）：
//
//	mgr.Register(source.NewShaSource("local",
//	    source.NewLocalSource(storageDir, peerjsSvc.FileIndex()),
//	    source.NewPeerSource(peerjsSvc)))
//
// 当前主干仍是 local + peer 分别注册（见 serverapp/app.go）——本 PR 不切换装配，
// 理由见 PR 描述的取舍一节（避免改变 GET /sources 的统计键）。

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// ShaSource 是内容地址（sha）数据源：file_index + CAS 本地优先，PeerJS 通道兜底。
type ShaSource struct {
	name  string
	paths []Source // 获取路径，按序尝试（先本地后 peer）

	mu       sync.RWMutex
	priority int
}

// NewShaSource 创建一个 sha 源。name 为空时默认 "sha"；paths 按序尝试，
// 典型是 LocalSource + PeerSource（本地优先、peer 兜底）。
func NewShaSource(name string, paths ...Source) *ShaSource {
	if name == "" {
		name = "sha"
	}
	return &ShaSource{name: name, paths: paths}
}

func (s *ShaSource) Name() string { return s.name }
func (s *ShaSource) Type() string { return "sha" }

// Capabilities 是各路径能力的并集：任一路径支持流式 → CapStream；任一支持整文件 → CapFile；
// 任一校验 → CapVerify；任一有元数据 → CapMeta。
func (s *ShaSource) Capabilities() Capability {
	var c Capability
	for _, p := range s.paths {
		c |= p.Capabilities()
	}
	return c
}

func (s *ShaSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *ShaSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available 任一路径可用即可用（本地目录可读 OR 有在线对端）。
func (s *ShaSource) Available(ctx context.Context) bool {
	for _, p := range s.paths {
		if p.Available(ctx) {
			return true
		}
	}
	return false
}

// Open 按路径序打开：跳过不可用/非流式的路径，返回第一条成功的流；全部失败返回聚合错误
// （带每条路径的原因，便于多源排查）。语义与 Manager.OpenRange 的降级链一致。
func (s *ShaSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	var lastErr error
	for _, p := range s.paths {
		if !IsStream(p) {
			continue
		}
		if !p.Available(ctx) {
			lastErr = fmt.Errorf("%s: %w", p.Name(), errUnavailable)
			continue
		}
		r, err := p.Open(ctx, hash, offset, size)
		if err == nil {
			return r, nil
		}
		lastErr = fmt.Errorf("%s: %w", p.Name(), err)
	}
	if lastErr == nil {
		return nil, fmt.Errorf("sha: no usable path")
	}
	return nil, fmt.Errorf("sha: all paths failed: %w", lastErr)
}

// OpenMeta 实现 MetaSource：优先用路径自己的 OpenMeta（少一次往返），否则 Open + Info 组合。
// Size 未知时填 -1（调用方按 -1 处理：meta 帧 total=-1、进度条未知总长）。
func (s *ShaSource) OpenMeta(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, *FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, nil, err
	}
	var lastErr error
	for _, p := range s.paths {
		if !IsStream(p) || !p.Available(ctx) {
			continue
		}
		var r io.ReadCloser
		var meta *FileMeta
		var err error
		if m, ok := p.(MetaSource); ok {
			r, meta, err = m.OpenMeta(ctx, hash, offset, size)
		} else {
			if r, err = p.Open(ctx, hash, offset, size); err == nil {
				meta, _ = p.Info(ctx, hash)
			}
		}
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", p.Name(), err)
			continue
		}
		if meta == nil {
			meta = &FileMeta{Hash: hash, Size: -1}
		}
		if meta.Hash == "" {
			meta.Hash = hash
		}
		if meta.Size < 0 {
			if fi, err := p.Info(ctx, hash); err == nil && fi != nil && fi.Size >= 0 {
				meta.Size = fi.Size
			} else {
				meta.Size = -1
			}
		}
		return r, meta, nil
	}
	if lastErr == nil {
		return nil, nil, fmt.Errorf("sha: no usable path")
	}
	return nil, nil, fmt.Errorf("sha: all paths failed: %w", lastErr)
}

// Fetch 整文件取回：委托 Open(0, -1) + ReadAll（各路径自己的校验语义保留）。
func (s *ShaSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	r, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Info 按路径序查元数据：第一个返回非 nil 的路径胜出（本地路径有 name/path，最完整）。
func (s *ShaSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	var lastErr error
	for _, p := range s.paths {
		if !p.Available(ctx) {
			continue
		}
		fi, err := p.Info(ctx, hash)
		if err == nil && fi != nil {
			if fi.Hash == "" {
				fi.Hash = hash
			}
			return fi, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// ---- LocalControl 委托（写路径）----
//
// ShaSource 实现 LocalControl：委托给第一个实现该能力的路径（本地路径）。这样用
// NewShaSource("local", local, peer) 替换 local + peer 两条注册时，/sources/local/add
// 与 /sources/local/write 控制面不用改（LocalControlOf(sha) 直接命中）。

// localControlPath 返回第一个实现 LocalControl 的路径（本地路径）。
func (s *ShaSource) localControlPath() (LocalControl, bool) {
	for _, p := range s.paths {
		if lc, ok := LocalControlOf(p); ok {
			return lc, true
		}
	}
	return nil, false
}

// AddLocalFile 把一个已存在的本地文件加入 sha 源（委托本地路径）。
func (s *ShaSource) AddLocalFile(path string) (*FileMeta, error) {
	lc, ok := s.localControlPath()
	if !ok {
		return nil, ErrControlUnsupported
	}
	return lc.AddLocalFile(path)
}

// WriteFile 直接写入文件到 sha 源（委托本地路径）。
func (s *ShaSource) WriteFile(name string, r io.Reader) (*FileMeta, error) {
	lc, ok := s.localControlPath()
	if !ok {
		return nil, ErrControlUnsupported
	}
	return lc.WriteFile(name, r)
}

// errUnavailable 路径不可用（Available()==false）。聚合错误里用它区分「不可用」与「请求失败」。
var errUnavailable = fmt.Errorf("unavailable")
