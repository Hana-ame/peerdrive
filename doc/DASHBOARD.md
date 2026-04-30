# Peerdrive 项目仪表盘

> 组长: Claude Opus · 更新: 2026-04-29 · 前端 complain 修复 + BT 38/38 重确认

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
internal/<module>/    (Go 后端)
react/src/            (React 前端)
```

## 运行测试

```bash
# 从项目根目录运行：
cd /mnt/d/WorkPlace/peerdrive

# 全部模块
bash back/test/bt-full-test.sh
bash back/test/ipfs-full-test.sh
bash back/test/p2p-full-test.sh
bash back/test/storage-full-test.sh
bash back/test/auth-full-test.sh

# 一键全部
bash back/test/all.sh
```

## 当前状态

| 模块 | 代码 | 文档 | 测试脚本 | 测试结果 | 闭环 |
|------|------|------|----------|----------|------|
| BT | ✅ merged | ✅ | ✅ | ✅ 38/38 | ✅ |
| IPFS | ✅ merged | ✅ | ✅ | ✅ | ✅ |
| P2P | ✅ merged | ✅ | ✅ | ✅ 35/35 | ✅ |
| Storage | ✅ merged | ✅ | ✅ | ✅ 28/28 | ✅ |
| Auth | ✅ merged | ✅ | ✅ | ✅ 20/20 | ✅ |
| WebRTC | ✅ | ✅ | ✅ | ✅ 23/23 | ✅ |
| Resume | ✅ | ✅ | ✅ | ✅ | ✅ |

## 最新突破 (2026-04-29)

### BT 协议完整验证
- **全球 DHT 网络**: Peerdrive BT DHT 连接到全球 Mainline DHT，发现真实 Transmission 客户端
- **Tracker 发现**: HTTP tracker announce + bencode 解析
- **Wire Protocol**: handshake → bitfield → unchoke → piece request → SHA1 验证
- **端到端**: 1MB 文件 16 片完整下载，SHA256 = `39b90efc...` ✓

### 6 项任务全部完成
详见 [TASK-COMPLETION-2026-04-29.md](report/TASK-COMPLETION-2026-04-29.md)

### complain.txt 前端修复 (2026-04-29 第二轮)
- **WebDAV URL**: 修复为后端 API 地址而非 CF Pages 前端地址 (Settings.jsx)
- **无效链接**: 移除 "Board 666" 死链接 (Settings.jsx)  
- **广播入口**: Plaza 广播按钮移除，广播仅在合集内操作 (Plaza.jsx)
- **按钮文案**: "🌐 P2P 打开" → "📡 广播" (AnonExplorer.jsx)
- **alert() 消除**: 3 处 alert 弹窗 → 内联 toast 消息 (AnonExplorer.jsx)
- **BT 重确认**: BEP44 38/38 PASS，前端编译 0 错误
