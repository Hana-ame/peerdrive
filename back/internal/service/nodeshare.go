package service

// nodeshare.go：节点共享范围解析（doc/NETDISK.md M2 / ROADMAP 阶段 5）。
//
// 职责：把运营者的共享声明（配置 + 本地状态）解析成一份 ShareSnapshot，
// 供两处消费：
//   - share 帧（对端问"你共享了什么"）→ transport.PeerJSService.SetShareProvider
//   - announce 的 loadInfo 摘要（只报数量）→ ShareSummary
//
// 默认关闭（PEERDRIVE_SHARE_ENABLE=false）：不显式开启就不对外暴露任何清单。
//
// 安全边界（为什么受限/私有合集一律跳过）：
// share 帧不携带请求者身份（ROADMAP 硬约束：第 7 阶段前不引入账号依赖）。
// 没有身份就无法校验 AccessList，因此受限/私有合集即使被写进
// PEERDRIVE_SHARE_COLLECTIONS 也必须跳过——否则一条 share 帧就能把
// "仅限指定账号"的合集清单和内容泄给任何连上的对端。

import (
	"path/filepath"
	"strings"

	"peerdrive/internal/pathutil"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/transport"
)

// shareAllToken 合集配置中代表"所有 public 合集"的取值。
const shareAllToken = "all"

// NodeShare 共享范围服务。
type NodeShare struct {
	enable      bool
	collections []string // 显式 hash 列表；含 shareAllToken 时表示全 public
	dirs        []string // 共享目录（绝对路径，前缀匹配）

	// anonGet/anonList/fileList 由 main 注入：避免 service 直接依赖
	// repository/transport 的具体装配（也便于单测注入假数据）。
	anonGet  func(hash string) (*model.AnonCollection, error)
	anonList func() ([]model.AnonCollectionSummary, error)
	fileList func() ([]transport.FileInfo, error)
}

// NewNodeShare 从配置构造。
func NewNodeShare(cfg *config.Config) *NodeShare {
	s := &NodeShare{enable: cfg.ShareEnable}
	for _, h := range strings.Split(cfg.ShareCollections, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if h == shareAllToken {
			s.collections = append(s.collections, shareAllToken)
			continue
		}
		// 只接受 64hex 合集 hash：配错了（写成名字/短 hash）宁可忽略并告警，
		// 不要让它变成一个永远查不到的"幽灵共享项"。
		if !isSHA256Hex(h) {
			log.LogWarn("nodeshare: ignore invalid collection hash %q in PEERDRIVE_SHARE_COLLECTIONS", h)
			continue
		}
		s.collections = append(s.collections, strings.ToLower(h))
	}
	// 与 main.go 注册可读根、FileService.isPathAllowed 用同一份拆分逻辑
	// （pathutil.SplitList）：三处对「哪些目录算共享目录」的理解必须一致，
	// 否则又会出现「清单列得出、拉不到」。
	for _, d := range pathutil.SplitList(cfg.ShareDirs) {
		abs, err := filepath.Abs(d)
		if err != nil {
			log.LogWarn("nodeshare: ignore invalid dir %q: %v", d, err)
			continue
		}
		s.dirs = append(s.dirs, abs)
	}
	if !s.enable {
		log.LogInfo("nodeshare: sharing disabled (PEERDRIVE_SHARE_ENABLE=false)")
	} else {
		log.LogInfo("nodeshare: sharing enabled collections=%d dirs=%d", len(s.collections), len(s.dirs))
	}
	return s
}

// Enabled 是否开启共享。
func (s *NodeShare) Enabled() bool { return s.enable }

// SetAnonAccess 注入匿合集读取器（main 装 service.AnonService 的两个方法）。
func (s *NodeShare) SetAnonAccess(
	get func(hash string) (*model.AnonCollection, error),
	list func() ([]model.AnonCollectionSummary, error),
) {
	s.anonGet = get
	s.anonList = list
}

// SetFileLister 注入文件索引列举器（main 装 fileIndex.List 适配）。
func (s *NodeShare) SetFileLister(fn func() ([]transport.FileInfo, error)) { s.fileList = fn }

// Snapshot 解析当前共享范围（share 帧的数据源）。
// 未开启共享 → 空快照（不是错误：对方未共享内容是合法业务状态）。
func (s *NodeShare) Snapshot() transport.ShareSnapshot {
	snap := transport.ShareSnapshot{
		Collections: []transport.ShareCollectionInfo{},
		Files:       []transport.ShareFileInfo{},
	}
	if !s.enable {
		return snap
	}
	snap.Collections = s.collectionsSnapshot()
	snap.Files = s.filesSnapshot()
	snap.Dirs = append([]string{}, s.dirs...)
	return snap
}

// Summary 共享摘要（announce loadInfo 用，只含数量）。
func (s *NodeShare) Summary() model.NodeShares {
	if !s.enable {
		return model.NodeShares{}
	}
	snap := s.Snapshot()
	return model.NodeShares{
		Collections: len(snap.Collections),
		Files:       len(snap.Files),
		Dirs:        len(snap.Dirs),
	}
}

// collectionsSnapshot 解析合集共享清单。
func (s *NodeShare) collectionsSnapshot() []transport.ShareCollectionInfo {
	if s.anonGet == nil {
		return []transport.ShareCollectionInfo{}
	}
	hashes := s.collections
	if containsToken(s.collections, shareAllToken) {
		// "all" = 所有 public 合集（需要列表接口）
		if s.anonList == nil {
			return []transport.ShareCollectionInfo{}
		}
		list, err := s.anonList()
		if err != nil {
			log.LogWarn("nodeshare: list collections failed: %v", err)
			return []transport.ShareCollectionInfo{}
		}
		hashes = make([]string, 0, len(list))
		for _, c := range list {
			// 空 visibility 视为 public（与 model.EffectiveVisibility 一致）
			if c.Visibility == "" || c.Visibility == model.VisibilityPublic {
				hashes = append(hashes, c.Hash)
			}
		}
	}

	out := make([]transport.ShareCollectionInfo, 0, len(hashes))
	for _, h := range hashes {
		if h == shareAllToken {
			continue
		}
		coll, err := s.anonGet(h)
		if err != nil || coll == nil {
			// 显式声明但读不到（已删/写错）：跳过并告警，不伪造条目
			log.LogDebug("nodeshare: collection %s not readable: %v", h, err)
			continue
		}
		if vis := coll.EffectiveVisibility(); vis != model.VisibilityPublic {
			// 见文件头安全边界注释：无请求者身份 → 受限/私有一律不共享
			log.LogInfo("nodeshare: skip non-public collection %s (visibility=%s)", h, vis)
			continue
		}
		info := transport.ShareCollectionInfo{
			Hash:    h,
			Name:    coll.FriendlyName,
			Tags:    coll.Tags,
			Entries: make([]transport.ShareEntryInfo, 0, len(coll.Entries)),
		}
		for _, e := range coll.Entries {
			info.Entries = append(info.Entries, transport.ShareEntryInfo{
				Path: e.Path,
				Hash: e.GetPrimaryHash(),
				Mime: e.GetPrimaryMime(),
			})
		}
		info.Size = int64(len(info.Entries))
		out = append(out, info)
	}
	return out
}

// filesSnapshot 按配置目录前缀过滤已登记文件。
//
// 为什么前缀匹配就够：文件本身来自 file_index（登记时已过
// IsPathAllowed —— 必须在上传根目录内），这里只是"在上传根目录里再划一个
// 更小的对外可见子集"。真正的读越权由 serveFile 的路径校验兜底。
func (s *NodeShare) filesSnapshot() []transport.ShareFileInfo {
	if len(s.dirs) == 0 || s.fileList == nil {
		return []transport.ShareFileInfo{}
	}
	files, err := s.fileList()
	if err != nil {
		log.LogWarn("nodeshare: list files failed: %v", err)
		return []transport.ShareFileInfo{}
	}
	out := make([]transport.ShareFileInfo, 0, len(files))
	for _, f := range files {
		if f.Path == "" || f.Delete {
			continue
		}
		if !s.underShareDir(f.Path) {
			continue
		}
		out = append(out, transport.ShareFileInfo{
			Hash: f.Hash,
			Name: f.Name,
			// 只回相对展示路径，不回本机绝对路径（对外最小信息原则，
			// 与 inbound.go 的 redactDisallowedPath 同一考虑）
			Path: filepath.Base(f.Path),
			Size: f.Size,
		})
	}
	return out
}

// underShareDir 判断已登记文件的路径是否落在某个共享目录内。
//
// 走 pathutil.Within 而不是手写前缀比较：手写的那份只认 `dir + 分隔符` 一种写法，
// 在 Windows 上会漏（盘符大小写、`/` 与 `\` 混写），也拦不住 `/a/shared-secret`
// 这类同名前缀目录。共享清单与读取侧必须用同一套判定，否则又会出现
// "清单列得出、拉取失败" 的口径不一致。
func (s *NodeShare) underShareDir(path string) bool {
	return pathutil.WithinAny(s.dirs, path)
}

func containsToken(list []string, tok string) bool {
	for _, v := range list {
		if v == tok {
			return true
		}
	}
	return false
}

// isSHA256Hex 64 位 hex 校验（小写/大写均可，调用方已转小写）。
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
