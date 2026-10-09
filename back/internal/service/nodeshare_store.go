package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
)

const shareAllToken = "all"
const shareScopeFile = "share_scope.json"
const levelCacheTTL = 10 * time.Second

// scopeFromConfig 解析环境变量里的共享声明（运行时状态的初值）。
//
// 环境变量里没法表达级别（一行一个目录，后面跟级别太易错），所以播种出来的
// 一律是 public——与"配了就是想共享"的意图一致。要别的级别在管理台改。
func scopeFromConfig(cfg *config.Config) ShareScope {
	sc := ShareScope{Enable: cfg.ShareEnable}
	for _, h := range strings.Split(cfg.ShareCollections, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if h == shareAllToken {
			sc.Collections = append(sc.Collections, ShareItem{ID: shareAllToken})
			continue
		}
		// 只接受 64hex 合集 hash：配错了（写成名字/短 hash）宁可忽略并告警，
		// 不要让它变成一个永远查不到的"幽灵共享项"。
		if !isSHA256Hex(h) {
			log.LogWarn("nodeshare: ignore invalid collection hash %q in PEERDRIVE_SHARE_COLLECTIONS", h)
			continue
		}
		sc.Collections = append(sc.Collections, ShareItem{ID: strings.ToLower(h)})
	}
	// 与 main.go 注册可读根、FileService.isPathAllowed 用同一份拆分逻辑
	// （pathutil.SplitList）：三处对「哪些目录算共享目录」的理解必须一致，
	// 否则又会出现「清单列得出、拉不到」。
	dirs, err := normalizeDirs(pathutil.SplitList(cfg.ShareDirs))
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
	} else {
		sc.Dirs = withLevels(nil, dirs) // 环境变量表达不了级别 → public
	}
	if friends, err := normalizeFriends(pathutil.SplitList(cfg.ShareFriends)); err != nil {
		log.LogWarn("nodeshare: %v", err)
	} else {
		sc.Friends = friends
	}
	return sc
}

// SetDirHook 注入"新增共享目录"回调（注册可读根）。

func (s *NodeShare) persistLocked(next ShareScope) error {
	old := s.scope
	s.scope = next
	s.levels = nil // 级别缓存立即失效（不等 TTL）
	if s.path == "" {
		// 内存模式：无盘可落，成功（回调仍然要发，语义一致）
		s.notifyDirsLocked(old, next)
		return nil
	}
	if err := s.save(next); err != nil {
		return err
	}
	s.notifyDirsLocked(old, next)
	return nil
}

// notifyDirsLocked 把**新增**的目录交给回调（旧的已经注册过，不重复）。
func (s *NodeShare) notifyDirsLocked(old, next ShareScope) {
	if s.onDirs == nil {
		return
	}
	have := make(map[string]bool, len(old.Dirs))
	for _, d := range old.Dirs {
		have[dirKey(d.ID)] = true
	}
	var added []string
	for _, d := range next.Dirs {
		if !have[dirKey(d.ID)] {
			added = append(added, d.ID)
		}
	}
	if len(added) > 0 {
		s.onDirs(added)
	}
}

// load 读取落盘状态。ok=false 表示没有可用状态（文件不存在/损坏）。
//
// 损坏时不删原文件（人工还能查），按"未保存过"处理 —— 与 node_directory 一致。
func (s *NodeShare) load() (ShareScope, bool) {
	if s.path == "" {
		return ShareScope{}, false
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.LogWarn("nodeshare: read %s failed: %v", s.path, err)
		}
		return ShareScope{}, false
	}
	var sc ShareScope
	if err := json.Unmarshal(raw, &sc); err != nil {
		log.LogWarn("nodeshare: parse %s failed: %v", s.path, err)
		return ShareScope{}, false
	}
	// 落盘内容也要过一遍校验：手改过的文件不该把非法值带进共享清单
	dirs, err := normalizeDirs(idsOf(sc.Dirs))
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	files, err := normalizeFileHashes(idsOf(sc.Files))
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	colls, err := normalizeCollections(idsOf(sc.Collections))
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	friends, err := normalizeFriends(sc.Friends)
	if err != nil {
		log.LogWarn("nodeshare: %v", err)
		return ShareScope{}, false
	}
	// 级别非法 → 退回 public（落盘文件是"已经生效过的状态"，宁可放宽也别让
	// 内容悄悄取不到；与 HTTP 入口的"整批拒绝"不同，那里是用户刚填的）
	dirItems := withLevels(sc.Dirs, dirs)
	fileItems := withLevels(sc.Files, files)
	collItems := withLevels(sc.Collections, colls)
	return ShareScope{Enable: sc.Enable, Dirs: dirItems, Files: fileItems, Collections: collItems, Friends: friends}, true
}

// save 原子落盘：临时文件 + rename。
// 为什么不能直接 WriteFile：进程被 kill / 断电会留下半截 JSON，下次启动解析失败
// → 悄悄退回环境变量初值，运营者精心选好的共享范围就这么丢了。
func (s *NodeShare) save(sc ShareScope) error {
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	// 目录可能还不存在（节点刚起、还没上传/拉取过任何文件）：先建出来，
	// 否则首次保存共享范围就会 ENOENT。走 pathutil 的安全建目录（父目录作根）。
	if err := pathutil.SafeMkdirAllAny([]string{filepath.Dir(dir)}, dir, 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	// 走 pathutil 的安全写：路径来自配置（storageDir），落盘与边界判定同一套
	// 口径，别再开一个"判完再按路径写"的 TOCTOU 窗口。
	if err := pathutil.SafeWriteFileAny([]string{dir}, tmp, raw, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot 解析当前共享范围（匿名视角，share 帧之外的场景用）。
