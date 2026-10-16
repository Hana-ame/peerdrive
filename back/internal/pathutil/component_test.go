package pathutil

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSafeComponent 发现背景：code-scanning 告警 #5/#6/#7/#8/#48/#49（go/path-injection,
// back/internal/controller/p2p_bt.go:349/357/370/398/405/412）。POST /bt/seed-collection
// 直接拿 collection 的 friendly_name 当路径分量拼进 tmpDir 与 BT download 目录：
// friendly_name="../../tmp/evil" 时 MkdirAll/WriteFile 就落到了两个根之外。
// 这里锁住「任何输入都不能逃出单个路径分量」。
// 修复本体在 PR #407；PR #409 追加了 filepath.Clean("/"+s) 归一化，让返回值派生自
// CodeQL 原生登记的净化器，这 6 条才不再随重扫复现。加/改用例时保持表驱动即可，
// 不要为了让 Clean 那行「可测」而重排语义——对表里的输入 Clean 是恒等变换。
func TestSafeComponent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 穿越与绝对路径：替换成分隔符后仍是安全分量（不会逃出根）
		{"../../etc/cron.d/x", ".._.._etc_cron.d_x"},
		{"../a", ".._a"},
		{"a/../../b", "a_.._.._b"},
		{"/abs/path", "_abs_path"},
		{`C:\Users\evil`, "C__Users_evil"},
		{"a\\..\\b", "a_.._b"},
		// 纯分隔符 / 纯点：清洗后无可用字符，调用方回退到生成名
		{"..", ""},
		{"...", ""},
		{".", ""},
		{"/", "_"},
		{"", ""},
		{"   ", ""},
		// Windows 保留名（含扩展名形式）
		{"CON", ""},
		{"con.txt", ""},
		{"COM1", ""},
		{"NUL", ""},
		// Windows 会静默截掉结尾的 '.'/'空格'，"a." 和 "a" 指向同一个文件
		{"abc.", "abc"},
		{"abc ", "abc"},
		{"a. .", "a"},
		// 非法字符替换成 '_'，控制字符丢弃
		{"a\"b<c>d", "a_b_c_d"},
		{"a|b?c*d", "a_b_c_d"},
		{"a;b", "a_b"},
		{"a\x00b", "ab"},
		{"a\u007fb", "ab"},
		// 正常名字原样保留（含中文、空格、内部点）
		{"my collection", "my collection"},
		{"a.b.c", "a.b.c"},
		{"合集-测试", "合集-测试"},
		// 超长截到 255 字节，且截断后不以 '.' 结尾
		{strings.Repeat("x", 300), strings.Repeat("x", 255)},
		{strings.Repeat("x", 256) + "..", strings.Repeat("x", 255)},
	}
	for _, c := range cases {
		if got := SafeComponent(c.in); got != c.want {
			t.Errorf("SafeComponent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSafeComponentInvariants 锁住真正的安全不变量：输出必须是「单个路径分量」——
// 没有分隔符、不是 . / ..、不是 Windows 保留名、长度不超 NAME_MAX。
func TestSafeComponentInvariants(t *testing.T) {
	inputs := []string{
		"../../etc/cron.d", "..", "../a", "/", `C:\`, "a/../../b", "CON", "NUL",
		"abc.", "a. .", "a\"b", strings.Repeat("x", 1000), "\x00", ".",
	}
	for _, in := range inputs {
		got := SafeComponent(in)
		if got == "" {
			continue // 调用方回退到生成名，不拼路径
		}
		if strings.ContainsAny(got, "/\\") {
			t.Errorf("SafeComponent(%q) = %q 含分隔符", in, got)
		}
		if got == "." || got == ".." {
			t.Errorf("SafeComponent(%q) = %q 是点路径", in, got)
		}
		if HasReservedName(got) {
			t.Errorf("SafeComponent(%q) = %q 是 Windows 保留名", in, got)
		}
		if len(got) > 255 {
			t.Errorf("SafeComponent(%q) = %d 字节，超过 255", in, len(got))
		}
	}
}

// TestSafeComponentJoinsStayInsideRoot 锁住本函数存在的目的：无论输入怎么构造，
// filepath.Join(root, SafeComponent(x)) 都必须落在根里（root 不是文件系统根，
// 避免拿测试机器的真实根目录做断言）。
func TestSafeComponentJoinsStayInsideRoot(t *testing.T) {
	root := "/fixed/root"
	for _, in := range []string{
		"../../etc/cron.d", "..", "../a", "/", `C:\`, "a/../../b", "CON",
		"abc.", "", strings.Repeat("x", 1000),
	} {
		name := SafeComponent(in)
		if name == "" {
			continue
		}
		if !Within(root, filepath.Join(root, name)) {
			t.Errorf("Join 逃逸: SafeComponent(%q)=%q", in, name)
		}
	}
}
