package source

// manager.go: SourceManager -- unified file management + source lifecycle management.
// Responsibilities:
//   - Register/unregister sources (reject duplicates), adjust priority at runtime
//   - Unified file retrieval entry (Open/OpenRange/OpenAny/Info): route by priority, try sources in order
//   - Unified management data plane (Stats/Snapshot) -> GET /sources endpoint
//
// Routing semantics:
//   - Sources with Available()==false are skipped directly (soft health check)
//   - Try in ascending priority order: local hit returns immediately (content-addressed
//     local authority, prefer local); on miss continue downgrading peer -> url -> ipfs
//   - OpenRange only uses CapStream (streaming ranges); OpenAny allows falling back to CapFile for full fetch
//   - All attempts are recorded in Stats; all failures return a summary error (with per-source failure reason)

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"peerdrive/internal/egress"
)

// 静态接口约束：确保 Manager 满足统一出口管线 ContentProvider 契约
var _ egress.ContentProvider = (*Manager)(nil)

// Manager manages all Sources uniformly.
type Manager struct {
	mu      sync.RWMutex
	sources []Source // sorted by ascending priority (re-sorted on change)
	stats   map[string]*Stats

	// Optional control planes (nil means not configured). Held per instance -- multiple
	// Managers do not share them.
	// Historical background: previously misused a package-level global var, causing multiple
	// instances to share the same control plane + tests needing defensive cleanup of leftovers
	// (see TestManager_ControlNilDefault old comment); changed to fields so each Manager
	// is independent; Set/Get access under m.mu, safe for concurrent HTTP requests.
	btControl   BTControl
	ipfsControl IPFSControl
}

// New creates an empty Manager.
func New() *Manager {
	return &Manager{stats: make(map[string]*Stats)}
}

// Register registers a source (rejects duplicate names).
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

// Unregister unregisters a source, returning whether it was found.
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

// SetBTControl injects a BT control plane instance (nil means not enabled). Held per instance,
// so multiple Managers do not interfere; write lock protected, safe with concurrent Get.
func (m *Manager) SetBTControl(bc BTControl) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.btControl = bc
}

// GetBTControl returns the current BT control plane instance (may be nil). Read lock protected,
// does not block concurrent injection.
func (m *Manager) GetBTControl() BTControl {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.btControl
}

// SetIPFSControl injects an IPFS control plane instance (nil means not enabled). Held per instance.
func (m *Manager) SetIPFSControl(ic IPFSControl) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ipfsControl = ic
}

// GetIPFSControl returns the current IPFS control plane instance (may be nil).
func (m *Manager) GetIPFSControl() IPFSControl {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.ipfsControl
}

// Get retrieves a source by name (used by control plane/management plane entry points).
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

// SetPriority adjusts a source's priority at runtime (unified management capability).
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

// sortLocked re-sorts by ascending priority (caller holds the lock).
func (m *Manager) sortLocked() {
	sort.SliceStable(m.sources, func(i, j int) bool {
		return m.sources[i].Priority() < m.sources[j].Priority()
	})
}

// snapshot copies the current sources (caller holds RLock).
func (m *Manager) snapshot() []Source {
	out := make([]Source, len(m.sources))
	copy(out, m.sources)
	return out
}

// Open is the unified file management entry: streams the full content of a hash (equivalent to OpenRange 0,-1).
func (m *Manager) Open(ctx context.Context, hash string) (io.ReadCloser, error) {
	return m.OpenRange(ctx, hash, 0, -1)
}

// OpenRange is the unified file management entry: routes by priority to fetch a range of a hash.
// Only tries CapStream sources -- CapFile sources have no range capability, falling back would
// go through an 8GB full buffer (OOM path). Callers needing full fetch should use OpenAny.
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

// OpenAny is a full-fetch compatible entry: prefers CapStream sources (streaming), falls back
// to CapFile sources for full fetch when all stream sources fail (memory-resident -- suitable
// for small files/metadata scenarios).
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

// Info queries metadata: tries sources supporting Info in priority order (first hit returns).
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

// InfoSize queries file size (transport.FileRouter adapter, 2026-08-18 optimization item #3):
// serveFile's meta frame needs total, but the transport layer cannot depend on the source package's
// FileMeta type (import cycle) -- the interface is converged to a scalar size.
func (m *Manager) InfoSize(ctx context.Context, hash string) (int64, error) {
	fi, err := m.Info(ctx, hash)
	if err != nil || fi == nil {
		return 0, err
	}
	return fi.Size, nil
}

// Snapshot is the management snapshot: each source's status + stats (data source for GET /sources).
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

// record records the result of an attempt (stats + most recent error).
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
