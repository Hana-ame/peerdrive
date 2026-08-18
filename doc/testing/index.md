# 测试文档入口

> 入口 → [README.md](README.md)（当前总纲）· 更新: 2026-08-18

---

## 新架构

| 文档/脚本 | 内容 |
|-----------|------|
| [**README.md**](README.md) | 测试总纲：分层矩阵、命令、独立包验证链、线上测试 |
| [**scripts/test-layers.sh**](../../scripts/test-layers.sh) | 一键按 AOP 分层（L1-L8 + 可选项）逐层跑测试 |
| [**分层文档 ../layers/README.md**](../layers/README.md) | L1-L8 每层职责、关键机制、测试、文件清单 |

## 快速命令

```bash
bash scripts/test-layers.sh               # L1-L8 逐层（实测 9/9 全绿）
bash scripts/test-layers.sh --integration # 追加真实信令集成段（-p 1 串行）
cd packages/peerdrive-media && npm test   # 独立包 17 项
cd packages/peerdrive-media && npx vitest 2>/dev/null   # （前端 vitest 见 front/）
cd front && npm test                      # 前端 32 项 vitest
```

## 归档

旧栈时代测试文档（2026-04~05，libp2p/e2e-all.sh/reg-server 等）已移
[archive/](archive/)——仅历史参考，不再维护，以 README.md 为准。