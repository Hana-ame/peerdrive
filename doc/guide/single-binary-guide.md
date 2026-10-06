# peerdrive 单二进制使用指南（v0.3.0 起）

> 适用范围：v0.3.0 及以后。此前发布的是三个二进制（`peerdrive-server` /
> `peersignal` / `peerdrive-reg-server`），本文档不适用于它们。

从 v0.3.0 起，主服务、信令服务器、注册认证服务合并为**一个** `peerdrive` 二进制，
按子命令启动。下载一份、升级一份，版本不再需要人工对齐。

---

## 0. 先跑这个（30 秒，不需要读任何配置）

```bash
./peerdrive demo
```

它会自己起一个信令和两个节点，把「两个节点之间传文件」完整跑一遍——
市场发现 → 加入 → 拿清单 → 跨节点拉取 → sha256 校验——然后逐步打印结论。

不需要 Go、不需要仓库、不需要 Node、不需要填任何环境变量、不需要装任何东西。
端口冲突风险也规避了（用 19100/19101/19102，不碰默认的 3000/9000/4000）。

看到全绿就说明这份二进制在你的机器上是好的。想自己动手：

```bash
./peerdrive serve
```

然后用**浏览器打开它打印的那个地址**（例如 `http://127.0.0.1:3000/panel`）。
面板已经内嵌在二进制里，会自己认出所在节点并自动连接——**不需要手填节点 ID、
信令地址、端口、密钥**。同一局域网内的手机/平板用打印出来的局域网地址。

> 为什么以前要读这么多文档：面板要人填 node/host/port/key 四项，节点 ID 每次
> 启动随机生成，只能从日志里翻出来再手工拼 URL。v0.3.2 起这几步由二进制替用户做完了。

---

## 1. 下载

从 [Releases](https://github.com/Hana-ame/peerdrive/releases/latest) 下载对应平台的二进制，
共 5 个资产：

```
peerdrive-linux-amd64      peerdrive-darwin-amd64
peerdrive-linux-arm64      peerdrive-darwin-arm64
peerdrive-windows-amd64.exe
```

```bash
gh release download v0.3.0 -p peerdrive-linux-amd64
chmod +x peerdrive-linux-amd64
mv peerdrive-linux-amd64 peerdrive
```

确认版本：

```bash
./peerdrive version
# peerdrive v0.3.0
```

> ⚠️ 一定要等下载**完整**再运行。中途被中断的残缺文件在执行时可能段错误，
> 表现为 `Segmentation fault`——这不是程序的问题，是文件没下完（校验大小即可）。

---

## 2. 五个子命令

```bash
peerdrive demo         # 一条命令跑通全链路（第一次用跑这个）
peerdrive [serve]      # 主服务（默认，不写子命令就是它）
peerdrive signal       # 信令服务器 + 节点发现
peerdrive reg          # 注册 / 认证 / 中继登记
peerdrive all          # 三者同进程、同端口
peerdrive version      # 打印版本
peerdrive help         # 帮助
```

写错子命令会打印用法并以退出码 `2` 结束。

### 选哪个？

| 你的情况 | 用哪个 |
|---|---|
| **第一次用 / 不知道从哪开始** | `peerdrive demo`（零配置跑一遍全链路） |
| 只跑一个普通节点（大多数用户） | `peerdrive serve`（默认端口 **3000**） |
| 多人组网，要自己托管信令 | `serve` + `signal` 分开跑 |
| 想在一个进程里跑全套（省端口、省内存） | `peerdrive all` |
| 单独提供注册/认证 | `peerdrive reg` |

**绝大多数用户只需要 `peerdrive serve`**——它已经内置 PeerJS 客户端，能连别人的信令服务器。

---

## 3. 最简用法

```bash
# 起一个节点（默认监听 :3000）
./peerdrive serve

# 从源码起
cd back && go build -tags nosqlite -o peerdrive ./cmd/peerdrive/ && ./peerdrive
```

启动后会打印一段「下一步做什么」，里面有一个**可以直接点开的面板地址**：

```
  下一步：打开面板

    http://127.0.0.1:3000/panel
```

面板已内嵌在二进制里，打开即用：**不用手填节点 ID、信令地址、端口、密钥**。
它会自己认出「我是从这个节点打开的」，反查节点 ID 与该节点所在的信令，然后自动连接。

启动后可访问：

| 地址 | 用途 |
|---|---|
| **`http://127.0.0.1:3000/panel`** | **公共网盘面板（零配置，推荐入口）** |
| `http://127.0.0.1:3000/swagger/index.html` | API 文档 |
| `http://127.0.0.1:3000/health` | 健康检查 |
| `http://127.0.0.1:3000/ping` | 存活探针，返回 `pong` |

---

## 4. 从旧版本迁移

**环境变量和路由路径全部没变**，原来的启动脚本一般不需要改。

| 旧的 | 现在的 |
|---|---|
| `./peerdrive-server` | `./peerdrive serve` |
| `./peerdrive-reg-server` | `./peerdrive reg` |
| `peersignal -addr :9000 -key peerjs` | `./peerdrive signal -addr :9000 -key peerjs` |

几点必须知道：

1. **`signal` 与 `reg` 的默认端口沿用旧值**（`:9000` / `:4000`），所以分开跑的部署可以直接换二进制。
2. **`all` 模式下端口会合并**，见第 6 节，有两处路径变化。
3. 旧二进制可以和新二进制并存一段时间——但它们各自连各自的数据库，不要指向同一个 `DB_PATH`。
4. **`reg` 的 `PORT` 两种写法都支持**：`PORT=4000`（旧 reg-server 的裸端口号写法）
   和 `PORT=:4000` / `HOST:PORT` 都能正常启动。

### systemd 单元改一行

```ini
# 旧
ExecStart=/opt/peerdrive/peerdrive-server
# 新
ExecStart=/opt/peerdrive/peerdrive serve
```

---

## 5. 各子命令的参数

### 5.1 `serve`

没有专属 flag，全部通过环境变量配置。常用的几个：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `3000` | 监听端口 |
| `HOST` | — | 监听地址 |
| `PEERDRIVE_STORAGE` | `./storage` | 共享目录根（生产环境建议显式设为绝对路径） |
| `PEERDRIVE_PEERJS_ID` | 自动生成 | 本节点 ID，组网时用 |
| `PEERDRIVE_PEERJS_HOST` / `_PORT` / `_SECURE` / `_KEY` | — | 连哪个信令服务器 |
| `PEERDRIVE_PSK` | 无 | 预共享密钥，设了就必须两端一致 |
| `PEERDRIVE_REG_SERVER` | 空 | 指向注册服务地址（要认证能力时填） |

完整列表见 `back/internal/config/config.go`。

> 🔐 `PEERDRIVE_PSK` 门禁的是 **PeerJS 节点互联**，不是 HTTP 接口——
> 设了之后，两端必须带同样的密钥才能建连；`/ping`、`/sources` 这类 HTTP
> 端点**不受它影响**（实测设了 PSK 仍然 200）。
> 想看本节点当前的 PSK 状态：`GET /peerjs/node`（注意是单数），
> 响应里有 `psk`（门禁是否启用）与 `psk_peers`（已通过门禁的连接数）两个字段。
> 详见 `doc/guide/USER_MANUAL.md`。

### 5.2 `signal`

```bash
./peerdrive signal -addr :9000 -key peerjs
```

| flag | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-addr` | `PEERSIGNAL_ADDR` | `:9000` | 监听地址 |
| `-key` | `PEERSIGNAL_KEY` | `peerjs` | API key，客户端要一致 |
| `-tokens` | `PEERJS_TOKENS` | 空 | 运维令牌白名单（逗号分隔） |
| `-tls-cert` / `-tls-key` | `PEERSIGNAL_TLS_CERT` / `_KEY` | 空 | 给了就走 HTTPS/WSS |
| `-cors-origin` | `PEERSIGNAL_CORS` | 空 | 面板类 REST 的 CORS 白名单 |

### 5.3 `reg`

```bash
JWT_SECRET=<你的密钥> ./peerdrive reg -addr :4000
```

| flag | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-addr` | `PORT` | `:4000` | 监听地址（`PORT=4000` 与 `PORT=:4000` 都可） |
| `-db` | `DB_PATH` → `PEERDRIVE_REG_DB` | `./reg.db` | SQLite 路径 |
| `-tls-cert` / `-tls-key` | `PEERDRIVE_REG_TLS_CERT` / `_KEY` | 空 | 给了就走 HTTPS |

> 🔑 **`JWT_SECRET` 是必填的**，缺失时 `reg` 直接拒绝启动并报
> `peerdrive reg: JWT_SECRET is required`——不会静默用空密钥。
>
> ⚠️ 改这个值等于作废所有已签发的令牌，客户端需要重新登录。
> `-tls-cert` 和 `-tls-key` 必须**成对**提供，只给一个会直接报错退出，不会悄悄降级成明文。

---

## 6. `all` 模式：三个服务一个端口

```bash
JWT_SECRET=<你的密钥> PEERDRIVE_STORAGE=/data ./peerdrive all
```

三者共享**一个进程、一个端口**（取 `serve` 的 `HOST`/`PORT`，默认 `:3000`）。
适合单机小规模部署：省端口、省内存、统一看日志。

### 路由分配

| 路径 | 归谁 |
|---|---|
| `/health`、`/sources`、`/swagger/*`、网盘相关接口 | 主服务 |
| `/peerjs/*`、`/discover/*`、`/status` | 信令 |
| `/auth/register`、`/auth/login`、`/auth/whoami`、`/auth/list`、`/p2p/*`、`/api/health` | 注册服务 |

### ⚠️ 两处路径与单独跑时不同

**1. 注册服务的 `/ping` 改成了 `/_reg/ping`**

主服务本来就有 `GET /ping`（返回 `pong`）。两个服务抢同一个路径时，
先注册的赢——如果不管，主服务的 `/ping` 会**静默变成注册服务的响应**：
不报错、不崩溃，监控看"通着"，只有依赖 `pong` 这个响应的脚本才会炸。

所以显式把注册服务的挪到了 `/_reg/ping`：

```bash
curl http://127.0.0.1:3000/ping         # pong          ← 主服务
curl http://127.0.0.1:3000/_reg/ping    # 注册服务 JSON  ← 注册服务
```

如果你的监控在用 `/ping`，`all` 模式下它仍然是 `pong`，**不受影响**。

**2. 信令面板从 `/` 挪到了 `/_signal`**

`all` 模式下 `/` 归主服务，所以面板换地址：

```
http://127.0.0.1:3000/_signal
```

要在 `all` 模式下开面板，给 `-cors-origin` 或 `PEERSIGNAL_CORS` 加上面板的源。

### `all` 模式下哪些变量失效

**只有「监听地址」失效**：`PEERSIGNAL_ADDR` 和 `reg` 的 `PORT` 会被忽略——
三者共享主服务的 `HOST`/`PORT`，留着会让人以为还在分别监听。

**其余信令变量照常生效**，实测确认：

| 变量 | `all` 模式 | 说明 |
|---|---|---|
| `PEERJS_TOKENS` | ✅ 生效 | `/status` 无 token 401，带 token 200 |
| `PEERSIGNAL_KEY` | ✅ 生效 | |
| `PEERSIGNAL_CORS` | ✅ 生效 | 面板跨域要用 |
| `PEERSIGNAL_TLS_CERT` / `_KEY` | ✅ 生效 | 成对提供 |
| `PEERSIGNAL_ADDR` | ❌ 忽略 | 端口由主服务决定 |
| `PORT`（注册服务用的） | ❌ 忽略 | 同上 |

---

## 7. 完整部署示例

### 7.1 单节点（最常见）

```bash
#!/usr/bin/env bash
set -euo pipefail

cd /opt/peerdrive

export PORT=3000
export PEERDRIVE_STORAGE=/srv/peerdrive
export PEERDRIVE_DOWNLOAD_DIR=/srv/peerdrive/downloads
export PEERDRIVE_PEERJS_ID=node-a

exec ./peerdrive serve
```

### 7.2 节点 + 自托管信令 + 注册认证（三个进程）

```bash
# 终端 1：信令（信令要先起来，节点要连它）
PEERJS_TOKENS=<运维令牌> ./peerdrive signal -addr :9000 -key peerjs

# 终端 2：节点
PEERDRIVE_STORAGE=/srv/peerdrive \
PEERDRIVE_PEERJS_HOST=127.0.0.1 PEERDRIVE_PEERJS_PORT=9000 \
PEERDRIVE_PEERJS_KEY=peerjs PEERDRIVE_PEERJS_SECURE=false \
PEERDRIVE_REG_SERVER=http://127.0.0.1:4000 \
./peerdrive serve

# 终端 3：注册认证
JWT_SECRET=<你的密钥> ./peerdrive reg -addr :4000 -db /srv/peerdrive/reg.db
```

### 7.3 合成一个进程

```bash
JWT_SECRET=<你的密钥> \
PEERDRIVE_STORAGE=/srv/peerdrive \
PEERDRIVE_PEERJS_ID=node-a \
./peerdrive all
```

### 7.4 systemd

```ini
[Unit]
Description=Peerdrive
After=network.target

[Service]
Type=simple
User=peerdrive
WorkingDirectory=/opt/peerdrive
EnvironmentFile=/etc/peerdrive/env
ExecStart=/opt/peerdrive/peerdrive serve
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

`/etc/peerdrive/env`：

```bash
PORT=3000
PEERDRIVE_STORAGE=/srv/peerdrive
PEERDRIVE_DOWNLOAD_DIR=/srv/peerdrive/downloads
```

---

## 8. 排错

| 现象 | 原因 | 处理 |
|---|---|---|
| `./peerdrive: Segmentation fault` | 下载被中断，文件残缺 | 删掉重下，校验大小（约 46MB） |
| `peerdrive reg: JWT_SECRET is required` | 没设 `JWT_SECRET` | 设置它；这个值决定令牌签发，不要泄露 |
| TLS 证书配了但仍是 HTTP | 只给了一个参数 | `-tls-cert` 和 `-tls-key` 必须成对 |
| `all` 模式下 `/ping` 不是 `pong` | 你用的是旧版本二进制 | 升到 v0.3.0 以上 |
| `all` 模式下打不开面板 | 面板地址已变 | 用 `/_signal`，并配 `-cors-origin` |
| 令牌突然全部失效 | `JWT_SECRET` 改了 | 改回去，或让客户端重新登录 |
| `go build` 链接期报「multiple definition of sqlite3_xxx」或「collect2: error: ld returned 1」 | 忘了 `-tags nosqlite` | 所有构建命令都要带 `-tags nosqlite` |

> 💡 最后一条很常见：项目同时依赖三个 SQLite 实现（`mattn/go-sqlite3`、
> `modernc.org/sqlite`、`go-llsqlite/crawshaw`），CGO 下会在链接期符号冲突，
> 报 `multiple definition of sqlite3_xxx`（具体符号名随版本变，不固定）。
> **任何时候构建都要带 `-tags nosqlite`**（CI 里也是这么配的）。

---

## 9. 从源码构建

```bash
cd back
go build -tags nosqlite -o peerdrive ./cmd/peerdrive/
```

跨平台：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -tags nosqlite -ldflags="-s -w" ./cmd/peerdrive/
```

跑测试：

```bash
go vet  -tags nosqlite ./...
go test -tags nosqlite ./...
```

---

## 10. 相关文档

| 文档 | 内容 |
|---|---|
| `doc/NETDISK.md` | 网盘链路原理与流水线 |
| `doc/PEERSIGNAL.md` | 信令协议细节 |
| `doc/NODE-API.md` | 节点 HTTP 接口 |
| `doc/guide/USER_MANUAL.md` | 普通用户使用手册 |
| `back/internal/regserver/README.md` | 注册服务内部说明 |
| `doc/guide/operation-manual.md` | 开发环境运维（⚠️ 部分章节较旧） |