# Peerdrive Collection Logic — Full Trace

> 2026-04-28 · 检查所有细节问题

## 1. 合集创建

### 1.1 AnonCreator → POST /anon/collections

```
用户拖文件 → entries[] → 点保存 → POST /anon/collections
  body: {entries: [{path, hash}], friendly_name, tags}
  handler: controller.CreateAnonCollection
    → anonSvc.CreateCollection(name, entries, tags)
      → 验证: 拒绝空path、含../的path、非64字符hash
      → model.NewAnonCollection(name, entries, tags)
        → version=1, CreatedAt=now, 计算name_preview (entries前3个文件名)
      → JSON序列化 → SHA256 → 写入storage/<hash[:2]>/<hash>
      → 返回 {hash, name_preview, entry_count}
```

### 1.2 广播按钮 (Plaza)

```
搜索栏输入hash → 点📡广播 → 
  → api.createAnonCollection([{path:'broadcast',hash}], '广播 '+hash前缀)
  → api.dualAnnounce(hash) // IPFS + BT
  → 显示hash作为分享链接
```

### 1.3 URL注册 → 合集

```
FileManager → URL tab → 输入url → POST /files/register_url
  → fileSvc.RegisterURL(url, filename)
    → HTTP GET url → SHA256 → InsertFileMeta + InsertFileProvider(type="http")
```

## 2. 合集列表 (Plaza)

### 2.1 数据来源

```
Plaza.loadAll()
  → listAnonCollections() → GET /anon/collections
    → 遍历 storage 目录, 读取每个blob, JSON反序列化
    → 返回 [{hash, friendly_name, name_preview, entry_count, tags, created_at}]
    → 注意: 不返回 entries 数组!
  → listPublicCollections() → GET /collections/public
    → 返回有 current_hash 的public合集
```

### 2.2 展示逻辑 (CollectionCard)

```
collFileCount(c):
  → c.entry_count || (c.entries ? c.entries.length : 0)
  → list API 返回 entry_count, entries=undefined → ✅

isSingleFile = count === 1
singleFile = c.entries?.[0] || (c.name_preview ? {path: c.name_preview} : null)
  → list API: entries=undefined → fallback to name_preview ✅
  → name_preview 如果是 "hello.txt" → {path: "hello.txt"} ✅

cardIcon:
  isSingleFile && singleFile 
    ? fileIconFromPath(singleFile.path)  // 📝🎬🖼️等
    : isDummy ? '🧪' : '📦'
  → 🔴 BUG: 刷新后可能仍显示📦 → CF缓存

名称显示:
  isSingleFile && singleFile ? singleFile.path : name
  → 单文件显示文件名, 多文件显示合集名 ✅
```

### 2.3 点击跳转

```
本地合集 (有hash):
  handleDownload → navigate(`/anon/collections/${c.hash}`) ✅
  
P2P合集 (有current_hash):
  handleDownload → navigate(`/anon/collections/${c.current_hash}`) ✅
  
P2P合集 (只有username+coll):
  handleDownload → navigate(`/${username}/${collection_name}`)
  → Explorer.jsx → getCollection → navigate to AnonExplorer ✅
```

## 3. 合集查看 (AnonExplorer)

### 3.1 加载

```
GET /anon/collections/:hash
  → controller.GetAnonCollection → anonSvc.GetCollectionByHash(hash)
    → 读取 storage/<hash[:2]>/<hash> → JSON反序列化
    → 返回 {hash, friendly_name, entries: [{path, hash}], tags, created_at}
    → 注意: 返回完整的 entries 数组!
```

### 3.2 空合集处理

```
fetchCollection(hash):
  coll = await api.getAnonCollection(hash)
  if (!coll.entries || coll.entries.length === 0)
    → api.deleteFile(hash) // 自动删除空合集
    → setError('空合集，已自动删除')
```

### 3.3 单文件预览

```
isSingleFile = entries.length === 1 && !entries[0].path.includes('/')

如果单文件:
  图片: <img src={downloadUrl}> ✅
  PDF: <iframe src={downloadUrl}> ✅
  文本/代码: <TextPreview> → fetch内容 → <pre> ✅
  其他: 下载按钮 ✅
```

### 3.4 嵌套合集

```
加载时获取所有合集hash: Set(allCollHashes)
文件hash在这个set里 → 显示为📦合集链接 → 点击跳转 ✅
```

## 4. P2P 广播与发现

### 4.1 宣告

```
POST /p2p/announce → p2pSvc.AnnounceHash(hash)
  → DHT.Provide(CID) → IPFS网络
  → 失败返回200+WARN (单节点DHT孤立) ✅

POST /p2p/bt/announce → btSvc.Announce(hash)
  → SHA256 → infohash(前20字节) → DHT.Announce
  → 成功返回 "announced on BT DHT" ✅

POST /p2p/dual/announce → dualSvc.Announce(hash)
  → 并行: IPFS announce + BT announce ✅
```

### 4.2 查找

```
POST /p2p/bt/find → btSvc.FindProviders(hash)
  → DHT遍历 → get_peers → 收集IP:port
  → 跨节点测试: Node B查Node A宣告的文件 → count=1 ✅

POST /p2p/dual/find → dualSvc.FindProviders(hash)
  → 并行: IPFS find + BT find → 合并结果 ✅
```

## 5. 用户合集 (Explorer)

### 5.1 路由

```
/:username/:collection_name → Explorer.jsx
  
现在行为:
  1. getCollection(username, collName)
  2. 如果有 current_hash → redirect to /anon/collections/:hash ✅
  3. 如果没有 → 显示旧 Explorer UI (版本管理/commit/merge/sync)
```

### 5.2 🔴 问题: Explorer 额外功能丢失

```
redirect后用户无法使用:
  - Commit新版本
  - 查看版本历史
  - Fork
  - Merge合并
  - 保存到本地
  - 上传文件到合集
  
AnonExplorer没有这些功能!
```

## 6. 🔴 发现的细节问题

### 6.1 Explorer功能丢失
- Explorer 的重定向会丢失 commit/merge/sync/upload 功能
- **修复**: AnonExplorer 需要加 "版本历史" 按钮和 actions

### 6.2 单文件图标CF缓存
- 源码修复正确但CF可能缓存旧版本
- **验证**: 清除CF缓存或等max-age=0生效

### 6.3 name_preview 可能为空
- 老合集 name_preview 为 null → fallback到 "N个文件" ✅
- 但如果friendly_name也为空而entries=undefined → 显示 "未命名合集" 

### 6.4 空合集处理不一致
- AnonExplorer: 自动删除空合集 ✅
- Plaza列表: 空合集仍然显示 "0个文件"
- **修复**: Plaza也应在list时将空合集标记为可清理

### 6.5 合集命名优先级
```
CollectionCard: collection_name → friendly_name → name_preview → N个文件 → hash前缀 → '未命名合集' ✅
AnonExplorer: friendly_name → name_preview → N个文件 → '未命名合集' ✅
不一致: CollectionCard有collection_name和hash fallback, AnonExplorer没有
```

### 6.6 P2P合集在Plaza无法区分
- P2P合集显示 "⚪ 仅本地" — 错误!
- P2P合集应显示 "🔵 P2P可用"
- **根因**: `c._type === 'public'` 检查 — 如果reg server没返回public合集, 这个永远false

## 7. 修复优先级

| 优先级 | 问题 | 修复 |
|--------|------|------|
| P0 | 单文件图标 | CF部署后已修复, 验证 |
| P0 | Explorer功能丢失 | AnonExplorer加版本历史+操作按钮 |
| P1 | P2P合集标记 | 修复 _type 标记逻辑 |
| P1 | 空合集在列表 | Plaza自动隐藏或标记空合集 |
---

## 双信道架构 (2026-04-28)

Peerdrive 奉行两套通信信道，用户自由切换：

### 信道 1: 中心服务器（硬编码，默认开启）
```
用户 → Registration Server (VPS :4000)
     → 用户认证、Relay列表、Peer发现
     → 合集发布、留言板、统计
```
- 写死的地址，始终可用
- 提供可靠的服务发现
- 无 P2P 网络时也能工作

### 信道 2: P2P 自由网络（可选开启）
```
用户 → IPFS DHT / BT DHT
     → P2P 节点发现、文件交换
     → WebRTC 直连、端口转发
```
- 完全去中心化
- 用户自行选择开关
- 不依赖任何中心服务器

### 一致性设计
- 两套信道的**用户逻辑完全一致**——同一个 API、同一个 UI
- 有节点时优先 P2P，无节点时降级到中心服务器
- 合集创建、分享、下载在两个信道下体验相同
