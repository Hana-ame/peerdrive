# Peerdrive
[English](README.md) | [简体中文](README.zh-CN.md)

[![Peerdrive CI](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml/badge.svg)](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml)

Peerdrive 是一个多协议文件合集管理器，支持 SHA256 内容寻址存储、URL 引用、P2P 传输与 BitTorrent 下载。通过 **Collection + Provider** 的统一抽象，将本地文件、HTTP 资源与 PeerJS/WebRTC 互联融为一体。

---

## 核心概念

```
Collection = name + entries[]
Entry      = path + providers[]
Provider   = { type: "sha256" | "url", value: hash | url }
```

剥离独立的「文件」概念 —— 一切皆合集。一个文件 = 单 entry 合集 + sha256 provider。

### 混合互联与多源检索

Peerdrive 不再依赖传统的单网络 DHT，而是通过 **PeerJS 信令 + WebRTC DataChannel** 以及 HTTP/MQTT 存在发现（presence discovery）实现节点互联。多协议内容检索可在本地存储、已连接的 P2P 对端、公共 IPFS HTTP 网关、可选的 BitTorrent DHT（`go-peerdrive-bt`）以及上游 HTTP 镜像之间进行路由。

### 关键技术栈

| 分层 | 技术 |
|----|------|
| HTTP | Gin |
| P2P | PeerJS 信令 + WebRTC DataChannel（`back/peerjs/` go-peerjs；发现：MQTT / 自托管 HTTP） |
| BT | `github.com/Hana-ame/go-peerdrive-bt`（back/p2p_bt，独立库，通过 `PEERDRIVE_BT_ENABLE` 可选启用） |
| 管理面 | 本地 WS admin verb（前端全部走 `front/src/ws.js`） |
| 消费端 | `packages/peerdrive-client`（零依赖纯浏览器消费端，走 `share`/`req` 帧；`packages/peerdrive-media` 是面向 img/video 的 URL 代理） |
| 存储 | SQLite + 内容寻址文件系统（磁盘原始 SHA 文件，带数据库支撑的文件名映射） |
| 前端 | React 19 + Vite 8 + TailwindCSS 3（3 级导航：ALWAYS、OWNER、GUEST） |

---

## 功能现状：现在能做什么

> 以下全部能力已在本分支（`refactor`）跑通：后端单元测试 **622**（16 包）· 集成测试 21 · 前端 101 · 客户端 115 ·
> 网盘端到端脚本 12 项断言 · 面板真实浏览器 9 项断言 · 零配置面板 3 项断言（v0.3.2 新增）· 共享级别端到端 12 项断言 ·
> 管理面冒烟 19 项断言（逐项清单、命令与盲区见 `doc/testing/README.md`）。
>
> **初次运行？请从 `peerdrive demo` 开始** —— 见下方的[快速开始](#快速开始)。无需任何配置。

### 无需运行节点即可使用（公共面板 `dist/panel.html`）

| 能力 | 说明 |
|------|------|
| **完全无需输入** | 自 **v0.3.2** 起，`http://127.0.0.1:<port>/panel` 内嵌于二进制中。面板会自动从服务端反查自身节点 ID 及其信令并自行连接 —— **无需填写节点 ID / 信令 / key**（v0.3.1 及更早版本都需要从日志中手动复制） |
| 连节点 | 填节点 peer id 直连，或「自动搜索」列出信令上在线的节点（请求信令的 REST API，不占 WebRTC 连接） |
| 看对方的共享文件链接 | 连上后自动拉取 `share` 清单：合集 / 文件 / 目录。只显示对方**显式声明**开放的范围 |
| 保存 · 预览 | 「保存」= 流式拉取 + 浏览器下载；<=2MB 可「预览」。任务行带进度与取消 |
| 取回校验 | 任务行「取回校验」按 hash 重新拉取并复算 sha256 —— 入库成功 != 能取回，这才是闭环证据 |
| 本地入库 | 选本地文件分片上传（64KB/片，串行，一条连接一个上传流），节点计算 sha256，存入 CAS 并返回 hash |
| 网络入库 | 给一个 URL 让节点替自己抓取并入库；SSRF 防护只允许公网 http/https，内网/本机地址会被拒绝（**被拒绝是预期行为**） |

### 节点运营者

| 能力 | 说明 |
|------|------|
| 内容寻址存储 | 写入的内容一律按 sha256 落盘（无扩展名的原始 SHA 命名文件，原始名称保存在 SQLite `file_index` / `file_meta`），天然去重 |
| 文件索引 | `file_index` 表持久化 sha256 -> 绝对路径，带 seq 游标做增量同步（`sync` verb） |
| 多协议取内容 | 下载器按 `local -> peer -> ipfsgw -> btdht -> http` 路由（顺序与超时可通过 `PEERDRIVE_DOWNLOAD_ORDER` 配置） |
| 节点市场与加入 | 在信令上发现节点，加入后写入 `joined_nodes.json` 并成为常驻对端 |
| 对外共享范围 | `share` verb；**默认全关** —— 不显式声明就不对外暴露任何清单。范围可在**运行时**修改：按目录、按合集、或按 hash 勾选单个文件（`GET/PUT /peerjs/share`、`POST /peerjs/share/files`），持久化至 `storageDir/share_scope.json`，无需重启；环境变量仅为首次启动的初始值。`share_only` 策略同时允许 `share` 清单发现与 `req` 数据流 |
| 共享级别与保护 | 每条共享声明包含一个级别：`public` 列出且可下载 / `unlisted` 不列出但凭 hash 可下载 / `private` 仅限自己与好友（`ShareScope.Friends`）。此外，合集支持三级访问策略（`public`、`protected`、`private`）与提取口令：受保护的合集在通过口令解锁前会隐藏文件项 |
| 隔离收件箱沙箱 | 远程对端上传的文件会被隔离至 `storage/inbox/`（`is_inbox = true`）等待主机运营者审核（`GET /files/inbox`、`POST /files/inbox/approve`、`DELETE /files/inbox/:hash`），防止不可信数据直接污染存储 |
| 出站 QoS 防护 | 出站流并发限制器（`PEERDRIVE_MAX_CONCURRENT_STREAMS`，默认 8）与令牌桶上传限速器（`PEERDRIVE_MAX_UPLOAD_SPEED`） |
| 动态能力协商 | 广场元数据宣告活跃能力（`auth: open|psk`、`caps`、`shares_count`），通过 WebRTC DataChannel 动态协商（`cap/cap-ack/cap-err`） |
| 准入控制 | `PEERDRIVE_PSK`：一旦设置，对端必须在连接上出示同一把密钥，否则所有请求返回 `PSK_REQUIRED` |
| 管理面 | 本地 WS `admin` verb，内部复用 gin 的全部 HTTP controller；WebRTC 侧刻意不实现，以防权限暴露 |
| 端口转发 | `fwd-open/challenge/auth/data/close`，HMAC 质询认证 + 端口白名单（通过 `PEERDRIVE_PORTFWD_ENABLE` 可选启用） |

---

## 各功能是怎么实现的

| 功能 | 代码位置 | 机制 |
|------|----------|------|
| 帧协议 | `back/internal/transport/conn.go` · `dispatchFrame` (L278) | **一份 verb 表同时服务 WS 与 WebRTC**：`req/meta/data/done/err` 拉取文件；`create/upload/list/info/delete/sync` 文件索引；`share` 共享范围；`admin/admin-resp/admin-bin` 管理面；`fwd-*` 端口转发；`cap/cap-ack/cap-err` 动态能力协商 |
| 传输 | `back/peerjs/`（独立库 `github.com/Hana-ame/go-peerjs`） | PeerJS 信令仅转发 SDP/ICE，不碰数据面；数据走 WebRTC DataChannel |
| 发现 | `transport/http_discovery.go` / `mqtt_discovery.go` | 自托管 HTTP 发现优先于 MQTT；另有一个固定的节点级「存在房间」，让零共享内容的节点也能互相发现互联 |
| 内容寻址落盘 | `service/anon_service.go` + `repository/file_index_repo.go` | 磁盘原始 SHA 文件 + SQLite 文件名元数据；`hash[:2]` 用于 CAS 目录分片；写入前校验 hash 长度 |
| 文件索引 | `repository/file_index_repo.go` + `service/file_service.go` | SQLite 持久化 + seq 游标增量；`sync` verb 让对端仅拉取增量 |
| 跨节点保存 | `service/peerpull.go` + `GET/POST /p2p/pull*` | 流式落盘 -> 复算 sha256 -> 登记索引，带进度 / 取消 / 去重跳过 |
| 共享范围 | `service/nodeshare.go` + `share` verb + `GET/PUT /peerjs/share` | 与 `list` **严格区分**：`list` 是本地管理索引全量，仅限可信对端；`share` 是运营者显式声明的对外共享范围。三个来源取并集（目录 / 单个文件 hash / 合集），运行时可修改并持久化；`share_only` 策略同时允许 `share` 和 `req` |
| 隔离收件箱 | `controller/file_inbox.go` + `repository/file_index_repo.go` | 远程上传的文件写入 `storage/inbox/` 且 `is_inbox=1`，等待运营者审核或删除 |
| QoS 防护 | `back/internal/transport/qos.go` | 并发限制保护（`PEERDRIVE_MAX_CONCURRENT_STREAMS`）与令牌桶带宽限速器（`PEERDRIVE_MAX_UPLOAD_SPEED`） |
| 路径安全 | `back/internal/pathutil` | 路径校验的唯一出处（`Within/WithinAny`）；**读边界 != 写边界**；读走 `SafeOpen`（`os.Root`），写走 `SafeWriteFileAny` 等，消除「先检查后按路径打开」的 TOCTOU；硬链接通过**已打开句柄**的 `nlink` 判断 |
| 准入（PSK） | `transport/gate.go` | 连接建立后本端第一帧发送 `psk-auth`；仅门禁「对端要求我干活」的 verb，**绝不门禁响应帧**；`local` 会话豁免 |
| 消费端 SDK | `packages/peerdrive-client/src/` | **传输无关**：仅要求传入 `{on, send, open, close}`，本包不 import peerjs；自带**增量** sha256（WebCrypto 的 `digest()` 是一次性的，与流式拉取冲突） |
| 公共面板 | 同包 `panel/` -> 构建产物 `dist/panel.html` | 单文件、源码内联、可通过 `file://` 打开、可托管到任意静态空间；修改 `src/` 或 `panel/` 后**必须**执行 `npm run build:panel`（CI 的 `check:panel` 会检测漂移） |
| 节点管理台 | `front/src/features/` | 运营者视角，支持 3 级解耦导航（`ALWAYS_NAV`、`OWNER_NAV`、`GUEST_NAV`），调用节点 HTTP API，**需要后端处于运行状态** —— 与公共面板是两回事 |
| 线上托管 | `https://hana-ame.github.io/peerdrive/` | push 后由 `pages.yml` 自动部署，并且**部署后回探线上版本**（比对本次构建指纹，防止验证到上一版本） |

---

## 网盘链路（2026-09，`doc/NETDISK.md`）

把「互联 + 文件」串成一条用户能看懂的链路，对应 ROADMAP 开发顺序的阶段 5/6：

```
我的节点 ──加入──▶ 节点市场 ──直连──▶ 对方共享的"文件链接" ──选中保存──▶ 我的网盘
                                                        （传输任务：进度 / 取消）
```

| 环节 | 实现 |
|---|---|
| 节点市场与加入 | `service.NodeDirectory` + `GET /peerjs/nodes*`；已加入清单持久化（`joined_nodes.json`）并成为常驻对端 |
| 共享范围 | `share` 帧 + `PEERDRIVE_SHARE_ENABLE/COLLECTIONS/DIRS/FRIENDS`（**默认全关**，不显式声明就不对外暴露任何清单）；这些仅为**初始值**，运行时可通过 `/peerjs/share` 修改，持久化至 `share_scope.json`。每条声明另有 `public/unlisted/private` 三档级别。合集支持提取口令与 `public`/`protected`/`private` 访问策略 |
| 跨节点保存 | `service.PeerPuller` + `GET/POST /p2p/pull*`：流式拉取 -> sha256 校验 -> 落盘 -> 登记文件索引，带进度/取消/去重跳过 |
| 网盘界面 | **公共面板** `packages/peerdrive-client/dist/panel.html`（单文件静态页，PeerJS 直连节点，无需服务器）+ 节点管理台 `front/src/features/`，带 3 级解耦导航 |
| 无节点消费端 | `packages/peerdrive-client`：面板与 SDK 均源自它，无需本地后端。面板拥有**固定 ID**（`pd-panel-*`，存入 localStorage）—— `private` 的好友名单认此 ID，随机 ID 等于名单白填；清单每行可生成**分享链接**（`?node=&hash=&auto=1`），是 `unlisted` 的落地动作 |
| 谁能连我的节点 | **PSK 门禁** `PEERDRIVE_PSK`（可选）：一旦设置，对端必须在连接上出示同一把密钥，否则所有请求返回 `PSK_REQUIRED`（`doc/NETDISK.md` §9） |

进度、模块拆分、**计划与实际偏差**以及验证结果均在 `doc/NETDISK.md`；开发顺序见 `doc/ROADMAP.md`。

---

## 快速开始

> **零配置中文指南（初次接触推荐阅读）**：[`doc/guide/single-binary-guide.md`](doc/guide/single-binary-guide.md)
> 手把手教程（下载 -> 启动节点 -> 用面板连接 -> 连不上怎么排查）：
> [`doc/tutorial/01-run-and-connect.md`](doc/tutorial/01-run-and-connect.md)
> 无需自行部署信令：默认连接公共信令 `peersignal.moonchan.xyz`（wss）。
> 修改代码 / 运行最新未发版提交（可跳过）：[附录 A · 从源码编译](doc/tutorial/appendix-build-from-source.md)
> 完整教程索引（共享级别 · 选择共享内容 · 跨节点保存）：[`doc/tutorial/README.md`](doc/tutorial/README.md)

```bash
# ── 自 v0.3.0 起合并为单二进制：server + signaling + registration 均为其子命令 ──
#   5 个平台产物，无需额外下载 peersignal-*（子命令说明详见 doc/guide/single-binary-guide.md）
gh release download --repo Hana-ame/peerdrive --pattern 'peerdrive-linux-amd64'
chmod +x peerdrive-linux-amd64

# ── 第 1 步（零配置，建议先跑这个）：自托管 demo + 双节点，验证整条链路 ──
./peerdrive-linux-amd64 demo
#   自动选择端口，自动生成测试数据，逐步输出结果。
#   无需 Go / 代码仓 / Node / 环境变量。支持完全离线运行（自托管信令）。

# ── 第 2 步：启动你自己的节点 ──
./peerdrive-linux-amd64 serve
#   启动后直接输出「下一步」指引，包含可点击的面板 URL：
#     http://127.0.0.1:3000/panel
#   在浏览器中打开即可 —— 面板会自动识别节点 ID 与信令配置。

# 从源码运行（修改代码时）
cd back && go build -tags nosqlite -o peerdrive ./cmd/peerdrive/ && ./peerdrive

# 网盘界面 = 公共面板
#   自 v0.3.2 起已内嵌到二进制中，可通过上方的 /panel 直接访问 —— 也是最高优先级的打开方式
#   独立版本（用于在没有本地节点的情况下连接他人节点）：
#     在线版：https://hana-ame.github.io/peerdrive/   （push 后自动部署）
#     本地版：npm run build:panel 生成 dist/panel.html，打开 file:// 即可运行
#   通过参数直连指定节点：
#     panel.html?node=<node peer id>&host=peersignal.moonchan.xyz&port=443&path=/
#                &key=pd-signal-1edf5e05e4a52b7351392574&secure=1&auto=1
#   注意：HTTPS 页面（包括在线版）只能使用 wss 信令，否则浏览器会按混合内容拦截。
#   线上托管自检：node scripts/verify-pages.mjs

# 节点管理台（面向节点运营者：市场 / 我的节点 / 传输任务，需后端运行中）
cd front && npm run dev

# 最小 demo（需要 HTTP 服务器提供包目录服务，仅供参考）
cd packages/peerdrive-client && npm run demo   # http://127.0.0.1:8123/demo/consumer.html

# 一键运行完整网盘链路（与 `peerdrive demo` 流程相同；面向 CI/开发的脚本版）
./scripts/netdisk-local-demo.sh                # --stop 用于停止

# 面板端到端自检（真实浏览器 + 真实点击，需先启动上述环境）
cd packages/peerdrive-client
SIG_HOST=<local IP> SIG_PORT=9100 NODE_ID=node-a node scripts/verify-panel.mjs

# 三档共享级别的端到端验证（固定 ID / unlisted 不在清单但链接可获取 / private 拒绝陌生人放行好友
# / 合集整包链接：清单中可识别 · unlisted 凭 hash 仍可获取 · private 连清单都不暴露）
NODE_PORT=3001 SIG_PORT=9100 NODE_ID=node-a PW_CHANNEL=default node scripts/verify-panel-share.mjs

# 零配置面板自检（v0.3.2：无需任何参数打开 /panel，断言面板能自动发现自身节点）
cd packages/peerdrive-client && PANEL_URL=http://127.0.0.1:3000/panel PW_CHANNEL=chromium \
  node scripts/verify-panel-zero-config.mjs

# 测试
cd back && go test -tags nosqlite ./... -count=1                                     # 622（16 个包）
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1    # 21 通过 / 4 跳过（脱外网，自托管信令；必须 -p 1）
cd back/signalserver && go test ./...            # 23（独立 go.mod；✅ go-build·submodules 已覆盖）
cd back/p2p_bt && go test ./...                  # 7（独立 go.mod；✅ 同上）
cd front && npx vitest run                       # 88
cd packages/peerdrive-client && npm test         # node --test（零依赖）98
bash scripts/test-layers.sh                      # 或按 AOP（L1-L8 + LB）逐层运行
node front/tests/e2e-admin-smoke.mjs             # 7 项管理面断言（需先启动节点；CI 已运行）

# 测试组件完整清单、选型与盲区：doc/testing/README.md
```

## 信令服务器实现方式

> **默认连接项目的公共信令 `peersignal.moonchan.xyz`（wss，key `pd-signal-1edf5e05e4a52b7351392574`）**，
> 节点与面板的默认配置一致，开箱即用，无需自行部署信令。
> 它兼容 PeerJS 协议（`back/signalserver` 是其源码，可自行部署替换）：
> 若需自建，修改 `PEERDRIVE_PEERJS_HOST/PORT/KEY` 与 `PEERDRIVE_DISCOVER_URL`，
> 详见教程附录 A。

```
wintools 或任何 PeerJS 兼容信令（独立部署）
│  /peerjs            <- PeerJS 兼容 WS 信令
│  /discover/announce <- Go/Web 节点启动自报
│  /discover/nodes    <- 节点发现
└───────────────┬────────────────────────────
                │ 仅转发 SDP/ICE，不碰数据面
┌───────────────▼────────────────────────────
peerdrive
│  back/peerjs  (Go PeerJS 客户端 + WebRTC DataChannel)
│  back/internal/transport/peerjs_service.go (文件服务 / 节点互联)
│  front        (浏览器 peerjs 消费端)
```

- Go 节点：使用 `back/peerjs` 连接信令，常驻在线，提供本地文件 `list/read`。
- Web 端：浏览器的 `peerjs` 连接同一信令，按需连接 Go 节点，消费文件。
- 信令实现选型：
  1. 使用线上/本地 wintools Go 信令（当前默认）
  2. 使用 peerdrive 的 `back/signalserver` 内嵌或独立运行
  3. 使用公共 PeerJS 云（`0.peerjs.com`）
  4. 使用 Node.js 或其他兼容 PeerJS 的信令服务

## 环境变量

> 完整配置见 `back/internal/config/config.go`（`PEERDRIVE_*` 前缀，未设置时使用默认值）。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 3000 | HTTP 端口 |
| `PEERDRIVE_STORAGE` | ./storage | 存储目录（内容寻址文件） |
| `PEERDRIVE_STORAGE_ENABLE` | true | 存储启用 |
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
| `PEERDRIVE_DISCOVER_MODE` | `auto` | 发现模式：`auto`（配置了 URL 则走 HTTP，否则走 MQTT），`peerjs`/`off`（关闭发现，仅通过静态 `PEERDRIVE_PEERJS_PEERS` 互联 —— 连官方信令配合 `PEERDRIVE_PEERJS_HOST=0.peerjs.com` 使用），`discover`（强制 HTTP），`mqtt`（强制 MQTT） |
| `PEERDRIVE_DISCOVER_URL` | - | 自托管发现 API（优先于 MQTT） |
| `PEERDRIVE_URL_SOURCE_TEMPLATE` | - | URL 源模板（%s=hash，多源兜底） |
| `PEERDRIVE_DOWNLOAD_DIR` | ./downloads | 下载/登记目录（file_index 根） |
| `PEERDRIVE_MAX_PEERS` | 8 | 互联对端上限 |
| `PEERDRIVE_DOWNLOAD_ORDER` / `PEERDRIVE_DOWNLOAD_TIMEOUT` | local,ipfsgw,btdht,http / 30s | 下载器路由顺序 / 超时（这四个名称的任意排列/子集） |
| `PEERDRIVE_FORWARD_RULES` | - | 端口转发规则（`key:port,...`，chmod 600） |
| `PEERDRIVE_DISCOVER_PRESENCE` | true | 节点级「存在房间」发现（零共享集合的节点也能互联） |
| `PEERDRIVE_SHARE_ENABLE` | **false** | 对外共享总开关。默认关 —— 不显式声明就不对外暴露任何清单 |
| `PEERDRIVE_SHARE_COLLECTIONS` | - | 共享合集：逗号分隔 hash 或 `all`（受限/私有一律跳过） |
| `PEERDRIVE_SHARE_DIRS` | - | 共享目录：逗号分隔。空 = 不共享文件（文件只回 basename，不回绝对路径） |
| `PEERDRIVE_SHARE_FRIENDS` | - | 好友节点 ID：逗号分隔，`private` 级别的内容放行给它们（见 `model.LevelPrivate`） |
| `PEERDRIVE_PSK` | - | 节点访问预共享密钥。空 = 开放（谁连上都服务）；设了 = 对端必须出示同一把密钥才能拉任何内容（`doc/NETDISK.md` §9） |
| `PEERDRIVE_ADMIN_TOKEN` | - | 未配置 `PEERDRIVE_REG_SERVER` 时本地 HTTP 管理面的 Bearer token。空 + 无注册服务器 = 关闭鉴权（仅回环接口）；非空 = 在 authRequired 路由上需要 `Authorization: Bearer <token>` |
| `PEERDRIVE_PEERJS_XOR_ENABLE` / `PEERDRIVE_PEERJS_XOR_KEY` | false / - | 数据面 XOR 混淆（针对 WebRTC 流量的轻量级混淆） |
| `PEERDRIVE_MAX_CONCURRENT_STREAMS` | 8 | QoS 每个节点最大并发传出数据流数 |
| `PEERDRIVE_MAX_UPLOAD_SPEED` | 0 | QoS 上传限速，单位字节/秒（令牌桶限流器；0 = 不限速） |
| `PEERDRIVE_PORTFWD_ENABLE` | false | 端口转发功能可选开关 |
| `PEERDRIVE_BT_ENABLE` | false | BitTorrent 协议能力可选开关 |
| `PEERDRIVE_IPFS_ENABLE` | false | IPFS 协议能力可选开关 |
| `PEERDRIVE_ANON_POLICY` | open | 匿名对端策略（`open`、`share_only`、`deny`） |
| `PEERDRIVE_PEER_BLOCKLIST` | - | 逗号分隔的黑名单节点 ID 列表 |
| `PEERDRIVE_RATE_LIMIT_RPS` | 30 | 每 IP 的 HTTP 限流每秒请求数（0 = 不限流） |
| `PEERDRIVE_CSP` | on | Content-Security-Policy 头（`off` 为禁用） |
| `PEERDRIVE_SWAGGER` | on | `/swagger/index.html` 处的 Swagger API 文档（`off` 为禁用） |
| `PEERDRIVE_AUTO_EXTRACT` | false | 在沙箱目录中自动解压上传的压缩包 |

> `PEERDRIVE_SHARE_*` 这几项只是**首次启动的初始值**：启动后可随时通过 `GET/PUT /peerjs/share`
> 与 `POST /peerjs/share/files` 修改（按目录 / 合集 / 单个文件 hash），持久化至
> `PEERDRIVE_STORAGE/share_scope.json`；之后以该文件为准，修改环境变量不会再覆盖
> 已经做出的选择（详见 `doc/NETDISK.md` §12.6，教程第 3 与第 4 章）。

## 启动后的安全状态（2026-10-04 新增）

> **节点启动后会打印安全状态摘要。** 开箱状态下，若干开关处于 **off**（= 敞开）状态，
> 此前**没有任何提示** —— 必须了解实现细节才能发现它是敞开的。
> 现在启动后可直接在日志中查看：

```
security: 4 open item(s) need operator attention, 1 informational item(s)
security: [open] inbound P2P has no gate (PEERDRIVE_PSK empty) — ...
security: [open] HTTP admin surface has no auth (PEERDRIVE_REG_SERVER empty) — ...
security: [open] node is present in the public roster (PEERDRIVE_DISCOVER_PRESENCE=true) — ...
security: [open] Swagger API docs are public (PEERDRIVE_SWAGGER not off) — ...
security: [info] external sharing not enabled (PEERDRIVE_SHARE_ENABLE not true) — ...
```

全部配置妥当后，将变为一行：

```
security: all gates closed (inbound PSK / admin-surface auth / public roster / Swagger)
```

**各项的含义与关闭方法**

| 项 | 含义 | 如何关闭 |
|---|---|---|
| `PEERDRIVE_PSK` 为空 | 一旦知晓 peer id，任何人都能连接并拉取共享内容 | 设置 `PEERDRIVE_PSK`；或绑定 `PEERDRIVE_HOST=127.0.0.1` |
| `PEERDRIVE_REG_SERVER` 为空 | 列出/删除文件、修改共享范围、查阅 Swagger —— 均无需凭据直接开放 | 设置 `PEERDRIVE_REG_SERVER`；或绑定 `PEERDRIVE_HOST=127.0.0.1` |
| `PEERDRIVE_DISCOVER_PRESENCE=true` | peer id 与共享摘要会宣告到信令服务器，陌生人可发起连接 | 设置 `PEERDRIVE_DISCOVER_PRESENCE=false` |
| `PEERDRIVE_SWAGGER` 未关闭 | `/swagger/index.html` 无需凭据公开发布所有 105+ 个端点的接口与参数结构 | 设置 `PEERDRIVE_SWAGGER=off` |

> **为什么默认不强制要求 `PEERDRIVE_PSK`**：强制要求会导致现有所有部署无法启动
> （`doc/tutorial/01-run-and-connect.md` §1.3 的 env 示例中没有 PSK，且公共面板默认不出示 PSK），
> CI 对 PSK 路径没有覆盖，而且 PSK 本身是**无身份的共享密钥** —— 它只能回答
> 「对端是否知晓这把密钥」，无法回答「对端是谁」（见 `back/internal/transport/gate.go`）。
> 因此：先报告暴露状态，再根据部署场景自行添加门禁。

> **关于 `PEERDRIVE_AUTH_TOKEN`**：已于 2026-10-04 **删除**。它从一开始就不是入站门禁 ——
> 在最初的实现（`539efc5`）中，它是本节点向注册服务器报到时使用的**出站**身份凭据，
> 而删除 libp2p 栈（`a5b090d`）后该字段已无消费者。
> 管理面真正的鉴权开关是 `PEERDRIVE_REG_SERVER`。

## 子系统与独立库

Peerdrive 单体代码仓包含多个独立的、可复用的包与独立库：

| 子系统 | 说明 | 路径 |
|-----------|-------------|------|
| `go-peerjs` | 纯 Go PeerJS 协议客户端与 WebRTC DataChannel 传输（`github.com/Hana-ame/go-peerjs`）。 | `back/peerjs/` |
| `go-peersignal` | 轻量级自托管 Go 信令与发现服务器（`github.com/Hana-ame/go-peersignal`，二进制命令 `cmd/peersignal`）。 | `back/signalserver/` |
| `go-peerdrive-bt` | 主线 BitTorrent DHT 能力（`github.com/Hana-ame/go-peerdrive-bt`），可选桥接层。 | `back/p2p_bt/` |
| `peerdrive-client` | 零依赖纯浏览器消费端 SDK 与单文件 Web 面板（`dist/panel.html`）。 | `packages/peerdrive-client/` |
| `peerdrive-media` | 用于浏览器端直接流式传输音频、视频与图片的 WebRTC DataChannel URL 代理。 | `packages/peerdrive-media/` |
| 活跃核心服务 | 统一多协议检索（`back/internal/provider/`）、内容寻址合集模型（`back/internal/model/anon.go`）与互联传输层（`back/internal/transport/peerjs_service.go`）。 | `back/internal/` |

> 注意：旧有的 libp2p 协议栈已于 2026-08-16 全部删除（历史迁移记录详见 `doc/REFACTOR.md` §8 与 `doc/archive/LEGACY.md`）。
