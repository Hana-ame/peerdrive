# Peerdrive 项目报告总览

> 更新: 2026-04-29 · 涵盖全部 16 份报告文档

---

## 项目当前状态

Peerdrive 是一个 P2P 文件共享系统，技术栈为 Go (Gin + libp2p + SQLite) 后端 + React (Vite + TypeScript + Tailwind) 前端。

### 模块闭环状态

| 模块 | 代码 | 文档 | 测试 | 结果 | 闭环 |
|------|------|------|------|------|------|
| BT (BitTorrent) | merged | modules/bt/ | bt-full-test.sh | 38/38 PASS | ✅ |
| IPFS | merged | modules/ipfs/ | ipfs-full-test.sh | PASS | ✅ |
| P2P (libp2p) | merged | modules/p2p/ | p2p-full-test.sh | 35/35 PASS | ✅ |
| Storage | merged | modules/storage/ | storage-full-test.sh | 28/28 PASS | ✅ |
| Auth (reg server) | merged | modules/auth/ | auth-full-test.sh | 20/20 PASS | ✅ |
| WebRTC | merged | modules/ipfs/ | webrtc_signal_test.sh | 23/23 PASS | ✅ |
| Resume/MultiPeer | merged | spec/API-REFERENCE.md | verified | PASS | ✅ |

### 关键指标

- **API 端点**: 105 个 (96 路径)，Swagger 文档完整
- **Go 单元测试**: 66 个，覆盖 5 个包
- **E2E 测试**: 85 条断言，12 个测试段
- **BT DHT**: 连接全球 Mainline DHT，13+ 节点，发现真实 Transmission 客户端
- **BT 端到端**: 1MB/16片完整下载，SHA256 验证通过
- **Swagger UI**: `/swagger/index.html`，96 路径全部覆盖

---

## 报告分类

### 一、活跃维护中（4 份）

这些文档反映当前状态，持续更新。

| 文件 | 说明 |
|------|------|
| [TODO-FIXES.md](TODO-FIXES.md) | **当前任务追踪** — P0/P1/P2 优先级，修复状态实时更新 |
| [TASK-COMPLETION-2026-04-29.md](TASK-COMPLETION-2026-04-29.md) | **6 项任务完成报告** — BEP44 / 断点续传 / 前端合集 / 文件浏览 / WebRTC / BT 端到端 |
| [SECURITY-REVIEW.md](SECURITY-REVIEW.md) | **安全审查** — 路径穿越、CORS、认证、P2P 攻击面，14 项发现 |
| [../DASHBOARD.md](../DASHBOARD.md) | **项目仪表盘** — 模块闭环状态、测试汇总、Agent 分工 |

### 二、参考文档（3 份）

这些文档记录设计决策和配置，完成后不再更新。

| 文件 | 说明 |
|------|------|
| [DEVELOPMENT_PLAN.md](DEVELOPMENT_PLAN.md) | 35KB 完整 Phase 2 开发计划 — P2P 合集发现、AuthKey/JWT 认证、Relay 优化 |
| [memo-go.md](memo-go.md) | Go 环境配置备忘 — 代理/WSL/CF Tunnel 设置 |
| [refactor-report.md](refactor-report.md) | 代码重构报告 — Service 层引入、路由拆分、Auth 移除、本地注册优化 |

### 三、已完成里程碑（4 份）

这些文档记录已完成的重要里程碑，存档保留。

| 文件 | 说明 |
|------|------|
| [MILESTONE-P2P.md](MILESTONE-P2P.md) | P2P 节点互通里程碑 — libp2p 双节点连接、文件交换、合集同步 |
| [MILESTONE-p2p-vps.md](MILESTONE-p2p-vps.md) | VPS 部署里程碑 — bwh.moonchan.xyz 部署、systemd 服务、CF Tunnel |
| [TODO-P2P-DUAL-STACK.md](TODO-P2P-DUAL-STACK.md) | P2P 双栈 TODO — IPFS + BT 双 DHT 全部完成 |
| [ROADMAP.md](ROADMAP.md) | v3.0 路线图 — 全部功能已实现 |

### 四、过时文档（5 份）

历史快照，内容已落地或过时，不再适用。

| 文件 | 说明 |
|------|------|
| [changelog.md](changelog.md) | `feat/stage2-e2e-test` 分支记录，已合并 |
| [MEMO.md](MEMO.md) | v3.0 备忘录，内容已落地到代码 |
| [ISSUES_FOR_GEMINI.md](ISSUES_FOR_GEMINI.md) | 给 Gemini agent 的上下文文档，已处理 |
| [grid.md](grid.md) | P2P 测试网格规划，已执行完毕 |
| [TXT-REPLY.md](TXT-REPLY.md) | 对 archive/ 下 .txt 投诉的逐条回复 |

---

## 项目演进时间线

```
2026-04-27  基础架构        File upload/register, SHA256 CAS, AnonCollection
2026-04-27  重构            Service 层引入，路由拆分，Auth 移除
2026-04-28  P2P Stage 2     libp2p DHT + mDNS，Exchange 协议，合集同步
2026-04-28  P2P Stage 3     Relay 中继，NAT 穿透，WS 传输
2026-04-28  VPS 部署        bwh.moonchan.xyz，systemd，CF Tunnel
2026-04-28  Auth Module     注册服务器，JWT 认证，Relay 注册
2026-04-28  BT Module       Mainline DHT，BEP 44/51，Wire Protocol
2026-04-28  IPFS Module     CID 索引，Pin/Unpin，网关健康检查
2026-04-29  BT 端到端       1MB/16片下载，SHA256 验证通过
2026-04-29  WebRTC 信令     23/23 测试通过
2026-04-29  断点续传        ResumeManager 端点接入路由
2026-04-29  前端修复        complain.txt 5 项修复，编译零错误
2026-04-29  Swagger 重建    24→96 路径，105 端点全覆盖
```

---

## 剩余工作

| 项目 | 优先级 | 状态 |
|------|--------|------|
| Docker 5 节点全通测试 | P1 | 代码就绪，待执行 |
| Storage test 4 failures | P1 | 已知问题 |
| 前端目录浏览回退导航 | P2 | 已知问题 |
| Python seeder tracker 被代理拦截 | P2 | 已知问题 |
| 安全修复（路径穿越、认证、限流） | P0-P2 | 审查完成，待修复 |
| 开发计划 Phase 2.1-2.7 | P1-P2 | 设计完成，待实现 |

---

## 阅读顺序建议

1. **[DASHBOARD.md](../DASHBOARD.md)** — 了解项目全貌和模块状态
2. **[TODO-FIXES.md](TODO-FIXES.md)** — 了解当前待修复项
3. **[TASK-COMPLETION-2026-04-29.md](TASK-COMPLETION-2026-04-29.md)** — 了解最新完成的功能
4. **[SECURITY-REVIEW.md](SECURITY-REVIEW.md)** — 了解安全风险
5. **[DEVELOPMENT_PLAN.md](DEVELOPMENT_PLAN.md)** — 了解下一步开发方向
