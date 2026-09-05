# Peerdrive 实际操作记录

> 2026-09-05 | 分支: refactor

---

## 1. CORS 通配符修复

**目的**: 修 playwright 测试 6「新建文件夹」被 CORS 阻断的问题

**真正测试了什么**:
- `IsOriginAllowed` 对 `https://*.pages.dev` 的匹配逻辑
- 修改前：`strings.HasPrefix(o, "*.")` 只认 `*.domain`，`https://*.domain` 永远不匹配
- 修改后：同时支持两种写法，提取 `*.domain` 部分做后缀匹配
- 17 个子用例：空/精确/大小写/两种通配/CF Pages 预览/混合/空格

**结果**: 17/17 通过，curl 验证 5 项 CORS 场景正确

**分析**: CF Pages 预览部署用子域名（`6f670b67.peerdrive.pages.dev`），精确匹配不上。修 `IsOriginAllowed` 同时支持 `*.domain` 和 `https://*.domain` 两种写法，提取通配部分做后缀匹配。

---

## 2. serve-dir 以指定 path serve 本地文件

**目的**: 实现「以指定 path 的方式 serve 本地文件」——浏览器通过 WebRTC 从 Node 端加载本地文件

**真正测试了什么**:
- HTTP 直连 6 项：目录列表、文本、SVG、MP4 流、目录穿越防护、子目录
- WebRTC 浏览器 E2E 18 项：6 种文件类型（txt/svg/jpg/json/md/mp4）通过 DataChannel 传输
- 传输性能：hello.txt 4ms、sample.svg 3ms、landscape.jpg 300KB 5ms、sample.mp4 5.9MB 65-87ms

**结果**: HTTP 6/6 ✅，WebRTC 18/18 ✅

**分析**: 不改 `server.js` 核心（它只接受 fetch URL），新建 `serve-dir.mjs` 独立脚本，通过 PeerJS 信令 + WebRTC DataChannel 把本地文件流回浏览器。关键设计：
- 目录穿越防护：`normalize(join(...))` + `startsWith(resolvedDir)`
- 流式传输：`createReadStream().pipe(res)` 不全部读入内存
- 串行队列：复用 peerdrive-media 核心，一次只传一个文件

---

## 3. peersignal 部署到 cloudcone

**目的**: 把信令服务器部署到 cloudcone，域名 peersignal.moonchan.xyz

**真正测试了什么**:
- Go 信令服务器二进制构建（Linux amd64，`-tags nosqlite`）
- 本地验证：`./peerserver --addr 127.0.0.1:9999 --key test` 可启动
- 部署脚本：scp 上传→systemd 单元→nginx site→Cloudflare DNS 指引

**结果**: 部署成功，peerserver 在 cloudcone 上运行，nginx 反代正常

**分析**: 部署架构：Cloudflare（橙云代理）→ cloudcone nginx :443 → peerserver 127.0.0.1:9000。关键配置：nginx WebSocket upgrade（`proxy_http_version 1.1` + `$connection_upgrade` + 300s 超时）。

---

## 4. /status 端点 404 修复

**目的**: 修 peersignal.moonchan.xyz/status 返回 404 的问题

**真正测试了什么**:
- SSH cloudcone，读 nginx 配置，发现缺 `location /status` 路由
- 加路由后 `/status` 仍 404——peerserver 二进制太旧，没有 `/status` 端点
- 上传新二进制后 `/status` 返回 JSON

**结果**: `/status` ✅ JSON、`/` ✅ HTML dashboard、Go 客户端信令连接成功

**分析**: 两个问题叠加：nginx 未配置路由 + peerserver 旧版无该端点。先修 nginx，再换二进制。

---

## 5. peersignal 全部 10 个端点验证

**目的**: 确认 peersignal 所有端点可用

**真正测试了什么**:
| # | 端点 | 测试内容 |
|---|---|---|
| 1 | `GET /peerjs/id` | 返回随机 ID |
| 2 | `GET /` | HTTP 200 HTML dashboard |
| 3 | `GET /status` | JSON（key/uptime/clients/nodes/links） |
| 4 | `GET /discover/nodes` | 空列表 `{"links":[],"nodes":[]}` |
| 5 | `GET /discover/nodes?coll=test` | 参数过滤 |
| 6 | `POST /discover/announce` | 节点登记 `{"ok":true}` |
| 7 | `GET /discover/nodes`（登记后） | 节点出现在列表中 |
| 8 | `POST /discover/leave` | 节点下线 `{"ok":true}` |
| 9 | `WebSocket /peerjs` | Go 客户端连接成功 |
| 10 | 错误路径 | 404（未知路径）、400（错误/缺失 key） |

**结果**: 10/10 通过

**分析**: 信令 + 发现 + 监控全通。key 校验生效（错误 key → 400），未知路径 → 404。

---

## 6. peer node 启动验证

**目的**: 启动一个 peer node 连接到 peersignal.moonchan.xyz

**真正测试了什么**:
- 第一次：BT DHT 默认启用，启动卡死（goroutine 阻塞）
- 第二次：`PEERDRIVE_BT_DHT_ENABLE=false`，启动成功但 ID-TAKEN（旧进程未干净断开）
- 第三次：随机 ID + `PEERDRIVE_MQTT_COLLECTIONS=<SHA-256 hash>`，完全成功

**验证项**:
- `/ping` → "pong" ✅
- `/files` → 158 个文件 ✅
- 信令 `/status` → `clients: 2, discovered: 1` ✅
- 发现 `/discover/nodes` → 节点出现 ✅
- `/peerjs/node` → `online: true` ✅

**结果**: 全部通过

**分析**: 三个坑：
1. BT DHT 默认启用阻塞启动 → 设 `PEERDRIVE_BT_DHT_ENABLE=false`
2. 旧进程未干净断开导致 ID-TAKEN → 换随机 ID 或等心跳超时
3. collections 必须是 SHA-256 hash 格式 → 用 `echo -n "name" | sha256sum`

---

## 7. 配置参数问答

**目的**: 解释关键配置参数

**问题与回答**:

| 问题 | 回答 |
|---|---|
| PEERDRIVE_PEERJS_KEY 用来干嘛 | 信令服务器的 API Key，客户端连接时必须带上，服务器校验后拒绝未授权连接。相当于"通行证"。 |
| PEERDRIVE_PEERJS_ID 用来干嘛 | 本节点的唯一标识符，其他节点通过这个名字找到你、发起 WebRTC 连接。相当于"门牌号"。 |
| key 是多少现在 | `pd-signal-b9447b406828e500`（查 `/status` 端点确认） |
| key 是固定的吗 | 不固定，配置值。公共云默认 `peerjs`，自托管手动指定。 |
| 哪个模块在用 key | signalserver（校验）、peerjs 库（URL 里发送）、peerjs_service（配置传递）、config（环境变量加载） |
| signal 用来协调什么 | 身份注册（OPEN/ID-TAKEN）+ 能力交换（OFFER→ANSWER）+ 地址发现（ICE candidates）。建链后退出。 |
| peerjs 和 webrtc 区别 | WebRTC=底层传输（SDP/ICE/DataChannel），PeerJS=上层管理（注册/匹配/重连/消息路由）。 |
| peersignal 服务器做了什么 | 当前 1 个客户端、0 消息、6.8MB 内存、几乎空闲（只有 1 个节点，无建链）。 |
| js 和 go 都实现了吗 | 都实现了。Go: signalserver + peerjs 库 + transport 层。JS: peerdrive-media（浏览器+Node）。 |
| 传输的是什么文件 | 任何文件。当前可传: 158 个本地文件 + serve-dir 的 8 个文件。 |
| peernode 在 serve 哪个文件夹 | 不是 serve 文件夹，是内容寻址存储（`./storage/<前2位hash>/<完整hash>`）。 |

---

## 8. GitHub 默认分支修改

**目的**: 把默认分支从 main 改成 refactor

**真正操作**:
- `gh repo edit Hana-ame/peerdrive --default-branch refactor`
- 验证: `git remote show origin` → `HEAD branch: refactor`

**结果**: ✅ 已修改

**分析**: 无需额外操作，GitHub CLI 直接修改仓库设置。

---

## 提交记录

| 提交 | 说明 |
|---|---|
| `ec206c1` | fix(cors): IsOriginAllowed 通配符匹配 https://*.domain |
| `74c2a56` | feat(serve-dir): 以指定 path serve 本地文件（HTTP + WebRTC） |
| `1a22ee5` | feat(deploy): peerserver 部署脚本 |
| `12bc620` | fix(deploy): peersignal.moonchan.xyz 更新（nginx + 二进制） |
| `aaf34a6` | docs: 验证清单 |
| `94f20ee` | docs: 重写验证清单（按实际操作记录） |
