# Peerdrive 开发操作手册

> 对应分支：`refactor` · 仓库：`github.com/Hana-ame/peerdrive`
> **本文档面向改代码的人。** 只是想用 peerdrive 的话不需要它，也不需要 Go：
> 下载二进制 → [`doc/guide/single-binary-guide.md`](single-binary-guide.md)（中文，零配置）
> 或 [`doc/tutorial/01-run-and-connect.md`](../tutorial/01-run-and-connect.md)（分步教程）。

> ⚠️ **重写说明（2026-10-06）**：本文档此前描述的是「Go 后端 + React 前端是两个独立仓库、
> 分支分别为 `feat/stage2-e2e-test` / `frontend`」的旧结构，并列了一串
> `back/cmd/server/`、`back/test/e2e-all.sh`、`test/p2p.sh`、
> `PEERDRIVE_P2P_ENABLE` / `PEERDRIVE_P2P_LISTEN` / `PEERDRIVE_MDNS_ENABLE` 等条目。
> **这些路径和变量现已全部不存在**（逐条核实过，不是推测）：
> `internal/serverapp` 已改为 `cmd/peerdrive`，上述 shell 脚本已删除，
> 三个环境变量在 `back/internal/config/` 里**一处引用都没有**。
> 照着旧版操作会直接踩空，故按当前实现重写。

---

## 1. 克隆仓库

**一个仓库、一个分支。** 不需要分别克隆 Go 和 React 两份。

```bash
git clone git@github.com:Hana-ame/peerdrive
cd peerdrive
```

分支一览：

| 分支 / ref | 用途 |
|---|---|
| `refactor` | **当前开发主线**（单二进制 `cmd/peerdrive`，v0.3.2） |
| `feat/stage2-e2e-test` | 旧的多二进制结构，**已冻结**，仅供历史追溯 |
| `frontend` | 旧的前端独立分支，已并入 `refactor` 的 `front/` |

---

## 2. 编译与启动

```bash
cd back
export GOCACHE=/tmp/pdcache        # 可选：绕开默认缓存目录的权限问题

# ⚠️ -tags nosqlite 必须加：三路 SQLite CGO 冲突，不加会在链接阶段失败
go build -tags nosqlite -o peerdrive ./cmd/peerdrive/
```

五个子命令（v0.3.0 起合并进同一个二进制）：

```bash
./peerdrive demo      # 零配置跑通全链路：信令 + 两节点 + 跨节点传文件 + sha256 校验
./peerdrive serve     # 主服务（默认子命令）
./peerdrive signal    # 只起信令 + 节点发现
./peerdrive reg       # 只起注册 / 认证 / 中继登记
./peerdrive all       # 三者同进程、同端口
```

最省事的一条：

```bash
./peerdrive serve
```

启动后会打印「下一步做什么」，其中一个地址**可以直接用浏览器打开**：

```
  下一步：打开面板

    http://127.0.0.1:3000/panel
```

`/panel` 的面板与 peerjs 都已 `go:embed` 进二进制，**打开即用**，
不用手填节点 ID / 信令地址 / 端口 / 密钥。

验证：

```bash
curl --noproxy '*' http://127.0.0.1:3000/ping    # → pong
```

> 若环境里有 Privoxy，所有 curl 都需要 `--noproxy '*'` 或 `-x ""`。

### 数据库位置

`peerdrive.db` 落在**工作目录（cwd）**下。**多节点必须各自使用不同的 cwd** ——
共用一个 cwd 等于多个节点共用一个库，`joined_nodes.json` 与节点目录会互相污染。

```bash
mkdir -p /tmp/pd/n1/run && cd /tmp/pd/n1/run && /path/to/peerdrive serve
```

---

## 3. 跑 Go 测试

```bash
cd back && export GOCACHE=/tmp/pdcache

go test -tags nosqlite ./... -count=1
# 16 个包 ok，622 个用例（2026-10-06 实测）

go vet -tags nosqlite ./...
```

集成测试是独立的 build tag，且**必须串行**（多个用例会抢同一个端口）：

```bash
go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -timeout 10m
# 21 passed / 4 skipped（跳过的是离线场景，需要自托管信令）
```

> `-p 1` 不能省。并行跑会因端口互相抢占而失败，症状与真实缺陷难以区分。

前端与客户端包（**无需 `npm ci`**：没有 lock 文件、零依赖）：

```bash
cd packages/peerdrive-client && npm test          # 115
cd front && npm test                               # 101
```

---

## 4. 端到端验证

### 4.1 全链路（信令 + 两节点 + 跨节点传文件）

两种等价入口，链路完全一致：

```bash
# ① 二进制里的（不需要仓库、Go、Node —— 面向使用者）
./peerdrive demo

# ② 仓库脚本（CI 用这个；改链路时以它为准）
./scripts/netdisk-local-demo.sh          # --stop 停止
```

> 改 `demo.go` 或 `netdisk-local-demo.sh` 任一侧都要同步另一侧，
> 否则两个「演示」会漂移，读者看到的现象不一样。

### 4.2 面板真实浏览器自检

```bash
# 先起好节点（serve），再跑：
cd packages/peerdrive-client

# 常规面板自检
SIG_HOST=<local IP> SIG_PORT=9100 NODE_ID=node-a node scripts/verify-panel.mjs

# 零配置面板自检（v0.3.2）：不带任何参数打开 /panel，
# 断言面板自己发现了节点 —— 不是匹配「在线节点」这类标签文字
PANEL_URL=http://127.0.0.1:3000/panel PW_CHANNEL=chromium \
  node scripts/verify-panel-zero-config.mjs
```

> ⚠️ **跑之前先确认端口是自己起的那个**：
> `ss -ltnp | grep :3000`。曾经有一次旧二进制占着 3000，
> 验证脚本打的是它，结论整个反过来。按 PID 杀：`kill <pid>`。

### 4.3 三种共享级别

```bash
NODE_PORT=3001 SIG_PORT=9100 NODE_ID=node-a PW_CHANNEL=chromium \
  node scripts/verify-panel-share.mjs
```

---

## 5. 前端（`front/`，节点管理员界面）

面向节点运维者：文件列表、集合市场、传输任务、共享范围配置、P2P 诊断。

```bash
cd front
npm install        # 首次
npm run dev        # → http://localhost:5173
```

`front/` 是**节点管理员界面**，与面向公众的网盘面板（`/panel`）是两个东西：
前者要连节点 id、直连节点的 `ws://localhost:3000/ws/peer`（同机管理通道），
不需要公网暴露，也不走信令。

构建：

```bash
npm run build && npm run preview
```

> 前端 API 地址在 `front/src/api.js` 第一行，本地开发改成 `http://localhost:3000`。

---

## 6. 常用环境变量

**注意别照抄名字。** 主服务读的是 `PORT`，**不是** `PEERDRIVE_PORT` ——
写错会被静默忽略、服务照常起在默认 3000，表现为「端口配了没生效 / 撞端口」。
（`reg` 子命令历史上犯过一次同类的错，`08ce575` 修的。）

| 变量 | 默认 | 说明 |
|------|------|------|
| `PORT` | `3000` | HTTP 端口（**没有 `PEERDRIVE_PORT`**） |
| `PEERDRIVE_STORAGE` | `./storage` | 内容寻址存储根目录 |
| `PEERDRIVE_DOWNLOAD_DIR` | `./storage/downloads` | 下载 / 登记目录 |
| `PEERDRIVE_PEERJS_ENABLE` | `true` | 启用 PeerJS / WebRTC |
| `PEERDRIVE_PEERJS_ID` | 随机 `peerdrive-<random>` | 本节点 peer id |
| `PEERDRIVE_PEERJS_HOST` / `_PORT` / `_KEY` / `_SECURE` | 公共信令 | 信令接入点 |
| `PEERDRIVE_DISCOVER_URL` | — | HTTP 节点发现（取代 MQTT） |
| `PEERDRIVE_SHARE_ENABLE` | `false` | **共享总开关，默认关** |
| `PEERDRIVE_SHARE_DIRS` | 空 | 声明共享目录；**未声明的目录一律拒绝** |
| `PEERDRIVE_PSK` | 空 | 预共享密钥；留空 = 开放模式（启动时会打 security 警告） |
| `PEERDRIVE_BT_DHT_ENABLE` | `true` | BT DHT |
| `PEERDRIVE_IPFS_GATEWAY_ENABLE` | `true` | IPFS 网关 |
| `PEERDRIVE_SWAGGER` | `on` | 是否挂 `/swagger` |
| `PEERDRIVE_ECH_PROXY_ENABLE` | `false` | **ech-proxy 可选模块总开关，默认关**：开后 `pbs.twimg.com` 改写为 `twimg-pbs.l.moonchan.xyz:8443` 经本地 ech-proxy 出网，需要同时配 `PEERDRIVE_URL_SOURCE_TEMPLATE` |
| `PEERDRIVE_ECH_PROXY_ADDR` | `127.0.0.1:8443` | 子进程监听地址（端口冲突会启动失败，不静默换端口） |
| `PEERDRIVE_ECH_PROXY_INSTALL_DIR` | 空（=`<PEERDRIVE_STORAGE>/ech-proxy`） | 下载与校验文件存放位置 |
| `PEERDRIVE_ECH_PROXY_VERSION` | `v1.3.0` | GitHub release 版本 |
| `PEERDRIVE_ECH_PROXY_SKIP_TLS` | `true` | 跳过本地自签证书校验 |
| `PEERDRIVE_ECH_PROXY_IP_MODE` | `v4` | 传给 ech-proxy 的 `-ip-mode`（`auto`/`v4`/`v6`） |

> ech-proxy 模块是**下载即运行**：节点自己从 GitHub release 拉 exe + `checksums.txt`，
> 校验不过 / 拉不到 / 端口被占 / 起不来，一律启动报错——**不会**悄悄退回直连 pbs.twimg.com。
> 关掉开关时行为与无此模块的节点完全一致（不下载、不起子进程、不改写 URL）。
>
> **和 iwara 模块的端口冲突**：`PEERDRIVE_IWARA_ENABLE=true` 的 iwara 模块会各起一个
> ech-proxy 实例，绑定 `127.0.0.1:<PEERDRIVE_IWARA_ECH_PROXY_PORT>`（host 写死 127.0.0.1）。
> 两个模块默认都是 8443，同时开启会在 `config.Validate()` 直接报错（提示改哪一个的地址）。
> 想让两个都跑：把其中一个挪到别的端口，例如
> `PEERDRIVE_ECH_PROXY_ADDR=127.0.0.1:8444` 或 `PEERDRIVE_IWARA_ECH_PROXY_PORT=8444`。

全部变量见 [`doc/guide/single-binary-guide.md`](single-binary-guide.md) §5
与 `back/internal/config/config.go`。

---

## 7. FAQ

**Q: `curl` 报 Privoxy 代理错误**
A: 加 `--noproxy '*'` 或 `export no_proxy='*'`。

**Q: `go build ./...` 在链接阶段失败**
A: 加 `-tags nosqlite`。三路 SQLite CGO 互相冲突，这是已知的构建约束，不是代码坏了。

**Q: 起节点报 `bind: address already in use`**
A: 先确认**是不是你以为的那个进程占着**：`ss -ltnp | grep :3000`。
更常见的原因是 `PORT` 写错（比如写成 `PEERDRIVE_PORT`）导致静默用了默认 3000。

**Q: 面板一片空白**
A: 查浏览器控制台。`Content Security Policy` 拦截内联脚本是这个症状的典型成因；
v0.3.2 已对 `/panel` 前缀豁免 CSP，若是更早的构建则需升级。

**Q: 面板转圈但连不上，console 报 `ws://` 混合内容**
A: 先看**页面是 http 还是 https**。浏览器只拦「降级」混合内容
（**https 页面**开 `ws://`），不拦「升级」（http 页面开 `wss://`）。
所以 http 的 `/panel` + 默认 wss 公共信令**开箱即通**，2026-10-06 用真浏览器实测过。
真遇到这条报错，说明你在 **https 页面**（在线面板 / 反代后的节点）上连了**明文 ws 信令**：
把信令换成 `wss://`，或给自托管信令开 TLS（`peerdrive signal -tls-cert/-tls-key`）。

**Q: 多节点跑不起来 / 互相看不见**
A: 确认各自 cwd 不同（`peerdrive.db` 落在 cwd 下），且 `PEERDRIVE_SHARE_DIRS` 已声明。

**Q: 测试挂住不退出**
A: 集成测试漏了 `-p 1`；或用了「删掉校验逻辑」的负向对照——那会走到真实监听、
挂满超时而不是失败。加显式超时守卫，别靠等待。

**Q: 前端页面 API 404**
A: 检查 `front/src/api.js` 第一行的 `API_BASE` 是否指向正确后端。
