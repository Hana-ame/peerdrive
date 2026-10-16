package main

import "testing"

// normalizePort 的测试随代码搬到了 internal/httpd/addr_test.go
// （TestNormalizePort / TestNormalizePortNeverDoublesPrefix）——这里只剩
// reg 子命令 flag 装配的护栏。

// TestRegFlagDefaultNormalization 守住 runReg **真正用的那份** flag 定义。
//
// 这条护栏改了三版才立住，前两版都是「永远绿的测试」：
//  1. 只测 normalizePort —— 摘掉 runReg 里的调用，测试照样绿（函数没坏，
//     坏的是没人调它，而那正是实际发生的回归）；
//  2. 改测 regAddrFromEnv —— 它有自己的实现，跟 runReg 那个 flag 无关，
//     把 runReg 改回 envOr("PORT", ":4000") 还是绿。
//
// 现在 runReg 与测试读同一个 regFlagSet()，摘掉 httpd.NormalizePort 即失败。
func TestRegFlagDefaultNormalization(t *testing.T) {
	t.Setenv("PORT", "4000")
	fs := regFlagSet()
	got := fs.Lookup("addr").Value.String()
	if got != ":4000" {
		t.Errorf("reg -addr default with PORT=4000 = %q, want \":4000\"", got)
	}
}

// TestRegFlagSetHasExpectedFlags 防止以后加 flag 时忘了同步抽出的构造器。
func TestRegFlagSetHasExpectedFlags(t *testing.T) {
	fs := regFlagSet()
	for _, name := range []string{"addr", "db", "tls-cert", "tls-key"} {
		if fs.Lookup(name) == nil {
			t.Errorf("regFlagSet() missing -%s", name)
		}
	}
}
