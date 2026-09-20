package pathutil

// 启动期的"配置是不是配成了全盘放行"检查。
//
// Within(root, path) 是**纯粹的包含判定**：root 配成 `/`（或 Windows 的 `C:\`）
// 时它当然会放行 `/etc/passwd`，而且这不是 bug——那是配置字面上的意图。
// 但几乎没人真的想把整个盘共享出去，这种值基本都是配错（比如 `PEERDRIVE_STORAGE=/`
// 写成了环境变量没展开）。与其让节点"安静地全盘放行"，不如启动就拒绝。

import (
	"path/filepath"
	"strings"
)

// IsUnsafeRoot 判断 root 是不是一个卷根 / 文件系统根。
//
//	Linux:   "/"                      → true
//	Windows: "C:\" "c:\" "D:"         → true
//	         "\\?\C:\"                → true（卷名之后没有剩余路径）
func IsUnsafeRoot(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	// 只写了盘符/共享名、没写路径的写法（Windows 的 `D:`、UNC 的
	// `\\server\share`）：`D:` 连绝对路径都不是（它是"D 盘的当前目录"），
	// 而 Go 的 filepath.Abs 拿不到别的盘的当前目录，会把它拼成 `<cwd>\D:` ——
	// 运营者以为共享的是 D 盘，实际落点是 C 盘下一个莫名其妙的目录。
	// 这种静默的语义漂移按卷根同等处理（Clean 会把 `D:` 变成 `D:.`）。
	if clean := filepath.Clean(root); clean != "" {
		if vol := filepath.VolumeName(clean); vol != "" {
			if rest := strings.TrimPrefix(clean, vol); rest == "" || rest == "." {
				return true
			}
		}
	}
	r, ok := normalize(root)
	if !ok {
		// 解析不了的路径交给别的校验去管，这里不判为"卷根"
		return false
	}
	rest := r[len(filepath.VolumeName(r)):]
	rest = strings.TrimPrefix(rest, string(filepath.Separator))
	return rest == ""
}

// UnsafeRoots 从一批候选根目录里挑出那些配成了卷根的。
// 空串与非法路径跳过（它们由别的校验负责）。
func UnsafeRoots(roots ...string) []string {
	var bad []string
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		if IsUnsafeRoot(r) {
			bad = append(bad, r)
		}
	}
	return bad
}
