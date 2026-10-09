package twitterpic

// index.go: user → collection sha 指针文件。
//
// 集合本体存 sha-文件系统（内容寻址，可按 sha 读出）；「某个 user 最近一次
// 构建的集合是哪个 sha」是一个名字→地址的映射，本身不是内容寻址的（重建会
// 变化），落成一个小的 JSON 指针文件 storageDir/twitterpic/index.json，写路径
// 走 pathutil.SafeWriteFileAny（AGENTS 硬要求）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/pathutil"
)

// UserRecord 是 index.json 里一个用户的最新集合指针。
type UserRecord struct {
	SHA     string `json:"sha"`
	BuiltAt int64  `json:"built_at"`
	Entries int    `json:"entries"`
}

type indexDoc struct {
	Users map[string]UserRecord `json:"users"`
}

// UserIndex 管理 storageDir/twitterpic/index.json。
type UserIndex struct {
	storageDir string
	mu         sync.Mutex
}

// NewUserIndex 创建指针文件管理器。
func NewUserIndex(storageDir string) *UserIndex {
	return &UserIndex{storageDir: storageDir}
}

func (ui *UserIndex) file() string {
	return filepath.Join(ui.storageDir, "twitterpic", "index.json")
}

// Set 记录 user 的最新集合 sha（read-modify-write，带锁）。
func (ui *UserIndex) Set(username, sha string, entries int) error {
	if username == "" {
		return errors.New("twitterpic: empty username")
	}
	ui.mu.Lock()
	defer ui.mu.Unlock()
	doc, err := ui.loadLocked()
	if err != nil {
		return err
	}
	if doc.Users == nil {
		doc.Users = make(map[string]UserRecord)
	}
	doc.Users[username] = UserRecord{SHA: sha, BuiltAt: time.Now().Unix(), Entries: entries}
	data, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("twitterpic: index: marshal: %w", err)
	}
	if err := pathutil.SafeWriteFileAny([]string{ui.storageDir}, ui.file(), data, 0o644); err != nil {
		return fmt.Errorf("twitterpic: index: write: %w", err)
	}
	return nil
}

// Get 读一个用户的最新集合指针（不存在返回 ok=false）。
func (ui *UserIndex) Get(username string) (UserRecord, bool, error) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	doc, err := ui.loadLocked()
	if err != nil {
		return UserRecord{}, false, err
	}
	rec, ok := doc.Users[username]
	return rec, ok, nil
}

// List 返回全部用户指针（复制，调用方可安全遍历）。
func (ui *UserIndex) List() (map[string]UserRecord, error) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	doc, err := ui.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make(map[string]UserRecord, len(doc.Users))
	for k, v := range doc.Users {
		out[k] = v
	}
	return out, nil
}

// loadLocked 读 index.json；不存在按空文档处理（首次使用）。调用方持锁。
func (ui *UserIndex) loadLocked() (*indexDoc, error) {
	doc := &indexDoc{Users: make(map[string]UserRecord)}
	data, err := os.ReadFile(ui.file())
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return nil, fmt.Errorf("twitterpic: index: read: %w", err)
	}
	if len(data) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(data, doc); err != nil {
		return nil, fmt.Errorf("twitterpic: index: parse: %w", err)
	}
	if doc.Users == nil {
		doc.Users = make(map[string]UserRecord)
	}
	return doc, nil
}
