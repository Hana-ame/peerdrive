# Peerdrive 测试文档

> 入口 → [测试方案](测试方案.md) · 更新: 2026-04-29

---

## 核心文档

| 文档 | 适合 | 内容 |
|------|------|------|
| [**测试方案**](测试方案.md) | 所有人 | 测试体系总览、分类、运行方式、文档导航 |
| [**如何测试**](如何测试.md) | 开发者 | 每条测试的运行命令、预期输出、常见错误和排错 |
| [**TEST-PIPELINE**](TEST-PIPELINE.md) | 排错 | 14 个测试脚本的流程、预期行为、错误原因分析 |
| [**TEST-MATRIX**](TEST-MATRIX.md) | QA | 109 项测试用例的 ID、前置条件、步骤、预期结果 |
| [**TESTING-HANDBOOK**](TESTING-HANDBOOK.md) | 入门 | 840 行完整手册：环境搭建、架构、手动流程、排错 |

## 扩展

| 文档 | 内容 |
|------|------|
| [TESTING-METHODOLOGY](TESTING-METHODOLOGY.md) | 9 Phase 测试方法论 |
| [CHAOS_TESTING](CHAOS_TESTING.md) | 混沌测试——恶劣网络模拟 |
| [README](README.md) | 原始 README |

## 快速命令

```bash
go test ./... -count=1              # 163 单元测试
bash test/e2e-all.sh                # 85 E2E 断言（自包含）
bash test/bt-full-test.sh           # 38 BT 专项
bash test/webrtc_signal_test.sh     # 23 WebRTC
bash test/p2p.sh                    # 13 P2P 双节点（自包含）
bash test/all.sh                    # 一键全量
```

## 测试环境

| 环境 | 地址 | 端口 |
|------|------|------|
| 本地 | localhost | 3000 |
| VPS Relay | bwh.moonchan.xyz | 3000 |
| Reg Server | bwh.moonchan.xyz | 4000 |
| CF Tunnel | wsl-3000.moonchan.xyz | 443 |

## 报告

| 文档 | 内容 |
|------|------|
| [../report/测试报告-2026-04-29.md](../report/测试报告-2026-04-29.md) | 最新测试报告 389+ PASS |
| [../report/CI-FIXES.md](../report/CI-FIXES.md) | CI 修复记录 |

## 归档

旧版测试文档移至 [archive/](archive/)，仅供参考。
