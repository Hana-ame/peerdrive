// 本文件定义跑在 KindMetadata / KindCheck / KindControl 之上的几种常用载荷。
//
// 它们只是约定，不是协议强制项：上层完全可以自己 json.Marshal 任意结构发出去。
// 这里给出默认形状，是为了让 Go 端与浏览器 demo 对得上。
package webrtc

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"time"
)

// FileInfo 是文件或目录的 metadata。
//
// Path 是相对路径（用 / 分隔）；Size 对目录为 0；Mode 是权限位；
// MTime 是 Unix 微秒；SHA 是文件内容的 sha256 十六进制（目录为空）。
type FileInfo struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mode  uint32 `json:"mode"`
	IsDir bool   `json:"is_dir"`
	MTime int64  `json:"mtime"`
	Mime  string `json:"mime,omitempty"`
	SHA   string `json:"sha256,omitempty"`
}

// Manifest 是一次传输里所有条目的清单。
type Manifest struct {
	Root    string     `json:"root"`
	Entries []FileInfo `json:"entries"`
	Total   int64      `json:"total_bytes"`
	Count   int        `json:"count"`
	At      int64      `json:"at"`
}

// NewManifest 汇总 entries 得到一个清单。
func NewManifest(root string, entries []FileInfo) *Manifest {
	m := &Manifest{Root: root, Entries: entries, Count: len(entries), At: time.Now().UnixMilli()}
	for _, e := range entries {
		if !e.IsDir {
			m.Total += e.Size
		}
	}
	return m
}

// Check 是一条校验结果。Algo 目前只有 sha256；OK 仅在回执中出现。
type Check struct {
	Stream  uint32 `json:"stream"`
	Path    string `json:"path,omitempty"`
	Algo    string `json:"algo"`
	Digest  string `json:"digest"`
	Size    int64  `json:"size,omitempty"`
	OK      *bool  `json:"ok,omitempty"`
	Note    string `json:"note,omitempty"`
	Elapsed int64  `json:"elapsed_ms,omitempty"`
}

// ControlAction 是控制信号的取值。
const (
	ActionBegin    = "begin"
	ActionProgress = "progress"
	ActionPause    = "pause"
	ActionResume   = "resume"
	ActionCancel   = "cancel"
	ActionEnd      = "end"
	ActionPing     = "ping"
	ActionPong     = "pong"
)

// HashFile 计算文件的 sha256 与大小。size 取实际读到的字节数，
// 这样即使文件在读取过程中被追加，两者也是一致的。
func HashFile(path string) (digest string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// HashBytes 计算一段字节的 sha256。
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// InfoOf 读一个本地路径并生成 FileInfo（目录不计算 sha）。
func InfoOf(path, rel string) (FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	fi := FileInfo{
		Path:  rel,
		Name:  st.Name(),
		Mode:  uint32(st.Mode().Perm()),
		IsDir: st.IsDir(),
		MTime: st.ModTime().UnixMicro(),
	}
	if !st.IsDir() {
		fi.Size = st.Size()
		sha, _, err := HashFile(path)
		if err != nil {
			return FileInfo{}, err
		}
		fi.SHA = sha
	}
	return fi, nil
}
