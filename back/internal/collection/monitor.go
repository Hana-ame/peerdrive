package collection

// monitor.go: 文件源访问监视（FetchMonitor）。
//
// 需求：单独监视文件源的访问方式——每次取文件时记录用了哪个备选源
// （sha / ech-url / url / private.url）、成功/失败、耗时、字节数，并且
// 备选源切换的降级轨迹（先 sha 失败 → 回退 ech-url → 回退 url…）可见。
//
// 落在哪：collection 包内新增的计数器/观察者结构，而不是改 source.Manager
// 的 Stats——Manager 的 Stats 是「按注册源名」的 hash 寻址统计（GET /sources），
// 而这里监视的是「按条目备选源」的访问，键是 sha/ech-url/url/private.url 这
// 四种备选类型，语义不同，混进 Manager 会让 /sources 的键含义失真。字段形态
// 与 source.Stats 对齐（attempts/success/fail/bytes/lastErr/lastAt），统计口径
// 一致，只是作用域是「collection 条目的备选源访问」。每次尝试同时经
// internal/log 输出一行（LogDebug），失败聚合另有 LogWarn，日志行与结构内
// 查询（Snapshot/Recent）互补。
//
// 可查询性：Snapshot() 回答「过去 maxRecent 次尝试内各源访问次数与成功率分布」
// （窗口统计）+ 累计计数；Recent(n) 返回最近 n 次尝试的原始轨迹（降级路径）。
// 暴露面：结构内查询 + HTTP 端点（controller 把 Snapshot 序列化成 JSON），
// 不新增全局状态。

import (
	"sort"
	"sync"
	"time"
)

// Attempt 是一次备选源访问的完整记录（降级轨迹的一个节点）。
type Attempt struct {
	Alt      string        `json:"alt"`         // sha / ech-url / url / private.url
	OK       bool          `json:"ok"`          // 成功与否
	At       time.Time     `json:"at"`          // 尝试时刻
	Duration time.Duration `json:"duration_ns"` // 耗时
	Bytes    int64         `json:"bytes"`       // 已知字节数（流式源打开时为 0）
	Err      string        `json:"err,omitempty"`
}

// altCounters 是单个备选源的累计计数。
type altCounters struct {
	attempts, success, fail, bytes int64
	lastErr                        string
	lastAt                         time.Time
}

// FetchMonitor 线程安全的备选源访问监视器：按备选源累计计数 + 最近
// maxRecent 次尝试的环形缓冲（窗口统计的来源）。
type FetchMonitor struct {
	maxRecent int
	mu        sync.Mutex
	byAlt     map[string]*altCounters
	recent    []Attempt // 最近一次在末尾
}

// NewFetchMonitor 创建监视器；maxRecent<=0 时用默认 256（窗口 = 最近 256 次尝试）。
func NewFetchMonitor(maxRecent int) *FetchMonitor {
	if maxRecent <= 0 {
		maxRecent = 256
	}
	return &FetchMonitor{maxRecent: maxRecent, byAlt: make(map[string]*altCounters)}
}

// Record 记录一次备选源访问。并发安全（-race 下多 goroutine 取文件计数不错）。
func (m *FetchMonitor) Record(a Attempt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.byAlt[a.Alt]
	if c == nil {
		c = &altCounters{}
		m.byAlt[a.Alt] = c
	}
	c.attempts++
	if a.OK {
		c.success++
	} else {
		c.fail++
		if a.Err != "" {
			c.lastErr = a.Err
		}
	}
	c.bytes += a.Bytes
	if !a.At.IsZero() {
		c.lastAt = a.At
	}
	m.recent = append(m.recent, a)
	if len(m.recent) > m.maxRecent {
		drop := len(m.recent) - m.maxRecent
		m.recent = append([]Attempt(nil), m.recent[drop:]...)
	}
}

// AltStat 是 Snapshot 里单个备选源的统计：窗口（最近 maxRecent 次尝试）内的
// 访问次数/成功/失败/字节/成功率，加上累计计数与最近一次错误。
type AltStat struct {
	Alt string `json:"alt"`
	// 窗口统计（过去 maxRecent 次尝试内）
	Attempts    int64   `json:"attempts"`
	Success     int64   `json:"success"`
	Fail        int64   `json:"fail"`
	Bytes       int64   `json:"bytes"`
	SuccessRate float64 `json:"success_rate"` // 窗口内成功率 [0,1]
	// 累计
	TotalAttempts int64 `json:"total_attempts"`
	TotalSuccess  int64 `json:"total_success"`
	TotalFail     int64 `json:"total_fail"`
	TotalBytes    int64 `json:"total_bytes"`
	// 最近一次
	LastErr string    `json:"last_err,omitempty"`
	LastAt  time.Time `json:"last_at,omitempty"`
}

// Snapshot 返回各备选源访问统计（按备选名排序，键稳定）。
func (m *FetchMonitor) Snapshot() []AltStat {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 窗口统计从 recent 环重算：环形缓冲只存尝试，统计在查询时聚合，避免
	// Record 路径为维护窗口计数器付出额外锁内工作量。
	win := make(map[string]*altCounters)
	for _, a := range m.recent {
		c := win[a.Alt]
		if c == nil {
			c = &altCounters{}
			win[a.Alt] = c
		}
		c.attempts++
		if a.OK {
			c.success++
		} else {
			c.fail++
		}
		c.bytes += a.Bytes
	}

	out := make([]AltStat, 0, len(m.byAlt))
	for alt, cum := range m.byAlt {
		w := win[alt]
		st := AltStat{
			Alt:           alt,
			TotalAttempts: cum.attempts,
			TotalSuccess:  cum.success,
			TotalFail:     cum.fail,
			TotalBytes:    cum.bytes,
			LastErr:       cum.lastErr,
			LastAt:        cum.lastAt,
		}
		if w != nil {
			st.Attempts = w.attempts
			st.Success = w.success
			st.Fail = w.fail
			st.Bytes = w.bytes
			if w.attempts > 0 {
				st.SuccessRate = float64(w.success) / float64(w.attempts)
			}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alt < out[j].Alt })
	return out
}

// Recent 返回最近 n 次尝试（原始降级轨迹，按时间先后；n<=0 或超出时返回全部）。
func (m *FetchMonitor) Recent(n int) []Attempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= 0 || n > len(m.recent) {
		n = len(m.recent)
	}
	out := make([]Attempt, n)
	copy(out, m.recent[len(m.recent)-n:])
	return out
}

// Reset 清空全部计数与轨迹。
func (m *FetchMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byAlt = make(map[string]*altCounters)
	m.recent = m.recent[:0]
}
