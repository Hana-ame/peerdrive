# Peerdrive 测试文档索引

> 面向测试人员和开发者的测试文档门户
> 更新: 2026-04-29

---

## 核心文档

| 文件 | 说明 |
|------|------|
| [TESTING-HANDBOOK.md](TESTING-HANDBOOK.md) | **测试手册** — 完整测试指南：环境搭建、测试运行、手动流程、故障排查、混沌测试 |
| [TEST-MATRIX.md](TEST-MATRIX.md) | **测试矩阵** — 109 项测试用例，覆盖全部功能点 |
| [TESTING-METHODOLOGY.md](TESTING-METHODOLOGY.md) | **测试方法详解** — 9 个 Phase 的测试方法论，含 VPS 真实环境测试 |

---

## 测试运行指南

### 按角色分类

| 角色 | 推荐阅读 | 推荐运行 |
|------|----------|----------|
| **新加入的测试人员** | [TESTING-HANDBOOK.md](TESTING-HANDBOOK.md) 第1-3节 | `go test ./...` → `e2e-all.sh` |
| **功能验证** | [TEST-MATRIX.md](TEST-MATRIX.md) 相关章节 | 对应模块专项测试 |
| **P2P/BT 专项测试** | [TESTING-METHODOLOGY.md](TESTING-METHODOLOGY.md) Phase 2-7 | `p2p.sh`, `relay.sh`, `bt-full-test.sh` |
| **前端测试** | [TESTING-METHODOLOGY.md](TESTING-METHODOLOGY.md) Phase 8 | `npx playwright test` |
| **安全测试** | [../report/SECURITY-REVIEW.md](../report/SECURITY-REVIEW.md) | 手动验证 path traversal / auth bypass |
| **混沌测试** | [CHAOS_TESTING.md](CHAOS_TESTING.md) | `chaos-net.sh start` → `test-under-chaos.sh` |

### 按测试类型分类

| 类型 | 运行命令 | 耗时 | 需要服务 |
|------|----------|------|----------|
| Go 单元测试 | `go test ./... -count=1` | ~1s | 否 |
| E2E 全端点 | `bash test/e2e-all.sh` | ~30s | 否（自包含） |
| BT 全功能 | `bash test/bt-full-test.sh` | ~60s | 是 :3000 |
| IPFS 全功能 | `bash test/ipfs-full-test.sh` | ~30s | 是 :3000 |
| P2P 全功能 | `bash test/p2p-full-test.sh` | ~30s | 是 :3000 |
| Storage 全功能 | `bash test/storage-full-test.sh` | ~30s | 是 :3000 |
| Auth 全功能 | `bash test/auth-full-test.sh` | ~20s | 是 :4000 |
| WebRTC 信令 | `bash test/webrtc_signal_test.sh` | ~15s | 是 :3000 |
| P2P 双节点 | `bash test/p2p.sh` | ~30s | 否（自包含） |
| Relay 穿透 | `bash test/relay.sh` | ~30s | 否（自包含） |
| 一键全模块 | `bash test/all.sh` | ~2min | 是 :3000 |
| 前端 | `npx playwright test` | ~30s | 是 :5173 |

---

## 专项文档

| 文件 | 说明 |
|------|------|
| [test-steps.md](test-steps.md) | **P2P 测试实录** — VPS 双节点部署、连接、文件交换、Relay 完整步骤日志 |
| [test-case-spec.md](test-case-spec.md) | **测试用例规格** — sha256/匿名合集/P2P Stage 2/3 的需求级测试说明 |
| [register.md](register.md) | **注册与下载测试** — RegisterLocal + SHA256 下载的集成测试说明 |
| [test-peers.md](test-peers.md) | **Peer 测试** — 多节点互联测试 |
| [p2p-stage2-report.md](p2p-stage2-report.md) | **P2P Stage 2 测试报告** — mDNS/DHT/Exchange 测试结果 |
| [CHAOS_TESTING.md](CHAOS_TESTING.md) | **混沌测试** — tc/iptables 模拟恶劣网络条件，完整使用指南 |
| [README.md](README.md) | **旧版测试概述** — 测试分类和 CI 配置说明 |

---

## 测试环境

| 环境 | 地址 | 端口 | 说明 |
|------|------|------|------|
| 本地开发 | localhost | 3000 | WSL Go 服务 |
| 本地前端 | localhost | 5173 | Vite dev server |
| VPS | bwh.moonchan.xyz | 3000 | 公网 P2P Relay |
| VPS | bwh.moonchan.xyz | 4000 | Registration Server |
| CF Tunnel | wsl-3000.moonchan.xyz | 443 | WSL 穿透到公网 |
| CF Pages | peerdrive.pages.dev | 443 | 前端生产部署 |

---

## 快速入口

```bash
# 最简验证 — 确认服务活着
curl -x "" http://localhost:3000/ping

# 全部单元测试 — 1 秒出结果
cd go && go test ./... -count=1

# 完整 E2E — 覆盖所有 HTTP 端点
cd go && bash test/e2e-all.sh

# BT 38 项测试 — 需服务在 :3000
cd go && bash test/bt-full-test.sh
```
