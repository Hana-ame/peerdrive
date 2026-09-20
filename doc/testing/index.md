# 测试文档入口

> 入口 → [README.md](README.md)（当前总纲）· 更新: 2026-09-20

---

| 文档/脚本 | 内容 |
|-----------|------|
| [**README.md**](README.md) | **测试组件总览**：「改了 X 该跑哪些」选表、14 个组件的命令/规模/CI 映射、盲区清单 |
| [**scripts/test-layers.sh**](../../scripts/test-layers.sh) | 一键按 AOP 分层（L1-L8 + LB + 可选 INT）逐层跑测试 |
| [**分层文档 ../layers/README.md**](../layers/README.md) | L1-L8 每层职责、关键机制、测试、文件清单 |
| [**网盘手测手册 ../NETDISK.md §7**](../NETDISK.md#7-本地跑通怎么亲手测这几个功能) | 端到端起环境 + 分功能 curl 清单 |

## 规模快照（2026-09-20 实测）

| 组件 | 用例 |
|------|------|
| back 主模块单元 | 308 |
| back 集成（`-tags integration -p 1`） | 21 通过 / 4 跳过 |
| back/peerjs（独立 go.mod） | 23 |
| back/signalserver（独立 go.mod，**无 CI job**） | 21 |
| back/p2p_bt（独立 go.mod，**无 CI job**） | 7 |
| front vitest | 88（9 文件） |
| packages/peerdrive-client | 60 |
| packages/peerdrive-media | 21 |

## 快速命令

```bash
bash scripts/test-layers.sh               # L1-L8 + LB 逐层跑，最后汇总
bash scripts/test-layers.sh --integration # 追加真实信令集成段（-p 1 串行）
cd back && go test -tags nosqlite ./...   # ⚠️ 分层脚本不等于这个全集，两者都跑
cd front && npm test                      # 前端 88
cd packages/peerdrive-client && npm test  # 消费端 60（零依赖）
cd packages/peerdrive-media && npm test   # media 21
./scripts/netdisk-local-demo.sh           # 网盘端到端（改网盘链路必跑）
```

失败日志在 `/tmp/layer-test-<层>.log`；脚本内置 go 代理 env（AGENTS.md 约定）。

## 归档

旧栈时代测试文档（2026-04~05，libp2p / e2e-all.sh / reg-server 等）已移
[archive/](archive/)——仅历史参考，不再维护，以 README.md 为准。
