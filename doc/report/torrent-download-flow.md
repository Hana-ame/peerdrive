# Torrent 下载流程分析

> 分析日期：2026-05-01

## 1. 前端触发 — BTController.jsx

路由：`/bt/controller`

用户可以通过三种方式发起下载：

| 输入类型 | 前端处理 | API 调用 |
|---------|---------|---------|
| 磁力链接 (`magnet:?...`) | 直接发送 | `POST /bt/magnet` |
| .torrent URL (`http(s)://...`) | fetch 获取后包装为 File | `POST /bt/torrent` (multipart) |
| 40位 info hash | 包装为 `magnet:?xt=urn:btih:<hash>` | `POST /bt/magnet` |
| 本地 .torrent 文件 | FileReader 读取 | `POST /bt/torrent` (multipart) |

前端每 2 秒轮询 `GET /bt/downloads` 刷新下载进度。

## 2. 后端路由 — router.go

```
POST   /bt/magnet              -> BTMagnetResolve
POST   /bt/torrent             -> BTTorrentUpload
GET    /bt/downloads           -> BTDownloadList
GET    /bt/download/:ih        -> BTDownloadProgress
POST   /bt/download/:ih/pause  -> BTPauseDownload
POST   /bt/download/:ih/resume -> BTResumeDownload
POST   /bt/download/:ih/seed   -> BTSeedTorrent
POST   /bt/download/:ih/unseed -> BTStopSeed
DELETE /bt/download/:ih        -> BTRemoveDownload
```

服务初始化时：
- 创建 `BTDHTService`（Mainline DHT，bootstrap 节点 `router.bittorrent.com:6881`, `dht.transmissionbt.com:6881`）
- 创建 `BTClient`，下载目录为 `DownloadDir`
- 将 DHT 关联到 BTClient 用于 peer 发现
- 注册 `OnComplete` 回调：计算 SHA256 → 写入 SQLite → 复制到内容寻址存储 `{StorageDir}/{sha256[:2]}/{sha256}`

## 3. 核心下载引擎 — client.go

`BTClient` 维护 `map[string]*BTDownload` 管理所有活跃下载。

### AddTorrent / AddMagnet

1. 去重检查（相同 infohash 不重复添加）
2. 创建 `BTDownload` 结构体，状态 `"downloading"`
3. 创建数据目录 `{dataDir}/{infohash}`
4. 启动 `go downloadTorrent(dl)` goroutine

### downloadTorrent 执行流程

**Step 1 — DHT 宣告**
```
globalDHT.Announce(infoHash)  // 向 Mainline DHT 宣告自己持有此 infohash
```

**Step 2 — Peer 发现（双路径并行）**

- **DHT 路径**：`globalDHT.FindProviders(infoHash)` → AnnounceTraversal，15s 超时收集结果
- **Tracker 路径**：`discoverPeersFromTrackers()` → BEP 3 HTTP announce，compact peer 解析
- 两条路径的结果去重合并

**Step 3 — Metadata 获取（仅磁力链接）**

尝试 `fetchMetadata()` 获取 `.torrent` 元数据（piece 哈希、文件列表）。
**当前状态：BEP 9 / ut_metadata 未实现。** 纯磁力链接若无 tracker 会在此步失败。

**Step 4 — Piece 下载（并发度 4）**

```go
for pieceIdx := 0; pieceIdx < numPieces; pieceIdx++ {
    go func() {
        peerAddr := peerAddrs[pieceIdx % len(peerAddrs)]  // 轮询分配 peer
        data, err := DownloadPiece(ctx, peerAddr, infoHash, pieceIdx, pieceLen, expectedHash)
        // 通过 progressCh 报告进度
    }()
}
```

**Step 5 — 文件重组**

- 单文件：所有 piece 拼接为一个文件
- 多文件：拼接后按 `TorrentMeta.Files` 边界切分
- 计算每个文件的 SHA256，存入 `CompletedFile`

**Step 6 — 完成回调**

- 状态 → `"completed"`
- 关闭 `DoneCh`
- 触发 `onComplete`：写入 SQLite → 注册 provider → 复制到内容寻址存储

## 4. BT 线协议 — piece.go

`DownloadPiece()` 与单个 peer 交互：

1. **TCP 连接** — 10s 超时
2. **握手** — 68 字节：协议标识 "BitTorrent protocol" + 8 字节保留 + 20 字节 infohash + 20 字节 peer ID (`-PD0001-...`)
3. **等待 unchoke** — 处理 bitfield / have 消息
4. **发送 interested**
5. **请求块** — 每块 16 KiB，逐个请求 → 接收 msgPiece → 拼装
6. **SHA1 校验** — 验证完整 piece 的 SHA1 是否与预期一致

## 5. 辅助模块

| 模块 | 文件 | 功能 |
|------|------|------|
| 磁力链接解析 | magnet.go | 解析 `magnet:` URI，提取 infohash/tracker/显示名 |
| Torrent 解析 | torrent.go | Bencode 解码 .torrent 文件 |
| DHT 服务 | bt_dht.go | Mainline DHT announce 和 peer 发现 |
| HTTP Tracker | tracker.go | BEP 3 tracker announce |
| Seeder | seeder.go | TCP 服务器，响应 BT 线协议 piece 请求 |
| DHT Bridge | bt_bridge.go | HTTP over DHT 替代文件获取路径 |
| BEP 44 | bep44.go | DHT 上存储任意数据（不可变 + 可变） |
| BEP 51 | bep51.go | DHT infohash 采样爬取 |

## 6. 已知限制

- **磁力链接 metadata 获取（BEP 9 / ut_metadata）未实现**。无 tracker 的纯磁力链接会下载失败，需提供 .torrent 文件或在 magnet URI 中附带 tracker。
- **不支持断点续传**。暂停/恢复后重新发现 peer 并从头下载。
- **无 piece 优先级策略**。仅做简单的 round-robin peer 分配。
- **无选择性下载**。总是下载全部文件。
