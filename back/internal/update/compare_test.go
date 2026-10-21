package update

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"v0.3.1", Version{Major: 0, Minor: 3, Patch: 1}},
		{"0.3.1", Version{Major: 0, Minor: 3, Patch: 1}},
		{"v1", Version{Major: 1}},
		{"v1.2", Version{Major: 1, Minor: 2}},
		{"v0.3.1-rc.1", Version{Major: 0, Minor: 3, Patch: 1, Pre: "rc.1"}},
		{"v0.3.1-beta2", Version{Major: 0, Minor: 3, Patch: 1, Pre: "beta2"}},
		{"(dev)", devVersion},
		{"dev", devVersion},
		{"", devVersion},
		{"garbage", devVersion},
		{"v1.2.3.4", devVersion}, // 超过三段 → 不识别
	}
	for _, c := range cases {
		got := ParseVersion(c.in)
		if got != c.want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.3.1", "v0.3.1", 0},
		{"v0.3.0", "v0.3.1", -1},
		{"v0.3.1", "v0.3.0", 1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.3.10", "v0.3.9", 1}, // 数字比较，不是字典序
		{"v0.3.1-rc.1", "v0.3.1", -1},
		{"v0.3.1", "v0.3.1-rc.1", 1},
		{"v0.3.1-rc.2", "v0.3.1-rc.1", 1},
		{"v0.3.1-rc.10", "v0.3.1-rc.2", 1}, // rc.10 > rc.2
		{"v0.3.2-rc.1", "v0.3.1", 1},
		{"(dev)", "v0.3.1", -1},
		{"(dev)", "(dev)", 0},
		{"v0.3.1", "(dev)", 1},
		{"v0.3.1-beta", "v0.3.1-rc.1", -1}, // beta < rc（字典序）
		{"v1", "v1.0.0", 0},
		{"", "v0.1.0", -1}, // 空串按 dev 处理
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
