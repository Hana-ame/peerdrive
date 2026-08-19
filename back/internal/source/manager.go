package source

// manager.go：SourceManager——统一文件管理 + source 生命周期管理。
// 职责：
//   - 注册/注销 source（重名拒绝）、运行时调整优先级
//   - 统一文件获取入口（Open/OpenRange/OpenAny/Info）：按优先级路由，逐源尝试
//   - 统一管理数据面（Stats/Snapshot）→ GET /sources 端点
//
// 路由语义：
//   - Available()==false 的 source 直接跳过（软健康检查）
//   - 按优先级升序尝试：local 命中即返回（内容寻址本地权威，优先本地）；
//     未命中继续降级 peer → url → ipfs
//   - OpenRange 只走 CapStream（流式分片）；OpenAny 允许降级 CapFile 整体拉取
//   - 所有尝试记录 Stats；全失败返回汇总错误（含每源失败原因）

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// Manager 统一管理所有 Source。
type Manager struct {
	mu      sync.RWMutex
	sources []Source // 按优先级升序（变更时重排）
	stats   map[string]*Stats
}

// New 创建空 Manager。
func New() *Manager {
	return &Manager{stats: make(map[string]*Stats)}
}

// Register 注册 source（重名拒绝）。
func (m *Manager) Register(s Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.sources {
		if e.Name() == s.Name() {
			return fmt.Errorf("source %q already registered", s.Name())
		}
	}
	m.sources = append(m.sources, s)
	m.stats[s.Name()] = &Stats{}
	m.sortLocked()
	return nil
}

// Unregister 注销 source，返回是否找到。
func (m *Manager) Unregister(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, e := range m.sources {
		if e.Name() == name {
			m.sources = append(m.sources[:i], m.sources[i+1:]...)
			delete(m.stats, name)
			return true
		}
	}
	return false
}

// btControl 可选 BT 控制面（nil 表示未配置）。
var btControl BTControl

// SetBTControl 注入 BT 控制面实例（nil 表示未启用）。
func (m *Manager) SetBTControl(bc BTControl) {
	btControl = bc
}

// GetBTControl 返回当前 BT 控制面实例（可能为 nil）。
func (m *Manager) GetBTControl() BTControl {
	return btControl
}

// ipfsControl 可选 IPFS 控制面（nil 表示未配置）。
var ipfsControl IPFSControl

// SetIPFSControl 注入 IPFS 控制面实例（nil 表示未启用）。
func (m *Manager) SetIPFSControl(ic IPFSControl) {
	ipfsControl = ic
}

// GetIPFSControl 返回当前 IPFS 控制面实例（可能为 nil）。
func (m *Manager) GetIPFSControl() IPFSControl {
	return ipfsControl
}

// Get 按名字取 source（控制面/管理面入口用）。
func (m *Manager) Get(name string) Source {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.sources {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// SetPriority 运行时调整 source 优先级（统一管理能力）。
func (m *Manager) SetPriority(name string, p int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.sources {
		if e.Name() == name {
			e.SetPriority(p)
			m.sortLocked()
			return nil
		}
	}
	return fmt.Errorf("source %q not registered", name)
}

// sortLocked 按优先级升序重排（调用方持锁）。
func (m *Manager) sortLocked() {
	sort.SliceStable(m.sources, func(i, j int) bool {
		return m.sources[i].Priority() < m.sources[j].Priority()
	})
}

// snapshot 拷贝当前 sources（调用方持 RLock）。
func (m *Manager) snapshot() []Source {
	out := make([]Source, len(m.sources))
	copy(out, m.sources)
	return out
}

// Open 统一文件管理入口：流式获取 hash 的完整内容（等价 OpenRange 0,-1）。
func (m *Manager) Open(ctx context.Context, hash string) (io.ReadCloser, error) {
	return m.OpenRange(ctx, hash, 0, -1)
}

// OpenRange 统一文件管理入口：按优先级路由获取 hash 的分片内容。
// 只尝试 CapStream source——CapFile 源无分片能力，降级会走 8GB 全量
// buffer（OOM 路径），需要整体获取的调用方请用 OpenAny。
func (m *Manager) OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	m.mu.RLock()
	sources := m.snapshot()
	m.mu.RUnlock()

	var lastErr error
	for _, s := range sources {
		if !IsStream(s) {
			continue
		}
		if !s.Available(ctx) {
			m.record(s.Name(), false, 0, errors.New("unavailable"))
			continue
		}
		r, err := s.Open(ctx, hash, offset, size)
		if err == nil {
			m.record(s.Name(), true, 0, nil)
			return r, nil
		}
		lastErr = fmt.Errorf("%s: %w", s.Name(), err)
		m.record(s.Name(), false, 0, err)
	}
	if lastErr == nil {
		lastErr = errors.New("no stream source available")
	}
	return nil, fmt.Errorf("all sources failed: %w", lastErr)
}

// OpenAny 兼容整体获取：优先 CapStream 源（流式），全部失败时降级
// CapFile 源整体拉取（内存驻留——适合小文件/元数据场景）。
func (m *Manager) OpenAny(ctx context.Context, hash string) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	m.mu.RLock()
	sources := m.snapshot()
	m.mu.RUnlock()

	var lastErr error
	for _, s := range sources {
		if !s.Available(ctx) {
			m.record(s.Name(), false, 0, errors.New("unavailable"))
			continue
		}
		if IsStream(s) {
			r, err := s.Open(ctx, hash, 0, -1)
			if err == nil {
				m.record(s.Name(), true, 0, nil)
				return r, nil
			}
			lastErr = fmt.Errorf("%s: %w", s.Name(), err)
			m.record(s.Name(), false, 0, err)
			continue
		}
		if IsFile(s) {
			data, err := s.Fetch(ctx, hash)
			if err == nil {
				m.record(s.Name(), true, int64(len(data)), nil)
				return io.NopCloser(bytes.NewReader(data)), nil
			}
			lastErr = fmt.Errorf("%s: %w", s.Name(), err)
			m.record(s.Name(), false, 0, err)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no source available")
	}
	return nil, fmt.Errorf("all sources failed: %w", lastErr)
}

// Info 元数据查询：按优先级尝试支持 Info 的 source（第一个命中返回）。
func (m *Manager) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	m.mu.RLock()
	sources := m.snapshot()
	m.mu.RUnlock()

	var lastErr error
	for _, s := range sources {
		if !s.Available(ctx) {
			continue
		}
		fi, err := s.Info(ctx, hash)
		if err == nil && fi != nil {
			return fi, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("not found in any source")
	}
	return nil, lastErr
}

// InfoSize 文件大小查询（transport.FileRouter 适配，2026-08-18 第 3 项
// 优化）：serveFile 的 meta 帧需要 total，但传输层不能依赖 source 包的
// FileMeta 类型（import 环）——接口收敛为标量 size。
func (m *Manager) InfoSize(ctx context.Context, hash string) (int64, error) {
	fi, err := m.Info(ctx, hash)
	if err != nil || fi == nil {
		return 0, err
	}
	return fi.Size, nil
}

// Snapshot 管理快照：每个 source 的状态 + 统计（GET /sources 数据源）。
func (m *Manager) Snapshot() []SourceStatus {
	m.mu.RLock()
	sources := m.snapshot()
	m.mu.RUnlock()

	out := make([]SourceStatus, 0, len(sources))
	for _, s := range sources {
		m.mu.RLock()
		st := *m.stats[s.Name()]
		m.mu.RUnlock()
		out = append(out, SourceStatus{
			Name:         s.Name(),
			Type:         s.Type(),
			Priority:     s.Priority(),
			Capabilities: s.Capabilities(),
			Stream:       IsStream(s),
			Available:    s.Available(context.Background()),
			Stats:        st,
		})
	}
	return out
}

// record 记录一次尝试结果（统计 + 最近错误）。
func (m *Manager) record(name string, ok bool, n int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.stats[name]
	if st == nil {
		st = &Stats{}
		m.stats[name] = st
	}
	st.LastAt = time.Now()
	if ok {
		st.Success++
		st.Bytes += n
		st.LastErr = ""
	} else {
		st.Fail++
		if err != nil {
			st.LastErr = err.Error()
		}
	}
}
