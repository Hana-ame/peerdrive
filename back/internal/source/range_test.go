package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBaseSource 仅用于单元测试的受控基类数据源。
type mockBaseSource struct {
	files map[string][]byte
}

func newMockBaseSource() *mockBaseSource {
	return &mockBaseSource{files: make(map[string][]byte)}
}

func (m *mockBaseSource) Name() string            { return "mock" }
func (m *mockBaseSource) Type() string            { return "mock" }
func (m *mockBaseSource) Capabilities() Capability { return CapStream | CapFile | CapVerify }
func (m *mockBaseSource) Priority() int           { return 0 }
func (m *mockBaseSource) SetPriority(int)         {}
func (m *mockBaseSource) Available(context.Context) bool { return true }

func (m *mockBaseSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	data, ok := m.files[hash]
	if !ok {
		return nil, fmt.Errorf("mock: file not found %s", hash)
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= int64(len(data)) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	end := int64(len(data))
	if size >= 0 && offset+size < end {
		end = offset + size
	}
	return io.NopCloser(bytes.NewReader(data[offset:end])), nil
}

func (m *mockBaseSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	data, ok := m.files[hash]
	if !ok {
		return nil, fmt.Errorf("mock: file not found %s", hash)
	}
	return data, nil
}

func (m *mockBaseSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	data, ok := m.files[hash]
	if !ok {
		return nil, nil
	}
	return &FileMeta{Hash: hash, Size: int64(len(data))}, nil
}

func sha256Of(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestRangeSource_BasicOperations 验证基础区间注册、读取与元数据返回。
// 发现背景：Issue #325 要求支持将源文件的字节区间以独立的 rangeHash 注册并流式读取。
func TestRangeSource_BasicOperations(t *testing.T) {
	base := newMockBaseSource()
	// 构造一个 100 字节的测试文件，分为 3 段区间：[0, 30), [30, 70), [70, 100)
	fileContent := make([]byte, 100)
	for i := range fileContent {
		fileContent[i] = byte(i)
	}
	fileHash := sha256Of(fileContent)
	base.files[fileHash] = fileContent

	range1Bytes := fileContent[0:30]
	range1Hash := sha256Of(range1Bytes)

	range2Bytes := fileContent[30:70]
	range2Hash := sha256Of(range2Bytes)

	src := NewRangeSource("test-range", base)

	// 1. 注册两个区间
	require.NoError(t, src.RegisterRange(range1Hash, fileHash, 0, 30))
	require.NoError(t, src.RegisterRange(range2Hash, fileHash, 30, 40))
	assert.Equal(t, 2, src.Count())

	// 2. Fetch 读取整区间（触发验证）
	got1, err := src.Fetch(context.Background(), range1Hash)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(range1Bytes, got1))

	got2, err := src.Fetch(context.Background(), range2Hash)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(range2Bytes, got2))

	// 3. Open 子区间读取 (在 range2 内读取偏移 5、长度 10)
	rc, err := src.Open(context.Background(), range2Hash, 5, 10)
	require.NoError(t, err)
	defer rc.Close()
	subSlice, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, range2Bytes[5:15], subSlice)

	// 4. Info 元数据
	meta, err := src.Info(context.Background(), range2Hash)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, range2Hash, meta.Hash)
	assert.Equal(t, int64(40), meta.Size)

	// 5. 注销映射
	assert.True(t, src.UnregisterRange(range1Hash))
	assert.Equal(t, 1, src.Count())
	_, err = src.Fetch(context.Background(), range1Hash)
	assert.ErrorIs(t, err, ErrRangeNotFound)
}

// TestRangeSource_SecurityAndBoundary 验证安全边界：非法哈希防路径注入、数值溢出与截断防越权读取。
// 发现背景：针对外部攻击者可能构造的越界偏移、负数长度、路径注入及整数溢出 payload，验证防护严密性。
func TestRangeSource_SecurityAndBoundary(t *testing.T) {
	base := newMockBaseSource()
	src := NewRangeSource("test-range-sec", base)
	const validHash1 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	const validHash2 = "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"

	t.Run("path traversal attack injection via hash", func(t *testing.T) {
		maliciousHashes := []string{
			"../../etc/passwd",
			"../" + validHash1,
			"valid_start_but_with_null\x00_byte",
			strings.Repeat("a", 63), // too short
			strings.Repeat("a", 65), // too long
			strings.Repeat("Z", 64), // uppercase not allowed
		}
		for _, mh := range maliciousHashes {
			err := src.RegisterRange(mh, validHash2, 0, 100)
			assert.Error(t, err, "bad range hash %q must be rejected", mh)

			err = src.RegisterRange(validHash1, mh, 0, 100)
			assert.Error(t, err, "bad file hash %q must be rejected", mh)

			_, err = src.Open(context.Background(), mh, 0, 10)
			assert.Error(t, err, "bad hash in Open must be rejected")
		}
	})

	t.Run("offset and length boundary checks", func(t *testing.T) {
		assert.ErrorIs(t, src.RegisterRange(validHash1, validHash2, -1, 100), ErrInvalidOffset)
		assert.ErrorIs(t, src.RegisterRange(validHash1, validHash2, 0, 0), ErrInvalidLength)
		assert.ErrorIs(t, src.RegisterRange(validHash1, validHash2, 0, -10), ErrInvalidLength)
	})

	t.Run("arithmetic integer overflow defense", func(t *testing.T) {
		// offset + length 超过 math.MaxInt64
		err := src.RegisterRange(validHash1, validHash2, math.MaxInt64-10, 20)
		assert.ErrorIs(t, err, ErrRangeOverflow)
	})

	t.Run("sub-offset out of bounds clamps safely", func(t *testing.T) {
		fileContent := []byte("hello-range-world")
		fHash := sha256Of(fileContent)
		base.files[fHash] = fileContent

		rBytes := fileContent[0:5] // "hello"
		rHash := sha256Of(rBytes)
		require.NoError(t, src.RegisterRange(rHash, fHash, 0, 5))

		// 请求 offset 超出该 range 的 length (5)
		rc, err := src.Open(context.Background(), rHash, 10, 5)
		require.NoError(t, err)
		defer rc.Close()
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Empty(t, data, "out of bounds offset must return empty reader without panic or leak")
	})
}

// TestRangeSource_TamperIntegrityVerification 验证读时完整性校验（防数据篡改）。
// 发现背景：验证在源文件物理内容被篡改导致与 rangeHash 不匹配时，verifyReadCloser 能够正确拦截并拒绝提供篡改数据。
func TestRangeSource_TamperIntegrityVerification(t *testing.T) {
	base := newMockBaseSource()
	src := NewRangeSource("test-range-tamper", base)

	origContent := []byte("secret-payload-authentic-version")
	fHash := sha256Of(origContent)
	base.files[fHash] = origContent

	// 正确注册 range [0, 14) -> "secret-payload"
	rangeBytes := origContent[0:14]
	rangeHash := sha256Of(rangeBytes)
	require.NoError(t, src.RegisterRange(rangeHash, fHash, 0, 14))

	// 模拟攻击者或外部写操作直接篡改了底层源文件中的字节
	tamperedContent := []byte("ATTACK-payload-authentic-version")
	base.files[fHash] = tamperedContent

	// 全量读取该 range 时，verifyReadCloser 必须在 EOF 拦截并返回哈希不匹配错误
	_, err := src.Fetch(context.Background(), rangeHash)
	assert.Error(t, err, "tampered range content must be rejected at EOF with hash mismatch")
}

// TestRangeSource_Concurrency 验证高并发注册与多协程并发读取。
// 发现背景：在多客户端或多调度器并行访问不同 range 引用时，内部 ranges 映射表必须保证零竞态。
func TestRangeSource_Concurrency(t *testing.T) {
	base := newMockBaseSource()
	content := bytes.Repeat([]byte("0123456789"), 100) // 1000 字节
	fHash := sha256Of(content)
	base.files[fHash] = content

	src := NewRangeSource("test-range-conc", base)

	const routines = 15
	const iterations = 40
	var wg sync.WaitGroup
	wg.Add(routines * 2)

	// 并发注册与注销
	for i := 0; i < routines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				sub := content[j*10 : (j+1)*10]
				rHash := sha256Of(sub)
				_ = src.RegisterRange(rHash, fHash, int64(j*10), 10)
				if j%5 == 0 {
					_ = src.UnregisterRange(rHash)
				}
			}
		}(i)
	}

	// 并发读取
	for i := 0; i < routines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				sub := content[j*10 : (j+1)*10]
				rHash := sha256Of(sub)
				_, _ = src.Fetch(context.Background(), rHash)
				_, _ = src.Info(context.Background(), rHash)
			}
		}(i)
	}

	wg.Wait()
}
