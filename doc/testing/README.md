# 测试文档（2026-08-18 重写）

> 入口 → [分层文档 doc/layers/README.md](../layers/README.md) ·
> 一键分层测试 `bash scripts/test-layers.sh [--integration]`
> 旧时代文档（libp2p 栈、e2e-all.sh 等）已移 [archive/](archive/)，仅历史参考。

---

## 一句话

项目按 **AOP 切面（L1-L8）** 组织代码与文档，测试也按层独立跑——每层有自己的
测试命令与测试集，依赖方向单向（下层不依赖上层），任一层可单独验证。

```bash
bash scripts/test-layers.sh           # L1-L8 逐层跑，汇总结果
bash scripts/test-layers.sh --integration   # 追加真实信令集成段
```

失败日志在每个 `/tmp/layer-test-<层>.log`；脚本内置 go 代理 env（AGENTS.md 约定）。

## 分层测试矩阵（2026-08-18 实测全绿）

| 层 | 切面 | 测试命令（脚本内） | 测试数 | 关键测试文件 |
|----|------|-------------------|--------|--------------|
| L1 | 信令/传输原语 | `cd back/peerjs && go test ./... -count=1 -race` | 21 | flowcontrol_test.go、peer_test.go |
| L2 | 帧协议 | `go test -tags nosqlite ./internal/transport/ -count=1 -skip "^TestAdmin"` | 32 | conn/stream/forward/file_index/peerjs_service_test.go |
| L3 | 管理面 | `go test -tags nosqlite ./internal/transport/ -count=1 -run "^TestAdmin"` | 9 | admin_test.go（含上传写失败/中止清理回归） |
| L4 | 业务核心 | `go test -tags nosqlite ./internal/controller/... ./internal/service/... ./internal/source/... ./internal/downloader/...` | 94 | controller/*_test.go、service/*_test.go |
| L5 | 数据 | `go test -tags nosqlite ./internal/repository/...` | 11 | file_repo/collection_repo_test.go |
| L6 | 发现 | `go test -tags nosqlite ./internal/signalserver/...` | 6 | signalserver_test.go |
| L7 | 外部能力 | `cd back/p2p_bt && go test ./... -count=1` | 7 | bt_test.go（独立 go.mod） |
| L8 | 前端 | `cd front && npm test` | 32（4 文件） | tests/ws.test.js 等（vitest） |
| INT | 真实信令集成 | `go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1` | — | integration/ 7 文件（**必须 -p 1 串行**，公共信令互扰） |

**必加 `-tags nosqlite`**：双 SQLite 驱动 CGO 冲突（AGENTS.md 硬性约束）。

## 独立包 peerdrive-media（packages/peerdrive-media/）

浏览器经 PeerJS 信令 + WebRTC DataChannel 从 Node 端加载 URL 资源。
改包代码后的完整验证链（AGENTS.md 要求，同步独立 repo 镜像 + tag v0.1.0）：

```bash
cd packages/peerdrive-media
npm run build        # dist 入库（IIFE/ESM/CJS 三构建）
npm test             # node --test：17 项（e2e 协议 7 + core 队列 4 + 其余）
# 浏览器 E2E（本机 Firefox，skill runner）：先起三服务再跑
node ~/.claude/skills/playwright-test/scripts/test-runner.mjs test/e2e-browser.mjs
```

- e2e.test.mjs：协议 E2E 7 项（分块/流式背压/MIME/404/白名单/连接串行复用）
- e2e-browser.mjs：浏览器 E2E 10 断言（mount/load/视频/白名单/dispose 重建）
- core.test.mjs：mock peerjs 的队列/中止边界 4 项（确定性，不用真实信令）
- 坑与浏览器 E2E 五连见 `doc/REFACTOR.md` §3.11

## 线上验证（可选）

自托管信令连真实节点（无代理直连 cloudcone）：

```bash
cd back && PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v
```

## 前端专项

- vitest 单测：`cd front && npm test`（32 项：ws 协议/admin/upload 回退、路由表、工具函数）
- 浏览器 E2E 另见独立包章节（公共信令 + 本机 Firefox，`scripts/static-serve.mjs` 起静态服务）

## 更新约定

- 改代码后行为变化 → 同步分层文档（doc/layers/，硬性要求）与该层测试
- 测试函数必须标注「发现背景」（AGENTS.md 硬性要求）
- 新增测试集时同步更新本矩阵与该层文档的测试小节