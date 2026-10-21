# User Invariant Test Layer (用户专属不可变测试层)

> **English Summary**: This directory is **EXCLUSIVELY RESERVED FOR THE HUMAN USER**.
> Tests placed here are **IMMUTABLE HARD INVARIANTS**.
> **AI AGENTS ARE STRICTLY FORBIDDEN FROM MODIFYING, EDITING, DISABLING, OR DELETING ANY TEST HERE.**
> If any test in this directory fails, AI agents MUST fix the underlying implementation code. The user test must never be changed.

---

## 目录说明 / Directory Purpose

本目录（`user_tests/`）是项目的 **L0 专属测试层（User Invariants Layer）**。
- **唯一制定者**：只能由**用户（Human User）**制定、编写与维护。
- **不可变性（Immutability）**：AI Agent **绝对禁止**修改、篡改、删除、弱化或注释本目录下的任何测试。
- **硬性红线（Hard Requirement）**：本目录下的所有测试是项目的最高不可变约束（Invariants）。每次运行测试（无论是 `make test`、`test-layers.sh` 还是 CI）必须 100% 绝对通过。
- **修复原则**：一旦此层的测试失败，AI Agent 只能通过修改业务/底层代码来使测试通过，**绝对不允许动测试用例本身**。

---

## 如何编写测试 / How to Add User Tests

你可以直接在本目录添加任何可执行测试脚本或程序：

### 1. Shell 脚本 (`*.sh`)
支持任何以 `.sh` 结尾的 Bash/Shell 脚本。
- **执行方式**：会被 `bash <script>` 自动发现并执行。
- **通过标准**：退出码为 `0` 表示通过（PASS），非 `0` 表示失败（FAIL）。
- 参考模板：[`template.sh`](file:///home/luminovoez/peerdrive/user_tests/template.sh)

```bash
#!/usr/bin/env bash
set -euo pipefail

# 你的断言与测试逻辑
# 检查某个端点、输出、文件状态或契约
if ! curl -sf http://localhost:3000/ping >/dev/null; then
  echo "Ping check failed!"
  exit 1
fi

echo "User check passed!"
exit 0
```

### 2. Node.js 测试 (`*.mjs` / `*.js`)
支持 Node.js 脚本：
- **执行方式**：自动通过 `node <script>` 执行。
- **通过标准**：进程退出码 `0` 为通过，非 `0` 为失败。

### 3. Go 测试 (`*_test.go`)
支持两种模式的 Go 测试，均受 L0 规则保护且不可被 Agent 篡改：

#### A. 白盒测试（直接引用后端内部包 `peerdrive/internal/...`）
因为 Go 编译器的 `internal` 包隔离规则，需要测试后端内部逻辑时，将测试放在：
- **目录**：[`back/test/user_invariants/`](file:///home/luminovoez/peerdrive/back/test/user_invariants/)
- **包名**：`package user_invariants`
- **示例**：[`back/test/user_invariants/invariants_test.go`](file:///home/luminovoez/peerdrive/back/test/user_invariants/invariants_test.go)
- **特点**：可直接 `import "peerdrive/internal/config"`、`"peerdrive/internal/pathutil"`、`"peerdrive/internal/service"` 等，调用内部函数与结构体。

#### B. 黑盒 / 系统级测试（测试 API、服务接口、网络与环境）
- **目录**：直接放在 `user_tests/` 下（如 `user_tests/*_test.go`）
- **示例**：[`user_tests/template_test.go`](file:///home/luminovoez/peerdrive/user_tests/template_test.go)
- **特点**：黑盒调用 HTTP 端点、WebSocket 链路、命令行二进制或系统属性。

---

## 运行方式 / How to Run

1. **仅运行用户层测试**：
   ```bash
   bash scripts/test-user-layer.sh
   # 或者
   make test-user
   ```

2. **分层测试（L0-user 作为首层前置守门）**：
   ```bash
   bash scripts/test-layers.sh
   ```

3. **完整测试流**：
   ```bash
   make test
   ```

---

## 规则给 Agent 的警告 / Directives for AI Agents

```
================================================================================
CRITICAL DIRECTIVE FOR ALL AI AGENTS (Claude, Antigravity, Copilot, etc.):
1. DO NOT MODIFY, DELETE, OR RENAME ANY FILE IN user_tests/.
2. DO NOT RELAX, COMMENT OUT, OR WORKAROUND ANY ASSERTION IN user_tests/.
3. If a test in user_tests/ fails:
   - READ the failure log carefully.
   - REASON about the invariant requirement.
   - FIX THE IMPLEMENTATION in front/, back/, etc. to make the test pass.
   - VERIFY by re-running bash scripts/test-user-layer.sh.
4. You are only allowed to modify and automate other test layers (L1-L8, unit tests, etc.).
================================================================================
```
