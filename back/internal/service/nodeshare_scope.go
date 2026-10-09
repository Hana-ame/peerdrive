package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/pathutil"
)

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
