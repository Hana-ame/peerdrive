# Peerdrive 实际操作记录

> 2026-09-05 | 分支: refactor | 会话实际操作，非文档复制

---

## 1. 修复 CORS 通配符匹配

**问题**: playwright 测试 6「新建文件夹」失败——前端 `peerdrive.pages.dev` 向 `wsl-3000.moonchan.xyz` 发 fetch 被 CORS 阻断。

**根因**: `IsOriginAllowed` 只认 `*.example.com` 开头的通配——`https://*.pages.dev` 以 `https://` 开头，永远进不了通配分支。

**操作**:
- 读 `back/internal/config/config.go`，找到 `IsOriginAllowed` 函数
- 修改通配检测逻辑：同时支持 `*.domain` 和 `https://*.domain` 两种写法
- 添加 17 个子用例到 `back/internal/config/config_test.go`
- 跑 `go test ./internal/config/ -count=1` → 17/17 通过
- 杀旧进程，重启后端，curl 验证 5 项 CORS 场景
- 提交 `ec206c1`，推送 origin/refactor

---

## 2. 实现 serve-dir 以指定 path serve 本地文件

**需求**: "以指定 path 的方式 serve 本地文件，能做了吗"

**操作**:
- 读 `packages/peerdrive-media/src/node/server.js`，理解 fetch URL 架构
- 读 `packages/peerdrive-media/src/core.js`，理解串行队列
- 读 `doc/REFACTOR.md` §3.11/§3.12，理解帧协议和浏览器 E2E 流程
- 创建 `packages/peerdrive-media/demo/serve-dir.mjs`（133 行）
  - 参数: `--dir/-d`、`--http-port/-p`、`--signal-port/-s`、`--peer-id/-id`、`--host`
  - 特性: 目录列表、MIME 自动映射、目录穿越防护、流式传输
- 修复 `SyntaxError: Unexpected reserved word`——把 `await import('node:fs')` 移到顶部
- 创建 `packages/peerdrive-media/demo/test-serve-dir.html`（140 行，6 张卡片）
- 启动信令 + serve-dir，跑 HTTP 直连 6 项验证
- 写 `packages/peerdrive-media/test-serve-dir.mjs`（18 项断言）
- 跑 test-runner → 18/18 通过
- 删测试文件（临时用），提交 `74c2a56`，推送

---

## 3. 部署 peersignal 信令服务器到 cloudcone

**需求**: "信令服务器叫什么，部署到 cloudcone 127.26.9.5:8080 然后部署到合适的名字"

**操作**:
- 读 `back/signalserver/cmd/peerserver/main.go`，确认参数
- 读 `back/signalserver/signalserver.go`，确认路由
- 构建 Linux amd64 二进制: `GOOS=linux GOARCH=amd64 go build -tags nosqlite`
- 本地验证: `./peerserver --addr 127.0.0.1:9999 --key test` → 可跑
- 创建 `scripts/deploy-peerserver.sh`（180 行）
  - 上传二进制 → 写 systemd 单元 → 写 nginx site → Cloudflare DNS 指引
- 提交 `1a22ee5`，推送

---

## 4. 修复 peersignal /status 端点 404

**问题**: "peersignal.moonchan.xyz 能用吗" → 测试发现信令+发现正常，但 `/status` nginx 404。

**操作**:
- SSH cloudcone，读 `/etc/nginx/sites-enabled/peersignal.conf`
- 发现缺 `location /status` 和 `location /` 路由
- 重写 nginx 配置，添加:
  ```nginx
  location /status { proxy_pass http://127.0.0.1:9000; }
  location / { proxy_pass http://127.0.0.1:9000; }
  ```
- `nginx -t && systemctl reload nginx` → 配置正确
- 发现 `/status` 仍返回 404——peerserver 二进制太旧，没有 `/status` 端点
- `scp` 上传新二进制，`systemctl restart peerserver`
- curl 验证: `/status` 返回 JSON、`/` 返回 HTML dashboard、Go 客户端信令连接成功
- 改 `scripts/deploy-peerserver.sh` 的 DOMAIN 回 `peersignal.moonchan.xyz`，删 peersignal2 引用
- 提交 `12bc620`，推送

---

## 5. 验证 peersignal 全部 10 个端点

**需求**: "peersignal 各个端点都验证了吗"

**操作**:
- 写 Go 测试脚本，逐个验证:
  1. `GET /peerjs/id` → 返回随机 ID ✅
  2. `GET /` → HTTP 200 HTML ✅
  3. `GET /status` → JSON 状态 ✅
  4. `GET /discover/nodes` → 空列表 ✅
  5. `GET /discover/nodes?coll=test` → 参数过滤 ✅
  6. `POST /discover/announce` → `{"ok":true}` ✅
  7. `GET /discover/nodes`（登记后）→ 节点出现 ✅
  8. `POST /discover/leave` → `{"ok":true}` ✅
  9. `WebSocket /peerjs` → Go 客户端连接成功 ✅
  10. 错误路径: 404（未知）、400（错误/缺失 key）✅

---

## 6. 启动 peer node 并验证

**需求**: "是否可以启动 peer node 了"

**操作**:
- 杀旧进程，释放端口 3000
- 构建: `go build -tags nosqlite -o /tmp/peerdrive-server ./cmd/server/`
- 第一次启动卡死——发现 BT DHT 默认启用，阻塞 goroutine
- 查 `config.go:113`: `BTDHTEnabled: getEnvBool("PEERDRIVE_BT_DHT_ENABLE", true)`
- 设置 `PEERDRIVE_BT_DHT_ENABLE=false`，重启
- 第二次启动成功，但 ID-TAKEN——旧进程未干净断开
- 用随机 ID (`local-peer-$(date +%s)`)，重启
- 发现 `collections=0`——未配 `PEERDRIVE_MQTT_COLLECTIONS`
- 生成 SHA-256 hash，设置 `PEERDRIVE_MQTT_COLLECTIONS=<hash>`
- 第三次启动成功，验证:
  - `/ping` → "pong" ✅
  - `/files` → 158 个文件 ✅
  - 信令 `/status` → `clients: 2, discovered: 1` ✅
  - 发现 `/discover/nodes` → 节点出现 ✅
  - `/peerjs/node` → `online: true` ✅

---

## 7. 回答配置问题

| 问题 | 回答 |
|---|---|
| "PEERDRIVE_PEERJS_KEY 和 ID 用来干嘛" | KEY=信令通行证（服务器校验），ID=节点门牌号（其他节点找到你） |
| "key 是多少现在" | `pd-signal-b9447b406828e500`（查 `/status` 端点确认） |
| "key 是固定的吗，哪个模块在用" | 不固定，配置值。signalserver 校验、peerjs 发送、peerjs_service 传递、config 加载 |
| "signal 用来协调什么" | 身份注册 + 能力交换（OFFER/ANSWER）+ 地址发现（ICE），建链后退出 |
| "peerjs 和 webrtc 区别" | WebRTC=底层传输（SDP/ICE/DataChannel），PeerJS=上层管理（注册/匹配/重连） |
| "peersignal 服务器做了什么" | 当前 1 个客户端、0 消息、6.8MB 内存、几乎空闲（只有 1 个节点，无建链） |
| "js 和 go 都实现了吗" | 都实现了。Go: signalserver + peerjs 库 + transport 层。JS: peerdrive-media（浏览器+Node） |
| "传输的是什么文件" | 任何文件。当前可传: 158 个本地文件（文本/证书/二进制）+ serve-dir 的 8 个文件 |
| "peernode 在 serve 哪个文件夹" | 不是 serve 文件夹，是内容寻址存储（`./storage/<前2位hash>/<完整hash>`） |

---

## 8. 修改 GitHub 默认分支

**需求**: "gh 改一下默认 branch 到 refactor"

**操作**:
- `gh repo edit Hana-ame/peerdrive --default-branch refactor`
- 验证: `git remote show origin` → `HEAD branch: refactor`

---

## 提交记录

| 提交 | 说明 |
|---|---|
| `ec206c1` | fix(cors): IsOriginAllowed 通配符匹配 https://*.domain |
| `74c2a56` | feat(serve-dir): 以指定 path serve 本地文件（HTTP + WebRTC） |
| `1a22ee5` | feat(deploy): peerserver 部署脚本 |
| `12bc620` | fix(deploy): peersignal.moonchan.xyz 更新（nginx + 二进制） |
| `aaf34a6` | docs: 验证清单 |
