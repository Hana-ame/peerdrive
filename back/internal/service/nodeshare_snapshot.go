package service

import (
	"path/filepath"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/transport"
)

func (s *NodeShare) Snapshot() transport.ShareSnapshot { return s.SnapshotFor("") }

// SnapshotFor 解析请求者可见的共享范围（share 帧的数据源）。
//
// 为什么要带 peerID：share 帧是点对点直连，请求者的节点 ID 是已知的（连接即
// 带过来），所以"好友能看到我的 private 清单"是可以实现的——好友也得知道有哪些
// 东西能取，否则 private 就成了"给了权限但没给目录"。
//
// 未开启共享 → 空快照（不是错误：对方未共享内容是合法业务状态）。
func (s *NodeShare) SnapshotFor(peerID string) transport.ShareSnapshot {
	snap := transport.ShareSnapshot{
		Collections: []transport.ShareCollectionInfo{},
		Files:       []transport.ShareFileInfo{},
	}
	sc := s.Scope()
	if !sc.Enable {
		return snap
	}
	friend := sc.isFriend(peerID)
	snap.Collections = s.collectionsSnapshotFor(sc.Collections, friend)
	snap.Files = s.filesSnapshotFor(sc, friend)
	// 只回 public 目录的路径摘要：private/unlisted 目录的存在本身就是信息
	for _, d := range sc.Dirs {
		if d.EffectiveLevel() == model.LevelPublic {
			snap.Dirs = append(snap.Dirs, d.ID)
		}
	}
	return snap
}

// Summary 共享摘要（announce loadInfo 用，只含数量）。
func (s *NodeShare) Summary() model.NodeShares {
	snap := s.Snapshot()
	return model.NodeShares{
		Collections: len(snap.Collections),
		Files:       len(snap.Files),
		Dirs:        len(snap.Dirs),
	}
}

// LevelOf 返回某个 hash 的共享级别（未声明 → 空串）。
func (s *NodeShare) LevelOf(hash string) string {
	if hash == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.levelMapLocked()[hash]
}

// AllowsDownload 判断请求者能否下载该 hash（传输层 req 的门禁）。
//
// 只有 private 会挡人：public / unlisted / **未声明** 都放行。
// 为什么未声明也放行：内容寻址取回（知道 hash 就能取）是这套系统的既有行为，
// PSK 才是准入门禁。改成"必须声明才能取"会让"上传→按 hash 取回校验"这种
// 最基本的自检都过不去，也让升级后所有老节点的下载全部断掉。
//
// self = 本节点的本地通道（HTTP 管理 API / 本机 WS 直连），由传输层判定。
func (s *NodeShare) AllowsDownload(peerID, hash string, self bool) bool {
	if self {
		return true
	}
	if s.LevelOf(hash) != model.LevelPrivate {
		return true
	}
	return s.Scope().isFriend(peerID)
}

// levelMapLocked 构建/返回级别缓存（调用方持锁）。
func (s *NodeShare) levelMapLocked() map[string]string {
	if s.levels != nil && time.Since(s.levelsAt) <= levelCacheTTL {
		return s.levels
	}
	m := make(map[string]string, len(s.scope.Files)+8)
	// 单文件勾选：按 hash 直接记
	for _, it := range s.scope.Files {
		if it.ID == "" {
			continue
		}
		m[it.ID] = model.LoosestLevel(m[it.ID], it.EffectiveLevel())
	}
	// 目录：路径落在目录内的文件继承该目录级别（多条取最宽松）
	if len(s.scope.Dirs) > 0 {
		for _, f := range s.resolveFiles(s.scope) {
			if f.Path == "" || f.Delete {
				continue
			}
			if lvl := dirLevelFor(s.scope.Dirs, f.Path); lvl != "" {
				m[f.Hash] = model.LoosestLevel(m[f.Hash], lvl)
			}
		}
	}
	// 合集：条目 hash 继承合集级别（合集自身非 public → 降为 private）
	for _, it := range s.scope.Collections {
		lvl := collectionLevelLocked(s, it)
		if lvl == "" {
			continue
		}
		for _, h := range s.collectionHashesLocked(it.ID) {
			m[h] = model.LoosestLevel(m[h], lvl)
		}
	}
	s.levels = m
	s.levelsAt = time.Now()
	return m
}

// collectionLevelLocked 合集条目的生效级别（含"合集自身 visibility 降级"）。
func collectionLevelLocked(s *NodeShare, it ShareItem) string {
	if it.ID == "" || s.anonGet == nil {
		return ""
	}
	lvl := it.EffectiveLevel()
	if it.ID == shareAllToken {
		return lvl
	}
	coll, err := s.anonGet(it.ID)
	if err != nil || coll == nil {
		return ""
	}
	// AccessList 无法校验（无身份）→ 受限/私有合集一律按 private：不列出，
	// 只有好友能取。见文件头安全边界第 2 条。
	if vis := coll.EffectiveVisibility(); vis != model.VisibilityPublic {
		return model.LevelPrivate
	}
	return lvl
}

// collectionHashesLocked 展开合集的条目 hash（"all" 展开成所有 public 合集）。
func (s *NodeShare) collectionHashesLocked(id string) []string {
	if s.anonGet == nil {
		return nil
	}
	ids := []string{id}
	if id == shareAllToken {
		if s.anonList == nil {
			return nil
		}
		all, err := s.anonList()
		if err != nil {
			log.LogWarn("nodeshare: list collections failed: %v", err)
			return nil
		}
		ids = make([]string, 0, len(all))
		for _, c := range all {
			// 空 visibility 视为 public（与 model.EffectiveVisibility 一致）
			if c.Visibility == "" || c.Visibility == model.VisibilityPublic {
				ids = append(ids, c.Hash)
			}
		}
	}
	out := make([]string, 0, len(ids))
	for _, h := range ids {
		coll, err := s.anonGet(h)
		if err != nil || coll == nil {
			continue
		}
		// 合集自身的 hash 也算进去：manifest 就是一份按内容寻址存的 JSON，凭
		// hash 能直接取回（面板的合集链接正是靠它）。漏了它 → private 合集的
		// manifest 会被陌生人取走：内容仍被条目级别挡着，但条目路径与 hash 全泄。
		out = append(out, h)
		for _, e := range coll.Entries {
			if eh := e.GetPrimaryHash(); eh != "" {
				out = append(out, eh)
			}
		}
	}
	return out
}

// CandidateFiles 可选文件清单（管理台勾选框的数据源）：file_index 里的文件
// + 每个文件当前是否已共享、什么级别。
//
// 为什么在这里算 shared 而不是让前端自己对比两份列表：目录共享与单文件共享是
// 两条来源，前端要正确显示勾选状态就得把目录前缀匹配再实现一遍——那份实现
// 迟早和后端不一致（Windows 盘符大小写、`/` 与 `\` 混写都是坑）。
func (s *NodeShare) CandidateFiles() []ShareFileItem {
	sc := s.Scope()
	files := s.resolveFiles(sc)
	sel := make(map[string]ShareItem, len(sc.Files))
	for _, it := range sc.Files {
		sel[it.ID] = it
	}
	out := make([]ShareFileItem, 0, len(files))
	dirs := idsOf(sc.Dirs)
	for _, f := range files {
		if f.Path == "" || f.Delete {
			continue
		}
		byDir := pathutil.WithinAny(dirs, f.Path)
		_, picked := sel[f.Hash]
		// 2026-09-26：声明了共享目录时，目录外且未被手动勾选的文件不再作为候选——
		// 切换共享目录不会把旧目录的文件一起带出来；已勾选的手动文件永远保留。
		if len(dirs) > 0 && !byDir && !picked {
			continue
		}
		shared := false
		lvl := ""
		if it, ok := sel[f.Hash]; ok {
			shared = true
			lvl = model.LoosestLevel(lvl, it.EffectiveLevel())
		}
		if dl := dirLevelFor(sc.Dirs, f.Path); dl != "" {
			shared = true
			lvl = model.LoosestLevel(lvl, dl)
		}
		out = append(out, ShareFileItem{
			Hash:   f.Hash,
			Name:   f.Name,
			Size:   f.Size,
			Shared: shared,
			ByDir:  byDir,
			Level:  lvl,
		})
	}
	return out
}

// collectionsSnapshotFor 解析合集共享清单（给定声明列表）。
//
// friend：请求者是好友时，private 级别的合集也列出来（否则好友拿到了权限却
// 不知道有什么）。unlisted 永远不列——它的语义就是"不列出"。
func (s *NodeShare) collectionsSnapshotFor(items []ShareItem, friend bool) []transport.ShareCollectionInfo {
	if s.anonGet == nil {
		return []transport.ShareCollectionInfo{}
	}
	list := make([]ShareItem, 0, len(items))
	for _, it := range items {
		if it.ID == shareAllToken {
			// "all" = 所有 public 合集，级别沿用这条声明的级别
			if s.anonList == nil {
				return []transport.ShareCollectionInfo{}
			}
			all, err := s.anonList()
			if err != nil {
				log.LogWarn("nodeshare: list collections failed: %v", err)
				return []transport.ShareCollectionInfo{}
			}
			for _, c := range all {
				// 空 visibility 视为 public（与 model.EffectiveVisibility 一致）
				if c.Visibility == "" || c.Visibility == model.VisibilityPublic {
					list = append(list, ShareItem{ID: c.Hash, Level: it.Level})
				}
			}
			continue
		}
		list = append(list, it)
	}

	out := make([]transport.ShareCollectionInfo, 0, len(list))
	for _, it := range list {
		coll, err := s.anonGet(it.ID)
		if err != nil || coll == nil {
			// 显式声明但读不到（已删/写错）：跳过并告警，不伪造条目
			log.LogDebug("nodeshare: collection %s not readable: %v", it.ID, err)
			continue
		}
		lvl := it.EffectiveLevel()
		if vis := coll.EffectiveVisibility(); vis != model.VisibilityPublic {
			// 见文件头安全边界第 2 条：AccessList 无法校验 → 按 private 处理
			lvl = model.LevelPrivate
		}
		if lvl == model.LevelUnlisted || (lvl == model.LevelPrivate && !friend) {
			continue
		}
		info := transport.ShareCollectionInfo{
			Hash:    it.ID,
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

// filesSnapshotFor 按"目录前缀 ∪ 单文件勾选"过滤已登记文件，再按级别决定
// 是否进入清单。
//
// 为什么前缀匹配就够：文件本身来自 file_index（登记时已过
// IsPathAllowed —— 必须在上传根目录内），这里只是"在上传根目录里再划一个
// 更小的对外可见子集"。真正的读越权由 serveFile 的路径校验兜底。
//
// 空 dirs 且空 files = 不共享任何文件（不是共享全部）：这是"默认关"在文件
// 维度的体现，空值被当成"不过滤"等于 PEERDRIVE_SHARE_ENABLE=true 就泄露
// 整个 file_index。
func (s *NodeShare) filesSnapshotFor(sc ShareScope, friend bool) []transport.ShareFileInfo {
	files := s.resolveFiles(sc)
	sel := make(map[string]ShareItem, len(sc.Files))
	for _, it := range sc.Files {
		sel[it.ID] = it
	}
	out := make([]transport.ShareFileInfo, 0, len(files))
	for _, f := range files {
		if f.Path == "" || f.Delete {
			continue
		}
		// 单文件勾选按 hash：与目录无关，可以共享"不在任何共享目录里的文件"。
		// 目录匹配走 pathutil.WithinAny 而不是手写前缀比较（理由见 underShareDir）。
		lvl := ""
		if it, ok := sel[f.Hash]; ok {
			lvl = model.LoosestLevel(lvl, it.EffectiveLevel())
		}
		if dl := dirLevelFor(sc.Dirs, f.Path); dl != "" {
			lvl = model.LoosestLevel(lvl, dl)
		}
		if lvl == "" || lvl == model.LevelUnlisted || (lvl == model.LevelPrivate && !friend) {
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

// resolveFiles 汇总"可能要共享的文件"：索引页（有上限）∪ 按 hash 勾选到的文件。
//
// 为什么要并上后者：fileList 有 1000 条上限（防远端 list verb 的 DoS），节点
// 登记的文件超过 1000 条时新上传的文件不在那一页里。只按那一页过滤的话，用户
// 勾了它却既列不出来也共享不出去——看到的现象是"我勾了，但什么都没发生"。
// 按 hash 单查不受分页影响，勾选过的文件永远算数。
func (s *NodeShare) resolveFiles(sc ShareScope) []transport.FileInfo {
	out := make([]transport.FileInfo, 0, len(sc.Files))
	seen := make(map[string]bool, len(sc.Files))
	if s.fileList != nil {
		files, err := s.fileList()
		if err != nil {
			log.LogWarn("nodeshare: list files failed: %v", err)
		}
		for _, f := range files {
			if f.Hash == "" || seen[f.Hash] {
				continue
			}
			seen[f.Hash] = true
			out = append(out, f)
		}
	}
	if s.fileInfo == nil {
		return out
	}
	for _, it := range sc.Files {
		if seen[it.ID] {
			continue
		}
		fi, err := s.fileInfo(it.ID)
		if err != nil || fi == nil {
			log.LogDebug("nodeshare: selected file %s not readable: %v", it.ID, err)
			continue
		}
		if fi.Path == "" || fi.Delete {
			continue
		}
		seen[fi.Hash] = true
		out = append(out, *fi)
	}
	return out
}

// underShareDir 判断已登记文件的路径是否落在某个共享目录内（保留给外部/测试）。
//
// 走 pathutil.Within 而不是手写前缀比较：手写的那份只认 `dir + 分隔符` 一种写法，
// 在 Windows 上会漏（盘符大小写、`/` 与 `\` 混写），也拦不住 `/a/shared-secret`
// 这类同名前缀目录。共享清单与读取侧必须用同一套判定，否则又会出现
// "清单列得出、拉取失败" 的口径不一致。
func (s *NodeShare) underShareDir(path string) bool {
	return pathutil.WithinAny(idsOf(s.Scope().Dirs), path)
}

// ---- 校验/归一化 ----

// idsOf 取出声明列表里的 id（喂给 pathutil / 旧接口用）。
