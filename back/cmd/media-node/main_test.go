package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"peerdrive/internal/repository"
	"peerdrive/internal/source"
	"peerdrive/internal/transport"
)

// fakeSender captures SendJSON messages and binary chunk data.
type fakeSender struct {
	mu     sync.Mutex
	frames []Msg
	chunks [][]byte
}

func (f *fakeSender) SendJSON(v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var m Msg
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	f.frames = append(f.frames, m)
	return nil
}

func (f *fakeSender) Send(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]byte, len(data))
	copy(copied, data)
	f.chunks = append(f.chunks, copied)
	return nil
}

func TestGuessMime(t *testing.T) {
	// 发现背景：media-node 媒体类型嗅探覆盖与回退逻辑验证
	assert.Equal(t, "video/mp4", guessMime("test.mp4", ""))
	assert.Equal(t, "image/jpeg", guessMime("test.jpg", ""))
	assert.Equal(t, "image/png", guessMime("test.png", ""))
	assert.Equal(t, "image/webp", guessMime("test.webp", ""))
	assert.Equal(t, "audio/mpeg", guessMime("test.mp3", ""))
	assert.Equal(t, "custom/mime", guessMime("test.mp4", "custom/mime"))
	assert.Equal(t, "application/octet-stream", guessMime("test.bin", ""))
}

func TestServeShaRequest_Validation(t *testing.T) {
	// 发现背景：Issue #101 支持 SHA/CAS 取数帧，缺少 hash/sha 或非法 hex 必须在首帧拒绝
	mgr := source.New()

	t.Run("empty hash and sha", func(t *testing.T) {
		sender := &fakeSender{}
		serveShaRequest(sender, Msg{Type: "req", ReqID: "r1"}, mgr, 1024)
		require.Len(t, sender.frames, 1)
		assert.Equal(t, "err", sender.frames[0].Type)
		assert.Equal(t, "hash or sha required", sender.frames[0].Msg)
		assert.Equal(t, "r1", sender.frames[0].ReqID)
	})

	t.Run("invalid length", func(t *testing.T) {
		sender := &fakeSender{}
		serveShaRequest(sender, Msg{Type: "sha", SHA: "abc", ReqID: "r2"}, mgr, 1024)
		require.Len(t, sender.frames, 1)
		assert.Equal(t, "err", sender.frames[0].Type)
		assert.Equal(t, "invalid hash: must be 64-char hex", sender.frames[0].Msg)
	})

	t.Run("non-hex characters", func(t *testing.T) {
		sender := &fakeSender{}
		badHash := fmt.Sprintf("%063sZ", "0")
		serveShaRequest(sender, Msg{Type: "req", Hash: badHash, ReqID: "r3"}, mgr, 1024)
		require.Len(t, sender.frames, 1)
		assert.Equal(t, "err", sender.frames[0].Type)
		assert.Equal(t, "invalid hash: non-hex character", sender.frames[0].Msg)
	})
}

func TestServeShaRequest_CASFetch(t *testing.T) {
	// 发现背景：Issue #101 media-node 通过 source.Manager 从本地 CAS 读取文件并分块传输
	storageDir := t.TempDir()
	content := []byte("hello media-node sha content stream!")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	// 准备本地 CAS 目录 storage/<hash[:2]>/<hash>
	casDir := filepath.Join(storageDir, hash[:2])
	require.NoError(t, os.MkdirAll(casDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(casDir, hash), content, 0644))

	fileIdx := transport.NewFileIndexService(storageDir)
	mgr := source.New()
	require.NoError(t, mgr.Register(source.NewLocalSource(storageDir, fileIdx)))

	t.Run("type req with hash", func(t *testing.T) {
		sender := &fakeSender{}
		serveShaRequest(sender, Msg{Type: "req", Hash: hash, ReqID: "req-1"}, mgr, 8) // 小 chunk 验证多块传输

		require.Len(t, sender.frames, 2)
		meta := sender.frames[0]
		assert.Equal(t, "meta", meta.Type)
		assert.Equal(t, 200, meta.Status)
		assert.Equal(t, int64(len(content)), meta.Size)
		assert.Equal(t, int64(len(content)), meta.Total)
		assert.Equal(t, hash, meta.Hash)
		assert.Equal(t, "req-1", meta.ReqID)

		done := sender.frames[1]
		assert.Equal(t, "done", done.Type)
		assert.Equal(t, "req-1", done.ReqID)
		assert.Equal(t, hash, done.Hash)

		// 检查传输的所有数据块并拼接
		var reassembled []byte
		for _, chunk := range sender.chunks {
			reassembled = append(reassembled, chunk...)
		}
		assert.Equal(t, content, reassembled)
	})

	t.Run("type sha with sha field", func(t *testing.T) {
		sender := &fakeSender{}
		serveShaRequest(sender, Msg{Type: "sha", SHA: hash, ReqID: "sha-1"}, mgr, 1024)

		require.Len(t, sender.frames, 2)
		assert.Equal(t, "meta", sender.frames[0].Type)
		assert.Equal(t, "done", sender.frames[1].Type)
		assert.Equal(t, "sha-1", sender.frames[1].ReqID)

		var reassembled []byte
		for _, chunk := range sender.chunks {
			reassembled = append(reassembled, chunk...)
		}
		assert.Equal(t, content, reassembled)
	})

	t.Run("missing hash not in storage", func(t *testing.T) {
		sender := &fakeSender{}
		missSum := sha256.Sum256([]byte("non-existent"))
		missing := hex.EncodeToString(missSum[:])
		serveShaRequest(sender, Msg{Type: "req", Hash: missing, ReqID: "miss-1"}, mgr, 1024)

		require.Len(t, sender.frames, 1)
		assert.Equal(t, "err", sender.frames[0].Type)
		assert.Contains(t, sender.frames[0].Msg, "fetch failed")
	})
}

func TestServeShaRequest_FileIndexAndRange(t *testing.T) {
	// 发现背景：Issue #101 支持带扩展名文件索引的 mime 解析与 range 读取能力
	storageDir := t.TempDir()
	dbPath := filepath.Join(storageDir, "peerdrive.db")
	err := repository.InitDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = repository.CloseDB()
	})

	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	// 写入 storage 并在 file_index 中登记带有 .mp4 扩展名的条目
	filePath := filepath.Join(storageDir, "sample.mp4")
	require.NoError(t, os.WriteFile(filePath, content, 0644))

	fileIdx := transport.NewFileIndexService(storageDir)
	_, err = fileIdx.Create(filePath)
	require.NoError(t, err)

	mgr := source.New()
	require.NoError(t, mgr.Register(source.NewLocalSource(storageDir, fileIdx)))

	sender := &fakeSender{}
	serveShaRequest(sender, Msg{
		Type:   "req",
		Hash:   hash,
		Offset: 10,
		Size:   6,
		ReqID:  "range-1",
	}, mgr, 1024)

	require.Len(t, sender.frames, 2)
	assert.Equal(t, "meta", sender.frames[0].Type)
	assert.Equal(t, "video/mp4", sender.frames[0].Mime) // 嗅探到 sample.mp4 的视频 mime

	var reassembled []byte
	for _, chunk := range sender.chunks {
		reassembled = append(reassembled, chunk...)
	}
	assert.Equal(t, []byte("abcdef"), reassembled)
}
