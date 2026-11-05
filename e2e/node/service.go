package node

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Hana-ame/peerdrive/h2f/metadata"
	"github.com/Hana-ame/peerdrive/webrtc"
)

// copyChunk 是一次 SendBytes 的载荷上限，与 webrtc 的 MAX_PAYLOAD 对齐：
// 正好落在「单帧不切片」的边界内，一个区间不需要先拼出一个巨大的中间切片。
const copyChunk = 64 * 1024

// Service 把 h2f 的 collection 与文件内容变成帧协议应答。
type Service struct {
	store      *metadata.Store
	collection Collection
}

// Load 打开（必要时创建）h2f 数据库，扫描 dir，再用 Collection 按路径回查，
// 得到对外声明的 collection。
//
// 为什么回查而不是直接用 Index 的返回值：Index 只证明「登记写进去了」，
// Collection 证明「按路径能取回同一份元数据」。两步都过，浏览器拿 sha256
// 才一定取得到文件；这也正是 collection 契约要守的东西。
func Load(dir, dbPath, name string) (*Service, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("e2e: 解析 share 目录: %w", err)
	}
	if name == "" {
		name = filepath.Base(abs)
	}

	store, err := metadata.Open(dbPath)
	if err != nil {
		return nil, err
	}

	entries, err := store.Index(abs)
	if err != nil {
		store.Close()
		return nil, err
	}

	// collection 是「已登记路径的 []string」：目录本身也算路径，但这里只
	// 传文件——目录的元数据可以取，但按区间下载它没有意义。
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir {
			paths = append(paths, e.FullPath)
		}
	}
	sort.Strings(paths)

	metas, err := store.Collection(paths)
	if err != nil {
		store.Close()
		return nil, err
	}

	files := make([]FileRef, 0, len(metas))
	var total int64
	for _, m := range metas {
		files = append(files, FileRef{
			SHA256:   m.SHA256,
			Path:     m.FullPath,
			Filename: m.Filename,
			MimeType: m.MimeType,
			Size:     m.Size,
		})
		total += m.Size
	}

	return &Service{
		store: store,
		collection: Collection{
			Name:       name,
			Root:       abs,
			Files:      files,
			Count:      len(files),
			TotalBytes: total,
			At:         time.Now().UnixMilli(),
		},
	}, nil
}

// Collection 返回集合快照。
func (s *Service) Collection() Collection { return s.collection }

// Close 释放数据库。
func (s *Service) Close() error { return s.store.Close() }

// sender 是 ServeCollection / ServeFile 需要的出站能力。*webrtc.Conn 天然满足；
// 单测用内存实现就能验证字节与校验，不必起真连接。
type sender interface {
	SendJSON(kind webrtc.Kind, stream uint32, v any) error
	SendBytes(stream uint32, b []byte) error
}

// ServeCollection 把整个 collection 当一条 metadata 帧发出去。
func (s *Service) ServeCollection(c sender, stream uint32) error {
	return c.SendJSON(webrtc.KindMetadata, stream, s.collection)
}

// ServeFile 按 sha256 回一段文件内容：
//
//	metadata(区间头) → data × N → check(区间 sha256)
//
// 传区间而不是整个文件，是为了让浏览器把下载切成可并行的块；断点续传时
// 只补没完成的那几块。Length ≤ 0 或越过文件尾都裁到文件尾——调用方不必
// 先知道精确长度，最后一个块自然收口。
func (s *Service) ServeFile(c sender, stream uint32, req FileRequest) error {
	meta, err := s.store.Stat(req.SHA256)
	if err != nil {
		return sendError(c, stream, err)
	}
	if meta.IsDir {
		return sendError(c, stream, fmt.Errorf("e2e: %s 是目录，不能按文件区间传", req.SHA256))
	}

	offset := req.Offset
	if offset < 0 || offset > meta.Size {
		return sendError(c, stream, fmt.Errorf("e2e: offset %d 越界（size %d）", offset, meta.Size))
	}
	length := req.Length
	if length <= 0 || offset+length > meta.Size {
		length = meta.Size - offset
	}

	if err := c.SendJSON(webrtc.KindMetadata, stream, FileHeader{
		SHA256: meta.SHA256,
		Offset: offset,
		Length: length,
		Size:   meta.Size,
	}); err != nil {
		return err
	}

	f, err := os.Open(meta.FullPath)
	if err != nil {
		return sendError(c, stream, fmt.Errorf("e2e: open %s: %w", meta.FullPath, err))
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, copyChunk)
	sent := int64(0)
	for sent < length {
		n := int64(len(buf))
		if remaining := length - sent; remaining < n {
			n = remaining
		}
		read, readErr := f.ReadAt(buf[:n], offset+sent)
		if read > 0 {
			piece := buf[:read]
			h.Write(piece)
			if err := c.SendBytes(stream, piece); err != nil {
				return err
			}
			sent += int64(read)
		}
		if readErr != nil {
			if readErr == io.EOF && sent == length {
				break
			}
			return sendError(c, stream, fmt.Errorf("e2e: read %s @%d: %w", meta.FullPath, offset+sent, readErr))
		}
	}

	return c.SendJSON(webrtc.KindCheck, stream, RangeCheck{
		Stream: stream,
		SHA256: meta.SHA256,
		Offset: offset,
		Length: sent,
		Digest: hex.EncodeToString(h.Sum(nil)),
	})
}

func sendError(c sender, stream uint32, err error) error {
	return c.SendJSON(webrtc.KindError, stream, ErrorPayload{Message: err.Error()})
}
