# 第一章：如何运行并连接自己的节点

> 适用：分支 `refactor` · **v0.1.1 起的 release**（第一个真正带资产的发布；
> `v0.1.0` 的 tag 打出来了但 release 那一步是红的，没有可下载的东西）。时间：2026-09。
> **这一章不需要 Go、不需要编译** —— 直接下载发布好的二进制。
> 想改代码 / 跑最新未发版的提交 / 跑测试，看[附录 A：从源码编译](appendix-build-from-source.md)
> —— 那是**可跳过**的，只影响想动代码的人。
> 端口约定：**节点 3001** —— 信令不用你开，见 §1.2。
> 连上之后想往里放文件：[第二章：添加本地文件到节点中查看](02-add-local-files.md)。

---

## 1.0 先选一条路

| 你的情况 | 做法 | 看哪一节 |
|---|---|---|
| 跑一个自己的节点，用面板连它 | 下载节点二进制 → 起节点 → 面板填 id | §1.1 → §1.3 → §1.5 |
| 从别的机器 / 手机（外网）连自己的节点 | 同上，什么都不用额外配（信令默认就是公网的） | §1.1 → §1.3 → §1.6 |
| 不跑节点，只想连别人的节点看看 | 直接开在线面板 | §1.5 |
| 我是运营者，要管文件 / 市场 / 传输任务 | 节点管理台 `front/` | §1.7 |
| 我想连的信令不是公共那个（自建 / 内网） | 改环境变量（附录 A 有自托管信令） | §1.2 |

---

## 1.1 下载发布包

打开 <https://github.com/Hana-ame/peerdrive/releases/latest>，按自己的机器挑：

| 机器 | 下载这个 |
|---|---|
| Linux x86_64 | `peerdrive-linux-amd64` |
| Linux ARM64（树莓派 / 服务器） | `peerdrive-linux-arm64` |
| Windows x86_64 | `peerdrive-windows-amd64.exe` |
| macOS Intel | `peerdrive-darwin-amd64` |
| macOS Apple Silicon | `peerdrive-darwin-arm64` |

> 发布包里还有一份 `peersignal-*`，那是**自托管信令**。
> 只有你想在自己机器上另起一套信令时才需要它 —— 正常玩不用下，见 §1.2。

命令行取（不用打开网页）：

```bash
# 有 gh
gh release download --repo Hana-ame/peerdrive --pattern 'peerdrive-linux-amd64'

# 只有 curl
curl -LO https://github.com/Hana-ame/peerdrive/releases/latest/download/peerdrive-linux-amd64
```

下完的收尾动作：

```bash
chmod +x peerdrive-linux-amd64     # Linux / macOS
```

| 平台 | 第一次运行会遇到的拦路虎 | 处理 |
|---|---|---|
| macOS | 二进制没有签名，Gatekeeper 会拦（"无法打开，因为它来自身份不明的开发者"） | `xattr -d com.apple.quarantine peerdrive-darwin-arm64`，或在「访达」里右键 → 打开 |
| Windows | SmartScreen 提示"Windows 已保护你的电脑" | 点「更多信息」→「仍要运行」 |
| Linux | 一般没有 | — |

节点程序**没有命令行参数**，全部靠环境变量配置（见 §1.3）。
确认它能跑：`./peerdrive-linux-amd64` 起来后访问 `http://127.0.0.1:3001/ping`。

---

## 1.2 信令：不用你部署

**所有 peerdrive 程序默认连同一个公共信令**，节点和面板都一样：

| 项 | 值 |
|---|---|
| host | `peersignal.moonchan.xyz` |
| port | `443` |
| key | `pd-signal-b9447b406828e500` |
| 加密 | `wss`（HTTPS 页面里也能直接用） |

两边都写死成这一对，所以**什么都不用填**就能互相找到。
（早先默认是 PeerJS 公共云 `0.peerjs.com`/`peerjs`，结果"照默认跑"的节点和面板
分属两个信令 —— 各是一张节点表，谁也搜不到谁。现在改掉了。）

它只负责转发 SDP/ICE 帮两端打洞，**不碰数据面**：文件内容走 WebRTC DataChannel，
不经过信令。想确认它活着：

```bash
curl -s https://peersignal.moonchan.xyz/status
# {"clients":N,"discovered":N,"key":"pd-signal-b9447b406828e500","nodes":[...]}
```

什么时候才需要换：内网离线环境、或想自己掌控信令。那种情况改环境变量指向自己的
（自托管信令的编译与启动在附录 A；开 TLS 时 `-tls-cert` / `-tls-key` **必须成对给**）。
**环境变量都能改，但教程不展开** —— 默认值就是推荐用法。

> 换信令时记住一条：**节点和面板必须指向同一个、且 `key` 一致**。
> 两边不一致的表现是"明明都在线却连不上"。

---

## 1.3 起自己的节点

```bash
mkdir -p /tmp/pd/n1/root/downloads/shared /tmp/pd/n1/run
cd /tmp/pd/n1/run                      # cwd 决定 peerdrive.db 落在哪（多节点必须各自 cwd）
env PORT=3001 \
    PEERDRIVE_PEERJS_ID=my-node-1 \
    PEERDRIVE_STORAGE=/tmp/pd/n1/root \
    PEERDRIVE_DOWNLOAD_DIR=/tmp/pd/n1/root/downloads \
    PEERDRIVE_SHARE_ENABLE=true \
    PEERDRIVE_SHARE_DIRS=/tmp/pd/n1/root/downloads/shared \
    PEERDRIVE_BT_DHT_ENABLE=false    PEERDRIVE_IPFS_GATEWAY_ENABLE=false \
    /tmp/pd/peerdrive-linux-amd64 > /tmp/pd/n1.log 2>&1 &
```

（Windows 用 `set VAR=...` 另起一行，或 PowerShell 的 `$env:VAR='...'`；程序名换成 `.exe`。）

必填的就这几个，其余用默认值：

| 变量 | 为什么要设 |
|---|---|
| `PORT` | HTTP 端口，默认 3000 |
| `PEERDRIVE_PEERJS_ID` | 你在网络里的名字（peer id）。不给就随机生成 —— **随机的话别人没法填 id 连你** |
| `PEERDRIVE_STORAGE` | 内容寻址根：进来的内容按 `storageDir/<hash前2位>/<hash>` 落盘，天然去重 |
| `PEERDRIVE_DOWNLOAD_DIR` | 下载 / 登记目录（`file_index` 的根） |
| `PEERDRIVE_SHARE_ENABLE` + `PEERDRIVE_SHARE_DIRS` | 对外共享。**默认全关**，不声明就不暴露任何清单 |
| `PEERDRIVE_PSK` | 可选。设了之后对端必须出示同一把密钥（见 §1.5.4） |

没列的就是用默认值：信令地址、发现服务都已指向公共信令（§1.2），不用动。

本地练习时把 `PEERDRIVE_BT_DHT_ENABLE` / `PEERDRIVE_IPFS_GATEWAY_ENABLE` 关掉，
省掉 DHT 引导和网关探测的等待与噪音。

完整变量表见根 `README.md` 的「环境变量」一节，源码定义在 `back/internal/config/config.go`。

### 拿到自己的 peer id

```bash
curl -s http://127.0.0.1:3001/ping          # 存活
curl -s http://127.0.0.1:3001/peerjs/node   # 自己的 id / 在线状态 / 当前连接 / PSK 状态
```

`/peerjs/node` 会回 `{"id":"my-node-1","online":true,...,"psk":false,"psk_peers":0}`。
**这个 `id` 就是别人在面板里要填的东西。**

---

## 1.4 先分清三种"连"

连不上时一半的问题是搞混了这三种通道，它们的失败原因完全不同：

| 通道 | 谁连谁 | 走什么 | 要点 |
|---|---|---|---|
| 公共面板 → 远端节点 | 浏览器 → 你的节点 | 信令换 SDP/ICE，然后 **WebRTC DataChannel** | 要打洞；信令配置（host/port/path/key/secure）必须与节点一致 —— **默认两边都是公共信令，已经一致** |
| 管理台 → 本机节点 | 浏览器 → 同一台机器上的节点 | HTTP **`/ws/peer`**（`local` 会话） | 毫秒级，不需要信令也不打洞；**但归 peerjs 路由组**（§1.7） |
| 节点 → 节点 | 你的节点 ↔ 别人的节点 | `PEERDRIVE_PEERJS_PEERS` 静态指定，或管理台里「加入」 | 加入后落 `joined_nodes.json`，重启后仍是常驻对端 |

---

## 1.5 用公共面板连自己的节点

面板是**单个静态文件**，`file://` 双击就能开，不需要本地后端：

- 在线版：<https://hana-ame.github.io/peerdrive/>（push 后自动部署）
- 本地版：从 release 下载，或自己构建（可选，见附录 A）

### 1.5.1 表单怎么填

| 字段 | 对应 | 说明 |
|---|---|---|
| 节点 peer id | `in-node` | §1.3 拿到的那个 id |
| 信令 host / port / path / key | `in-host` `in-port` `in-path` `in-key` | **已预填公共信令**（§1.2），一般不用动；只有节点换了信令才跟着改，`key` 不一致是最常见的"明明在线却连不上" |
| wss 勾选框 | `in-secure` | 默认勾（公共信令是 wss）。**HTTPS 页面必须勾**（§1.5.3） |
| 预共享密钥 | `in-psk` | 只有节点开了 `PEERDRIVE_PSK` 才要填 |

填好点「连接」。成功的日志是 `已建立 WebRTC 直连`，紧接着 `清单：N 合集 · M 文件`。
失败时面板会补一句原因提示，例如
`提示：对方节点离线、peer id 写错，或信令配置不同（host/port/path/key/secure）`。

### 1.5.2 自动搜索

点「自动搜索」会直接问信令的发现接口（`GET /discover/nodes`），**不占 WebRTC 连接**，
没连任何节点也能用。公共信令已放开 CORS，所以在线版面板直接用就行。

> 它唯一的前提：信令要回 `Access-Control-Allow-Origin`。面板是 `file://`
> （origin 为 `null`）或托管在自己的域上，**与信令不同源**。
> 公共信令实测带 `access-control-allow-origin: *`（2026-09-22 复验）。
> 换成自托管信令时若搜不出来，先查这个头。

### 1.5.3 HTTPS 页面只能用 wss 信令

浏览器按「混合内容」拦掉 HTTPS 页面里的 `ws://`，而 PeerJS **只报"连不上"，不会告诉你原因**。
面板内置了守卫，会直接提示：

> 本页是 HTTPS，浏览器会按「混合内容」拦掉 ws:// 信令（本机 localhost 的 ws://
> 多数浏览器仍放行，局域网 IP 一律拦）。请改用 wss……

所以：**用在线版面板连局域网里的节点，信令必须走 wss**（§1.6），光起个 `ws://` 是不行的。

### 1.5.4 节点开了 PSK 怎么办

节点设了 `PEERDRIVE_PSK` 之后，对端必须在连接上出示同一把密钥，否则所有请求回 `PSK_REQUIRED`。
面板会明确提示「去填密钥」而不是让你排查网络。

密钥**不进地址栏、不写 localStorage**（会被历史记录 / 截图 / 长期明文留存）：
一次性分享链接里的 `psk=` 参数允许预填，但读完后**立刻从地址栏抹掉**。

### 1.5.5 分享一个「连这个节点」的链接

面板会把当前状态回写地址栏，可直接复制：

```
panel.html?node=my-node-1&host=peersignal.moonchan.xyz&port=443&path=/&key=pd-signal-b9447b406828e500&secure=1&auto=1
```

（`psk` 不会被写进去，正是 §1.5.4 的原因。）

---

## 1.6 从别的机器 / 外网连

**什么都不用额外配**：公共信令本来就在公网上、本来就是 wss（§1.2），
所以节点在家里的机器上跑着，你在外面用在线版面板填它的 peer id 就能连。

真正要满足的只有两条：

1. **节点能出网**（能连到 `peersignal.moonchan.xyz:443`）—— 起来后看日志
   `peerjs: connected, id=<你的id> host=peersignal.moonchan.xyz`
2. **节点在网络里有个固定名字** —— 就是 `PEERDRIVE_PEERJS_ID`，不给就随机，
   随机的 id 别人没法填

打洞失败（对称 NAT / 严格防火墙）时两端会一直停在"正在连接"；
那种情况才需要 TURN，或把节点放到有公网 IP 的机器上。

> 想完全自建信令（内网离线、或自己掌控）：见附录 A 的自托管信令。
> 自建时记住两点：HTTPS 页面必须用 wss（`-tls-cert`/`-tls-key` 成对给，或挂 TLS 反代）；
> 节点与面板要指向同一个、key 一致。

---

## 1.7 用节点管理台连（运营者）

管理台是 **Node 工程**，需要 npm 构建（不需要 Go）：

```bash
cd front
npm ci
npm run dev        # http://localhost:5173
```

在设置页把节点地址填进去，形如 `http://127.0.0.1:3001`（要带 token 就写
`http://host:3001#token123`，`#` 后面的部分会被单独存起来）。
它走的是 `/ws/peer`（`local` 会话），**不经过信令、不打洞**。

> ⚠️ `/ws/peer` 归 peerjs 路由组：`PEERDRIVE_PEERJS_ENABLE=false` 时这条路径直接 404。
> 表现是"管理台连不上 / 什么都没有"，而节点本身是健康的。
> 要同时满足管理台，就得开着 peerjs —— 默认就是开的，别把它关掉。

管理台是**运营者视野**（我的网盘 / 节点市场 / 我的节点 / 传输任务），
和给纯消费者用的公共面板是两回事 —— 它需要后端在跑。

> 面板要不要单独下载？不用。在线版 <https://hana-ame.github.io/peerdrive/> 就是最新产物；
> 想留一份本地的单文件版，见附录 A 的 `npm run build:panel`。

---

## 1.8 怎么确认真的连上了

按顺序自查，每一步都能单独验证：

1. **节点活着**：`curl http://127.0.0.1:3001/ping`
2. **节点在信令上注册了**：`curl https://peersignal.moonchan.xyz/status` 里出现你的 peerId，
   且 `curl http://127.0.0.1:3001/peerjs/node` 回的 `online` 是 `true`
3. **面板连上了**：日志出现 `已建立 WebRTC 直连`，左栏节点状态变在线
4. **清单拿得到**：`清单：N 合集 · M 文件`（`M>0` 才说明对端真的共享了东西）
5. **能取回**：点「保存」落盘，或点任务行的「取回校验」—— 它按 hash 重新拉一遍并复算 sha256
6. **端到端**（需要源码，见附录 A）：`./scripts/netdisk-local-demo.sh` 六项断言全 PASS

> 第 1–2 步的默认配置实测输出（不设任何 `PEERDRIVE_PEERJS_*`）：
>
> ```
> # 节点日志
> ... peerjs: connected, id=pd-default-signal host=peersignal.moonchan.xyz
> ... peerjs: http discovery enabled url=https://peersignal.moonchan.xyz collections=1
>
> $ curl -s http://127.0.0.1:3001/peerjs/node
> {"id":"pd-default-signal","online":true,"peers":[],"psk":false,"psk_peers":0}
>
> $ curl -s https://peersignal.moonchan.xyz/status
> {"clients":1,"discovered":1,"key":"pd-signal-b9447b406828e500",
>  "nodes":[{"peerId":"pd-default-signal","nodeType":"go-persistent",
>            "loadInfo":{"shares":{"collections":0,"dirs":1,"files":0}}}],...}
> ```
>
> `status` 的 `nodes` 里出现你的 `peerId` = 已在公共信令上登记成功（`clients` 是 WS 连接数；
> announce 是心跳上报，刚起要等几十秒才会出现）。
> 这里 `files:0` 是正常的：共享目录刚声明、还没登记文件索引
> （登记走 `POST /files/register_folder`，见第二章）。

---

## 1.9 症状 → 原因 → 处置

| 症状 | 真因 | 处置 |
|---|---|---|
| 面板只说"连不上"，没别的信息 | HTTPS 页面 + `ws://` 信令被混合内容拦掉 | 勾 wss（§1.5.3）；自建信令要开 TLS（附录 A） |
| 自动搜索失败 | 信令没回 `Access-Control-Allow-Origin`，或节点还没 announce | 查 `curl -D - -H 'Origin: null' <信令>/discover/nodes`；等一个心跳周期（约 30s）再来 |
| 提示 `psk: 本节点需要预共享密钥`，但我填了密钥 | 对端的 `psk-auth` 帧被丢了（旧版本的 `bindConn` 顺序问题），或两把密钥不一致 | 看节点日志：`psk ok from X` 有 → 没问题；`psk mismatch from X` → 密钥不对；**两个都没有** → 那帧根本没被看见（不是密钥问题） |
| 管理台连上但什么都没有 | `/ws/peer` 404：`PEERDRIVE_PEERJS_ENABLE=false` | 打开 peerjs 并指向可用信令 |
| 清单列得出，一拉就 `read failed` | 共享目录没在 `PEERDRIVE_SHARE_DIRS` 里声明过 | 声明它（目录可以在任意位置，但**必须声明**） |
| 请求卡 15 秒后 TIMEOUT（不是 err） | 跑的是老版本二进制，不认识新动词 | 重新下载最新 release |
| 起完节点立刻点面板连不上 | 节点还没 announce 完、心跳未稳 | 等 `/ping` 通、心跳稳定几秒再来 |
