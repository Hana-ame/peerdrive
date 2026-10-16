package pathutil

import (
	"strings"
	"unicode/utf8"
)

// SafeComponent 把一个任意的用户输入字符串变成**单个**安全的路径分量
// （一个目录名或文件名），供 `filepath.Join(root, SafeComponent(name))` 使用。
//
// 为什么单开这个函数：`filepath.Join` 不会替你拦下 `..`——
// `Join("/tmp/x", "../../etc/cron.d")` 就是 `/etc/cron.d`。而「把用户给的
// 名字拼进固定根下」这种用法在仓库里有两处（BT seed 的 rootDir / dataRoot），
// 之前是各自内联一个 strings.Map，漏掉 `..` 与 Windows 保留名。边界判定只保留
// 一套，见 path.go 的包注释。
//
// 返回 "" 表示清洗后没有可用字符（纯分隔符、纯 `..`、命中保留名等），
// 调用方据此回退到一个生成的名字，而不是拿到一个 `.` 去 MkdirAll 自己的根。
func SafeComponent(name string) string {
	// 非法字符替换成 '_'；控制字符直接丢弃（rename/展示都会出问题）。
	// ':' 一起换掉：Windows 上是卷名/交替数据流（ADS）的入口。
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ';':
			return '_'
		}
		return r
	}, name)
	s = strings.TrimSpace(s)
	// Windows 会静默截掉结尾的 '.' 与空格，"a." 和 "a" 指向同一个文件；
	// 连字符截完再判一次，`".."`/`"..."` 这类截完会变空。
	s = strings.TrimRight(s, ". ")
	if s == "" || s == "." || s == ".." {
		return ""
	}
	if HasReservedName(s) {
		return ""
	}
	// 超长名字不是安全问题但会直接 ENAMETOOLONG；截到 255 字节（POSIX NAME_MAX）。
	// 按 rune 边界截，避免把多字节字符（中文等）切成半个而产出非法 UTF-8 文件名；
	// 截完再剔一次结尾的 '.'/'_'，免得造出一个和原名不可区分的名字。
	if len(s) > 255 {
		var b strings.Builder
		for _, c := range s {
			if b.Len()+utf8.RuneLen(c) > 255 {
				break
			}
			b.WriteRune(c)
		}
		s = strings.TrimRight(b.String(), ". ")
	}
	return s
}
