# 第一章：如何运行并连接自己的节点

> 适用：分支 `refactor`（2026-09）。命令按 Linux/macOS 写，Windows 用 Git Bash 同理
> （`setsid nohup` 换成「另开一个终端」即可，路径写法自己换算）。
> 前置：Go ≥ 1.26（`back/go.mod` 写的是 1.26.2）、Node ≥ 20（只在用管理台时需要）、
> 端口空闲（下文约定：**信令 9100 / 节点 3001**）。

---

## 1.0 先选一条路

| 你的情况 | 做法 | 看哪一节 |
|---|---|---|
| 想最快看到效果，几台"机器"都在一台电脑上 | 一键脚本：信令 + 两个节点 + 自动验收 | §1.1 |
| 自己跑一个节点，想从另一台电脑连它 | 手工起节点 + 面板（公网要用 wss 信令） | §1.2 → §1.4 → §1.5 |
| 不跑节点，只想连别人的节点看看 | 直接开在线面板 | §1.4 |
| 我是运营者，要管自己的文件 / 市场 / 传输任务 | 节点管理台 `front/` | §1.6 |

---

## 1.1 最快路径：一键脚本

```bash
./scripts/netdisk-local-demo.sh          # 起信令 :9100 + node-a :3001 + node-b :3002，跑 6 项断言
./scripts/netdisk-local-demo.sh --stop   # 停掉它起的所有进程
```

它会自动跑完：节点市场发现 → 加入 → 共享清单 → 跨节点拉取 → sha256 校验 → PSK 门禁。
**服务会保留下来供手测**，所以跑完还能接着用面板点。
CI 的 `.github/workflows/e2e.yml` 跑的是同一套脚本，它能过 = 环境没问题。

> ⚠️ 它假设自己是 9100/3001/3002 的唯一主人。上次的进程还在跑时，新起的 server 会因
> 端口占用直接退出，而后面的 curl 全打到**旧进程**上：节点看得到、join 也成功，
> 但清单是旧的、落盘路径也是旧的 —— 表现为"任务 done 但文件没落盘"。
> 重跑前先 `--stop`。

---

## 1.2 手工起一个节点

### 1.2.1 编译

```bash
cd back
go build -tags nosqlite -o /tmp/pd/bin/server ./cmd/server
```

`-tags nosqlite` 是硬约束，不是可选项：仓库为了「发布时 `CGO_ENABLED=0` 也能跑」
同时挂了两个 SQLite 驱动（mattn 走 cgo、modernc 纯 Go），不加这个 tag 会冲突。

### 1.2.2 起自托管信令（推荐，脱外网）

```bash
cd back/signalserver
go run ./cmd/peersignal -addr :9100 -key peerjs
# 探活
curl -s http://127.0.0.1:9100/status
```

| 参数 | 说明 |
|---|---|
| `-addr` | 监听地址，默认 `:9000` |
| `-key` | 命名空间。**节点侧 `PEERDRIVE_PEERJS_KEY` 必须与它一致**，否则连不上 |
| `-tokens` | 信令 token 白名单（逗号分隔，空 = 不限制） |
| `-tls-cert` / `-tls-key` | **必须成对给**才切到 HTTPS/WSS；只给一个会报错退出（§1.5） |

信令只转发 SDP/ICE，**不碰数据面** —— 文件内容走 WebRTC DataChannel，不经过信令。

### 1.2.3 起节点

```bash
mkdir -p /tmp/pd/n1/root/downloads/shared /tmp/pd/n1/run
cd /tmp/pd/n1/run                      # cwd 决定 peerdrive.db 落在哪（多节点必须各自 cwd）
env PORT=3001 \
    PEERDRIVE_PEERJS_ID=my-node-1 \
    PEERDRIVE_STORAGE=/tmp/pd/n1/root \
    PEERDRIVE_DOWNLOAD_DIR=/tmp/pd/n1/root/downloads \
    PEERDRIVE_SHARE_ENABLE=true \
    PEERDRIVE_SHARE_DIRS=/tmp/pd/n1/root/downloads/shared \
    PEERDRIVE_PEERJS_ENABLE=true \
    PEERDRIVE_PEERJS_HOST=127.0.0.1  PEERDRIVE_PEERJS_PORT=9100 \
    PEERDRIVE_PEERJS_KEY=peerjs      PEERDRIVE_PEERJS_SECURE=false \
    PEERDRIVE_DISCOVER_URL=http://127.0.0.1:9100 \
    PEERDRIVE_BT_DHT_ENABLE=false    PEERDRIVE_IPFS_GATEWAY_ENABLE=false \
    /tmp/pd/bin/server > /tmp/pd/n1.log 2>&1 &
```

必填的就这几个，其余用默认值：

| 变量 | 为什么要设 |
|---|---|
| `PORT` | HTTP 端口，默认 3000 |
| `PEERDRIVE_PEERJS_ID` | 你在网络里的名字（peer id）。不给就随机生成 —— **随机的话别人没法填 id 连你** |
| `PEERDRIVE_STORAGE` | 内容寻址根：进来的内容按 `storageDir/<hash前2位>/<hash>` 落盘，天然去重 |
| `PEERDRIVE_DOWNLOAD_DIR` | 下载/登记目录（`file_index` 的根） |
| `PEERDRIVE_SHARE_ENABLE` + `PEERDRIVE_SHARE_DIRS` | 对外共享。**默认全关**，不声明就不暴露任何清单 |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY/SECURE` | 信令地址与命名空间，必须和 §1.2.2 一致 |
| `PEERDRIVE_DISCOVER_URL` | 发现 API（节点市场/自动搜索靠它）。设了它优先于 MQTT |
| `PEERDRIVE_PSK` | 可选。设了之后对端必须出示同一把密钥（见 §1.4.4） |

本地练习时把 `PEERDRIVE_BT_DHT_ENABLE` / `PEERDRIVE_IPFS_GATEWAY_ENABLE` 关掉，
省掉 DHT 引导和网关探测的等待与噪音。

完整变量表见根 `README.md` 的「环境变量」一节，源码定义在 `back/internal/config/config.go`。

### 1.2.4 拿到自己的 peer id

```bash
curl -s http://127.0.0.1:3001/ping          # 存活
curl -s http://127.0.0.1:3001/peerjs/node   # 自己的 id / 在线状态 / 当前连接 / PSK 状态
```

`/peerjs/node` 会回 `{"id":"my-node-1","online":true,...,"psk":false,"psk_peers":0}`。
**这个 `id` 就是别人在面板里要填的东西。**

---

## 1.3 先分清三种"连"

连不上时一半的问题是搞混了这三种通道，它们的失败原因完全不同：

| 通道 | 谁连谁 | 走什么 | 要点 |
|---|---|---|---|
| 公共面板 → 远端节点 | 浏览器 → 你的节点 | 信令换 SDP/ICE，然后 **WebRTC DataChannel** | 要打洞；信令配置（host/port/path/key/secure）必须与节点完全一致 |
| 管理台 → 本机节点 | 浏览器 → 同一台机器上的节点 | HTTP **`/ws/peer`**（`local` 会话） | 毫秒级，不需要信令也不打洞；**但归 peerjs 路由组**（§1.6） |
| 节点 → 节点 | 你的节点 ↔ 别人的节点 | `PEERDRIVE_PEERJS_PEERS` 静态指定，或管理台里「加入」 | 加入后落 `joined_nodes.json`，重启后仍是常驻对端 |

---

## 1.4 用公共面板连自己的节点

面板是**单个静态文件**，`file://` 双击就能开，不需要本地后端：

- 在线版：<https://hana-ame.github.io/peerdrive/>（push 后自动部署）
- 本地版：`cd packages/peerdrive-client && npm run build:panel` → `dist/panel.html`

### 1.4.1 表单怎么填

| 字段 | 对应 | 说明 |
|---|---|---|
| 节点 peer id | `in-node` | §1.2.4 拿到的那个 id |
| 信令 host / port / path / key | `in-host` `in-port` `in-path` `in-key` | 必须与节点侧的 `PEERDRIVE_PEERJS_*` 一致；`key` 不一致是最常见的"明明在线却连不上" |
| wss 勾选框 | `in-secure` | **HTTPS 页面必须勾**（§1.4.3）；本地 `ws://` 可取消 |
| 预共享密钥 | `in-psk` | 只有节点开了 `PEERDRIVE_PSK` 才要填 |

填好点「连接」。成功的日志是 `已建立 WebRTC 直连`，紧接着 `清单：N 合集 · M 文件`。
失败时面板会补一句原因提示，例如
`提示：对方节点离线、peer id 写错，或信令配置不同（host/port/path/key/secure）`。

### 1.4.2 自动搜索

点「自动搜索」会直接问信令的发现接口（`GET /discover/nodes`），**不占 WebRTC 连接**，
没连任何节点也能用。

> ⚠️ 它的唯一硬前提：信令必须回 `Access-Control-Allow-Origin`。
> 面板是 `file://`（origin 为 `null`）或托管在自己的域上，**与信令不同源**。
> 本仓库的 `back/signalserver` 已放开 CORS；**线上的 `peersignal.moonchan.xyz`
> 实测没有这个头**（2026-09-22 复验），所以在那条路上只能用「手动填 id」。

### 1.4.3 HTTPS 页面只能用 wss 信令

浏览器按「混合内容」拦掉 HTTPS 页面里的 `ws://`，而 PeerJS **只报"连不上"，不会告诉你原因**。
面板内置了守卫，会直接提示：

> 本页是 HTTPS，浏览器会按「混合内容」拦掉 ws:// 信令（本机 localhost 的 ws://
> 多数浏览器仍放行，局域网 IP 一律拦）。请改用 wss……

所以：**用在线版面板连局域网里的节点，信令必须走 wss**（§1.5），光起个 `ws://` 是不行的。

### 1.4.4 节点开了 PSK 怎么办

节点设了 `PEERDRIVE_PSK` 之后，对端必须在连接上出示同一把密钥，否则所有请求回 `PSK_REQUIRED`。
面板会明确提示「去填密钥」而不是让你排查网络。

密钥**不进地址栏、不写 localStorage**（会被历史记录/截图/长期明文留存）：
一次性分享链接里的 `psk=` 参数允许预填，但读完后**立刻从地址栏抹掉**。

### 1.4.5 分享一个「连这个节点」的链接

面板会把当前状态回写地址栏，可直接复制：

```
panel.html?node=my-node-1&host=127.0.0.1&port=9100&path=/&key=peerjs&secure=0&auto=1
```

（`psk` 不会被写进去，正是 §1.4.4 的原因。）

---

## 1.5 让别的机器连得上：公网 + wss

节点在家里/内网，想从外面连，需要（1）信令两边都能访问（2）信令是 wss。

**用现成的线上信令**（省事）：

```bash
PEERDRIVE_PEERJS_HOST=peersignal.moonchan.xyz
PEERDRIVE_PEERJS_PORT=443
PEERDRIVE_PEERJS_KEY=pd-signal-b9447b406828e500
PEERDRIVE_PEERJS_SECURE=true
PEERDRIVE_DISCOVER_URL=https://peersignal.moonchan.xyz
```

**自托管信令开 wss**：

```bash
peersignal -addr :9100 -key peerjs -tls-cert cert.pem -tls-key key.pem
```

两个 TLS 参数必须**同时给**，只给一个会直接报错退出（这是故意的：静默降级成 ws 更危险）。
不想改端口就挂一层 TLS 反代（nginx/Caddy）也行。

> 注意：信令实现方式不限，任何 PeerJS 兼容信令都可以（公共 PeerJS 云、
> Node.js `peerjs-server`、本仓库 `back/signalserver`）—— 只要节点和面板指向同一个、
> `key` 一致即可。

---

## 1.6 用节点管理台连（运营者）

```bash
cd front
npm ci
npm run dev        # http://localhost:5173
```

在设置页把节点地址填进去，形如 `http://127.0.0.1:3001`（要带 token 就写
`http://host:3001#token123`，`#` 后面的部分会被单独存起来）。
它走的是 `/ws/peer`（`local` 会话），**不经过信令、不打洞**。

> ⚠️ `/ws/peer` 归 peerjs 路由组：`PEERDRIVE_PEERJS_ENABLE=false` 时这条路径直接 404。
> 表现是"管理台连不上/什么都没有"，而节点本身是健康的。
> 要同时满足管理台，就得开着 peerjs（指向一个能连的信令即可）。

管理台是**运营者视野**（我的网盘 / 节点市场 / 我的节点 / 传输任务），
和给纯消费者用的公共面板是两回事 —— 它需要后端在跑。

---

## 1.7 怎么确认真的连上了

按顺序自查，每一步都能单独验证：

1. **节点活着**：`curl http://127.0.0.1:3001/ping`
2. **节点在信令上注册了**：`curl http://127.0.0.1:9100/status`，且 `/peerjs/node` 回的 id 是你设的那个
3. **面板连上了**：日志出现 `已建立 WebRTC 直连`，左栏节点状态变在线
4. **清单拿得到**：`清单：N 合集 · M 文件`（`M>0` 才说明对端真的共享了东西）
5. **能取回**：点「保存」落盘，或点任务行的「取回校验」—— 它按 hash 重新拉一遍并复算 sha256
6. **端到端**：`./scripts/netdisk-local-demo.sh` 六项断言全 PASS

---

## 1.8 症状 → 原因 → 处置

| 症状 | 真因 | 处置 |
|---|---|---|
| 面板只说"连不上"，没别的信息 | HTTPS 页面 + `ws://` 信令被混合内容拦掉 | 勾 wss / 信令开 TLS（§1.5） |
| 自动搜索失败 | 信令没回 `Access-Control-Allow-Origin` | 换开了 CORS 的信令，或手动填 peer id |
| 提示 `psk: 本节点需要预共享密钥`，但我填了密钥 | 对端的 `psk-auth` 帧被丢了（旧版本的 `bindConn` 顺序问题），或两把密钥不一致 | 看节点日志：`psk ok from X` 有 → 没问题；`psk mismatch from X` → 密钥不对；**两个都没有** → 那帧根本没被看见（不是密钥问题） |
| 管理台连上但什么都没有 | `/ws/peer` 404：`PEERDRIVE_PEERJS_ENABLE=false` | 打开 peerjs 并指向可用信令 |
| 清单列得出，一拉就 `read failed` | 共享目录没在 `PEERDRIVE_SHARE_DIRS` 里声明过 | 声明它（目录可以在任意位置，但**必须声明**） |
| 节点看得到、join 成功，但文件没落盘 | 端口被上一次的旧进程占着，curl 全打到旧进程 | `--stop` 后重跑；或换端口 |
| 请求卡 15 秒后 TIMEOUT（不是 err） | 跑的是老二进制，不认识新动词 | 重新 `go build -tags nosqlite` |
| 起完节点立刻点面板连不上 | 节点还没 announce 完、心跳未稳 | 等 `/ping` 通、心跳稳定几秒再来 |
