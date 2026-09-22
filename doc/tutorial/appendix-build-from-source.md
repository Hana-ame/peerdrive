# 附录 A：从源码编译（可跳过）

> 第一章是「下载 release 直接用」，**一到用不了 release 的场景才需要看这里**：
> 改了代码、要跑还没发版的提交、要跑测试与端到端、或要自己出包。
> 只是想跑起来的话，回到[第一章](01-run-and-connect.md)就行 —— 本附录整篇都可以跳过。

---

## A.0 什么时候需要编译

| 你想做的事 | 需不需要编 | 用什么 |
|---|---|---|
| 跑一个节点 / 起信令 | ❌ | 第一章下载的 release 二进制 |
| 用面板连节点 | ❌ | 在线面板，或第一章的链接 |
| 改 Go 代码并验证 | ✅ | §A.2 / §A.3 |
| 跑最新未发版的 `refactor` | ✅ | 本章 |
| 跑端到端验收（市场/加入/拉取/门禁） | ✅ | §A.4 的一键脚本 |
| 自己出一份发布包 | ✅ | §A.8（打 `v*` tag 让 CI 编） |

---

## A.1 前置

- **Go ≥ 1.26**（`back/go.mod` 写的是 `go 1.26.2`，`pathutil` 用到 `os.OpenRoot`，需要 1.24+）
- **Node ≥ 20**（面板产物 / 管理台 / 消费端单测）
- 代理：国内拉 Go 模块建议 `GOPROXY=https://goproxy.cn,direct`

---

## A.2 编译节点

```bash
cd back
go build -tags nosqlite -o /tmp/pd/bin/peerdrive-server ./cmd/server
```

> **`-tags nosqlite` 是硬约束，不是可选项。**
> 仓库为了「发布时 `CGO_ENABLED=0` 也能跑」同时挂了两个 SQLite 驱动
> （`mattn/go-sqlite3` 走 cgo、`modernc.org/sqlite` 纯 Go），按 cgo 是否可用二选一；
> 不加这个 tag 两个驱动会打架。驱动名分别是 `sqlite3` / `sqlite`，`sql.Open` 不能写死。

交叉编译（给别的机器编）：

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags nosqlite -o peerdrive-server.exe ./cmd/server
```

编完的二进制怎么用，和第一章 §1.3 完全一样（配置全靠环境变量）。

---

## A.3 编译自托管信令

它是**独立 go 模块**（`back/signalserver`，module `github.com/Hana-ame/go-peersignal`），
所以不能在上一步的 `./...` 里顺带编出来：

```bash
cd back/signalserver
go build -o /tmp/pd/bin/peersignal ./cmd/peersignal
# 或者不落盘直接跑（第一章 §1.2 的等价写法）
go run ./cmd/peersignal -addr :9100 -key peerjs
```

另外两个独立模块同理，改到它们时别只跑主模块的测试：
`back/peerjs`（go-peerjs）、`back/p2p_bt`（go-peerdrive-bt）。

---

## A.4 一键端到端验收脚本

```bash
./scripts/netdisk-local-demo.sh          # 起信令 :9100 + node-a :3001 + node-b :3002，跑 6 项断言
./scripts/netdisk-local-demo.sh --stop   # 停掉它起的所有进程
```

它会自己编译，然后跑完：节点市场发现 → 加入 → 共享清单 → 跨节点拉取 → sha256 校验 → PSK 门禁。
**服务会保留下来供手测**，跑完还能接着用面板点。
CI 的 `.github/workflows/e2e.yml` 跑的是同一套，它能过 = 环境没问题。

> ⚠️ 它假设自己是 9100/3001/3002 的唯一主人。上次的进程还在跑时，新起的 server 会因
> 端口占用直接退出，而后面的 curl 全打到**旧进程**上：节点看得到、join 也成功，
> 但清单是旧的、落盘路径也是旧的 —— 表现为"任务 done 但文件没落盘"。
> 重跑前先 `--stop`。

---

## A.5 构建公共面板

面板是**单文件静态页**：源码内联进 `dist/panel.html`，`file://` 双击可开，也能托管到任意静态空间。

```bash
cd packages/peerdrive-client
npm run build:panel     # 改了 src/ 或 panel/ 必须重跑
npm run check:panel     # CI 用它拦「源码改了产物没重建」
```

顺序上有个坑：**改完必须构建**，否则线上（GitHub Pages）跑的是旧产物。
`pages.yml` 在部署后还会回头验一次线上站点，并且是**比对本次产物的 sha256** 才开验
—— 只探测"页面能打开"会一直验到上一版。

---

## A.6 管理台（front）

```bash
cd front
npm ci
npm run dev        # 开发服务器 http://localhost:5173
npm run build      # 产物
```

它调的是节点的 HTTP / WS 接口，**需要后端在跑**。

---

## A.7 跑测试

改代码后至少跑这些（完整清单与选型见 `doc/testing/README.md`）：

```bash
cd back && go build -tags nosqlite ./... && go test -tags nosqlite ./... -count=1        # 550
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1        # 21（必须 -p 1）
cd back/peerjs && go test ./... -count=1 -race                                           # 23
cd back/signalserver && go test ./... -count=1                                           # 23
cd back/p2p_bt && go test ./... -count=1                                                 # 7
cd front && npm test                                                                     # 88
cd packages/peerdrive-client && npm test                                                 # 98
bash scripts/test-layers.sh                                                              # 或按分层逐层跑
```

---

## A.8 自己出一份发布包

发布是 **打 `v*` tag 触发**的，不需要本地编 5 个平台：

```bash
git tag -a v0.1.1 -m "说明"
git push origin v0.1.1
```

`.github/workflows/release.yml` 会：

1. **gate**：后端 vet+测试+构建 → 集成测试 → 信令模块 → peerjs 模块 → 消费端包 + 面板产物一致性。
   **红了就不出包** —— 门禁长在 release 自己身上，因为 workflow 之间没有 `needs`，
   靠 `ci.yml` 并行跑拦不住它。
2. **build × 5 平台**：`CGO_ENABLED=0`，产物按 `peerdrive-<goos>-<goarch>[.exe]` 命名
   （不重命名的话 5 个包都叫 `peerdrive-server`，上传时按 basename 会互相覆盖）。
3. **build-signalserver × 5 平台**：同理产出 `peersignal-<goos>-<goarch>[.exe]`。
4. **release**：把 10 个文件挂到 GitHub Release 上。

想只验证门禁会不会过、不想真发版：Actions 页面手动跑 `Release`，`dry_run` 保持默认 `true`
（`release` 那一步会被跳过）。

> 改了 `release.yml` 里的资产命名逻辑时，别忘了 `build` 和 `release` 两处是配套的：
> 一边改名、另一边的 `files:` 就得跟着改。
