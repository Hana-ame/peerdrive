// Package source 提供统一的「数据源（Source）」抽象：任何能提供「内容寻址字节流」的东西
// 都是一个 Source——本地磁盘（file_index + 内容寻址存储 CAS）、P2P 对端（PeerJS req 帧透传）、
// URL/HTTP（可经 ech-proxy 或 echcore 域前置出网）、ECH 直取、IPFS gateway 等。
// 上层（Manager 的消费者）只问「给我这个 hash 的内容」，不关心来源与网络路径。
//
// ── 统一寻址：sha256 ─────────────────────────────────────────────────────────
// 所有内容寻址的字节流都用 64 位小写 hex sha256 作为地址。这是跨数据源的公因子：
// 不管字节是从本地磁盘、对端节点、还是某个远端 CDN 取来的，只要 sha256 相同就
// 是同一份内容，就可以互换。因此接口一律以 hash 为参数，而不是 URL/路径。
//
// 「取」与「地址」是两回事（本包最重要的设计取舍）：
//   - sha 是地址（content addressing）——它描述「是什么内容」；
//   - ECH/HTTP/WebRTC 是出口（egress）——它描述「怎么把字节搬过来」。
// 一个数据源 = 一种「把 hash 解析成可读取字节流」的方式。ECH 源就是
// 「resolver 把 hash 解析成 CDN URL + ECH 域前置出口取字节 + 回来算 sha256 校验」；
// sha 源就是「hash 直接映射到本地文件索引/CAS，必要时问对端」。
// ECH 取到的内容经过 sha256 校验后即可登记进 CAS/file_index，之后它就是 sha 源的
// 一份本地副本——两个源不冲突，是「冷路径（远端取）」与「热路径（本地读）」的关系。
//
// ── 如何接入一个新数据源（实现步骤）────────────────────────────────────────────
// 新数据源照以下 5 步实现即可，接口就是模板；参考实现见 local.go（本地）/ sha.go
// （多路径组合）/ url.go（URL 模板）/ ech.go（ECH 直取）：
//
//  1. 实现 Source 接口。必须实现：
//     - Name()：唯一标识，作为注册键（Manager 拒绝重复名）。
//     - Type()：分类标签（local/sha/peer/url/ech/ipfs...），仅用于 GET /sources 展示。
//     - Capabilities()：声明 CapStream / CapFile，见下方「能力位」。
//     - Priority() / SetPriority()：路由优先级（小=先试），Manager 可运行时调整。
//     - Available(ctx)：软健康检查，false 时路由跳过（不要做真实 ping，见下）。
//     - Open(ctx, hash, offset, size)：流式打开分片，CapStream 源必实现。
//     - Fetch(ctx, hash)：整文件取回 []byte，CapFile 源必实现（CapStream 源可委托 Open）。
//     - Info(ctx, hash)：元数据，不支持就返回 nil, nil（可选能力）。
//  2. 入口一律先过 validHash(hash)（64 位小写 hex）。对端/HTTP 传入的 hash 不可信，
//     裸取 hash[:2] 会越界 panic（见 transport/inbound.go 的 H1 修复背景）。
//  3. 整文件语义（offset==0 && size<0）必须做 sha256 校验（声明 CapVerify）。
//     这是内容寻址的基线：远端/对端可能被篡改或截断，不校验就会把错误内容当成
//     「就是这个文件」。复用 verifyReadCloser（整文件在 EOF 时校验）。分片请求
//     （offset>0 或 size>=0）不校验——分片无法独立校验，由整文件路径兜底。
//  4. 偏移钳制：offset<0 → 0；offset>size(file) → 文件尾；size<0 → 到文件尾；
//     offset+size 越界 → 截断到文件尾。用 io.LimitReader 限制读取长度，
//     offset/size 是调用方输入，必须防御。
//  5. Close 语义：Open 返回的 io.ReadCloser 必须可安全重复 Close（调用方可能
//     defer + 显式 close），用 sync.Once 包住释放动作（见 peerReadCloser）。
//
// 可选增强接口（实现即可声明，不实现则走 Source 默认路径）：
//   - MetaSource：OpenMeta 在打开流的同时返回 *FileMeta（total 已知，形态类似
//     Open(ctx, addr, offset, size) → (stream, meta, error)）。
//   - LocalControl / BTControl / IPFSControl（control.go）：写路径，与读路径分离。
//
// Available() 约定：不做真实网络探测。探测会浪费请求（URL 源 ping 一次就消耗一次
// 配额，peer 源探测一次就占用连接槽），且探测结果与真实请求的时序差会让「探测绿、
// 请求红」的假象更难排查。默认返回 true，把失败交给路由统计（Stats.LastErr）暴露，
// 未来可加「最近一次成功时间窗」。
//
// ── 能力位 ────────────────────────────────────────────────────────────────────
// CapFile：支持整文件取回（Fetch 返回 []byte）。没有 CapStream 的源（如不支持
//   Range 的 HTTP 端点）只能整文件取。
// CapStream：支持流式/分片读取（Open 支持 offset/size）。大文件路由必须优先
//   CapStream 源——整文件缓冲有内存风险（8GB 全量入内存会 OOM）。
// CapVerify：Open 对整文件请求（offset==0 && size<0）做 sha256 校验（内容寻址基线）。
// CapMeta：Info 有实现（返回非 nil *FileMeta）；否则 Info 返回 nil, nil。
//
// ── 路由语义（Manager.Open/OpenRange/OpenAny）────────────────────────────────
// 按优先级升序尝试；Available()==false 的源直接跳过；命中即返回（本地优先，
// 内容寻址下本地是权威）；未命中继续降级 local → peer → url/ech。OpenRange 只用
// CapStream 源；OpenAny 允许 CapFile 源整文件兜底。全部尝试都记入 Stats（统一
// 管理数据面，经 GET /sources 暴露）。
//
// ── 现有实现的映射关系 ───────────────────────────────────────────────────────
//   LocalSource（local.go）：file_index 映射优先 + CAS 兜底（storageDir/<h[:2]>/<h>），
//     本地权威，路径校验走 pathutil（读取边界 IsPathReadable，SafeOpen 防 TOCTOU）。
//   ShaSource（sha.go）：把「同一个 sha 的多条获取路径」合并成一个源——file_index +
//     CAS 本地优先、PeerJS req 帧通道兜底（本地优先/peer 兜底语义保留），一次注册。
//   PeerSource（peer.go）：P2P 透传，多对端并发 race（连接级 expect 单槽，TryLock 跳过忙碌对端）。
//   URLSource（url.go）：URL 模板（%s=hash, %d=offset/size），client 可注入 ech-proxy Transport。
//   EchSource（ech.go）：resolver 把 hash 解析成直连 CDN URL + echcore 域前置出口取字节
//     （ECH 是出口，sha 是地址——本接口把两者接到同一层）。
//
// 未来数据源如何映射：
//   - iwara / exhentai：没有内容寻址语义（video ID / gallery ID）。流程是
//     ① resolver 从 hash 反查登记的 ID（file_index 的 name 字段或 collection 表）
//     ② 用各自 API 解析成直连 CDN URL（iwara 还需 X-Version 签名）
//     ③ 用 EchSource 取字节 + sha256 校验
//     ④ 首次取到后登记进 file_index/CAS，之后走 ShaSource 热路径。
//   - twimg：已经是 URL 模板形态（pbs.twimg.com/<path>?<query> 经 ech-proxy 重写），
//     用 URLSource 模板即可；如需域前置而非代理，改注入 echcore 出口。
//   - IPFS gateway：URL 模板 https://gateway/ipfs/<hash>，或 provider 包的 PinCID 控制面。
//   - 任意 URL/twimg 型：都是「resolver + egress + 校验」，直接套 EchSource。
//
// 依赖方向：本包依赖 transport（FileIndexService/PeerJSService）、echcore（ECH 出口）、
// provider（IPFS）；transport 不反向依赖本包（serveFile 只经 FileRouter 接口回调）——
// 装配在 serverapp/app.go 完成。

package source

import (
	"context"
	"fmt"
	"io"
	"time"

	hashutil "peerdrive/pkg/hashutil"
)

// Capability 数据源能力位（可组合的 flag）。
type Capability uint8

const (
	// CapFile 支持整文件取回（Fetch(ctx, hash) → []byte）。没有 CapStream 的源
	// （如不支持 Range 的 HTTP 端点）只能整文件取。
	CapFile Capability = 1 << iota
	// CapStream 支持流式/分片读取（Open(ctx, hash, offset, size) → io.ReadCloser）。
	// 大文件路由必须优先 CapStream 源——整文件缓冲有内存风险。
	CapStream
	// CapVerify Open 对整文件请求（offset==0 && size<0）做 sha256 校验——内容寻址
	// 语义的基线：远端/对端内容可能被篡改或截断，不校验就会把错误内容当成该文件。
	// 分片请求不校验（分片无法独立校验），由整文件路径兜底。
	CapVerify
	// CapMeta Info 有实现（返回非 nil *FileMeta）。否则 Info 返回 nil, nil。
	CapMeta
)

// FileMeta 数据源返回的文件元数据。
type FileMeta struct {
	Hash string
	Size int64
	Name string
	// Path 仅本地类源有意义（peer/url/ech 源不暴露本地路径）。
	Path string
}

// Source 是统一的数据源契约：按内容地址（sha256）打开字节流。
//
// 这是所有数据源的公共接口，也是新数据源的参考基座——实现步骤见包注释
// 「如何接入一个新数据源」。寻址一律用 64 位小写 hex sha256；「取」（ECH/HTTP/
// WebRTC 出口）与「地址」（sha 内容寻址）在这里汇合：ECH 源把 hash 解析成 URL 取
// 字节并校验，sha 源把 hash 直接映射到本地索引/CAS 或对端通道。
type Source interface {
	// Name 唯一标识（注册键，Manager 拒绝重复名）。
	Name() string
	// Type 分类标签：local / sha / peer / url / ech / ipfs（仅用于展示与排查）。
	Type() string
	// Capabilities 声明能力位（见 Capability）。
	Capabilities() Capability
	// Priority 路由优先级（小=先试）；SetPriority 可运行时调整（统一管理面能力）。
	Priority() int
	// SetPriority 运行时调整优先级。
	SetPriority(p int)
	// Available 软健康检查：false 时路由跳过。不做真实网络探测（浪费请求且
	// 「探测绿、请求红」更难排查）——默认 true，失败交给 Stats.LastErr 暴露。
	Available(ctx context.Context) bool
	// Open 流式打开内容分片（需要 CapStream）。offset<0 归一化为 0；size<0 表示
	// 到文件尾；offset+size 越界截断到文件尾。整文件请求（offset==0 && size<0）
	// 若声明 CapVerify 必须做 sha256 校验。返回的 io.ReadCloser 必须可安全重复 Close。
	Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error)
	// Fetch 整文件取回（需要 CapFile）。整文件取回必须做 sha256 校验；CapStream
	// 源可委托 Open(0, -1) + ReadAll 实现。
	Fetch(ctx context.Context, hash string) ([]byte, error)
	// Info 元数据查询（可选能力，CapMeta）。不支持返回 nil, nil（不是错误）。
	Info(ctx context.Context, hash string) (*FileMeta, error)
}

// MetaSource 是 Source 的可选增强：打开流的同时返回元数据，形态类似
// Open(ctx, addr, offset, size) → (stream, meta, error)。用于「打开前就能知道
// 总大小」的源（本地 stat 已知 / 对端 meta 帧已知）——serveFile 的 meta 帧、
// 断点续传进度条都靠它，比「先 Open 再 Info」少一次往返。
//
// 不实现 MetaSource 的源不受影响：OpenMetaOf 回退到 Open + Info 组合。
type MetaSource interface {
	Source
	// OpenMeta 与 Open 同语义，额外返回 *FileMeta（Size 未知时填 -1）。
	// 不支持元数据时 Size 填 -1，调用方按 -1 处理。
	OpenMeta(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, *FileMeta, error)
}

// OpenMetaOf 返回 Source 的 OpenMeta 能力；ok=false 表示未实现 MetaSource。
func OpenMetaOf(s Source) (MetaSource, bool) {
	m, ok := s.(MetaSource)
	return m, ok
}

// IsStream 判断源是否支持流式分片。
func IsStream(s Source) bool { return s.Capabilities()&CapStream != 0 }

// IsFile 判断源是否支持整文件取回。
func IsFile(s Source) bool { return s.Capabilities()&CapFile != 0 }

// IsVerify 判断源是否在整文件打开时校验 sha256（内容寻址基线）。
func IsVerify(s Source) bool { return s.Capabilities()&CapVerify != 0 }

// IsMeta 判断源的 Info 是否有实现。
func IsMeta(s Source) bool { return s.Capabilities()&CapMeta != 0 }

// Stats 每个源的累计统计（统一管理数据面）。
type Stats struct {
	Success int64     // 成功次数
	Fail    int64     // 失败次数
	Bytes   int64     // 累计传输字节
	LastErr string    // 最近一次失败原因（多源排查用）
	LastAt  time.Time // 最近一次尝试时间
}

// SourceStatus 管理快照条目（GET /sources 输出）。
type SourceStatus struct {
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Priority     int        `json:"priority"`
	Capabilities Capability `json:"capabilities"`
	Stream       bool       `json:"stream"` // 便捷字段：是否支持流式分片
	Available    bool       `json:"available"`
	Stats        Stats      `json:"stats"`
}

// validHash 校验 hash 是否为合法 64 位小写 hex（所有源入口的统一防御）。
// 为什么在这里统一：对端/HTTP 传入的 hash 不可信，裸取 hash[:2] 会越界 panic，
// 而源调用常发生在 goroutine 里——一次 panic 直接杀进程（见 transport/inbound.go 的 H1）。
func validHash(hash string) error {
	if !hashutil.IsStrictSHA256(hash) {
		return fmt.Errorf("invalid sha256 hash %q", hash)
	}
	return nil
}
