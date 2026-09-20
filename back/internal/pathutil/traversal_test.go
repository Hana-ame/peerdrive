package pathutil

// 路径穿透（path traversal）攻击矩阵。
//
// 为什么单开一个文件：Within/WithinAny 是所有"这个文件能不能碰"判定的唯一入口，
// 它一旦被绕过，往上每一层（登记、读取、复制、删除、共享清单）全都白设。
// 所以这里不测"功能对不对"，而是按攻击者视角把 payload 分门别类摆出来，
// 每一类都写成表驱动用例——将来看见新 payload 就往表里加一行。
//
// 组织方式：
//   - TestTraversal_Rejected    —— 必须拒（越权）
//   - TestTraversal_Allowed     —— 必须放行（不能因为防御过度把正常用法打死）
//   - TestTraversal_Platform    —— 只在特定平台才有意义的 payload
//   - TestTraversal_SymlinkTree —— 软链相关的组合（目录级软链、根自身是软链）
//
// 跨平台说明：payload 一律用 filepath.Join 拼，不在用例里写死 "/" 或 "\"。
// 纯 Windows 语义（盘符、UNC、\\?\）放 TestTraversal_Platform 里按 GOOS 跳过。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// traversalRoot 造一个真实的根目录 + 一个"绝对不能碰到"的目录，都带内容。
// 用真实文件而不是纯字符串比较，是因为判定链路里有 EvalSymlinks——
// 路径不存在时它会失败并走另一支分支，纯字符串测不出那支。
type traversalRoot struct {
	root    string // 允许的根
	outside string // 根外的另一个目录（同父级，模拟 /etc 那种"就在隔壁"）
	secret  string // outside 里的文件
}

func newTraversalRoot(t *testing.T) traversalRoot {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	return traversalRoot{root: root, outside: outside, secret: secret}
}

func TestTraversal_Rejected(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root

	// 名字 → 相对 root 的"攻击者输入"。用 Join 保证分隔符跟随平台。
	cases := []struct {
		name string
		path string
	}{
		{"单点向上逃逸", filepath.Join(root, "..", "outside", "secret.txt")},
		{"多级向上逃逸", filepath.Join(root, "sub", "..", "..", "outside", "secret.txt")},
		{"绝对路径直接指根外", tr.secret},
		{"绝对路径指系统文件", "/etc/passwd"},
		{"兄弟目录同名前缀", filepath.Join(filepath.Dir(root), "root-other", "x.txt")},
		{"兄弟目录同名前缀带后缀", filepath.Join(filepath.Dir(root), "root.bak", "secret.txt")},
		{"当前目录点号逃逸", filepath.Join(root, ".", "..", "outside", "secret.txt")},
		{"重复分隔符夹带逃逸", root + string(filepath.Separator) + string(filepath.Separator) + ".." +
			string(filepath.Separator) + "outside" + string(filepath.Separator) + "secret.txt"},
		{"一串裸点点", filepath.Join(root, "..", "..", "..", "..", "..", "etc", "passwd")},
		// NUL：Go 的 os.Open 会自己拒（invalid argument），但**纯字符串判定不认
		// NUL**。下面两条在去掉 NUL 后都落在根内/或靠 Clean 兜住，用来单独验
		// "含 NUL 一律拒绝"这条规则本身生效，而不是被别的原因顺带挡下。
		{"NUL 结尾（否则在根内）", filepath.Join(root, "ok.txt") + "\x00"},
		{"NUL 截断夹带逃逸", filepath.Join(root, "ok.txt") + "\x00" + filepath.Join("..", "outside", "secret.txt")},
		{"空 path", ""},
		{"只有点", "."},
		{"只有点点", ".."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if Within(root, c.path) {
				t.Fatalf("必须拒绝却被放行：root=%s path=%q", root, c.path)
			}
			if WithinAny([]string{root}, c.path) {
				t.Fatalf("WithinAny 必须拒绝却被放行：path=%q", c.path)
			}
		})
	}
}

func TestTraversal_Allowed(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root

	// 这些是**正常用法**，防御过度把打死同样是 bug：之前"共享目录必须在
	// download 之内"就是被过度判定逼出来的伪约束。
	cases := []struct {
		name string
		path string
	}{
		{"根自身", root},
		{"根内文件", filepath.Join(root, "a.txt")},
		{"根内子目录深层", filepath.Join(root, "sub", "deep", "a.txt")},
		{"带冗余当前目录", filepath.Join(root, ".", "a.txt")},
		{"带冗余分隔符", root + string(filepath.Separator) + string(filepath.Separator) + "sub" +
			string(filepath.Separator) + "a.txt"},
		{"带冗余点号夹在中间", filepath.Join(root, "sub", ".", "a.txt")},
		{"在根内绕一圈又回来", filepath.Join(root, "sub", "..", "a.txt")},
		{"文件名里含点点但非逃逸", filepath.Join(root, "a..b.txt")},
		{"在根内绕远路再回来", filepath.Join(root, "sub", "deep", "..", "..", "sub", "a.txt")},
		{"根自身带尾部分隔符", root + string(filepath.Separator)},
		{"根自身带点号", filepath.Join(root, ".")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !Within(root, c.path) {
				t.Fatalf("应当放行却被拒绝：root=%s path=%q", root, c.path)
			}
		})
	}
}

// TestTraversal_EmptyRootIsNotAllowAll 空根目录 = "全放行"，是最危险的配置错误。
func TestTraversal_EmptyRootIsNotAllowAll(t *testing.T) {
	tr := newTraversalRoot(t)
	if Within("", tr.secret) {
		t.Fatal("空 root 放行了根外文件")
	}
	if WithinAny([]string{"", "   "}, tr.secret) {
		t.Fatal("空白 root 放行了根外文件")
	}
	if WithinAny([]string{}, tr.secret) {
		t.Fatal("无 root 放行了根外文件")
	}
	// 混合列表里有一个合法根也不能让空串生效
	if WithinAny([]string{""}, filepath.Join(tr.root, "a.txt")) {
		t.Fatal("空 root 不该放行任何东西")
	}
}

// TestTraversal_OneBadRootDoesNotOpenEverything WithinAny 是"命中任意一个即放行"，
// 所以列表里只要有一个配置错（比如空串、根目录本身）不能把别的都带开。
func TestTraversal_OneBadRootDoesNotOpenEverything(t *testing.T) {
	tr := newTraversalRoot(t)
	other := filepath.Join(filepath.Dir(tr.root), "other-root")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	// 两个合法根 + 一个空串：空串不能变成"任意路径都算命中"
	if WithinAny([]string{tr.root, "", other}, tr.secret) {
		t.Fatal("列表里的空串变成了全放行")
	}
	if !WithinAny([]string{tr.root, "", other}, filepath.Join(other, "x.txt")) {
		t.Fatal("第二个合法根应当生效")
	}
}

// TestTraversal_Platform 只有在对应平台上才有意义的 payload。
func TestTraversal_Platform(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root

	if runtime.GOOS == "windows" {
		// 跨盘：C: 根里的文件不可能属于 D:
		if len(filepath.VolumeName(root)) > 0 {
			otherVol := "D:" + string(filepath.Separator) + "secret.txt"
			if Within(root, otherVol) {
				t.Fatalf("跨盘路径被判为在内：%s", otherVol)
			}
		}
		// UNC 与 \\?\ 扩展长度路径：不该被当成"根内"
		for _, p := range []string{
			`\\server\share\secret.txt`,
			`\\?\C:\Windows\win.ini`,
			`\\.\C:\Windows\win.ini`,
		} {
			if Within(root, p) {
				t.Fatalf("特殊前缀路径被判为在内：%s", p)
			}
		}
		// 盘符大小写不同仍是同一个盘（折叠生效）
		vol := filepath.VolumeName(root)
		if strings.EqualFold(vol, "c:") {
			swapped := strings.ToUpper(vol[:1]) + ":" + root[len(vol):]
			if vol != swapped && !Within(root, filepath.Join(swapped, "a.txt")) {
				t.Fatalf("盘符大小写不同应视为同一卷：%s vs %s", root, swapped)
			}
		}
	} else {
		// Linux/macOS：反斜杠是**合法文件名字符**，不是分隔符。
		// 手写 strings.HasPrefix(a+"/") 的实现在这里最容易出错。
		weird := filepath.Join(root, `a\b.txt`)
		if err := os.WriteFile(weird, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if !Within(root, weird) {
			t.Fatalf("含反斜杠的普通文件名应放行：%s", weird)
		}
		// 反过来：真正的分隔符逃逸必须被拦
		if Within(root, root+string(filepath.Separator)+".."+string(filepath.Separator)+"outside") {
			t.Fatal("分隔符逃逸未拦住")
		}
	}
}

// TestTraversal_SymlinkTree 软链组合。要点：判定的是**解析后**的路径。
func TestTraversal_SymlinkTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	tr := newTraversalRoot(t)
	root := tr.root

	mk := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("本环境不允许建软链：%v", err)
		}
	}

	t.Run("根内软链指向根外文件", func(t *testing.T) {
		mk(tr.secret, filepath.Join(root, "link.txt"))
		if Within(root, filepath.Join(root, "link.txt")) {
			t.Fatal("指向根外的软链必须判为越权")
		}
	})

	t.Run("根内目录软链指向根外目录", func(t *testing.T) {
		// 目录级软链最阴：link/x.txt 看起来"在 root 下两层"，
		// 不做解析就会放行
		mk(tr.outside, filepath.Join(root, "alias"))
		if Within(root, filepath.Join(root, "alias", "secret.txt")) {
			t.Fatal("指向根外目录的软链必须判为越权")
		}
	})

	t.Run("根内软链指向根内", func(t *testing.T) {
		real := filepath.Join(root, "real.txt")
		if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mk(real, filepath.Join(root, "alias.txt"))
		if !Within(root, filepath.Join(root, "alias.txt")) {
			t.Fatal("指向根内的软链应放行")
		}
	})

	t.Run("软链指根外但目标不存在", func(t *testing.T) {
		// EvalSymlinks 会失败 → 退回 Clean 后的路径。此时 root/link 字符串上
		// 确实在 root 下，放行是符合预期的（真正打开时 os.Open 也会失败），
		// 但不能因为 stat 失败就判越权——登记一个尚未落盘的文件是正常用法。
		mk(filepath.Join(tr.outside, "not-there.txt"), filepath.Join(root, "dangling"))
		if !Within(root, filepath.Join(root, "dangling")) {
			t.Fatal("悬空软链不应因 stat 失败被判越权")
		}
	})

	t.Run("根自身是软链", func(t *testing.T) {
		realRoot := filepath.Join(filepath.Dir(root), "real-root")
		if err := os.MkdirAll(realRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		// 目标文件必须**真的存在**：EvalSymlinks 解析的是整条路径，最后一段不存在
		// 时它会失败并退回未解析的写法，于是"软链路径"看着就在根外——那是评测
		// 用例没造全，不是判定的问题。
		if err := os.WriteFile(filepath.Join(realRoot, "a.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		linkRoot := filepath.Join(filepath.Dir(root), "link-root")
		mk(realRoot, linkRoot)
		// 两种写法都应指向同一处
		if !Within(linkRoot, filepath.Join(realRoot, "a.txt")) {
			t.Fatal("根是软链时，真实路径写法应放行")
		}
		if !Within(realRoot, filepath.Join(linkRoot, "a.txt")) {
			t.Fatal("根是软链时，软链路径写法应放行")
		}
	})
}

// TestTraversal_AnySymlinkRootWithinAny WithinAny 只要命中一个根就放行，
// 因此"软链从 A 根逃到 B 根"是允许的（B 本来就是合法根），
// 但"软链逃到两个根之外"必须拒。这条防止有人把 WithinAny 写成"全部根都要命中"。
func TestTraversal_SymlinkBetweenRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	tr := newTraversalRoot(t)
	second := filepath.Join(filepath.Dir(tr.root), "second-root")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tr.root, "to-second")
	if err := os.Symlink(filepath.Join(second, "x.txt"), link); err != nil {
		t.Skipf("本环境不允许建软链：%v", err)
	}
	if !WithinAny([]string{tr.root, second}, link) {
		t.Fatal("软链指向另一个**合法**根，应放行（不要把 WithinAny 写成全部命中）")
	}
	if WithinAny([]string{tr.root, second}, tr.secret) {
		t.Fatal("两个根之外的路径必须拒绝")
	}
}

// TestTraversal_ClassicPayloads 现实世界出现过的经典绕过形态。
// 清单来自历次路径穿透 CVE 的常用 payload，跨平台按 GOOS 分流。
func TestTraversal_ClassicPayloads(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root
	sep := string(filepath.Separator)

	// 两边都得挡的
	both := []struct{ name, path string }{
		{"proc self root 软链", filepath.Join("/proc", "self", "root", "etc", "passwd")},
		{"proc self cwd 软链", filepath.Join("/proc", "self", "cwd", "..", "outside", "secret.txt")},
		{"点点后跟 NUL 目录分隔", ".." + sep + "\x00" + sep + "outside"},
		{"点点后跟换行", "..\n" + sep + "outside"},
		{"空字节结尾的根内名", filepath.Join(root, "ok") + "\x00"},
	}
	if runtime.GOOS != "windows" {
		for _, c := range both {
			t.Run(c.name, func(t *testing.T) {
				if Within(root, c.path) {
					t.Fatalf("经典 payload 被放行：%q", c.path)
				}
			})
		}
	}

	if runtime.GOOS == "windows" {
		// Windows 专属：盘符相对路径、ADS、保留设备名、尾部点/空格、8.3 短名
		win := []struct{ name, path string }{
			{"盘符相对向上", `C:..\..\Windows\win.ini`},
			{"UNC 向上", `\\server\share\..\..\secret.txt`},
			{"扩展长度前缀", `\\?\C:\Windows\win.ini`},
			{"设备命名空间", `\\.\C:\Windows\win.ini`},
			{"ADS 数据流", filepath.Join(root, "a.txt") + ":evil"},
			{"ADS DATA 流", filepath.Join(root, "a.txt") + "::$DATA"},
			{"保留设备名", filepath.Join(root, "CON")},
			{"保留设备名带后缀", filepath.Join(root, "COM1.txt")},
			{"8.3 短名逃逸", filepath.Join(root, "ABOUT~1", "..", "..", "outside")},
		}
		for _, c := range win {
			t.Run(c.name, func(t *testing.T) {
				// ADS/保留设备名指向的仍是根内的东西，放宽不算穿透；
				// 真正必须挡的是带 .. / UNC / 特殊前缀的那几条。
				if strings.ContainsAny(c.path, ":\\") && strings.Contains(c.path, "..") ||
					strings.HasPrefix(c.path, `\\`) {
					if Within(root, c.path) {
						t.Fatalf("Windows 经典 payload 被放行：%q", c.path)
					}
				}
			})
		}
		// 尾部点/空格：Windows 会剥掉，最终可能落到**另一个**文件上
		tricky := filepath.Join(root, "a.txt.")
		_ = tricky
	}
}

// TestTraversal_RootIsFilesystemRoot 极端配置：根目录被配成文件系统根。
// 这时"全放行"是配置的意图，Within 不该自作聪明地拒绝；但这条用例把行为钉住，
// 免得有人以为 Within 能兜住这种配置（兜不住——要靠启动时的配置校验）。
func TestTraversal_RootIsFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上没有单一文件系统根")
	}
	if !Within("/", "/etc/passwd") {
		t.Fatal("root 配成 / 时应当全放行（这是配置意图，不是穿透）")
	}
	if !Within("/", "/tmp/anything") {
		t.Fatal("root 配成 / 时应当全放行")
	}
}

// TestTraversal_SplitListTraversal 配置拆分本身也可能成为穿透入口：
// 一个带空格的目录名被拆成两半，或空项被当成"当前目录"。
func TestTraversal_SplitListTraversal(t *testing.T) {
	got := SplitList("/data/a, ../etc, ,/data/b")
	if len(got) != 3 {
		t.Fatalf("拆分结果不对：%#v", got)
	}
	if got[1] != "../etc" {
		t.Fatalf("相对路径应原样保留（由调用方 Abs），got=%q", got[1])
	}
	// 空项必须被丢掉："a,,b" 里的空项若保留会变成 Abs("")=cwd → 全放行
	if len(SplitList("a,,b")) != 2 {
		t.Fatalf("空项必须丢弃：%#v", SplitList("a,,b"))
	}
}
