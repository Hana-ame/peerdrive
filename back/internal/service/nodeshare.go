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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/pathutil"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/transport"
)

// shareAllToken 合集配置中代表"所有 public 合集"的取值。
const shareAllToken = "all"

// shareScopeFile 运行时共享范围的落盘文件名（位于 storageDir 下）。
// 格式与 node_directory 的 joined_nodes.json 同思路：小文件、原子语义由
// 调用方（写临时文件 + rename）保证。
const shareScopeFile = "share_scope.json"

// levelCacheTTL 级别缓存有效期。
//
// 为什么要有过期：目录共享的级别是按"文件当前路径"算出来的，而文件会在范围
// 设定之后才上传（先共享目录、再往里放文件）。缓存不过期的话，新放进来的文件
// 会被当成"没声明"——而"没声明"默认可下载，于是 private 目录里的新文件谁都
// 能取。10s 是"最多泄漏 10s"与"每次下载都扫一遍索引"之间的折中；
// 范围本身变动时立即失效（见 persistLocked），只有外部新增文件才等过期。
const levelCacheTTL = 10 * time.Second

// ShareItem 一条共享声明：目标 + 级别。
//
// JSON 兼容两种写法（UnmarshalJSON）：
//   - 字符串（历史落盘 / 环境变量播种）：`"/data/media"` → 级别 public
//   - 对象：{"id":"/data/media","level":"unlisted"}
//
// 为什么要兼容字符串：本次升级前写下的 share_scope.json 只有 id。读不懂就退回
// 环境变量初值，等于把运营者精心选好的范围丢掉（见 NewNodeShare 注释）。
type ShareItem struct {
	ID    string `json:"id"`
	Level string `json:"level,omitempty"`
}

// UnmarshalJSON 兼容字符串与对象两种写法。
func (i *ShareItem) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		i.ID = strings.TrimSpace(s)
		i.Level = ""
		return nil
	}
	var o struct {
		ID    string `json:"id"`
		Level string `json:"level"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	i.ID = strings.TrimSpace(o.ID)
	i.Level = strings.TrimSpace(o.Level)
	return nil
}

// EffectiveLevel 归一化后的级别（空 → public）。
func (i ShareItem) EffectiveLevel() string { return model.NormalizeLevel(i.Level) }

// ShareScope 运营者选择的共享范围。
//
// 三条来源互相独立、取并集：dirs（整个目录）+ files（单个文件）+ collections。
// 空 dirs 不代表"不过滤"（那是"整库共享"），而是"不按目录共享"——见
// filesSnapshotFor 的注释。
//
// Friends 是 private 级别的放行名单（节点 ID）。
type ShareScope struct {
	Enable      bool        `json:"enable"`
	Dirs        []ShareItem `json:"dirs"`
	Files       []ShareItem `json:"files"`
	Collections []ShareItem `json:"collections"`
	Friends     []string    `json:"friends,omitempty"`
}

// clone 深拷贝（快照/返回值都不该让调用方改到内部状态）。
func (s ShareScope) clone() ShareScope {
	return ShareScope{
		Enable:      s.Enable,
		Dirs:        append([]ShareItem{}, s.Dirs...),
		Files:       append([]ShareItem{}, s.Files...),
		Collections: append([]ShareItem{}, s.Collections...),
		Friends:     append([]string{}, s.Friends...),
	}
}

// isFriend 判断 peerID 是否在好友名单里（去空白、大小写不敏感）。
//
// 大小写不敏感的理由：节点 ID 由对端自报，手工抄写时差一个大小写就"明明加了
// 好友却取不到"，这种反馈几乎无法自查。而 ID 冲突到只有大小写不同的概率极低。
func (s ShareScope) isFriend(peerID string) bool {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return false
	}
	for _, f := range s.Friends {
		if strings.EqualFold(strings.TrimSpace(f), peerID) {
			return true
		}
	}
	return false
}

// ScopePatch 局部更新（nil = 该项不改）。
//
// 为什么用指针而不是"整体替换"：管理台一次只改一类东西（勾个文件、开关共享），
// 让它每次都把整份范围重发一遍，等于把"我只想取消一个文件"变成一次可能被并发
// 覆盖的全量写。
type ScopePatch struct {
	Enable      *bool        `json:"enable"`
	Dirs        *[]ShareItem `json:"dirs"`
	Files       *[]ShareItem `json:"files"`
	Collections *[]ShareItem `json:"collections"`
	Friends     *[]string    `json:"friends"`
}

// ShareFileItem 可选文件清单里的一行（GET /peerjs/share 的 files[]）。
// shared = 勾选共享（含"整个目录共享"带上的），by_dir 区分这两种来源，
// 让管理台能显示"这个是因为目录共享才共享的"，避免用户勾不掉它时困惑。
// level = 该行最终生效的级别（多条来源取最宽松，见 model.LoosestLevel）。
type ShareFileItem struct {
	Hash   string `json:"hash"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Shared bool   `json:"shared"`
	ByDir  bool   `json:"by_dir,omitempty"`
	Level  string `json:"level,omitempty"`
}

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
	fileList func() ([]transport.FileInfo, error)
	// fileInfo 按 hash 查单个文件（file_index.Info）。
	//
	// 为什么必须有它：fileList 有 1000 条上限，节点登记的文件超过 1000 条时
	// 新上传的文件不在那一页里——勾选了却既列不出来也共享不出去，用户看到的是
	// "我勾了，但什么都没发生"。按 hash 单独查不受分页影响。
	fileInfo func(hash string) (*transport.FileInfo, error)
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
func (s *NodeShare) SetFileLister(fn func() ([]transport.FileInfo, error)) { s.fileList = fn }

// SetFileInfoReader 注入按 hash 查单个文件的读取器（main 装 fileIndex.Info）。
// 缺失时按 hash 的兜底查找不可用（勾选仍会记录，只是大节点上列不出来）。
func (s *NodeShare) SetFileInfoReader(fn func(hash string) (*transport.FileInfo, error)) {
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
	for _, f := range files {
		if f.Path == "" || f.Delete {
			continue
		}
		byDir := pathutil.WithinAny(idsOf(sc.Dirs), f.Path)
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
func idsOf(items []ShareItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.ID != "" {
			out = append(out, it.ID)
		}
	}
	return out
}

// withLevels 把请求里的级别贴回归一化后的 id 列表。
//
// 为什么不在归一化里直接带上级别：归一化（去重/排序/校验）是按 id 做的，一条
// id 在请求里出现两次（一次 public 一次 private）时取最宽松的那条，语义与
// "同一文件被目录和单文件同时命中"保持一致。
func withLevels(src []ShareItem, ids []string) []ShareItem {
	lv := make(map[string]string, len(src))
	for _, it := range src {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			continue
		}
		lv[id] = model.LoosestLevel(lv[id], it.Level)
	}
	out := make([]ShareItem, 0, len(ids))
	for _, id := range ids {
		out = append(out, ShareItem{ID: id, Level: lv[id]})
	}
	return out
}

// validateLevels 校验声明列表里的级别（空 = 沿用默认 public，非法 = 报错）。
//
// 为什么非法值不能兜成 public：级别是从 HTTP 请求体来的，拼错一个字母就按最
// 宽松档生效，等于把本想限制的内容公开出去。宁可整批拒绝让运营者重填。
func validateLevels(items []ShareItem) error {
	for _, it := range items {
		v := strings.TrimSpace(it.Level)
		if v == "" {
			continue
		}
		if model.NormalizeLevel(v) == "" {
			return fmt.Errorf("无效的共享级别 %q（只能是 public / unlisted / private）", it.Level)
		}
	}
	return nil
}

// dirLevelFor 返回路径命中的目录里**最宽松**的级别（未命中 → 空串）。
func dirLevelFor(dirs []ShareItem, path string) string {
	if path == "" {
		return ""
	}
	lvl := ""
	for _, d := range dirs {
		if d.ID == "" {
			continue
		}
		if pathutil.Within(d.ID, path) {
			// NormalizeLevel：历史声明没有级别（空串）按 public，不能因为
			// "没写级别"就让它从清单里消失
			lvl = model.LoosestLevel(lvl, model.NormalizeLevel(d.Level))
		}
	}
	return lvl
}

// normalizeDirs 目录列表归一化：去空、转绝对路径、去重、拒卷根。
//
// 拒卷根的理由与 main.checkUnsafeRoots 一致：root 配成 `/`（Windows 的 `C:\`）
// 时 pathutil.Within 当然会放行 `/etc/passwd`——那不是判定错了，是配置字面上的
// 意图。但这里的值来自 HTTP 请求体，绝不能让一次误填就把整个盘共享出去。
func normalizeDirs(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, d := range in {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if pathutil.IsUnsafeRoot(d) {
			return nil, fmt.Errorf("拒绝把文件系统卷根 %q 设为共享目录（那等于共享整个盘）", d)
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("无效的共享目录 %q: %w", d, err)
		}
		abs = filepath.Clean(abs)
		k := dirKey(abs)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, abs)
	}
	sort.Strings(out)
	return out, nil
}

// dirKey 目录去重键：Windows 上盘符与大小写不敏感（`D:\Media` 与
// `d:\media` 是同一个目录），Linux 上小写化只是无害的保守做法。
func dirKey(dir string) string {
	return strings.ToLower(filepath.Clean(dir))
}

// normalizeFileHashes 单文件共享列表：只接受 64hex，去重、排序。
func normalizeFileHashes(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if !isSHA256Hex(h) {
			return nil, fmt.Errorf("无效的文件 hash %q（必须是 64 位 hex 的 sha256）", raw)
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// normalizeCollections 合集列表：64hex 或 "all"。
func normalizeCollections(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		h := strings.ToLower(strings.TrimSpace(raw))
		if h == "" {
			continue
		}
		if h == shareAllToken {
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
			continue
		}
		if !isSHA256Hex(h) {
			return nil, fmt.Errorf("无效的合集 hash %q（必须是 64 位 hex，或 all）", raw)
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// normalizeFriends 好友节点 ID 列表：去空、去重、排序（保留原始大小写）。
func normalizeFriends(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		// 节点 ID 里的空白是抄写错误（肉眼分不出 `abc ` 和 `abc`），
		// 保留会让"加了好友却取不到"无法自查。
		if strings.ContainsAny(id, " \t\r\n") {
			return nil, fmt.Errorf("好友节点 ID 不能含空白: %q", raw)
		}
		k := strings.ToLower(id)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
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
