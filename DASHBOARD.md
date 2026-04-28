# Peerdrive 项目仪表盘

> 组长: Claude Opus · 更新: 2026-04-28 · 5 个模块并行

---

## 模块分工

| 模块 | Agent | Worktree | 文档 | API 设计 | 测试脚本 | 测试结果 |
|------|-------|----------|------|----------|----------|----------|
| 📥 BT | bt-agent | worktrees/bt-agent | modules/bt/ | API-DESIGN.md | bt-full-test.sh | bt-full-test-results.txt |
| 🌐 IPFS | ipfs-agent | worktrees/ipfs-agent | modules/ipfs/ | API-DESIGN.md | ipfs-full-test.sh | ipfs-full-test-results.txt |
| 🔗 P2P | p2p-agent | worktrees/p2p-agent | modules/p2p/ | API-DESIGN.md | p2p-full-test.sh | p2p-full-test-results.txt |
| 💾 Storage | storage-agent | worktrees/storage-agent | modules/storage/ | API-DESIGN.md | storage-full-test.sh | storage-full-test-results.txt |
| 🔐 Auth | auth-agent | worktrees/auth-agent | modules/auth/ | API-DESIGN.md | auth-full-test.sh | auth-full-test-results.txt |

## 每个 Agent 的闭环节点

```
API-DESIGN.md (设计文档)
    ↓
<module>-full-test.sh (可执行测试脚本)
    ↓
<module>-full-test-results.txt (实际运行结果)
    ↓
TODO-FIXES.md 状态更新 (完成情况)
```

## 查找方式

```
# 文档
docs/modules/<module>/

# 测试脚本 + 结果
go/test/<module>-full-test.sh
go/test/<module>-full-test-results.txt

# 代码
worktrees/<module>-agent/    (Go 后端)
frontend-worktrees/<module>-frontend/  (React 前端)
```

## 运行测试

```bash
# 全部模块
bash go/test/bt-full-test.sh
bash go/test/ipfs-full-test.sh
bash go/test/p2p-full-test.sh
bash go/test/storage-full-test.sh
bash go/test/auth-full-test.sh

# 一键全部
bash go/test/all.sh
```

## 当前状态

| 模块 | 代码 | 文档 | 测试脚本 | 测试结果 | 闭环 |
|------|------|------|----------|----------|------|
| BT | ✅ merged | 🏃 | 🏃 | 🏃 | ⏳ |
| IPFS | ✅ merged | 🏃 | 🏃 | 🏃 | ⏳ |
| P2P | ✅ merged | 🏃 | 🏃 | 🏃 | ⏳ |
| Storage | ✅ merged | 🏃 | 🏃 | 🏃 | ⏳ |
| Auth | ✅ merged | 🏃 | 🏃 | 🏃 | ⏳ |

⬜ 未开始 · 🏃 进行中 · ✅ 完成 · ⏳ 等待 agent 交付
