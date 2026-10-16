package main

import (
	"os"
	"strings"
	"testing"
)

// TestDemoEnvUsesPortNotPeerdrivePort 守住 v0.3.0 那类回归的同一个变种。
//
// 背景：主服务读的是 `PORT`，不是 `PEERDRIVE_PORT`（config.go:213）。
// v0.3.0 的 reg 子命令犯过一次同类的错（把 PORT 当监听地址直接用），
// 症状完全一致：**环境变量被静默忽略 → 服务照常起在默认端口**，
// 用户以为配好了，实际连错地方。demo 里写成 PEERDRIVE_PORT 时，
// 表现是 \"bind: address already in use\"（默认 3000 已被占用）。
//
// 为什么放在 demo 上守：demo 是给「第一次用它的人」跑的第一条命令，
// 这里的端口配错 = 第一印象直接坏掉。
func TestDemoEnvUsesPortNotPeerdrivePort(t *testing.T) {
	env := demoEnv("/tmp/x", "n", 19101, "", "")
	var port, wrong string
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "PORT="):
			port = kv
		case strings.HasPrefix(kv, "PEERDRIVE_PORT="):
			wrong = kv
		}
	}
	if port != "PORT=19101" {
		t.Errorf("演示节点应设 PORT=19101，实际 %q（漏了会静默起在 3000）", port)
	}
	if wrong != "" {
		t.Errorf("出现了 %q —— 主服务不读这个变量，会被静默忽略", wrong)
	}
}

// TestDemoEnvDeclaresShareDirs 守住「共享目录必须显式声明」这条边界。
//
// 未声明的目录一律拒绝读取（防任意文件读写的边界），不是「目录存在就放行」。
// 漏掉这个变量时，演示的前 6 步仍然全绿，只有「清单」那步空——
// 症状离原因很远，很难自查。
func TestDemoEnvDeclaresShareDirs(t *testing.T) {
	env := demoEnv("/tmp/x", "n", 19101, "", "/data/shared")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PEERDRIVE_SHARE_DIRS=/data/shared") {
		t.Error("演示未声明 PEERDRIVE_SHARE_DIRS，共享清单会恒为空")
	}
	if !strings.Contains(joined, "PEERDRIVE_SHARE_ENABLE=true") {
		t.Error("演示未打开 PEERDRIVE_SHARE_ENABLE")
	}
}

// TestDemoEnvDoesNotGateSignaling 守住「本机演示不设信令 token」。
//
// 设了 PEERJS_TOKENS 之后信令要求带 token，而节点侧没有对应字段可发，
// WebSocket 握手直接 bad handshake —— 症状是节点反复 \"connect failed\"、
// 信令 /discover/nodes 永远为空、演示第 4 步失败。距原因很远。
func TestDemoEnvDoesNotGateSignaling(t *testing.T) {
	if os.Getenv("PEERJS_TOKENS") == "" {
		t.Skip("只在带 token 的环境下才有意义")
	}
	// demo 自己起的信令不走 demoEnv，这里只钉住意图：
	// demoEnv 不应该注入任何 token 门禁。
	joined := strings.Join(demoEnv("/tmp/x", "n", 19101, "", ""), "\n")
	if strings.Contains(joined, "PEERJS_TOKENS") {
		t.Error("demoEnv 不应注入 PEERJS_TOKENS")
	}
}

// TestDemoHelperKeepsPortsOffDefaults 确认演示端口不与默认端口相撞。
func TestDemoHelperKeepsPortsOffDefaults(t *testing.T) {
	for _, p := range []int{demoSignalPort, demoNodeAPort, demoNodeBPort} {
		if p == 3000 || p == 9000 || p == 4000 {
			t.Errorf("演示端口 %d 与服务默认端口相撞", p)
		}
	}
}