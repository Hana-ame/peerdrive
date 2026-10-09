package service

// nodeshare.go：节点共享范围解析（doc/NETDISK.md M2 / ROADMAP 阶段 5）。
//
// 职责：把运营者的共享声明解析成一份 ShareSnapshot，供两处消费：
//   - share 帧（对端问"你共享了什么"）→ transport.PeerJSService.SetShareProvider
//   - announce 的 loadInfo 摘要（只报数量）→ ShareSummary
//
// 默认关闭（PEERDRIVE_SHARE_ENABLE=false）：不显式开启就不对外暴露任何清单。
//
// 共享范围由**两个来源**合成，后者覆盖前者（doc/NETDISK.md M2.6）：
//  1. 环境变量（PEERDRIVE_SHARE_*）——只是**初值**：进程第一次启动时播种进
//     运行时状态并落盘，之后改环境变量不会再把已选择的范围改回去；
//  2. 运行时选择（管理台勾选 / PUT /peerjs/share）——落在 storage 下的
//     share_scope.json，重启后仍然有效。
//
// 为什么必须能运行时改：共享范围是"我愿意把哪些文件给出去"，本来就是随手的
// 决定（新上传一个文件想立刻共享、某个目录不想给了）。要求运营者改环境变量再
// 重启节点，等于把这个决定变成一次运维动作——实际结果是没人改，于是要么长期
// 共享一个过宽的目录，要么干脆不开共享。
//
// 粒度（三条互相独立的来源）：
//   - dirs：整个目录（file_index 里路径落在其中的文件全部共享）
//   - files：按 hash 单独勾选的文件（不必在某个共享目录里）
//   - collections：合集（64hex 或 "all" = 全部 public 合集）
//
// 级别（每一条声明都带一个，见 model.Level* 与 doc/NETDISK.md §12.6）：
//   - public   —— 出现在共享清单里，任何人都能下载
//   - unlisted —— 不出现在清单里，知道 hash 就能下载
//   - private  —— 不出现在清单里，只有自己和好友能下载
//
// 好友 = ShareScope.Friends 里的节点 ID 白名单。"自己" = 不经 P2P 的本地通道
// （HTTP 管理 API、本机 WS 直连），由传输层判定后传进来（见
// NodeShare.AllowsDownload 的 self 参数）。
//
// 安全边界（两条，都别放宽）：
//  1. share 帧不携带可校验的身份（ROADMAP 硬约束：第 7 阶段前不引入账号依赖），
//     peer id 由对端自报。因此 private 的判定只在**已通过 PSK 准入**的连接上
//     有意义——没设 PSK 时谁都能连上，好友名单就退化成"自称是这个 id 的人"。
//     要强身份得等账号体系，别在这里假装它有。
//  2. 合集自身的 visibility 非 public（restricted/private）时不进对外清单——
//     AccessList 是账号列表，无身份就无法校验。这类合集按 private 处理：
//     只给好友、不列出。

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
)

// NodeShare 共享范围服务。
type NodeShare struct {
	// mu 保护 scope / path 之外的全部可变字段（Snapshot 会并发调用）。
	mu    sync.Mutex
	scope ShareScope

	// path 运行时状态落盘位置（storageDir/share_scope.json）。
	// 空 = 不落盘（单元测试/纯内存场景），此时环境变量就是全部来源。
	path string

	// levels 级别缓存：hash → 级别（LevelOf 用，见 levelCacheTTL）。
	levels   map[string]string
	levelsAt time.Time

	// onDirs 新增共享目录的通知回调（main 注入：注册 file_index 可读根）。
	//
	// 为什么要回调：运行时新增的目录如果只进范围不注册可读根，就会出现
	// "清单列得出、对端一拉 read failed"——登记侧放行了，读取侧判它越权
	// （见 transport.FileIndexService.AddReadRoot 的注释）。
	onDirs func(dirs []string)

	// anonGet/anonList/fileList/fileInfo 由 main 注入：避免 service 直接依赖
	// repository/transport 的具体装配（也便于单测注入假数据）。
	anonGet  func(hash string) (*model.AnonCollection, error)
	anonList func() ([]model.AnonCollectionSummary, error)
	fileList func() ([]model.FileInfo, error)
	// fileInfo 按 hash 查单个文件（file_index.Info）。
	//
	// 为什么必须有它：fileList 有 1000 条上限，节点登记的文件超过 1000 条时
	// 新上传的文件不在那一页里——勾选了却既列不出来也共享不出去，用户看到的是
	// "我勾了，但什么都没发生"。按 hash 单独查不受分页影响。
	fileInfo func(hash string) (*model.FileInfo, error)
}

// NewNodeShare 从配置 + 已保存的运行时状态构造。
//
// storageDir 为空时不落盘（内存模式）：环境变量即最终范围。
// 有落盘文件时**以文件为准**——否则运营者在管理台上取消掉的共享项，一次重启
// 又被环境变量播种回来（"我明明取消了共享"）。
func NewNodeShare(cfg *config.Config, storageDir string) *NodeShare {
	s := &NodeShare{scope: scopeFromConfig(cfg)}
	if strings.TrimSpace(storageDir) != "" {
		abs, err := filepath.Abs(filepath.Join(storageDir, shareScopeFile))
		if err == nil {
			s.path = abs
		}
	}
	if saved, ok := s.load(); ok {
		s.scope = saved
	} else if s.path != "" {
		// 首次启动：把配置播种进运行时状态，之后以文件为准。
		// 落盘失败不致命（最坏是重启后回到配置值），只告警。
		if err := s.save(s.scope); err != nil {
			log.LogWarn("nodeshare: persist initial scope failed: %v", err)
		}
	}
	log.LogInfo("nodeshare: enable=%v collections=%d dirs=%d files=%d friends=%d (persisted=%v)",
		s.scope.Enable, len(s.scope.Collections), len(s.scope.Dirs), len(s.scope.Files),
		len(s.scope.Friends), s.path != "")
	return s
}


func (s *NodeShare) SetDirHook(fn func(dirs []string)) { s.onDirs = fn }

// SetAnonAccess 注入匿合集读取器（main 装 service.AnonService 的两个方法）。
func (s *NodeShare) SetAnonAccess(
	get func(hash string) (*model.AnonCollection, error),
	list func() ([]model.AnonCollectionSummary, error),
) {
	s.anonGet = get
	s.anonList = list
}

// SetFileLister 注入文件索引列举器（main 装 fileIndex.List 适配）。
func (s *NodeShare) SetFileLister(fn func() ([]model.FileInfo, error)) { s.fileList = fn }

// SetFileInfoReader 注入按 hash 查单个文件的读取器（main 装 fileIndex.Info）。
// 缺失时按 hash 的兜底查找不可用（勾选仍会记录，只是大节点上列不出来）。
func (s *NodeShare) SetFileInfoReader(fn func(hash string) (*model.FileInfo, error)) {
	s.fileInfo = fn
}

// Enabled 是否开启共享。
func (s *NodeShare) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scope.Enable
}

// Scope 返回当前共享范围（拷贝）。
func (s *NodeShare) Scope() ShareScope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scope.clone()
}

// Update 局部更新共享范围并落盘。返回更新后的完整范围。
//
// 校验失败时**整体不生效**（先校验再写入）：勾了 10 个文件其中 1 个 hash 写错，
// 不该把另外 9 个悄悄写进去——用户看到的是"我点了保存但没保存上"，而不是
// 一份残缺的范围。级别写错同理（拼错的级别宁可拒，也不兜成 public 把内容公开）。
func (s *NodeShare) Update(p ScopePatch) (ShareScope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.scope.clone()
	if p.Enable != nil {
		next.Enable = *p.Enable
	}
	if p.Dirs != nil {
		if err := validateLevels(*p.Dirs); err != nil {
			return s.scope.clone(), err
		}
		dirs, err := normalizeDirs(idsOf(*p.Dirs))
		if err != nil {
			return s.scope.clone(), err
		}
		next.Dirs = withLevels(*p.Dirs, dirs)
	}
	if p.Files != nil {
		if err := validateLevels(*p.Files); err != nil {
			return s.scope.clone(), err
		}
		files, err := normalizeFileHashes(idsOf(*p.Files))
		if err != nil {
			return s.scope.clone(), err
		}
		next.Files = withLevels(*p.Files, files)
	}
	if p.Collections != nil {
		if err := validateLevels(*p.Collections); err != nil {
			return s.scope.clone(), err
		}
		colls, err := normalizeCollections(idsOf(*p.Collections))
		if err != nil {
			return s.scope.clone(), err
		}
		next.Collections = withLevels(*p.Collections, colls)
	}
	if p.Friends != nil {
		friends, err := normalizeFriends(*p.Friends)
		if err != nil {
			return s.scope.clone(), err
		}
		next.Friends = friends
	}
	if err := s.persistLocked(next); err != nil {
		return s.scope.clone(), err
	}
	log.LogInfo("nodeshare: scope updated enable=%v dirs=%d files=%d collections=%d friends=%d",
		next.Enable, len(next.Dirs), len(next.Files), len(next.Collections), len(next.Friends))
	return next.clone(), nil
}

// SetFilesShared 勾选/取消若干文件（按 hash）。管理台逐行勾选走这条。
//
// level 为空表示沿用该 hash 已有级别（没有就 public）；取消勾选时忽略。
func (s *NodeShare) SetFilesShared(hashes []string, shared bool, level string) (ShareScope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(hashes) == 0 {
		return s.scope.clone(), fmt.Errorf("hashes is required")
	}
	// 上限：一次勾选不该把整个 file_index 塞进来（也是防超大请求体）。
	if len(hashes) > 1000 {
		return s.scope.clone(), fmt.Errorf("too many hashes (max 1000)")
	}
	lvl := ""
	if shared {
		lvl = model.NormalizeLevel(level)
		if lvl == "" {
			return s.scope.clone(), fmt.Errorf("无效的共享级别 %q（public / unlisted / private）", level)
		}
	}
	next := s.scope.clone()
	idx := make(map[string]ShareItem, len(next.Files))
	for _, it := range next.Files {
		idx[it.ID] = it
	}
	for _, raw := range hashes {
		h := strings.ToLower(strings.TrimSpace(raw))
		if !isSHA256Hex(h) {
			return s.scope.clone(), fmt.Errorf("invalid hash %q", raw)
		}
		if !shared {
			delete(idx, h)
			continue
		}
		// 显式传了级别就**覆盖**，不是取并集：用户在下拉里选"私密"就是要把它
		// 收回去，取最宽松会让"公开→私密"永远改不动（界面上看到的正是
		// "我改了但没变化"）。多条来源的合并发生在解析时（目录 ∪ 单文件），
		// 不是在这里。
		cur := lvl
		if cur == "" {
			cur = model.NormalizeLevel(idx[h].Level) // 没传 → 沿用（默认 public）
		}
		idx[h] = ShareItem{ID: h, Level: cur}
	}
	next.Files = make([]ShareItem, 0, len(idx))
	for _, it := range idx {
		next.Files = append(next.Files, it)
	}
	sort.Slice(next.Files, func(i, j int) bool { return next.Files[i].ID < next.Files[j].ID })
	if err := s.persistLocked(next); err != nil {
		return s.scope.clone(), err
	}
	return next.clone(), nil
}

// persistLocked 应用并落盘（调用方持锁）。目录变化会回调注册可读根。
