# Peerdrive 项目 Agent 说明

> 全局规范见 `~/.config/opencode/AGENTS.md`（账户/代理/编码规范等）。

## 先读这些文档（按顺序）

0. **`doc/ROADMAP.md`** ← **开发顺序（用户定序）**：PeerJS 互联 → 文件 → 组合 → 管理链路 →
   文件范围管理 → 上传下载保存 → **身份管理（最后）**。前 6 阶段不得引入账号依赖（用 peerId）。
0.5 **`doc/NETDISK.md`** ← **网盘目标（用户定形）**：前端是普通网盘界面（自己的节点/别人的节点 →
   市场加入 → 看到"文件链接" → 选中保存）+ 纯 WebRTC 消费端。目标定义 / 模块拆分（M1-M6）/
   验收口径 / **实施状态与计划偏差**都在这里。做前端或跨节点拉取前先看它。
1. **`doc/REFACTOR.md`** ← 重构记录：**所有新做的东西、架构决策、坑、帧协议、E2E 验证方式都在这**。动代码前必读。
2. `doc/archive/LEGACY.md` — 旧代码清单（libp2p/BT/WebDAV/前端死代码，标注可删/待迁移/保留）
3. `README.md` — 项目概览（注意：README 的 p2p_bt 旧断言「可独立使用」已于 2026-08-18 修正——依赖桥接层，以 REFACTOR.md 第 6 节为准）

## 核心事实（30 秒版）

- **信令服务器实现方式（2026-09 决策）**：信令可以用**多种方式实现**，只要兼容
  PeerJS 协议即可——公共 PeerJS 云、wintools 自托管 Go 信令、peerdrive
  `back/signalserver`（`github.com/Hana-ame/go-peersignal`）、Node.js `peerjs-server`
  等。当前线上优先使用 wintools 维护的 Go 自托管信令，peerdrive 通过
  `PEERDRIVE_PEERJS_HOST/PORT/KEY` 和 `PEERDRIVE_DISCOVER_URL` 连接；
  如后续需要在 peerdrive 内嵌信令，`back/signalserver` 是现成的 Go 实现。
- **互联层 = PeerJS 信令 + WebRTC DataChannel**：`back/peerjs/` 是独立模块
  （module 路径 `github.com/Hana-ame/go-peerjs`，主 go.mod `replace` 指向本地
  `./peerjs`；**独立 repo 已创建** `github.com/Hana-ame/go-peerjs`，tag=v0.1.0 同步，
  改动随主 repo 提交后需镜像同步）；`internal/service/peerjs_service.go`
  是业务用法（文件服务 + 节点互联，`internal/transport/peerjs_service.go`）；发现：
  `transport/mqtt_discovery.go`（公共 broker）或
  `transport/http_discovery.go` + `back/signalserver/`（独立模块
  `github.com/Hana-ame/go-peersignal`，tag=v0.1.0 同步，自托管信令 `cmd/peersignal` 独立二进制，
  `PEERDRIVE_DISCOVER_URL` 设置后优先于 MQTT；当前线上/本地优先连 wintools 的信令）。
  **发现两层**：内容分片房间（配置 `MQTT_COLLECTIONS` 声明的 hash，
  只有同房间的节点碰面）+ 节点级存在房间（`transport.PresenceRoom`，固定
  `sha256("peerdrive/presence/v1")`，让零共享 collection 的节点也能互联，见 REFACTOR §3.18）。
  存在房间名**必须保持合法 64hex**：线上信令实现若做 64hex 校验，可读名会让整条 announce 被拒。
- **帧协议 verb**（WS/WebRTC 同一套）：`req/meta/data/done/err`（文件拉取）+ `create/upload/list/info/delete/sync`
  （文件索引：SQLite `file_index` 表持久化 sha256→绝对路径 + seq 游标增量同步）+ **`share`/`share-resp`**
  （节点共享范围查询：网盘链路的"看到对方文件链接"，2026-09-20 新增；与 `list` 严格区分——
  `list` 是本地管理索引全量、只对可信对端，`share` 是运营者**显式声明**的对外共享范围，
  默认关。字段：`{type:"share"}` → `{type:"share-resp",collections,files,dirs,total,reqId}`，详见 REFACTOR §4）
  + `fwd-open/challenge/auth/ok/err/data/close`
  （端口转发 v2，HMAC 质询认证 + 端口白名单，见 REFACTOR.md 第 3.9 节）+ **`admin/admin-resp/admin-bin`**
  （管理面 verb，2026-08-17 起前端全面迁移至此：**仅本地 WS 会话**可用，内部转发 gin engine
  复用全部 HTTP controller；WebRTC 不实现管理 verb 防权限暴露；二进制上传=声明帧+后续二进制帧，
  文件流响应=admin-bin 头+单二进制帧，详见 REFACTOR.md 第 3.10 节与 NODE-API.md §2.4），详见 REFACTOR.md 第 4 节
- **网盘链路（2026-09-20，`doc/NETDISK.md` M1-M5）**：节点市场与加入（`service.NodeDirectory` +
  `GET /peerjs/nodes*`，已加入清单落 `joined_nodes.json` 并成为常驻对端）→ 共享范围（`share` 帧）
  → 跨节点拉取保存（`service.PeerPuller` + `GET/POST /p2p/pull*`，流式落盘 + sha256 校验 +
  `file_index` 登记 + 进度/取消）→ 前端网盘界面（`front/src/pages/{Drive,Market,Peers,PeerDetail,Transfers}`
  + `components/netdisk/*`）。**改动这一条链路前先读 doc/NETDISK.md §6**（含计划偏差与踩坑）。
- 旧的 libp2p/BT DHT 栈已于 2026-08-16 全部删除（REFACTOR §8），**新代码禁止 import**；BT 能力经独立库 `github.com/Hana-ame/go-peerdrive-bt`（back/p2p_bt）
- **消费端包 `peerdrive-client`**（`packages/peerdrive-client/`，**零运行时依赖**）：纯浏览器
  从 peerdrive 节点拉文件（走 `share`/`req` 帧），不需要本地部署节点。**传输无关**设计——
  `PeerDriveClient` 只要求传入 `{on(type,cb),send(data),open,close}`（PeerJS DataConnection
  天然满足），本包不 import peerjs，使用方注入构造函数。包本身仍是 ESM 源码直发（不编译）。
  **网盘 UI 的公共形态就是本包构建出的单文件面板** `dist/panel.html`（file:// 可直接打开、
  可托管到任意静态空间，不需要本地 http 服务）——改 `src/` 或 `panel/` 后必须
  `npm run build:panel` 重新生成产物（CI 的 `check:panel` 会拦漂移）；
  改了面板行为请照 README「公共面板」一节手测（真实浏览器）。
  **改动后必须**：`npm test`（`node --test`，61 个用例；零依赖所以不需要 npm ci）。
  自实现的**增量** SHA-256（`src/sha256.js`）是因为 `crypto.subtle.digest()` 一次性、
  与流式拉取冲突——别"优化"成只用 WebCrypto。协议约束（连接级 expect / 字段名逐字对齐 /
  raw 序列化）见其 README「协议」一节。
- **第三方独立包 `peerdrive-media`**（`packages/peerdrive-media/`，无独立 repo）：浏览器经 PeerJS 信令 + WebRTC DataChannel 从 Node 端加载 URL 资源渲染 img/video。三入口：react /
  vanilla（IIFE+CDN）/ node（createPeerMediaServer）。npm 依赖用
  `github:Hana-ame/peerdrive#v0.1.0`（主 repo tag）。（`@v0.1.0` 语法 npm 不认）。
  **改动后必须**：`npm run build`（dist 入库）+ `npm test`（21）+
  浏览器 E2E（`node ~/.claude/skills/playwright-test/scripts/test-runner.mjs
  test/e2e-browser.mjs`，10 项，本机 Firefox）。
  协议：connection 级串行、raw 序列化、64KB 块、背压 4MB。坑与浏览器 E2E
  七连（含串行槽空占三入口）见 REFACTOR.md §3.11；keepalive（断线 5s/15s
  阈值）与排队 abort 立即 settle 见 REFACTOR.md §3.12 第 4/5 项。
- 编码规范：关键/易错/非显然代码旁必须写「为什么这么写」的注释；测试函数必须标注「发现背景」（全局 AGENTS.md 硬性要求）

## 源码控制与 CI（单仓 + 基座 + 模块分支）

> 背景（用户 2026-09 要求）：仓库是**单一仓库**，前后端同仓同 commit；第一个 commit 是
> 单纯「基座」，之后按**模块分支**开发，最后 merge 回主干集成。**每个分支都要有
> 自己的 test 任务，且必须跑通 CI/CD；merge 后的分支同样要再确认测试与 CI 通过。**

- **目录形态（硬约束）**：`/front` + `/back` + `/doc` 平铺在仓库根。新增顶层目录
  前先确认是否该并进这三者之一。
- **分支模型**：
  - 主干：`refactor`（当前开发主干）。
  - `base`：基座分支（骨架 + 可构建的空跑通状态），模块分支都从它派生。
  - 模块分支：`module/<name>`（如 `module/auth`、`module/p2p`、`module/sync`），
    一个模块一条分支，模块自己要能独立构建 + 测试通过。
  - 功能分支：`feat/<name>`、`fix/<name>`。
  - 角色分支：`<role>-agent` / `<role>-frontend`（同一模块的前后端拆分开发）。
  - **checkpoint**：跨模块重构或大型改动落地前，先把当前主干 push 成 checkpoint，
    再开新分支动刀。
- **CI 覆盖（两条 workflow，都别漏）**：push 到上述任一类分支都会触发。
  - `.github/workflows/ci.yml`：job 覆盖 back（vet/test/build）、integration
    （`-p 1` 串行）、peerjs 独立模块、`packages/peerdrive-media`、
    `packages/peerdrive-client`、front（test/build）。
  - `.github/workflows/go-build.yml`：5 个平台交叉构建矩阵
    （linux amd64/arm64、windows amd64、darwin amd64/arm64），
    `go build -tags nosqlite ./cmd/server/` + 非 Windows 跑 `go test -tags nosqlite ./...`。
    **跨平台的调度差异会放大时序敏感的竞态**，只在本机 Linux 跑通不代表这里绿——
    2026-09-20 就是靠它暴露了 `internal/source` 一个 ~1% 的竞速用例偶发失败
    （本机 `-count=400` 也能复现，见 REFACTOR §3.20）。
  **只往主干 merge 前必须本地跑过与 CI 相同的命令**（见「构建与验证」一节），
  否则等于让 CI 替你发现编译错误。
- **merge 后动作**：主干上再跑一遍 `back` 全量测试 + 集成测试 + 前端测试/构建，
  确认没有「各自分支绿、合起来红」的耦合问题。

## 构建与验证

```bash
cd back
go build -tags nosqlite ./...     # 必须 -tags nosqlite（双 SQLite 驱动 CGO 冲突）
go test -tags nosqlite ./...      # 单元/包测试
cd peerjs && go test ./... -count=1 -race      # peerjs 模块（独立 go.mod，改动需同步独立 repo）
cd ../signalserver && go test ./...            # peersignal 模块（独立 go.mod，**无 CI job**，改了 CI 不会红）
cd ../p2p_bt && go test ./...                  # BT 模块（独立 go.mod，**无 CI job**）
# 上面几个可以换成一条（仓库根执行）：bash scripts/test-layers.sh
#   按 AOP 分层（L1-L8 + LB）逐层跑并汇总，失败日志在 /tmp/layer-test-<层>.log
#   ⚠️ 它不等于 `go test -tags nosqlite ./...` 的全集，改后端建议两个都跑
# 集成测试（脱外网：TestMain 起全局自托管信令 + 同机 WebRTC，无需代理）：
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1
#   ⚠️ 必须 -p 1 串行：多组测试共享全局自托管信令，并行会互相干扰
#   外网测试显式门控：PEERDRIVE_MQTT_TEST=1（公共 broker）/ PEERDRIVE_LIVE_TEST=1（线上）
#   无 UDP 沙箱（docker 默认）跳过互联类：PEERDRIVE_SKIP_RTC=1
# go 命令需代理：HTTPS_PROXY=http://172.29.80.1:10809 GOPROXY=https://goproxy.cn,direct
# （cloudcone 443 例外：直连）

cd ../packages/peerdrive-client && npm test   # 消费端包：node --test，60 个用例，零依赖无需 npm ci
cd ../../front && npm test && npm run build   # 前端：vitest 88 + vite build
```

> **全部测试组件（14 个：命令、规模、CI 映射、哪些没被自动化覆盖）见
> [`doc/testing/README.md`](doc/testing/README.md)。**
> 选不出该跑哪个时先看它的 §1「我改了 X，该跑哪些」。
> 网盘链路相关的分功能对照见 [`doc/NETDISK.md` §7.6](doc/NETDISK.md#76-这几个功能各由哪些测试组件兜底)。

> 本机（Windows 侧）bash 工具受限时，改经 WSL 跑：
> `ssh -i ~/.ssh/id_rsa lumin@127.0.0.1 "bash -s" < 脚本`，脚本必须先 `tr -d '\r'`
> （PowerShell 写出的 CRLF 会让 `$'\r': command not found`）。
> WSL 里 node 需注入 nvm 路径：`export PATH="$HOME/.nvm/versions/node/v22.9.0/bin:$PATH"`。
> Go 用 `go build/test -tags nosqlite`；前端 `npx vitest run`（**不要**加 `--reporter=basic`，
> 该 reporter 在此版本不存在）。

**端到端（改网盘链路必做）**：单元/集成绿不代表链路可用——
`scripts/netdisk-local-demo.sh` 起自托管信令 + 两个节点，自动跑完
市场 → 加入 → 清单 → 拉取 → sha256 校验 → **PSK 门禁**（A 带密钥重启 → B 无密钥
被拦 → B 带同一把密钥恢复）。它覆盖的是单测与集成都没覆盖的组合
（登记 → 共享清单 → 跨节点拉取 → 门禁），历史上两个运行时缺陷只有它能发现。

**CI 已覆盖**：`.github/workflows/e2e.yml` 会起同一套环境，先跑链路 8 项断言，
再装 bundled chromium 用真实浏览器点面板跑 9 项断言（连上 → 清单 → 点保存真下载
→ 预览 → sha256 一致）。本地仍可手工跑上面的脚本快速复现。
两条配置陷阱：共享目录必须在 `PEERDRIVE_SHARE_DIRS` 里**声明过**（位置随意，
storage 之外也行；未声明的目录一律拒绝）；同机多节点必须各自 cwd
（`main.go` 硬编码 `InitDB("./peerdrive.db")`，
同库会让拉取被判 `skipped: already local` 而假装成功）。

**路径边界只有一套判定**：`back/internal/pathutil`（`Within` / `WithinAny`）。
「写/登记边界」（storage 根 ∪ SHARE_DIRS ∪ download 根，`FileService.isPathAllowed`）
与「读取边界」（storage 根 ∪ download 根 ∪ 声明过的共享目录，
`FileIndexService.IsPathReadable`）是**两个不同的集合**，不要合并——合并会让对端
能往你对外共享的目录里写文件，或者出现「清单列得出、一拉 `read failed`」。
新增任何路径校验都必须走 `pathutil`，别再手写 `strings.HasPrefix(a+"/")`
（Windows 分隔符是 `\`，且大小写不敏感）。
`scripts/netdisk-sharedir-outside.sh` 专项验证共享目录放在 storage 之外。
详见 `doc/NETDISK.md` §7.4。

**改任何接受路径的入口，都要跑穿透矩阵**（`doc/NETDISK.md` §11）：
`go test -tags nosqlite -run TestTraversal ./internal/...`（四层 162 条）
+ `./scripts/netdisk-traversal-probe.sh`（对着真节点打 payload + 磁盘复核）。
新加 payload 就往对应层的表里加一行，别另起一套判定。

**Windows 语义必须真在 Windows 上跑过**（2026-09-20 起的硬要求，§11.4）。CI 的
Windows 那格不再跳过 `go test`；本机没装 Go 也能验：
`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -tags nosqlite -c -o x.test.exe <pkg>`
把 `.test.exe` 拷到 Windows、cd 到该包目录执行。别再写死的 POSIX payload
（`/etc/passwd` 在 Windows 上不是绝对路径，会被拼进 storage 根内 → 断言假绿），
用 `systemAbsolutePath()` 这类按平台取值的写法。

**打开就一定要关**（Windows 会惩罚）：库文件（新增 `repository.CloseDB()`）、
上传会话（`FileIndexService.Close()`）、管理面上传临时文件
（`adminUploadState.cleanupTemp()`，先 Close 再 Remove）。Windows 上打开着的
文件删不掉，`os.Remove` 静默失败，Linux 上则毫无症状——所以只在 Windows
跑测试才看得见。测试里统一用 `t.Cleanup`。

**写路径和读路径同等对待**（2026-09-20 起的硬要求，§11.5）：任何**产生或改动
文件**的动作（copy / delete / upload 落盘 / 分片上传的临时文件）都走
`pathutil.Safe*`Any 系列，不许再裸写 `os.MkdirAll + os.WriteFile` /
`os.Remove` / `os.Rename`——它们是"先判边界、再按路径落盘"的两步走，中间换一次
软链就写到根外去了。简单记法：**任何 `filepath.Join` 出来的路径都不许直接喂给
`os.WriteFile` / `os.OpenFile` / `os.Remove`**，中间必须过允许根的 `os.Root`。

**Windows 上还有两个实测出来的语义差异**（2026-09-20）：① `filepath.Rel` 自己
折叠大小写；② 8.3 短名（`C:\PROGRA~1`）是同一个目录的**另一个字符串名字**，而
`EvalSymlinks` 只对**已存在**的路径还原它——"长名配的共享目录 + 短名访问"会被
判成越权（文件在里面却读不到）。Windows 侧的归一化因此先过
`pathutil.ExpandShortNames`（`normalize` 与写路径的 `pickRoot` 都已接好）。

## 关键配置（env）

| 变量 | 默认 | 说明 |
|---|---|---|
| `PEERDRIVE_PEERJS_ENABLE/ID/PEERS` | true/-/- | PeerJS 信令；PEERS 逗号分隔对端自动互联 |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY` | 0.peerjs.com/443/peerjs | 可指向自托管 peersignal |
| `PEERDRIVE_DISCOVER_URL` | - | 自托管发现 API（优先于 MQTT） |
| `PEERDRIVE_DISCOVER_PRESENCE` | true | 节点级「存在房间」：让**零共享 collection** 的节点也能互相发现。关掉退回纯内容分片发现 |
| `PEERDRIVE_MAX_PEERS` | 8 | 发现触发的拨号上限（防存在房间退化成 O(n²) 全互联）。静态 `PEERJS_PEERS` 不受限 |
| `PEERDRIVE_MQTT_ENABLE/BROKER/COLLECTIONS` | false/broker.emqx.io/- | MQTT 分片房间发现（公共 broker 不加存在房间） |
| `PEERDRIVE_SHARE_ENABLE` | **false** | 对外共享总开关。**默认关**——不显式开启就不对外暴露任何清单（`share` 帧回空） |
| `PEERDRIVE_SHARE_COLLECTIONS` | - | 共享的合集：逗号分隔 hash，或 `all`（= 所有 public 合集；受限/私有一律跳过） |
| `PEERDRIVE_SHARE_DIRS` | - | 共享的目录：逗号分隔。空 = **不共享文件**（不是"共享全部"）。文件只回 basename，不回绝对路径 |
| `PEERDRIVE_PSK` | - | 节点访问预共享密钥。空 = 开放（谁连上都服务）；设了 = 对端必须在连接上出示同一把密钥，否则回 `PSK_REQUIRED`。**只做准入，不做身份/分级**（详见 `doc/NETDISK.md` §9） |
| `PEERDRIVE_ALLOW_HARDLINKS` | - | `=1` 放行有多个名字的文件（硬链接）。默认拒绝——硬链接没有方向，判不出它在允许根外还有没有别的名字。pnpm `node_modules` / `cp -l` 备份目录需要开 |
| `PEERDRIVE_ALLOW_UNSAFE_ROOT` | - | `=1` 允许把 storage / download / share dir 配成**卷根**（`/`、`C:\`）。默认拒绝启动：那等于把整盘共享出去，几乎都是配错（env 没展开之类） |

## 线上部署（cloudcone 自托管信令）

> 当前线上信令由 wintools 维护，peerdrive 作为客户端连接。
> 信令实现方式不限，部署时也可以选择 peerdrive `back/signalserver`
> 或任何 PeerJS 兼容信令。

- 服务：`peersignal`（systemd）监听 `127.0.0.1:9000`，nginx 反代
- 域名：`wss://peersignal.moonchan.xyz/peerjs`（WS 信令）+ `https://peersignal.moonchan.xyz/discover/*`（发现 API）
- key：`pd-signal-b9447b406828e500`
- **DNS 决策（橙云，已用）**：peersignal.moonchan.xyz → 117.55.237.217 **proxied=true（橙云）**。
  橙云已验证可行：CF 按 A 记录 IP 回源到 cloudcone nginx（自有证书），CF 100s 空闲超时对信令
  无影响（心跳 5s 保活）。踩坑记录：加 A 记录前 peersignal 橙云路径下实测 404（nginx/1.18.0，
  非 cloudcone 的 1.22.1）——当时回源目标不确定，A 记录建立后回源即正确。
  **注意：cloudcone.moonchan.xyz 被 livekit 占用**（livekit.conf → 127.0.0.1:7880），不能复用。
- **代理注意**：cloudcone 443 **不走宿主机代理**（代理连 cloudcone 超时）——curl/测试无代理直连；
  0.peerjs.com 等公共服务则必须走代理。两者按目标域名区分。
- 线上测试：`PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v`（无代理跑）
- 节点配置：`PEERDRIVE_PEERJS_HOST=peersignal.moonchan.xyz PEERDRIVE_PEERJS_KEY=<key> PEERDRIVE_DISCOVER_URL=https://peersignal.moonchan.xyz`
- 部署更新：当前从 wintools 仓库构建并部署 peersignal；若改用 peerdrive/back/signalserver，
  请同步更新本段并确保 `PEERDRIVE_DISCOVER_URL` 指向对应发现 API
- `PEERDRIVE_P2P_ENABLE` 已删除（2026-08-16 批2，libp2p 栈移除）
