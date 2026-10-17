// deploy_script_test.go — 给部署脚本里**已经出过事**的几处加回归闸门。
//
// 背景（2026-10-06，全部实测撞出来）：
//
//   1. `systemctl start` 对已在运行的服务是**空操作**。脚本上传新二进制、
//      重载单元、打印「✅ 服务已启动」，而旧进程仍在用被删掉的 inode 继续服务
//      （/proc/<pid>/exe 显示 (deleted)）。脚本必须用 restart。
//
//   2. scp 直接覆盖运行中的可执行文件会 `Text file busy`——只有服务在跑时撞上，
//      所以「第一次成功、第二次起失败」。必须先传 .new 再 mv。
//
//   3. 远端 grep 自己副本拿变量 → 拿到字面量/空串，于是写出 `server_name ;`，
//      nginx -t emerg，而脚本紧接着 reload 并打印「✅ nginx 已配置」。
//      必须在 reload 前校验 nginx -t，且传值靠 PD_DOMAIN 而不是远端 grep。
//
// 这些都是「脚本读起来对」但「跑起来错」的地方。断言脚本文本看起来合理
// 没有意义，真正该断言的是**它不能再退回到那个坏写法**。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readScript(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "scripts", "deploy-peersignal.sh"))
	if err != nil {
		t.Fatalf("读 deploy-peersignal.sh: %v", err)
	}
	return string(b)
}

// TestDeployUsesRestartNotStart 保证用 restart。
//
// 对照为什么必要：`grep -q "systemctl start"` 也会命中 `systemctl restart`
// 里的 "start" 子串——所以这里匹配整条命令，且排除注释行。
func TestDeployUsesRestartNotStart(t *testing.T) {
	s := readScript(t)
	foundRestart := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // 注释里可以讨论这个坑
		}
		switch {
		case strings.Contains(trimmed, "systemctl restart"):
			foundRestart = true
		case strings.Contains(trimmed, "systemctl start"):
			t.Errorf("发现裸 systemctl start：对已在运行的服务它是空操作，旧进程会继续用"+
				"被删掉的 inode 服务，而脚本照样打印「✅ 已启动」。必须用 restart。\n  %s", trimmed)
		}
	}
	if !foundRestart {
		t.Error("脚本里没找到 systemctl restart——服务不会被重启，新二进制不会生效")
	}
}

// TestDeployDoesNotScpOverRunningBinary 保证不会 scp 到正在运行的可执行文件。
func TestDeployDoesNotScpOverRunningBinary(t *testing.T) {
	s := readScript(t)
	hasNewSuffix := strings.Contains(s, ".new") || strings.Contains(s, ".tmp")
	if !hasNewSuffix {
		t.Error("没看到 .new/.tmp 暂存名：scp 就地截断写运行中的可执行文件会 Text file busy。" +
			"应先传到临时路径再 mv（rename 是原子的）。")
	}
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "scp") {
			continue
		}
		// 目标不能是 ${REMOTE_DIR}/peersignal 本身
		if strings.Contains(trimmed, "${REMOTE_DIR}/peersignal ") ||
			strings.HasSuffix(strings.TrimSpace(trimmed), "${REMOTE_DIR}/peersignal\"") {
			t.Errorf("scp 直接写运行中的可执行文件：\n  %s\n  改为先传 .new 再 mv -f。", trimmed)
		}
	}
}

// TestDeployValidatesNginxBeforeReload 保证 reload 之前先 nginx -t，且失败即中止。
//
// 这是本轮最隐蔽的一个：原来 nginx -t 失败后脚本**照样** reload 并打印
// 「✅ nginx 已配置」，旧 worker 顶着旧配置继续服务，HTTP 探测照样通，
// 只有 WebSocket 升级受影响。
func TestDeployValidatesNginxBeforeReload(t *testing.T) {
	s := readScript(t)
	lines := strings.Split(s, "\n")

	idxTest, idxReload := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "nginx -t") && idxTest < 0 {
			idxTest = i
		}
		if strings.Contains(trimmed, "systemctl reload nginx") && idxReload < 0 {
			idxReload = i
		}
	}
	switch {
	case idxTest < 0:
		t.Error("脚本里没有 nginx -t：配置坏了也不会知道")
	case idxReload < 0:
		t.Error("脚本里没有 systemctl reload nginx")
	case idxTest > idxReload:
		t.Errorf("nginx -t 在第 %d 行，reload 在第 %d 行——校验必须在 reload 之前。"+
			"否则配置无效时旧 worker 会继续顶着旧配置服务，把「配置已坏」伪装成正常。",
			idxTest+1, idxReload+1)
	}
}

// TestDeployDoesNotGrepOwnCopyOnRemote 保证不再用「远端 grep 脚本自己副本」取变量。
//
// 那个写法在远端执行时那份文件根本不存在，于是变量为空：
// KEY 拿到字面量、DOMAIN 拿到空串 → server_name ; → nginx emerg。
func TestDeployDoesNotGrepOwnCopyOnRemote(t *testing.T) {
	s := readScript(t)
	for i, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// 形如 grep '^KEY=' /tmp/deploy-peersignal.sh —— 远端读自己那份拷贝
		if strings.Contains(trimmed, "grep") && strings.Contains(trimmed, "deploy-peersignal.sh") {
			t.Errorf("第 %d 行在远端 grep 脚本自己的副本：\n  %s\n"+
				"  那份文件不在远端，变量会解析成空串/字面量。应改为本地求值后用 PD_* 传进去。",
				i+1, trimmed)
		}
	}
}

// TestDeployAlwaysRebuildsBinary 保证无条件重建，而不是「文件已存在就跳过」。
//
// 原来 `[ ! -f "$BINARY" ]` 为假时直接用 /tmp 里的旧二进制——它不认识新加的
// --ops-token，服务以 status=2/INVALIDARGUMENT 起不来。
func TestDeployAlwaysRebuildsBinary(t *testing.T) {
	s := readScript(t)
	// ⚠️ 只看**非注释**行：这段历史上就存在，且脚本里有一段注释专门解释
	// 「原来是 [ ! -f "$BINARY" ]」——朴素子串匹配会命中注释，把正确的脚本报成错的。
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, `[ ! -f "$BINARY" ]`) {
			t.Errorf("脚本仍是「二进制文件不存在才构建」：会把磁盘上残留的旧产物推上线。\n  %s\n"+
				"  对「把新代码送上线」这个动作，缓存是负资产——应无条件重建。", trimmed)
		}
	}
	// 无条件重建的标志：构建块不再被任何 [ -f ... ] 条件包住。
	if !strings.Contains(s, "每次都重建") {
		t.Error("脚本里没有「每次都重建」的痕迹，需确认构建无条件执行。")
	}
}

// TestDeployPassesDomainAsEnv 保证域名是本地求值后传进远端的，而不是远端自己找。
func TestDeployPassesDomainAsEnv(t *testing.T) {
	s := readScript(t)
	if !strings.Contains(s, "PD_DOMAIN=") {
		t.Error("没有 PD_DOMAIN：域名没有从本地传进远端，远端只能自己猜（于是得到空串）。")
	}
	if !strings.Contains(s, "server_name PEERSIGNAL_DOMAIN_PLACEHOLDER") {
		t.Error("nginx 模板里没有 PEERSIGNAL_DOMAIN_PLACEHOLDER 占位符——" +
			"如果直接写字面量，改域名时容易漏改其中一处。")
	}
}

// TestDeployKeyIsStableNotRandomPerRun 保证信令 key 不再每次随机生成。
//
// 这条直接对应「每部署一次所有客户端连不上」的故障：客户端硬编码 key，
// 脚本却每跑一次换一个。必须是「读服务器上已有的，读不到才生成」。
func TestDeployKeyIsStableNotRandomPerRun(t *testing.T) {
	s := readScript(t)
	// ⚠️ 只看**顶层**的 KEY= 赋值（即不缩进的那些）。
	// load_or_create_key() 内部「读不到才生成」分支里的
	//     KEY="pd-signal-$(openssl rand -hex 12)"
	// 是**正确**的——它只在服务器上还没有 key 时才跑一次。朴素匹配会把它误判。
	// 真正的坏写法是顶层 `KEY="pd-signal-$(openssl rand …)"`（每次部署都换）。
	for i, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(trimmed, `KEY="pd-signal-$`) {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent != 0 {
			continue // 缩进的 = 函数内的「仅首次生成」分支，正确
		}
		t.Errorf("第 %d 行：顶层 KEY 由 openssl rand 生成 = 每次部署都换 key：\n  %s\n"+
			"  客户端硬编码了 key，会全部连不上。应从服务器上已有的 key 文件读取。",
			i+1, trimmed)
	}
	// 幂等性的实际来源：必须先尝试读服务器上已有的 key。
	if !strings.Contains(s, "load_or_create_key") {
		t.Error("脚本没有 load_or_create_key，key 无从持久化。")
	}
	if !strings.Contains(s, "signal.key") {
		t.Error("脚本没有引用服务器上的 key 文件，key 无处持久化。")
	}
}

// TestOpsTokenIsSeparateFromSignalingWhitelist 保证 --ops-token 与 -tokens 是两个开关。
//
// 复用同一个开关的后果实测过：所有不发 token 的既有节点全部建不了 WebSocket，
// 跨节点传输断掉，而 HTTP 探测全部 200 —— 看起来完全健康。
func TestOpsTokenIsSeparateFromSignalingWhitelist(t *testing.T) {
	s := readScript(t)
	if !strings.Contains(s, "--ops-token") {
		t.Error("单元命令里没有 --ops-token：运维面鉴权只能复用 -tokens，" +
			"而那会让所有不发 token 的既有节点连不上信令。")
	}
	// 单元里出现 --tokens 说明信令白名单被打开了，默认应保持不填
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "ExecStart=") && strings.Contains(line, "--tokens ") {
			t.Errorf("ExecStart 里带了 --tokens：信令注册白名单一旦非空，所有节点都必须发白名单里的 token。\n  %s", strings.TrimSpace(line))
		}
	}
}
