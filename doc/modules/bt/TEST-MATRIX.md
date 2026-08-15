# Peerdrive 测试矩阵

> 2026-04-28 · 覆盖全部功能点的测试逻辑与预期行为

---

## 1. 文件系统

### 1.1 文件上传

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| F-01 | 上传小文件 | `curl -F "file=@test.txt" /files/upload` | 200, 返回 hash+size+mime | 检查 hash 为 64 位 hex |
| F-02 | 上传无文件 | POST /files/upload 不传 file | 400 "file is required" | HTTP 400 |
| F-03 | 上传大文件 (超限) | 上传 >100MB 文件 | 413/400 拒绝 | 认证用户 100MB, 匿名 10MB |
| F-04 | 重复上传同内容 | 上传相同文件两次 | 200, 相同 hash, already_exists=true | hash 一致 |
| F-05 | 上传带特殊字符文件名 | `file=@/tmp/你好 世界.txt` | 200, filename 保留原字符 | URL 编码正确处理 |

### 1.2 本地文件注册

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| F-06 | 注册已存在文件 | POST /files/register_local {path:"/etc/hostname"} | 200, 返回 hash | SHA256 可下载 |
| F-07 | 注册不存在的路径 | {path:"/nonexistent/file"} | 400/500 错误 | 错误信息明确 |
| F-08 | 注册带自定义文件名 | {path:"/etc/hostname", filename:"my.txt"} | filename="my.txt" | 自定义名生效 |
| F-09 | 重复注册同文件 | 注册两次同一路径 | 相同 hash, 不重复插入 DB | file_meta 只有一条 |
| F-10 | 路径穿越防护 | {path:"../etc/passwd"} | 400 拒绝 | 不允许 ../ |

### 1.3 文件夹注册

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| F-11 | 注册非空文件夹 | {folder_path:"/etc/ssl"} | 200, registered 数组非空 | count > 0 |
| F-12 | 注册空文件夹 | {folder_path:"/tmp/empty"} | 200, count=0 | 不报错 |
| F-13 | 注册不存在的文件夹 | {folder_path:"/nonexistent"} | 400 错误 | 错误信息 |
| F-14 | 部分已注册 | 先注册一个文件，再注册整个文件夹 | 仅注册新文件 | count 少于总文件数 |

### 1.4 SHA256 下载

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| F-15 | 下载已存在文件 | GET /sha256sum/:hash | 200, 返回文件内容 | sha256sum 验证一致 |
| F-16 | 下载不存在的 hash | GET /sha256sum/000...000 | 404 | 错误信息 |
| F-17 | Range 分块下载 | Range: bytes=0-99 | 206, Content-Range 头 | 返回 100 bytes |
| F-18 | Range 超出范围 | Range: bytes=999999- | 206, 返回末尾部分 | 正确处理边界 |
| F-19 | 无效 hash 格式 | GET /sha256sum/abc | 400 "invalid sha256" | 不崩溃 |

### 1.5 CID/IPFS 下载

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| F-20 | CID 下载 | GET /ipfs/bafkrei... | 200, X-CID 头 | 与 SHA256 下载相同内容 |
| F-21 | 无效 CID | GET /ipfs/invalid | 404 | 错误信息 |
| F-22 | IPFS 网关拉取 | 本地无文件, 开启 IPFS 网关 | 从 ipfs.io 拉取 | 自动缓存到本地 |

### 1.6 文件删除

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| F-23 | 删除已存在文件 | DELETE /files/:hash | 200 | 之后 GET /sha256sum/:hash → 404 |
| F-24 | 删除不存在的 hash | DELETE /files/000...000 | 200 (不报错) | 幂等操作 |
| F-25 | 物理文件不删除 | 删除后检查 storage | 文件仍在磁盘 | 只删 DB 记录 |

---

## 2. 合集系统

### 2.1 匿名合集创建

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| C-01 | 创建合集 | POST /anon/collections {entries:[{path,hash}], friendly_name, tags} | 200, 返回 hash | 64 位 hex |
| C-02 | 创建空条目合集 | entries:[] | 400 拒绝 | 至少一个条目 |
| C-03 | 创建无效 hash | entries:[{path:"a", hash:"xxx"}] | 400 拒绝 | hash 非 64 位 hex |
| C-04 | 路径穿越 | entries:[{path:"../etc", hash:valid}] | 400 拒绝 | 不允许 ../ |
| C-05 | 无名称 (AI 推荐) | friendly_name 留空 | 弹窗→AI推荐/留空 | LLM 返回名称 |
| C-06 | 带标签 | tags:["test","p2p"] | 200, tags 保存在合集 | GET 合集确认 tags |

### 2.2 合集查看

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| C-07 | 查看已存在合集 | GET /anon/collections/:hash | 200, 返回 entries/friendly_name/tags | entries 非空 |
| C-08 | 查看不存在的合集 | GET /anon/collections/000...000 | 404 | 错误信息 |
| C-09 | 空合集自动删除 | 查看 entry_count=0 的合集 | 自动 deleteFile | 返回 "空合集，已自动删除" |
| C-10 | 单文件合集预览 | 查看 1 个文件的合集 | 图片→img, PDF→iframe, 文本→pre | 预览渲染正确 |
| C-11 | 嵌套合集链接 | 合集包含另一合集的 hash | 显示 📦 合集链接 | 点击跳转到嵌套合集 |

### 2.3 合集列表

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| C-12 | 列出合集 | GET /anon/collections | 200, 返回数组 | 包含 hash/friendly_name/entry_count |
| C-13 | 空列表 | 无合集时 | 返回 [] 或 dummy 合集 | 不报错 |
| C-14 | 合集名优先级 | friendly_name→name_preview→hash→"未命名" | 正确的 fallback 链 | entry_count=0 时显示 "0 个文件" |

### 2.4 合集操作

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| C-15 | Fork 合集 | POST /anon/collections/fork {source_hash, ...} | 200, 新 hash | 新合集包含源条目+新增条目 |
| C-16 | 版本提交 | POST /collections/:u/:c/commit {message} | 200, version_number++ | 版本日志新增一条 |
| C-17 | 版本回滚 | POST /collections/:u/:c/rollback/:vid | 200, 工作区恢复到指定版本 | 条目恢复 |
| C-18 | 版本历史 | GET /collections/:u/:c/log | 200, 返回版本列表 | 按时间倒序 |

---

## 3. P2P 网络

### 3.1 libp2p 基础

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| P-01 | P2P 启动 | 启动 Peerdrive | enabled=true, peer_id 非空 | GET /p2p/status |
| P-02 | P2P 禁用 | PEERDRIVE_P2P_ENABLE=false | enabled=false, 其他功能正常 | HTTP 服务仍可用 |
| P-03 | Node 信息 | GET /p2p/node | 返回 peer_id + addrs | addrs 非空 |
| P-04 | Ping | GET /p2p/ping/:peer_id | 返回 rtt | < 10ms (同机) |
| P-05 | mDNS 发现 | 两节点同局域网 | discovered > 0 | GET /p2p/discovered |
| P-06 | 手动连接 | POST /p2p/connect {addr} | status:"connected" | peers 列表包含对方 |

### 3.2 Exchange 协议

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| P-07 | 文件请求 | POST /p2p/request-file {hash, peer_ids} | responses > 0, size 正确 | 返回的数据 hash 一致 |
| P-08 | 跨节点下载 | 节点 A 上传, 节点 B 通过 P2P 下载 | 200, 内容一致 | sha256sum 相同 |
| P-09 | 合集同步 | A 创建合集, B sync | synced > 0 | B 可通过 SHA256 访问 synced 文件 |

### 3.3 DHT

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| P-10 | IPFS Announce | POST /p2p/announce {hash} | status:"announced" | DHT Provide 成功 |
| P-11 | IPFS Find | 宣告后查找 | 在 bootstrap 环境应找到 | FindProviders 返回 peer |
| P-12 | 单节点 (孤立) | 无 bootstrap, Announce | 200 + warning | 不报 500, 返回 "announced locally" |

---

## 4. BitTorrent

### 4.1 BT DHT

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| B-01 | BT DHT 启动 | PEERDRIVE_BT_DHT_ENABLE=true | num_nodes > 0 | GET /bt/status |
| B-02 | BT DHT 禁用 | PEERDRIVE_BT_DHT_ENABLE=false | enabled:false | 其他功能正常 |
| B-03 | BT Announce | POST /bt/announce {hash} | status:"announced on BT DHT" | 64 位 hex hash |
| B-04 | BT Find (跨节点) | A announce, B find | count >= 1 | B 找到 A 宣告的 peer |
| B-05 | BT Find (自查找) | 查找自己宣告的 hash | count >= 0 | 不报错 |
| B-06 | 无效 hash (BT) | 40 字符 hash | 正确处理 | 接受 40 位 infohash |

### 4.2 Torrent/Magnet

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| B-07 | 解析 .torrent | ParseTorrent(bencode) | 返回 name/pieces/size/infohash | infohash 20 字节 |
| B-08 | 解析 Magnet | ParseMagnet("magnet:?xt=urn:btih:...") | 返回 infohash/name/trackers | 支持 hex 和 base32 |
| B-09 | 上传 .torrent | POST /bt/torrent (multipart) | 200, 返回 files/infohash/status | status:"downloading" |
| B-10 | 添加 Magnet | POST /bt/magnet {uri} | 200, 同上 | infohash 正确 |

### 4.3 Wire Protocol

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| B-11 | Handshake | TCP 连接 → 发送 handshake | 双方交换 infohash | 协议 "BitTorrent protocol" |
| B-12 | Piece 下载 | 从 seeder 下载单个 piece | SHA1 验证通过 | 数���正确 |
| B-13 | 多 Piece 下载 | 下载多个 piece | 全部 SHA1 验证通过 | 文件重组后 SHA256 正确 |

### 4.4 BEP 标准

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| B-14 | BEP 44 Put | 存储 immutable 数据 | 返回 target hash | 数据存入 DHT |
| B-15 | BEP 44 Get | 获取已存储数据 | 返回原数据 | base64 编码一致 |
| B-16 | BEP 51 Sample | GET /bt/bep51/sample | 返回 samples 数组 | 每个 40 位 hex |

---

## 5. Dual-Stack

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| D-01 | Dual Announce | POST /p2p/dual/announce {hash} | IPFS+BT 均成功 | status:"announced on both networks" |
| D-02 | Dual Find | POST /p2p/dual/find {hash} | 返回 ipfs_peers + bt_peers | 两个字段均存在 |
| D-03 | 单网禁用 | BT 禁用时 Dual announce | IPFS 成功, BT skip | 不报错 |

---

## 6. 注册与认证

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| R-01 | 用户注册 | POST /auth/register | 201, 返回 token | token 可解码 |
| R-02 | 用户登录 | POST /auth/login | 200, 返回 JWT | JWT 包含 username+role |
| R-03 | Token 验证 | GET /auth/whoami (Bearer) | 200, 返回 username+role | 正确识别 |
| R-04 | 无效 Token | 错误/过期 token | 401 | 错误信息 |
| R-05 | Relay 注册 | POST /p2p/relay/register | 200 | relay 列表中出现 |
| R-06 | Relay 心跳 | POST /p2p/relay/heartbeat | 200 | last_heartbeat 更新 |
| R-07 | Relay 列表 | GET /p2p/relay/list | 200, 返回活跃 relay | 5 分钟内有心跳 |
| R-08 | endpoint#token | API URL 包含 #token | 提取 token, 用于 Authorization | 前端自动解析 |

---

## 7. 前端

### 7.1 页面加载

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| UI-01 | Plaza 首页 | 打开 / | 显示合集卡片/搜索栏/标签页 | Playwright |
| UI-02 | AnonCreator | 打开 /anon/create | 4-tab 平铺, 时间线默认 | 可见时间线/已注册/本机/合集 |
| UI-03 | FileManager | 打开 /files | 文件列表+复选框+排序 | 复选框可见, 排序按钮有效 |
| UI-04 | P2P 面板 | 打开 /ipfs, /bt, /p2p | 各面板正确渲染 | Playwright |
| UI-05 | BT 控制器 | 打开 /bt/controller | 磁力输入+下载列表+统计栏 | 刷新按钮有效 |
| UI-06 | Settings | 打开 /settings | IPFS/BT/WebDAV/LLM 配置节 | 设置持久化 |
| UI-07 | PWA | 移动端打开 | manifest + service worker | 可添加到主屏幕 |

### 7.2 交互

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| UI-08 | 粘贴 SHA256 导航 | Plaza 搜索栏粘贴 64 位 hash | 自动导航到合集页 | URL 变为 /anon/collections/:hash |
| UI-09 | 拖拽文件 | AnonCreator 拖拽文件到编辑区 | 添加条目到 FileTree | 条目可见 |
| UI-10 | 复选框多选 | FileManager 勾选多个文件 | 显示 "已选择 N 个文件" | 创建合集按钮可用 |
| UI-11 | 分享输入框 | 点击分享 | 显示输入框(非 alert) | 可复制链接 |
| UI-12 | 天线 | 切换到天线 tab | 拉取 P2P 发现 | 合集卡片出现 |
| UI-13 | LLM 上下文 | 切换页面后使用 LLM | LLM 知道当前页面 | 回复相关 |

### 7.3 错误处理

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| UI-14 | 无节点横幅 | 未配置 API, 打开 BT 控制器 | 显示 "未连接到本地节点" 横幅 | 橙色警告 |
| UI-15 | 错误 tooltip | BT 任务错误, 鼠标悬停 | 显示错误详情 | title 属性 |
| UI-16 | 空合集隐藏 | Plaza 空合集 (0 entries) | 不显示或标记 | 至少不是 "未命名合集" |

---

## 8. 部署与运维

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| O-01 | 单二进制编译 | go build ./cmd/server/ | 51MB 二进制 | file peerdrive-server |
| O-02 | 跨平台编译 | GOOS=windows/darwin/linux | 各平台均成功 | CI matrix |
| O-03 | VPS relay 启动 | systemctl start peerdrive-relay | active, P2P online | curl /ping |
| O-04 | CF Tunnel | curl wsl-3000.moonchan.xyz/ping | pong | TLS 连接 |
| O-05 | CF Pages | curl peerdrive.pages.dev | HTTP 200 | 前端可访问 |
| O-06 | Docker compose up | docker compose up -d | 5 容器 running | relay healthcheck pass |
| O-07 | 内存占用 | 长期运行后 | < 100MB (Go 进程) | ps aux RSS |
