# 第二章：添加本地文件到节点中查看

> 接第一章：节点已经在跑（信令 9100 / 节点 3001 那条约定继续用），面板也能连上。
> 这一章解决「我本地有个文件，怎么让它进节点、并且看得见」。
> **全程不用编译**：面板是拖拽，命令行是 `curl` 打本机 HTTP。

---

## 2.0 先分清两个东西：内容库 ≠ 共享清单

这是本章最容易踩的坑，先把口径定死：

| | **内容库（CAS）** | **共享清单（对外可见）** |
|---|---|---|
| 回答什么 | 这个节点手里有哪些字节 | 这个节点愿意给别人看什么 |
| 谁能看到 | 只有你自己（`/files` 查得到） | 任何连上来的对端（`share` 帧） |
| 怎么进去 | 「本地入库」/「网络入库」/ `POST /files/upload` | 文件落在**共享目录里**且**登记进索引** |
| 落盘位置 | `PEERDRIVE_STORAGE/<hash前2位>/<hash>` | 文件留在原地，不动 |
| 默认状态 | 一直可用 | **默认关闭**（`PEERDRIVE_SHARE_ENABLE=false`） |

一句话：**入库只是把内容放进去，不等于对外共享**。
入库成功拿到的 `hash` 立刻就能取回（面板「取回校验」），但它不会自己出现在别人的清单里 —— 见 §2.5。

---

## 2.1 方式一：面板「本地入库」（浏览器里拖文件）

适合：临时放点东西进去、想马上验证能不能取回。

1. 面板连上节点（第一章 §1.4）
2. 右上角「选择文件」→ 可以多选（`in-file` 是 `multiple`）
3. 点 **本地入库**
4. 看日志：`入库完成：<名字> → sha256 <64位hex>`

> 面板日志会接着提示一句：**这条内容之后在任何地方都能用这个 hash 取回**；
> 要立刻验证，点任务行上的 **取回校验** —— 它按 hash 重新拉一遍并复算 sha256。

实现的几个硬约束（都在 `panel/app.js` 顶部）：

| 约束 | 值 | 为什么 |
|---|---|---|
| 单文件提醒阈值 | 256 MB | `put()` 先把整个内容读进浏览器内存再分片，超了大文件传到一半标签页会崩 |
| 分片 | 64 KB，**串行** | 一条连接同时只能有一个上传流 |
| 每轮超时 | 30 s | 接收方要落盘，别把"慢"当成"卡死" |
| 预览上限 | 2 MB | 超过直接存盘，不进预览框 |

---

## 2.2 方式二：`POST /files/upload`（脚本 / 服务器场景）

适合：批量、定时、没有浏览器的机器。

```bash
curl -F "file=@./upload-me.txt" http://127.0.0.1:3001/files/upload
```

实测返回（v0.1.1，Windows）：

```json
{"already_exists":false,"filename":"upload-me.txt",
 "hash":"b71c35903f41425f7173b1fe6c1c264d529c809492a3682573ef2e136b252488",
 "mime":"text/plain; charset=utf-8","size":22}
```

同一个文件再传一次会回 `"already_exists":true` —— 内容寻址存储，相同内容只存一份，不报错。

落盘位置（这就是 CAS 布局）：

```
PEERDRIVE_STORAGE/b7/b71c35903f41425f7173b1fe6c1c264d529c809492a3682573ef2e136b252488
```

### 大小上限（很反直觉，记得看）

| 场景 | 上限 | 环境变量 |
|---|---|---|
| 匿名请求 | **10 MB** | `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` |
| 认证用户 | 100 MB | `PEERDRIVE_MAX_UPLOAD_BYTES` |

**单机跑的节点按匿名算**：`AuthRequired` 在没配注册服务器时直接放行（否则本地单机全站 401），
但 `authenticated` 仍是 false ⇒ 走匿名档的 10 MB。要放开就设 `PEERDRIVE_MAX_UPLOAD_ANON_BYTES`。

---

## 2.3 方式三：把已有的文件登记进节点（想让它出现在清单里，走这条）

文件已经在磁盘上、不想复制一份进去 —— 用登记：

```bash
# 单个文件
curl -X POST -H 'Content-Type: application/json' \
  -d '{"path":"D:/.../shared/demo.txt","filename":"demo.txt"}' \
  http://127.0.0.1:3001/files/register_local

# 整个目录（推荐：一次挂一批）
curl -X POST -H 'Content-Type: application/json' \
  -d '{"folder_path":"D:/.../downloads/shared"}' \
  http://127.0.0.1:3001/files/register_folder
```

实测（目录里有两个文件）：

```json
{"registered":[{"filename":"demo.txt","hash":"c787d82a71a194ff..."},
               {"filename":"note.md","hash":"f957b19529906961..."}]}
```

**登记 ≠ 复制**：文件内容留在原地，节点只是把它的路径和 hash 记进索引。
所以原文件删了/改了，这条索引就废了。

### 路径边界（会被拒绝的情况）

登记接口只接受落在这几个根里的路径，越界直接 `path outside storage root`：

- `PEERDRIVE_STORAGE`
- `PEERDRIVE_DOWNLOAD_DIR`
- 已在 `PEERDRIVE_SHARE_DIRS` 里声明过的目录

另外 **硬链接文件会被拒**（防止绕过边界），真要放就复制一份。

---

## 2.4 查看：三种视角

### ① 面板（对端视角，也就是"别人看到的样子"）

- 连上后日志：`清单：N 合集 · M 文件`
- 列表点一下 → 预览（图片/音视频直接渲染，文本按 2 MB 截断）
- 「保存」→ 落盘到本地；任务行「取回校验」→ 按 hash 拉回并复算 sha256

> `M > 0` 才说明对端真的共享了东西。**空清单是合法结果**（对方没开共享/没声明目录），不是错误。

### ② 本机 HTTP（你自己看到的全量）

```bash
curl http://127.0.0.1:3001/files                       # 本地索引（含 provider_path）
curl http://127.0.0.1:3001/files/verify/<hash>         # 某个 hash 在不在
curl 'http://127.0.0.1:3001/files/browse?path=.'       # 按目录浏览
```

`/files` 实测片段（注意上传的和登记的都在这里）：

```json
[{"hash":"f957b195...","filename":"note.md","size":12,
  "provider_path":"D:\\...\\shared\\note.md"},
 {"hash":"b71c3590...","filename":"upload-me.txt","size":22,
  "provider_path":"b7/b71c3590..."}]
```

### ③ 从另一个节点问（最像真实使用场景）

```bash
curl http://127.0.0.1:3002/peerjs/nodes/my-node-1/shares
```

实测（登记前 → 登记后）：

```json
// 登记前
{"collections":[],"dirs":["D:\\...\\downloads\\shared"],"files":[],"peer":"my-node-1","total":0}
// 登记后
{"collections":[],"dirs":["D:\\...\\downloads\\shared"],
 "files":[{"hash":"f957b195...","name":"note.md","path":"note.md","size":12},
          {"hash":"c787d82a...","name":"demo.txt","path":"demo.txt","size":22}],
 "peer":"my-node-1","total":2}
```

---

## 2.5 为什么"入库成功了，清单里却没有"

这不是 bug，是两个索引的职责不同（`back/internal/service/nodeshare.go`）：

- 共享清单只列 **本地文件索引里 `Path` 落在共享目录内** 的条目；
- 而 `upload` / 面板「本地入库」写的是**内容库**（`file_meta` + provider），
  它的 `provider_path` 是 CAS 相对路径（如 `b7/b71c...`），**不在任何共享目录里** ⇒ 筛掉。

实测对照（同一批操作）：

| 动作 | `/files` 看得到 | 对端 `shares` 看得到 |
|---|---|---|
| HTTP 上传 `upload-me.txt`（22 B） | ✅ | ❌ |
| 共享目录里放 `demo.txt` + `register_folder` | ✅ | ✅ |

### 想让它出现在清单里，三条路

1. **把文件放进共享目录，再 `register_folder`**（推荐，§2.3）
2. **共享目录就是你的工作目录**：把 `PEERDRIVE_SHARE_DIRS` 指到你本来就放文件的地方，
   登记一次之后，往里加文件就自动在清单里（新文件仍需再登记一次索引）
3. **不进清单也行**：只想要"存进去、随时取回"，就用 hash 取回 —— 面板「取回校验」、
   或管理台按 hash 下载。内容一直在，只是不对外广播

> 反过来也成立：**把 `PEERDRIVE_SHARE_DIRS` 设成 storage 根目录不会让入库内容变可见** ——
> 因为筛的是索引里的 `Path`，而入库内容没有落盘到某个"目录"里的路径记录。

---

## 2.6 限制与默认值速查

| 项 | 值 | 出处 |
|---|---|---|
| HTTP 上传（匿名） | 10 MB | `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` |
| HTTP 上传（认证） | 100 MB | `PEERDRIVE_MAX_UPLOAD_BYTES` |
| 面板入库提醒阈值 | 256 MB | `panel/app.js` `PUT_WARN_BYTES` |
| 面板分片 / 每轮超时 | 64 KB 串行 / 30 s | 同上 |
| 预览上限 | 2 MB | `PREVIEW_MAX_BYTES` |
| 共享清单单次上限 | 1000 个文件 | `NodeShare.SetFileLister` |
| 共享开关 | 默认 **关** | `PEERDRIVE_SHARE_ENABLE` |
| 共享目录 | 默认空 | `PEERDRIVE_SHARE_DIRS`（逗号分隔） |

---

## 2.7 自查清单

1. 节点活着：`curl http://127.0.0.1:3001/ping` → `pong`
2. 内容进去了：`curl http://127.0.0.1:3001/files/verify/<hash>` → 有 `filename`/`size`
3. 落盘了：`<storage>/<hash前2>/<hash>` 存在
4. 共享开着：`/peerjs/nodes/<你的id>/shares` 的 `dirs` 非空（说明 `SHARE_DIRS` 生效）
5. 清单里有它：同一个响应的 `files` 里能找到那个 `name`
6. 面板能取回：点「取回校验」→ `sha256 与入库返回值一致`

---

## 2.8 症状 → 原因 → 处置

| 症状 | 原因 | 处置 |
|---|---|---|
| 入库成功，但清单里没有 | 入库写内容库，共享清单只看共享目录内已登记的索引 | §2.5：放共享目录 + `register_folder` |
| 清单 `total:0`，`dirs` 也是空 | `PEERDRIVE_SHARE_ENABLE=false` 或 `SHARE_DIRS` 没设 | 两个都要设，改完重启节点 |
| `path outside storage root` | 登记的路径不在 storage / download / 已声明共享目录里 | 把目录加进 `PEERDRIVE_SHARE_DIRS` 再登记 |
| 上传返回 413 / 连接被重置 | 超过匿名 10 MB | 调 `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` |
| `already_exists: true` | 相同内容已经存过（内容寻址，不是失败） | 直接用返回的 hash |
| `psk: 本节点需要预共享密钥`（面板入库时） | 面板没填 / 填错 PSK | 面板 PSK 框填上节点那把；**HTTP 接口不受 PSK 影响**（它不走 WebRTC） |
| 入库到一半超时 | 单轮 30 s 无应答（对端在落盘/卡住） | 重试；大于 256 MB 先别走面板 |
| 硬链接文件登记被拒 | 防绕过边界的硬链接检查 | 复制一份再登记 |

> 下一步：[第三章：共享级别 —— public / unlisted / private](03-share-levels.md)
> （放进来的东西，将来给谁看）；再往后是
> [第四章：自由选择共享什么](04-choose-what-to-share.md)（逐行勾选 + 设级别）。
