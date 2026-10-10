package source

// range.go: RangeSource — 基于源文件 byte range 的引用式内容寻址数据源 (Issue #325)。
//
// 架构定位：
// 提出一种引用式数据方案：sha256 指向「源文件 + 字节区间 (fileHash, offset, length)」。
// 当上层或对端按 rangeHash 寻址取数时，直接读取已存在源文件的对应字节区间并流式返回，
// 物理存储为零拷贝引用，不生成、不落盘任何实体切片文件，极大节省磁盘空间与 IO 开销。
//
// 安全与边界防线（严格遵循 AGENTS.md 规范）：
// 1. 哈希白名单校验：rangeHash 与 fileHash 必须严格符合 64 位十六进制 SHA-256（validHash），
//    彻底杜绝任何路径遍历、空字符注入或畸形字符串。
// 2. 算术溢出与区间边界防御：
//    - 严格校验 offset >= 0, length > 0；
//    - 防御整数溢出攻击：offset + length 必须大于等于 offset；
//    - 读取时的局部偏移（sub-offset）与请求尺寸（sub-size）进行严格截断，绝对不超出注册区间的物理范围。
// 3. 读时完整性校验（CapVerify）：
//    - 整区间读取时（sub-offset == 0 且 size == length），通过 verifyReadCloser 在 EOF 时校验 SHA-256；
//    - 若底层源文件被修改、替换或截断，导致区间字节与 rangeHash 不匹配，立即在 EOF 处拦截报错。
// 4. 并发安全性：注册映射表受读写锁保护，支持并发注册与读取。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
)

var (
	ErrRangeNotFound   = errors.New("range: hash not found in registered ranges")
	ErrInvalidOffset   = errors.New("range: offset must be non-negative")
	ErrInvalidLength   = errors.New("range: length must be strictly positive")
	ErrRangeOverflow   = errors.New("range: offset and length arithmetic overflow")
	ErrBaseUnavailable = errors.New("range: base source is unavailable")
)

// RangeTarget 描述一个 range 所引用的源文件区间。
type RangeTarget struct {
	FileHash string // 源文件的 SHA-256
	Offset   int64  // 在源文件中的起始字节偏移 (>= 0)
	Length   int64  // 该区间的字节长度 (> 0)
}

// RangeSource 引用式数据源。
type RangeSource struct {
	name     string
	base     Source
	ranges   map[string]RangeTarget
	mu       sync.RWMutex
	priority int
}

// NewRangeSource 创建 RangeSource。
func NewRangeSource(name string, base Source) *RangeSource {
	if name == "" {
		name = "range"
	}
	return &RangeSource{
		name:   name,
		base:   base,
		ranges: make(map[string]RangeTarget),
	}
}

// Name 返回数据源唯一注册名。
func (s *RangeSource) Name() string { return s.name }

// Type 返回数据源类型标签。
func (s *RangeSource) Type() string { return "range" }

// Capabilities 声明能力位：支持流式分片读取、整文件取回、元数据查询与整区间哈希校验。
func (s *RangeSource) Capabilities() Capability {
	return CapStream | CapFile | CapVerify | CapMeta
}

// Priority 返回路由优先级。
func (s *RangeSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

// SetPriority 运行时调整路由优先级。
func (s *RangeSource) SetPriority(p int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.priority = p
}

// Available 检查基础数据源是否可用。
func (s *RangeSource) Available(ctx context.Context) bool {
	if s.base == nil {
		return false
	}
	return s.base.Available(ctx)
}

// RegisterRange 注册一个 byte range 映射：rangeHash -> (fileHash, offset, length)。
func (s *RangeSource) RegisterRange(rangeHash, fileHash string, offset, length int64) error {
	if err := validHash(rangeHash); err != nil {
		return fmt.Errorf("invalid range hash: %w", err)
	}
	if err := validHash(fileHash); err != nil {
		return fmt.Errorf("invalid file hash: %w", err)
	}
	if offset < 0 {
		return ErrInvalidOffset
	}
	if length <= 0 {
		return ErrInvalidLength
	}
	if offset > math.MaxInt64-length {
		return ErrRangeOverflow
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.ranges[rangeHash] = RangeTarget{
		FileHash: fileHash,
		Offset:   offset,
		Length:   length,
	}
	return nil
}

// UnregisterRange 注销指定 rangeHash 的映射。
func (s *RangeSource) UnregisterRange(rangeHash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.ranges[rangeHash]; ok {
		delete(s.ranges, rangeHash)
		return true
	}
	return false
}

// GetRange 获取指定 rangeHash 的区间信息。
func (s *RangeSource) GetRange(rangeHash string) (RangeTarget, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	target, ok := s.ranges[rangeHash]
	return target, ok
}

// Count 返回当前注册的 range 引用数量。
func (s *RangeSource) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ranges)
}

// Open 流式打开由 rangeHash 寻址的内容。
// offset 与 size 代表请求方针对「该 range 自身」的子区间偏移。
func (s *RangeSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if s.base == nil {
		return nil, ErrBaseUnavailable
	}

	target, ok := s.GetRange(hash)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRangeNotFound, hash)
	}

	// 1. 钳制局部偏移 offset：若超出该 range 的 length 则返回空流
	if offset < 0 {
		offset = 0
	}
	if offset >= target.Length {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}

	// 2. 计算映射到底层源文件的全局偏移与读取字节数
	actualSourceOffset := target.Offset + offset
	remainingInRange := target.Length - offset

	actualReadSize := remainingInRange
	if size >= 0 && size < remainingInRange {
		actualReadSize = size
	}

	// 3. 从底层源打开实际字节流
	rc, err := s.base.Open(ctx, target.FileHash, actualSourceOffset, actualReadSize)
	if err != nil {
		return nil, err
	}

	// 使用 LimitReader 限制读取边界，防止底层源超发
	limited := &limitReadCloser{
		r: io.LimitReader(rc, actualReadSize),
		c: rc,
	}

	// 4. 内容寻址安全校验：若请求为完整 range（offset == 0 且覆盖完整 length），通过 verifyReadCloser 进行最终校验
	if offset == 0 && (size < 0 || size == target.Length) {
		return &verifyReadCloser{
			r:     limited,
			hash:  hash,
			label: "range",
		}, nil
	}

	return limited, nil
}

// Fetch 整文件取回。
func (s *RangeSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	rc, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Info 元数据查询。
func (s *RangeSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	target, ok := s.GetRange(hash)
	if !ok {
		return nil, nil // 不存在返回 nil, nil 符合 Source 契约
	}
	return &FileMeta{
		Hash: hash,
		Size: target.Length,
	}, nil
}

// limitReadCloser 将 io.LimitReader 与底层的 io.Closer 封装组合。
type limitReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *limitReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitReadCloser) Close() error               { return l.c.Close() }
