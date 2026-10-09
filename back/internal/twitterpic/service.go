package twitterpic

// service.go: twitter-pic 整合的服务门面 —— controller/router 只依赖这一个类型。
//
// 组装（serverapp/app.go，开关 PEERDRIVE_TWITTERPIC_ENABLE 默认关）：
//
//	svc, _ := twitterpic.NewService(twitterpic.ServiceConfig{
//	    BaseURL:    cfg.TwitterPicBaseURL,    // 空 → DefaultBaseURL
//	    ProxyBase:  cfg.TwitterPicProxyBase,  // 空 → DefaultProxyBase
//	    StorageDir: cfg.StorageDir,           // 必填（媒体+集合都落 sha-文件系统）
//	    MaxFiles:   cfg.TwitterPicMaxFiles,
//	    Timeout:    …,
//	    Client:     urlClient,                // 复用 ech-proxy Transport（nil=默认）
//	})
//
// 开关关掉时完全不构造（deps.TwitterPic == nil → 路由不注册），对默认节点是
// 零变化。监视器（FetchMonitor）随 Service 创建，HTTP 暴露面
// GET /twitterpic/fetch-stats 输出 Snapshot（各备选源时间窗内次数与成功率）。

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"peerdrive/internal/collection"
)

// ServiceConfig 是 twitter-pic 服务的装配参数。默认值在 NewService 里补，
// config 层只透传 env（空值 → 本包默认），默认值单点定义不漂移。
type ServiceConfig struct {
	BaseURL    string        // 空 → DefaultBaseURL
	ProxyBase  string        // 空 → DefaultProxyBase
	StorageDir string        // 必填：sha-文件系统根
	MaxFiles   int           // 媒体摄取上限，0 = 全部
	MaxBytes   int64         // 单文件摄取上限，0 = 不限
	Timeout    time.Duration // 单请求超时；0 → 默认 20s
	Client     *http.Client  // nil → 默认 client
}

// Service 是 twitter-pic 整合的对外门面。
type Service struct {
	client    *Client
	index     *UserIndex
	buildOpts BuildOptions
	timeout   time.Duration
	baseURL   string
}

// NewService 构造服务；StorageDir 为空直接报错（写 sha-文件系统是核心能力，
// 配错要在启动时暴露而不是第一个请求才失败）。
func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.StorageDir == "" {
		return nil, fmt.Errorf("twitterpic: StorageDir is required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	proxy := cfg.ProxyBase
	if proxy == "" {
		proxy = DefaultProxyBase
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	hc := cfg.Client
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	} else if hc.Timeout == 0 {
		// 保留调用方注入的 Transport（ech-proxy 出口），只补超时——Clone 不
		// 存在（那是 http.Transport 的方法），新建 client 共享 Transport。
		hc = &http.Client{Transport: hc.Transport, Timeout: timeout}
	}
	client, err := NewClient(base, hc)
	if err != nil {
		return nil, err
	}
	return &Service{
		client: client,
		index:  NewUserIndex(cfg.StorageDir),
		buildOpts: BuildOptions{
			Client:     hc,
			StorageDir: cfg.StorageDir,
			ProxyBase:  proxy,
			MaxFiles:   cfg.MaxFiles,
			MaxBytes:   cfg.MaxBytes,
			Monitor:    collection.NewFetchMonitor(0),
		},
		timeout: timeout,
		baseURL: base,
	}, nil
}

// BaseURL 返回实际使用的 API 基址（汇报/日志用）。
func (s *Service) BaseURL() string { return s.baseURL }

// ListUsers 拉远端用户列表。
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.client.ListUsers(cctx)
}

// BuildUserCollection 构建并保存一个用户的集合，同时更新用户指针文件，
// 返回集合 sha（可按 sha 从 sha-文件系统读出）。
func (s *Service) BuildUserCollection(ctx context.Context, username string) (*BuildResult, error) {
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	res, err := BuildUserCollection(cctx, s.client, username, s.buildOpts)
	if err != nil {
		return nil, err
	}
	if err := s.index.Set(username, res.CollectionSHA, len(res.Collection.Entries)); err != nil {
		return nil, err
	}
	return res, nil
}

// ReadCollection 按 sha 读出集合 JSON 原文（respond-by-sha 路径）。
func (s *Service) ReadCollection(ctx context.Context, sha string) ([]byte, error) {
	return collection.ReadJSON(s.buildOpts.StorageDir, sha)
}

// UserIndex 暴露指针文件（供只读管理查询）。
func (s *Service) UserIndex() *UserIndex { return s.index }

// FetchStats 返回文件源访问监视快照（各备选源时间窗内次数/成功率/累计）。
func (s *Service) FetchStats() []collection.AltStat {
	return s.buildOpts.Monitor.Snapshot()
}

// RecentAttempts 返回最近 n 次备选源访问轨迹（降级路径）。
func (s *Service) RecentAttempts(n int) []collection.Attempt {
	return s.buildOpts.Monitor.Recent(n)
}
