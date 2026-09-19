// Package service 提供 Peerdrive 业务逻辑层，包括匿名集合管理、下载、P2P 传输、文件注册等功能。
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type AnonService struct {
	config *config.Config
}

// NewAnonService 创建一个新的匿名集合服务实例。
func NewAnonService(cfg *config.Config) *AnonService {
	return &AnonService{config: cfg}
}

func isRelativePath(p string) bool {
	return !filepath.IsAbs(p) && !strings.HasPrefix(p, "/")
}

var hashRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

func isValidHash(h string) bool {
	return hashRe.MatchString(h)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func isValidProviders(providers []model.Provider) bool {
	if len(providers) == 0 {
		return false
	}
	hasValid := false
	for _, p := range providers {
		if p.Type == "" && p.Value == "" {
			continue
		}
		hasValid = true
		switch p.Type {
		case "sha256":
			if !isValidHash(p.Value) {
				return false
			}
		case "url":
			u, err := url.ParseRequestURI(p.Value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return false
			}
		default:
			return false
		}
	}
	return hasValid
}

// CreateCollection 创建匿名集合（默认 public，兼容历史调用方/测试）。
func (s *AnonService) CreateCollection(name string, entries []model.AnonCollectionEntry, tags []string) (string, error) {
	return s.CreateCollectionWithVisibility(name, entries, tags, model.VisibilityPublic, nil, "")
}

// CreateCollectionWithVisibility 创建带可见性的匿名集合。
// 坑：集合是 content-addressed（hash = JSON 内容摘要），visibility/access_list/owner 一旦写进
// JSON 就参与摘要 —— 改权限等于生成新 hash 的新集合，调用方必须拿返回值当新的身份用。
func (s *AnonService) CreateCollectionWithVisibility(
	name string,
	entries []model.AnonCollectionEntry,
	tags []string,
	visibility string,
	accessList []string,
	owner string,
) (string, error) {
	defer log.LogDuration("AnonService.CreateCollectionWithVisibility")()
	log.LogDebug("anon-svc: CreateCollectionWithVisibility name=%s entries=%d visibility=%s owner=%s",
		name, len(entries), visibility, owner)

	if !model.IsValidVisibility(visibility) {
		err := fmt.Errorf("invalid visibility: %s", visibility)
		log.LogError("anon-svc: %v", err)
		return "", err
	}
	// restricted 必须有放行名单：否则这个集合除了 owner 谁也看不到，等于误设 private
	if visibility == model.VisibilityRestricted && len(accessList) == 0 {
		err := fmt.Errorf("access_list required for restricted visibility")
		log.LogError("anon-svc: %v", err)
		return "", err
	}

	// 规范化：struct literal 可能直接设置 Hash 但没设 Providers
	for i := range entries {
		entries[i].Normalize()
	}

	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			err := fmt.Errorf("invalid path: %s", e.Path)
			log.LogError("anon-svc: CreateCollectionWithVisibility invalid path: %s", e.Path)
			return "", err
		}
		if !strings.HasSuffix(e.Path, "/") && !isValidProviders(e.Providers) {
			err := fmt.Errorf("invalid providers for path: %s", e.Path)
			log.LogError("anon-svc: CreateCollectionWithVisibility invalid providers for path: %s", e.Path)
			return "", err
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	coll := model.NewAnonCollection(name, entries, tags)
	coll.Visibility = visibility
	coll.AccessList = accessList
	coll.Owner = owner
	jsonBytes, err := json.MarshalIndent(coll, "", "  ")
	if err != nil {
		log.LogError("anon-svc: CreateCollectionWithVisibility marshal failed: %v", err)
		return "", err
	}

	hash := sha256Hex(jsonBytes)

	targetDir := filepath.Join(s.config.StorageDir, hash[:2])
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.LogError("anon-svc: CreateCollection mkdir failed: %v", err)
		return "", err
	}
	targetPath := filepath.Join(targetDir, hash)
	if err := os.WriteFile(targetPath, jsonBytes, 0644); err != nil {
		log.LogError("anon-svc: CreateCollection write failed: %v", err)
		return "", err
	}

	relPath := filepath.Join(hash[:2], hash)
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(jsonBytes)),
		MimeType: "application/json",
		Filename: "anon_collection.json",
		Gziped:   false,
		Type:     repository.FileTypeAnonCollection,
	})
	_ = repository.InsertFileProvider(hash, "local", relPath)

	log.LogInfo("anon-svc: CreateCollection %s -> hash=%s", name, hash)
	return hash, nil
}

// GetCollectionByHash 通过 SHA256 hash 从 content-addressed 存储中读取匿名集合。
func (s *AnonService) GetCollectionByHash(hash string) (*model.AnonCollection, error) {
	defer log.LogDuration("AnonService.GetCollectionByHash")()
	log.LogDebug("anon-svc: GetCollectionByHash hash=%s", hash)

	// 防御：hash 来自 URL 路径/请求体/远端 P2P sync，无长度校验时 hash[:2] 会切片越界 panic；
	// 非 64 位 hex（如 ".."）还会让 filepath.Join 逃逸 storage 目录。非法直接返回 not-found。
	if !isValidHash(hash) {
		log.LogWarn("anon-svc: GetCollectionByHash invalid hash=%q", hash)
		return nil, fmt.Errorf("collection not found")
	}

	filePath := filepath.Join(s.config.StorageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s not found: %v", hash, err)
		return nil, fmt.Errorf("collection not found")
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(data, &coll); err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s invalid JSON: %v", hash, err)
		return nil, fmt.Errorf("invalid collection json")
	}
	if coll.Version < 1 {
		log.LogError("anon-svc: GetCollectionByHash %s unsupported version: %d", hash, coll.Version)
		return nil, fmt.Errorf("unsupported version: %d", coll.Version)
	}
	coll.NormalizeEntries()
	log.LogInfo("anon-svc: GetCollectionByHash %s found (version=%d, entries=%d)", hash, coll.Version, len(coll.Entries))
	return &coll, nil
}

// ListCollections 返回所有已注册的匿名集合摘要列表。
func (s *AnonService) ListCollections() ([]model.AnonCollectionSummary, error) {
	return repository.ListAnonCollections(s.config.StorageDir)
}

// GetCollectionVisibleTo 按可见性给请求者返回集合：越权等价于不存在（404 语义）。
// requester 是当前请求绑定的账号；本地 WS 会话由 controller 传入本节点 operator，
// 因此运营者始终能看到自己的 private/restricted 合集。
// 坑：不能因为「拿不到账号」就放行——未认证只能看 public。
func (s *AnonService) GetCollectionVisibleTo(hash, requester string) (*model.AnonCollection, error) {
	coll, err := s.GetCollectionByHash(hash)
	if err != nil {
		return nil, err
	}
	if !coll.CanView(requester) {
		log.LogWarn("anon-svc: GetCollectionVisibleTo denied hash=%s requester=%q visibility=%s",
			hash, requester, coll.EffectiveVisibility())
		return nil, fmt.Errorf("collection not found")
	}
	return coll, nil
}

// UpdateCollectionVisibility 基于原集合生成一份换过权限的新集合。
// 背景：前端把「广播」改成三档开关，用户会随时切档；集合是 content-addressed，
// 权限写在 JSON 里参与摘要，所以切档必然产生新 hash —— 返回值是新的身份。
// 越权保护：只有 Owner（或历史无 Owner 的本机集合）能改。
func (s *AnonService) UpdateCollectionVisibility(hash, visibility string, accessList []string, requester string) (string, error) {
	defer log.LogDuration("AnonService.UpdateCollectionVisibility")()
	if !model.IsValidVisibility(visibility) {
		return "", fmt.Errorf("invalid visibility: %s", visibility)
	}
	if visibility == model.VisibilityRestricted && len(accessList) == 0 {
		return "", fmt.Errorf("access_list required for restricted visibility")
	}

	src, err := s.GetCollectionByHash(hash)
	if err != nil {
		return "", err
	}
	// 历史集合没有 Owner（权限字段上线前的产物），视为本机集合放行；
	// 一旦有 Owner 就必须匹配，防止拿到 hash 的第三方改写别人的权限档位。
	if src.Owner != "" && src.Owner != requester {
		log.LogWarn("anon-svc: UpdateCollectionVisibility denied hash=%s owner=%s requester=%q", hash, src.Owner, requester)
		return "", fmt.Errorf("collection not found")
	}

	dst := *src
	dst.Visibility = visibility
	dst.AccessList = accessList
	if dst.Owner == "" && requester != "" {
		dst.Owner = requester
	}
	return s.saveCollectionJSON(&dst)
}

// CommitCollection 基于源集合创建新版本，支持添加/更新/删除条目，自动递增版本号。
//
// 权限继承（2026-09-19 修复）：集合换 hash 就等于换了一个新 JSON，权限字段
// （Visibility/AccessList/Owner）不显式继承就会被丢掉 → 一个 restricted/private
// 合集只要被 commit 一次就变回 public，内容随新 hash 对外敞开。
// 因此这里整组复制源集合的权限字段；越权（requester 看不到源集合）直接拒绝，
// 语义与 GetCollectionVisibleTo 一致（拿不到 = 不存在）。
func (s *AnonService) CommitCollection(
	sourceHash string,
	entries []model.AnonCollectionEntry,
	commitMessage string,
	requester string,
) (string, error) {
	defer log.LogDuration("AnonService.CommitCollection")()
	log.LogDebug("anon-svc: CommitCollection sourceHash=%s entries=%d", sourceHash, len(entries))

	src, err := s.GetCollectionVisibleTo(sourceHash, requester)
	if err != nil {
		log.LogError("anon-svc: CommitCollection source %s not found: %v", sourceHash, err)
		return "", fmt.Errorf("source collection not found: %w", err)
	}

	// 规范化：struct literal / 旧格式可能只设了 Hash 而没设 Providers ——
	// 不归一化的话「只想加一个文件」会被误判成「删除该条目」（空 providers）。
	for i := range entries {
		entries[i].Normalize()
	}

	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			err := fmt.Errorf("invalid path: %s", e.Path)
			log.LogError("anon-svc: CommitCollection invalid path: %s", e.Path)
			return "", err
		}
		// 空 providers = 删除该条目（API 约定见 controller/anon.go 的
		// CommitAnonCollection 注释：非空 hash 增改 / 空 hash 删除）。
		// 坑：这里若沿用 CreateCollection 的校验直接拒绝空 providers，
		// 下面 removeEmpty 分支永远走不到——「commit 删除条目」这条路径
		// 拿到空 hash 请求会 400，功能形同不存在。
		if len(e.Providers) > 0 && !strings.HasSuffix(e.Path, "/") && !isValidProviders(e.Providers) {
			err := fmt.Errorf("invalid providers for path: %s", e.Path)
			log.LogError("anon-svc: CommitCollection invalid providers for path: %s", e.Path)
			return "", err
		}
	}

	// 用 providers 合并源条目和新条目
	entryMap := map[string][]model.Provider{}
	for _, e := range src.Entries {
		entryMap[e.Path] = e.Providers
	}
	for _, e := range entries {
		entryMap[e.Path] = e.Providers
	}

	sortedEntries := make([]model.AnonCollectionEntry, 0, len(entryMap))
	removeEmpty := false
	for path, providers := range entryMap {
		if len(providers) == 0 {
			removeEmpty = true
			continue
		}
		sortedEntries = append(sortedEntries, model.AnonCollectionEntry{Path: path, Providers: providers})
	}
	if removeEmpty {
		coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1, src.Tags)
		inheritVisibility(coll, src)
		return s.saveCollectionJSON(coll)
	}

	sort.Slice(sortedEntries, func(i, j int) bool {
		return sortedEntries[i].Path < sortedEntries[j].Path
	})

	coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1, src.Tags)
	inheritVisibility(coll, src)
	return s.saveCollectionJSON(coll)
}

// inheritVisibility 把源集合的权限三件套复制到派生集合（commit/版本滚动用）。
// 为什么单独一个函数：派生路径（commit、removeEmpty 分支）不止一处，漏掉任何
// 一处都会静默把受限合集降级为 public，集中在一处便于 review。
func inheritVisibility(dst, src *model.AnonCollection) {
	if dst == nil || src == nil {
		return
	}
	dst.Visibility = src.Visibility
	dst.AccessList = src.AccessList
	dst.Owner = src.Owner
}

func newAnonCollectionWithVersion(name string, entries []model.AnonCollectionEntry, version int, tags []string) *model.AnonCollection {
	if entries == nil {
		entries = []model.AnonCollectionEntry{}
	}
	if tags == nil {
		tags = []string{}
	}
	return &model.AnonCollection{
		Version:      version,
		FriendlyName: name,
		Entries:      entries,
		Tags:         tags,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *AnonService) saveCollectionJSON(coll *model.AnonCollection) (string, error) {
	sort.Slice(coll.Entries, func(i, j int) bool {
		return coll.Entries[i].Path < coll.Entries[j].Path
	})

	jsonBytes, err := json.MarshalIndent(coll, "", "  ")
	if err != nil {
		return "", err
	}

	hash := sha256Hex(jsonBytes)

	targetDir := filepath.Join(s.config.StorageDir, hash[:2])
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(targetDir, hash)
	if err := os.WriteFile(targetPath, jsonBytes, 0644); err != nil {
		return "", err
	}

	relPath := filepath.Join(hash[:2], hash)
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(jsonBytes)),
		MimeType: "application/json",
		Filename: "anon_collection.json",
		Gziped:   false,
		Type:     repository.FileTypeAnonCollection,
	})
	_ = repository.InsertFileProvider(hash, "local", relPath)

	return hash, nil
}
