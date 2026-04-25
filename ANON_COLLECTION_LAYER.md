# 匿名合集层分析

## 涉及文件

```
internal/controller/anon.go       — HTTP 入口 /anon/collections/:hash
internal/repository/anon_repo.go  — 确定性序列化 + 存储 + files 表注册
internal/model/anon.go            — AnonCollection / AnonEntry 结构体
internal/service/downloader.go    — 底层文件流读取（复用 sha256sum）
internal/provider/                — 内容寻址读取（local）
internal/repository/db.go         — files 表 schema（含 type 列）
```

## 请求生命周期

### 创建匿名合集

```
POST /anon/collections
  body: {"entries":[{"path":"...","hash":"...","size":123}]}
        │
        ▼
controller.CreateAnonCollection
        │  解析 JSON → model.AnonCollection
        ▼
repository.SaveCollection(coll, storageDir)
        │
        ├─ 1. sort.Slice(entries, by Path)
        │
        ├─ 2. json.Marshal(coll) → []byte（字段顺序固定）
        │
        ├─ 3. SHA256([]byte) → hashStr
        │
        ├─ 4. 写本地文件 storage/anon/{hashStr[:2]}/{hashStr}
        │
        └─ 5. INSERT INTO files (hash, provider_type, path, filename, type)
               VALUES (hashStr, 'local', 'anon/{h[:2]}/{h}', 'anon_{h}.json', 'anon_collection')
               ON CONFLICT(hash) DO NOTHING
        │
        return hashStr
        │
        ▼
返回 {"hash": "a1b2c3d4..."}
```

### 读取匿名合集

```
GET /anon/collections/a1b2c3d4...
        │
        ▼
controller.GetAnonCollection
        │  校验 :hash 参数
        ▼
repository.GetCollection(hash, storageDir)
        │
        ├─ 读取 storage/anon/{hash[:2]}/{hash}
        ├─ json.Unmarshal → model.AnonCollection
        └─ 校验 Version == 1
        │
        ▼
返回 AnonCollection JSON（同创建格式）
```

### 下载合集内文件

```
GET /anon/collections/:hash/entries/path/to/file
        │
        ▼
controller.DownloadAnonFile
        │
        ├─ 1. GetCollection(hash) → 解析 JSON
        │
        ├─ 2. 遍历 Entries 匹配 path
        │
        ├─ 3. 策略判断：
        │   ├─ 本地有（files 表存在且 provider_type='local'）
        │   │   └─ downloader.GetFileStream(hash) → 200 stream
        │   ├─ 有 URL（entry.URL != nil）
        │   │   └─ 302 Redirect → *entry.URL
        │   └─ 本地无 + 无 URL
        │       └─ downloader.GetFileStream(hash) → P2P 回退或 404
        │
        ▼
返回文件流或 302/404
```

## 数据流方向

```
创建:  JSON → sort(entries) → json.Marshal → SHA256 → storage/anon/{hash[:2]}/{hash} → files 表
        ↑                                                                                 ↑
  用户 POST 请求                                                              type='anon_collection'

读取:  hash → storage/anon/{hash[:2]}/{hash} → json.Unmarshal → AnonCollection JSON
                                                        ↓
                                             返回给客户端（含 entries 列表）

下载:  path → 匹配 entry.hash → files 表查询 → local provider → 文件流
                                  ↓
                           storage/{hash[:2]}/{hash}
```

## 与 sha256sum 层的关系

```
匿名合集 JSON 本身也是一个 SHA256 寻址的文件：
  hash = SHA256(canonical JSON) → 存入 files 表（type='anon_collection'）
  → 可通过 /sha256sum/:hash 直接下载原始 JSON
  → 也可通过 /anon/collections/:hash 获取解析后的结构体

合集内文件的下载最终依赖 sha256sum 层：
  DownloadAnonFile → GetFileStream(entry.Hash) → /sha256sum/:hash 相同的代码路径
```

## 设计决策

| 决策 | 方案 | 原因 |
|------|------|------|
| 合集标识 | SHA256(canonical JSON) | 内容寻址，天然防冲突，不可篡改 |
| JSON 规范性 | entries 按 Path 排序后序列化 | 保证相同内容产出相同 hash |
| 存储路径 | storage/anon/{hash[:2]}/{hash} | 与普通文件隔离，方便策略管理 |
| 幂等创建 | ON CONFLICT DO NOTHING | 相同合集重复创建返回相同 hash |
| files 表复用 | type='anon_collection' 区分 | 与 blob 文件共用同一套寻址/下载/缓存机制 |
| URL 下载策略 | 有 URL 则 302 重定向 | 节省本地存储，利用原始 CDN/HTTP 源 |
| 本地兜底 | 有本地缓存则直接提供 | 优先本地，避免额外网络请求 |
| P2P 回退 | 走 downloader.GetFileStream | 复用 sha256sum 层的 P2P 能力 |
