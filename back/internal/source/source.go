// Package source 统一文件获取抽象（source 体系）。
//
// 核心思想：任何能提供「内容寻址字节流」的东西都是 Source——本地磁盘、
// p2p 对端（透传）、URL/HTTP（可经 ech-proxy 等出口）、IPFS 网关。上层
// （Manager 消费方）只问「给我 hash 的内容」，不关心来源与网络路径。
//
// 能力标记（Capabilities）：每个 source 声明自己支持整体获取（CapFile，
// Fetch 返回 []byte）还是流式/分片读取（CapStream，Open 支持 offset/size）。
// Manager 路由时按能力选择调用方式——大文件必须走 CapStream（8GB 全量
// buffer 会 OOM，见 transport 流式改造）。
//
// 路由语义（Manager.Open）：按优先级升序尝试；Available()==false 跳过；
// 本地优先命中即返回（内容寻址本地权威），未命中降级到 peer/url。所有
// 尝试记录进 Stats（统一管理面，GET /sources 暴露）。
//
// 依赖方向：本包依赖 transport（FileIndexService/PeerJSService）与 provider
// （IPFS），transport 不反向依赖本包——装配在 cmd/server/main 完成。
package source

import (
	"context"
	"fmt"
	"io"
	"time"

	hashutil "peerdrive/pkg/hashutil"
)

// Capability source 能力位标记（flag，可组合）。
type Capability uint8

const (
	// CapFile 支持整体文件获取（Fetch(ctx, hash) → []byte）。
	// 无 CapStream 的 source（如不支持 Range 的 HTTP 端点）只能整体拉取。
	CapFile Capability = 1 << iota
	// CapStream 支持流式/分片读取（Open(ctx, hash, offset, size) → io.ReadCloser）。
	// 大文件路由必须优先 CapStream source——全量 buffer 有内存上限风险。
	CapStream
)

// FileMeta source 返回的文件元数据（Info 可选能力，不支持返回 nil,nil）。
type FileMeta struct {
	Hash string
	Size int64
	Name string
	// Path 仅 local source 有意义（可能为空——对端/URL 源不暴露本地路径）
	Path string
}

// Source 统一文件获取源。
type Source interface {
	// Name 唯一标识（注册表 key，重复注册拒绝）。
	Name() string
	// Type 分类标识：local / peer / url / ipfs。
	Type() string
	// Capabilities 声明能力位（见 Capability）。
	Capabilities() Capability
	// Priority 路由优先级（小 = 先尝试；Manager.SetPriority 可运行时调整）。
	Priority() int
	// SetPriority 运行时调整优先级（统一管理能力之一）。
	SetPriority(p int)
	// Available 健康检查（软状态）：false 时路由跳过。本地=目录可读，
	// peer=有在线连接，url=最近成功/可 ping。
	Available(ctx context.Context) bool
	// Open 流式打开（需 CapStream）。offset<0 归一为 0；size<0 表示到文件尾。
	// 全量请求（offset==0 && size<0）的实现必须做 sha256 校验（内容寻址兜底）。
	Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error)
	// Fetch 整体获取（需 CapFile）。全量获取必须做 sha256 校验。
	Fetch(ctx context.Context, hash string) ([]byte, error)
	// Info 元数据查询（可选能力；不支持返回 nil, nil）。
	Info(ctx context.Context, hash string) (*FileMeta, error)
}

// IsStream 判断 source 是否支持流式分片。
func IsStream(s Source) bool { return s.Capabilities()&CapStream != 0 }

// IsFile 判断 source 是否支持整体获取。
func IsFile(s Source) bool { return s.Capabilities()&CapFile != 0 }

// Stats 每 source 的累计统计（统一管理数据面）。
type Stats struct {
	Success int64     // 成功次数
	Fail    int64     // 失败次数
	Bytes   int64     // 累计传输字节
	LastErr string    // 最近一次失败原因（排查多源问题）
	LastAt  time.Time // 最近一次尝试时间
}

// SourceStatus 管理快照条目（GET /sources 输出）。
type SourceStatus struct {
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Priority     int        `json:"priority"`
	Capabilities Capability `json:"capabilities"`
	Stream       bool       `json:"stream"` // 便捷：是否支持流式分片
	Available    bool       `json:"available"`
	Stats        Stats      `json:"stats"`
}

// 校验 hash 是否合法 64hex（所有 source 入口的统一防御）。
func validHash(hash string) error {
	if !hashutil.IsStrictSHA256(hash) {
		return fmt.Errorf("invalid sha256 hash %q", hash)
	}
	return nil
}
