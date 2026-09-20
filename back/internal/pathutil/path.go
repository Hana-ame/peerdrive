// Package pathutil 只做一件事：判断「一个路径是否落在某个根目录之内」。
//
// 为什么值得单开一个包：这个判断同时被三处用到，而且三处的判断必须**完全一致**
// ——文件登记（`service.FileService`）、对外服务（`transport.FileIndexService`
// 的 serveFile / `source.LocalSource`）、共享清单过滤（`service.NodeShare`）。
// 之前这三处各写一份 `filepath.Rel` + `HasPrefix`，于是出现了经典的组合错误：
// 登记放行了（storage 根内）、共享清单也列得出来（ShareDirs 前缀匹配），
// 唯独读取那一份判定说"越权"，回退到一个根本不存在的内容寻址副本 ——
// 对端的表现是"清单里看得见、一拉就 read failed"。
//
// 跨平台要点（Linux/macOS/Windows 都要对）：
//   - 分隔符：`filepath.Rel` 自带平台语义，不要手写 `strings.HasPrefix(a+"/")`
//     （Windows 上是 `\`，而且 `/` 也被接受，手写必错）。
//   - 大小写：Windows（NTFS）默认大小写不敏感，`C:\Data` 与 `c:\data` 是同一
//     目录；Linux/ext4 严格区分。所以 Windows 上按折叠大小写比较，其它平台不折。
//   - 盘符/卷：`filepath.Rel("C:\\a", "D:\\a")` 会直接报错，天然拦住跨盘；
//     但盘符大小写不同（C: vs c:）时 Rel 仍能算出结果，靠上面的折叠解决。
//   - 符号链接：解析后判定（best-effort）。目录内的软链指向外面 → 判为越权，
//     这是刻意的（宁可少给，不能多给）。
package pathutil

import (
	"path/filepath"
	"runtime"
	"strings"
)

// SplitList 拆分逗号分隔的目录配置（PEERDRIVE_SHARE_DIRS 这类）。
// 去空白、丢弃空项；**不**做绝对路径化（调用方自己知道基准目录）。
func SplitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Within 判断 path 是否位于 root 之内（root 自身算在内）。
// root 或 path 为空 → false（不给默认值兜底：空根目录等于"全放行"）。
func Within(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	r, ok := normalize(root)
	if !ok {
		return false
	}
	p, ok := normalize(path)
	if !ok {
		return false
	}
	if r == p {
		return true
	}
	// Rel 已经处理了分隔符与跨盘（跨盘时返回 error）
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	// 防御：Rel 理论上不会返回绝对路径，真返回了说明不在一个树上
	return !filepath.IsAbs(rel)
}

// WithinAny 判断 path 是否落在 roots 中任意一个之内。
func WithinAny(roots []string, path string) bool {
	for _, r := range roots {
		if Within(r, path) {
			return true
		}
	}
	return false
}

// foldCase 是否折叠大小写：Windows（NTFS）默认大小写不敏感，`C:\Data` 与
// `c:\data` 是同一个目录；Linux/ext4 严格区分，折叠反而会把两个不同路径当成同一个。
//
// 做成**变量**而不是直接读 runtime.GOOS，是为了让单测能在任何平台上验证
// Windows 那一支——否则 CI 跑在 Linux 上永远 skip，Windows 语义等于没测过。
var foldCase = runtime.GOOS == "windows"

// normalize 把路径整理成可比较的形式：绝对路径 → Clean → 解析软链（尽力）→
// 按需折叠大小写。解析失败（路径不存在）时保留 Clean 后的绝对路径——
// 登记一个还没落盘的文件时很常见，不能因为 stat 失败就判越权。
func normalize(p string) (string, bool) {
	// NUL 字节：Go 的 os.Open 会拒绝（`invalid argument`），所以单靠系统调用也漏
	// 不出去；但纯字符串判定（Rel/Clean）不认 NUL，`root/x\x00../../etc/passwd`
	// 在字符串层面算"根内"。显式拒掉，免得将来有人拿 Within 去 gate 一个
	// 自己拼命令/写日志的路径时被打穿。
	if strings.IndexByte(p, 0) >= 0 {
		return "", false
	}
	// Windows 保留设备名（`CON`/`NUL`/`COM1`…）：文本上在根内、实际上指向设备，
	// 判"在根内"没有意义。只在 Windows 上启用——Linux 上它们就是普通文件名。
	if foldCase && hasReservedNameIn(p) {
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = filepath.Clean(resolved)
	}
	if foldCase {
		// Windows：同一个目录可能有 8.3 短名（`C:\PROGRA~1`）这个别名。不还原的
		// 话，长名配的共享目录会用短名判成越权（文件确实在里面却读不到）。
		// 详见 shortname_windows.go。
		// 细节（实测 2026-09-20）：Windows 的 EvalSymlinks 对**已存在**的路径会顺带
		// 还原短名，但对"最后一段还不存在"的路径无能为力——而那正是所有写操作和
		// 所有软链目标的样子。所以这一步不能指望 EvalSymlinks。
		abs = filepath.Clean(ExpandShortNames(abs))
		abs = strings.ToLower(abs)
	}
	return abs, true
}
