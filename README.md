# Peerdrive
[![Peerdrive CI](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml/badge.svg)](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml)

Peerdrive 是一个多协议文件集合管理器，支持 SHA256 内容寻址存储、URL 引用、P2P 传输和 BitTorrent 下载。通过 **Collection + Provider** 的统一抽象，将本地文件、HTTP 资源、PeerJS/WebRTC 互联整合到一个系统中。

---

## 核心理念

```
Collection = 名称 + 条目[]
Entry     = 路径 + Provider[]
Provider  = { type: "sha256" | "url", value: hash | url }
```

剥离独立的"文件"概念——一切皆合集。文件 = 单 entry 的 collection + sha256 provider。

### 双 DHT 架构

SHA256 hash 转为 CIDv1 在 IPFS DHT 上 announce，同时作为 infohash 在 BT DHT 上 announce。双栈查询合并两个网络的结果。

### 关键技术栈

| 层 | 技术 |
|----|------|
| HTTP | Gin |
| P2P | PeerJS 信令 + WebRTC DataChannel（`back/peerjs/` go-peerjs；发现：MQTT / 自托管 HTTP） |
| BT | `github.com/Hana-ame/go-peerdrive-bt`（back/p2p_bt，独立库） |
| 管理面 | 本地 WS admin verb（前端全走 `front/src/ws.js`） |
| 消费端 | `packages/peerdrive-client`（零依赖纯浏览器消费端，走 `share`/`req` 帧；`packages/peerdrive-media` 面向 img/video 的 URL 代理） |
| 存储 | SQLite + 内容寻址文件系统 |
| 前端 | React 19 + Vite 8 + TailwindCSS 3 |

---

## 功能介绍：现在能做什么

> 以下能力都在本分支（`refactor`）实跑过：后端单测 550 · 集成 21 · 网盘端到端脚本 8 项断言 ·
> 面板真实浏览器 9 项断言 · 管理面冒烟 7 项断言，CI 覆盖合计 843 个用例
> （逐项清单、命令与盲区见 `doc/testing/README.md`）。

### 不开节点也能用（公共面板 `dist/panel.html`）

| 能力 | 说明 |
|------|------|
| 连节点 | 填节点 peer id 直连，或「自动搜索」列出信令上在线的节点（打信令的 REST 接口，不占 WebRTC 连接） |
| 看对方的共享文件链接 | 连上后自动拉 `share` 清单：合集 / 文件 / 目录。只显示对方**显式声明**开放的范围 |
| 保存 · 预览 | 「保存」= 流式拉取 + 浏览器下载；≤2MB 可「预览」。任务行带进度与取消 |
| 取回校验 | 任务行「取回校验」按 hash 重新拉一遍并复算 sha256 —— 入库成功 ≠ 能取回，这才是闭环证据 |
| 本地入库 | 选本地文件分片上传（64KB/片，串行，一条连接一个上传流），节点算 sha256 存进 CAS 并返回 hash |
| 网络入库 | 给一个 URL 让节点替自己去抓并入库；SSRF 防护只放行公网 http/https，内网/本机地址会被拒（**被拒是预期行为**） |

### 节点运营者

| 能力 | 说明 |
|------|------|
| 内容寻址存储 | 进来的内容一律按 sha256 落盘 `storageDir/<hash[0:2]>/<hash>`，天然去重 |
| 文件索引 | `file_index` 表持久化 sha256 → 绝对路径，带 seq 游标做增量同步（`sync` verb） |
| 多协议取内容 | 下载器按 `local → ipfs → ipfsgw → btdht → http` 路由（顺序与超时可配） |
| 节点市场与加入 | 信令上发现节点，加入后落 `joined_nodes.json` 并成为常驻对端 |
| 对外共享范围 | `share` verb；**默认全关** —— 不显式声明就不对外暴露任何清单。范围可**运行时**改：按目录、按合集、或按 hash 勾单个文件（`GET/PUT /peerjs/share`、`POST /peerjs/share/files`），落盘 `storageDir/share_scope.json`，不用重启；环境变量只是首次启动的初值 |
| 准入控制 | `PEERDRIVE_PSK`：设了之后对端必须在连接上出示同一把密钥，否则所有请求回 `PSK_REQUIRED` |
| 管理面 | 本地 WS 的 `admin` verb，内部复用 gin 的全部 HTTP controller；WebRTC 侧刻意不实现，防权限暴露 |
| 端口转发 | `fwd-open/challenge/auth/data/close`，HMAC 质询认证 + 端口白名单 |

---

## 各功能是怎么实现的

| 功能 | 代码位置 | 机制 |
|------|----------|------|
| 帧协议 | `back/internal/transport/conn.go` · `dispatchFrame`（L278） | **一份 verb 表同时服务 WS 与 WebRTC**：`req/meta/data/done/err` 拉文件；`create/upload/list/info/delete/sync` 文件索引；`share` 共享范围；`admin/admin-resp/admin-bin` 管理面；`fwd-*` 端口转发 |
| 传输 | `back/peerjs/`（独立库 `github.com/Hana-ame/go-peerjs`） | PeerJS 信令只转发 SDP/ICE、不碰数据面；数据走 WebRTC DataChannel |
| 发现 | `transport/http_discovery.go` / `mqtt_discovery.go` | 自托管 HTTP 发现优先于 MQTT；另有一个固定的节点级「存在房间」，让零共享内容的节点也能互联 |
| 内容寻址落盘 | `service/anon_service.go` | `hash[:2]` 分目录；写入前校验 hash 长度（未校验时 `hash[:2]` 会越界 panic，已修） |
| 文件索引 | `repository/file_index_repo.go` + `service/file_service.go` | SQLite 持久化 + seq 游标增量；`sync` verb 让对端只拉增量 |
| 跨节点保存 | `service/peerpull.go` + `GET/POST /p2p/pull*` | 流式落盘 → 复算 sha256 → 登记索引，带进度 / 取消 / 去重跳过 |
| 共享范围 | `service/nodeshare.go` + `share` verb + `GET/PUT /peerjs/share` | 与 `list` **严格区分**：`list` 是本地管理索引全量、只给可信对端；`share` 是运营者显式声明的对外范围。三条来源取并集（目录 / 单个文件 hash / 合集），运行时可改并落盘；卷根目录在入口被拒 |
| 路径安全 | `back/internal/pathutil` | 判定只有这一份（`Within/WithinAny`）；**读边界 ≠ 写边界**；读走 `SafeOpen`（`os.Root`），写走 `SafeWriteFileAny` 等，杜绝「判完再按路径打开」的 TOCTOU；硬链接按**打开着的句柄**判 `nlink` |
| 准入（PSK） | `transport/psk.go` | 连接建立后本端第一帧 `psk-auth`；只拦「对端要我干活」的 verb，**绝不拦应答帧**；`local` 会话豁免 |
| 消费端 SDK | `packages/peerdrive-client/src/` | **传输无关**：只要求传入 `{on, send, open, close}`，本包不 import peerjs；自带**增量** sha256（WebCrypto 的 `digest()` 一次性，与流式拉取冲突） |
| 公共面板 | 同包 `panel/` → 构建产物 `dist/panel.html` | 单文件、源码内联、`file://` 可开、可托管到任意静态空间；改 `src/` 或 `panel/` 后**必须** `npm run build:panel`（CI `check:panel` 拦漂移） |
| 节点管理台 | `front/src/pages/{Drive,Market,Peers,PeerDetail,Transfers}` | 运营者视野，调节点 HTTP API，**需要后端在跑** —— 与公共面板是两回事 |
| 线上托管 | `https://hana-ame.github.io/peerdrive/` | push 后由 `pages.yml` 自动部署，并**部署后回头验线上**（比对本次产物指纹，防止验到上一版） |

---

## 网盘链路（2026-09，`doc/NETDISK.md`）

把「互联 + 文件」串成一条用户能看懂的链路，对应开发顺序里 ROADMAP 的阶段 5/6：

```
我的节点 ──加入──▶ 节点市场 ──直连──▶ 对方共享的"文件链接" ──选中保存──▶ 我的网盘
                                                        （传输任务：进度 / 取消）
```

| 环节 | 实现 |
|---|---|
| 节点市场与加入 | `service.NodeDirectory` + `GET /peerjs/nodes*`；已加入清单持久化（`joined_nodes.json`）并成为常驻对端 |
| 共享范围 | `share` 帧 + `PEERDRIVE_SHARE_ENABLE/COLLECTIONS/DIRS`（**默认全部关闭**，不声明就不对外暴露任何清单）；这三个只是**初值**，运行时经 `/peerjs/share` 改，落盘 `share_scope.json` |
| 跨节点保存 | `service.PeerPuller` + `GET/POST /p2p/pull*`：流式拉取 → sha256 校验 → 落盘 → 登记文件索引，带进度/取消/去重跳过 |
| 网盘界面 | **公共面板** `packages/peerdrive-client/dist/panel.html`（单文件静态页，PeerJS 直连节点，无需服务器）＋ 节点管理台 `front/src/pages/{Drive,Market,Peers,PeerDetail,Transfers}` |
| 无节点消费端 | `packages/peerdrive-client`：面板与 SDK 都源自它，不需要本地后端 |
| 谁能连我的节点 | **PSK 门禁** `PEERDRIVE_PSK`（可选）：设了之后对端必须在连接上出示同一把密钥，否则所有请求回 `PSK_REQUIRED`（`doc/NETDISK.md` §9） |

进度、模块拆分、**计划与实际偏差**、验证结果都在 `doc/NETDISK.md`；开发顺序见 `doc/ROADMAP.md`。

---

## 快速开始

> 手把手教程（下载 → 起节点 → 用面板连上它 → 连不上怎么查）：
> [`doc/tutorial/01-run-and-connect.md`](doc/tutorial/01-run-and-connect.md)
> 信令不用自己部署：默认连公共信令 `peersignal.moonchan.xyz`（wss）。
> 要改代码 / 跑最新未发版的提交（可跳过）：[附录 A · 从源码编译](doc/tutorial/appendix-build-from-source.md)

```bash
# 后端（不用编译：下载发布好的二进制）
#   https://github.com/Hana-ame/peerdrive/releases/latest
#   节点：peerdrive-<linux|darwin|windows>-<amd64|arm64>[.exe]
#   （peersignal-* 是自托管信令，只有想自建时才下，见教程附录 A）
gh release download --repo Hana-ame/peerdrive --pattern 'peerdrive-linux-amd64'

# 想从源码跑（改代码时）
cd back && go run -tags nosqlite ./cmd/server/main.go

# 网盘 UI = 公共面板（单文件，不需要任何服务器）
#   在线版：https://hana-ame.github.io/peerdrive/   （push 后自动部署）
#   本地版：npm run build:panel 生成 dist/panel.html，双击 file:// 就能开
#   带参数直达某个节点：
#   panel.html?node=<节点 peer id>&host=peersignal.moonchan.xyz&port=443&path=/
#              &key=pd-signal-b9447b406828e500&secure=1&auto=1
#   信令 host/key 默认就是这样（面板已预填），一般不用写；
#   注意：HTTPS 页面（含在线版）只能用 wss 信令，否则浏览器按混合内容拦掉。
#   线上托管自检：node scripts/verify-pages.mjs

# 节点管理台（给节点运营者：市场/我的节点/传输任务，需要后端在跑）
cd front && npm run dev

# 最小演示（需要 http 服务提供包目录，仅供参考）
cd packages/peerdrive-client && npm run demo   # http://127.0.0.1:8123/demo/consumer.html

# 一键跑整套网盘链路（自托管信令 + 两个节点，自动验证市场/加入/清单/拉取/校验）
./scripts/netdisk-local-demo.sh                # --stop 停止

# 面板端到端自检（真实浏览器 + 真实点击，需先起上面的环境）
cd packages/peerdrive-client
SIG_HOST=<本机IP> SIG_PORT=9100 NODE_ID=node-a node scripts/verify-panel.mjs

# 测试
cd back && go test -tags nosqlite ./... -count=1                                     # 550（12 包）
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1    # 21 通过 / 4 跳过（脱外网，自托管信令；必须 -p 1）
cd back/signalserver && go test ./...            # 23（独立 go.mod；✅ go-build·submodules 已覆盖）
cd back/p2p_bt && go test ./...                  # 7（独立 go.mod；✅ 同上）
cd front && npx vitest run                       # 88
cd packages/peerdrive-client && npm test         # node --test（零依赖）98
bash scripts/test-layers.sh                      # 或按 AOP 分层（L1-L8 + LB）逐层跑
node front/tests/e2e-admin-smoke.mjs             # 管理面 7 项断言（需先起节点；CI 已跑）

# 全部测试组件的清单、选型与盲区：doc/testing/README.md
```

## 信令服务器实现方式

> **默认连项目公共信令 `peersignal.moonchan.xyz`（wss，key `pd-signal-b9447b406828e500`）**，
> 节点与面板的默认值一致，所以开箱即用、不需要部署任何信令。
> 它兼容 PeerJS 协议（`back/signalserver` 就是它的源码，可自行部署替换）：
> 想自建时改 `PEERDRIVE_PEERJS_HOST/PORT/KEY` 与 `PEERDRIVE_DISCOVER_URL`，
> 见教程附录 A。

```
wintools 或任何 PeerJS 兼容信令（独立部署）
│  /peerjs            ← PeerJS 兼容 WS 信令
│  /discover/announce ← Go/Web 节点上线自报
│  /discover/nodes    ← 节点发现
└───────────────┬────────────────────────────
                │ 仅转发 SDP/ICE，不碰数据面
┌───────────────▼────────────────────────────
peerdrive
│  back/peerjs  (Go PeerJS 客户端 + WebRTC DataChannel)
│  back/internal/transport/peerjs_service.go (文件服务/节点互联)
│  front        (浏览器 peerjs 消费者)
```

- Go 节点：用 `back/peerjs` 连接信令，常驻在线，提供本地文件 `list/read`。
- Web 端：浏览器 `peerjs` 连接同一信令，按需连接 Go 节点，消费文件。
- 信令实现选择：
  1. 使用线上/本地 wintools Go 信令（当前默认）
  2. 使用 peerdrive `back/signalserver` 内嵌或独立运行
  3. 使用公共 PeerJS 云（`0.peerjs.com`）
  4. 使用 Node.js 或其他 PeerJS 兼容信令

## 环境变量

> 完整配置见 `back/internal/config/config.go`（`PEERDRIVE_*` 前缀，未设置用默认值）。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 3000 | HTTP 端口 |
| `PEERDRIVE_STORAGE` | ./storage | 存储目录（内容寻址文件） |
| `PEERDRIVE_STORAGE_ENABLE` | true | 存储启用 |
| `PEERDRIVE_AUTH_TOKEN` | - | 节点认证 token（HTTP 管理面） |
| `PEERDRIVE_MAX_UPLOAD_BYTES` | 100MB | 单文件上传上限 |
| `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` | 10MB | 匿名上传上限 |
| `PEERDRIVE_BT_DHT_ENABLE` / `PEERDRIVE_BT_DHT_LISTEN` | true / :6881 | BT DHT（独立库 go-peerdrive-bt） |
| `PEERDRIVE_IPFS_GATEWAY_ENABLE` / `PEERDRIVE_IPFS_GATEWAYS` | true / 三网关 | IPFS 网关兜底 |
| `PEERDRIVE_WEBRTC_STUN` / `PEERDRIVE_WEBRTC_TURN` | stun.l.google.com / - | ICE 服务器 |
| `PEERDRIVE_PEERJS_ENABLE` | true | PeerJS 信令（互联层） |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY` | 0.peerjs.com/443/peerjs | 信令服务器（可指向自托管 peerserver） |
| `PEERDRIVE_PEERJS_ID` | 随机生成 | 节点 peer id |
| `PEERDRIVE_PEERJS_SECURE` | true | 信令 wss |
| `PEERDRIVE_PEERJS_PEERS` | - | 逗号分隔对端自动互联 |
| `PEERDRIVE_MQTT_ENABLE` / `PEERDRIVE_MQTT_BROKER` | false / tcp://broker.emqx.io:1883 | MQTT 分片房间发现 |
| `PEERDRIVE_MQTT_TOPIC_PREFIX` / `PEERDRIVE_MQTT_COLLECTIONS` | peerdrive/v1 / - | MQTT topic 前缀 / 关注集合 |
| `PEERDRIVE_DISCOVER_URL` | - | 自托管发现 API（优先于 MQTT） |
| `PEERDRIVE_URL_SOURCE_TEMPLATE` | - | URL 源模板（%s=hash，多源兜底） |
| `PEERDRIVE_DOWNLOAD_DIR` | ./downloads | 下载/登记目录（file_index 根） |
| `PEERDRIVE_MAX_PEERS` | 8 | 互联对端上限 |
| `PEERDRIVE_DOWNLOAD_ORDER` / `PEERDRIVE_DOWNLOAD_TIMEOUT` | local,ipfs,ipfsgw,btdht,http / 30s | 下载器路由顺序 / 超时 |
| `PEERDRIVE_FORWARD_RULES` | - | 端口转发规则（`key:port,...`，chmod 600） |
| `PEERDRIVE_DISCOVER_PRESENCE` | true | 节点级「存在房间」发现（零共享 collection 的节点也能互联） |
| `PEERDRIVE_SHARE_ENABLE` | **false** | 对外共享总开关。默认关——不显式开启就不对外暴露任何清单 |
| `PEERDRIVE_SHARE_COLLECTIONS` | - | 共享合集：逗号分隔 hash 或 `all`（受限/私有一律跳过） |
| `PEERDRIVE_SHARE_DIRS` | - | 共享目录：逗号分隔。空 = 不共享文件（文件只回 basename，不回绝对路径） |

> `PEERDRIVE_SHARE_*` 这三项只是**首次启动的初值**：启动后可经 `GET/PUT /peerjs/share`
> 与 `POST /peerjs/share/files` 随时改（按目录 / 合集 / 单个文件 hash），落在
> `PEERDRIVE_STORAGE/share_scope.json`；之后以该文件为准，改环境变量不会再覆盖
> 已经做出的选择（详见 `doc/NETDISK.md` §12、教程第三章）。
| `PEERDRIVE_PSK` | - | 节点访问预共享密钥。空 = 开放（谁连上都服务）；设了 = 对端必须出示同一把密钥才能拉东西（`doc/NETDISK.md` §9） |


## 留下的东西

> 注意：以下列为早期遗留模块。**libp2p 栈已于 2026-08-16
> 全部删除**（`back/internal/service/p2p.go`、`p2p_dual.go` 等已不存在），当前
> 互联层为 PeerJS/WebRTC，见 `doc/REFACTOR.md`。

| 模块 | 价值 |
|------|------|
| `back/p2p_bt/` | BT DHT 能力（独立库 `github.com/Hana-ame/go-peerdrive-bt`；**README 旧断言「可独立使用」是错的**——依赖桥接层，以 REFACTOR.md 第 6 节为准） |
| `back/internal/provider/` | 多协议文件获取抽象（下载管线） |
| `back/internal/model/anon.go` | Content-addressed collection JSON 格式 |
| `back/internal/transport/peerjs_service.go` | 当前互联层：PeerJS 信令 + WebRTC DataChannel |
| `doc/` | 完整的架构决策和测试记录 |
