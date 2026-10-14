package update

import (
	"strconv"
	"strings"
)

// Version 是可比较的版本号（从 "v0.3.1" / "v0.3.1-rc.1" / "(dev)" 解析而来）。
//
// 为什么单独一档：release.yml 用 ldflags 把 tag 注入 version.Version，手工构建
// 是 "(dev)"；更新检查要在这两类字符串之间做可靠排序——字符串比较会错判
// "v0.3.10" < "v0.3.9" 这种数字位变长的 case。
type Version struct {
	Major, Minor, Patch int
	// Pre 是连字符后的预发布段（不含 '-'），空 = 正式版。
	// 按 semver 规则：正式版 > 任何预发布版；预发布段按点分段比较，
	// 数字段按数值比较（rc.10 > rc.2），字母段按字典序。
	Pre string
}

// devVersion 是解析失败 / "(dev)" 的兜底：0.0.0-dev 排在任何 vX.Y.Z 之下
// （包括 -rc），保证手工构建跑 check-update 时永远认为「有新版本」而不是误报最新。
var devVersion = Version{Pre: "dev"}

// ParseVersion 解析版本串。接受的形态：
//
//	v0.3.1 / 0.3.1 / v1（缺位补 0）/ v0.3.1-rc.1 / v0.3.1-beta2 / (dev)
//
// 无法解析时返回 devVersion（调用方不感知失败——更新逻辑对「当前版本读不懂」
// 的合理动作就是当它是最老的版本，而不是报错停止）。
func ParseVersion(s string) Version {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" || s == "(dev)" || s == "dev" {
		return devVersion
	}
	v := Version{}
	main, pre, _ := strings.Cut(s, "-")
	v.Pre = pre

	nums := strings.Split(main, ".")
	if len(nums) > 3 {
		return devVersion
	}
	parts := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, n := range nums {
		x, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil || x < 0 {
			return devVersion
		}
		*parts[i] = x
	}
	return v
}

// CompareVersions 比较两个版本串：a < b 返回 -1，相等返回 0，a > b 返回 1。
// 任一侧解析失败都按 devVersion 处理（见 ParseVersion）。
func CompareVersions(a, b string) int {
	va, vb := ParseVersion(a), ParseVersion(b)
	if c := compareInt(va.Major, vb.Major); c != 0 {
		return c
	}
	if c := compareInt(va.Minor, vb.Minor); c != 0 {
		return c
	}
	if c := compareInt(va.Patch, vb.Patch); c != 0 {
		return c
	}
	return comparePre(va.Pre, vb.Pre)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// comparePre 按 semver 规则比较预发布段：
//   - 空（正式版）> 任何非空（预发布）；
//   - 两者都非空时按 '.' 分段比较：数字段按数值，字母段按字典序；
//     段数不等时缺段补 0（"rc.1" vs "rc"：1 > 0）。
func comparePre(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y string
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if c := compareSegment(x, y); c != 0 {
			return c
		}
	}
	return 0
}

// compareSegment 比较预发布段的一个点分段。
func compareSegment(x, y string) int {
	if x == y {
		return 0
	}
	xn, xerr := strconv.Atoi(x)
	yn, yerr := strconv.Atoi(y)
	switch {
	case xerr == nil && yerr == nil:
		return compareInt(xn, yn)
	case xerr == nil: // 数字段 < 字母段（semver）
		return -1
	case yerr == nil:
		return 1
	default:
		if x < y {
			return -1
		}
		return 1
	}
}
