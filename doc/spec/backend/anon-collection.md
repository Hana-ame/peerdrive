# 匿名合集层 (Anonymous Collection Layer)

## 概述
匿名合集是一个不可变的、内容寻址的 JSON 文件，包含一组 `path → hash` 映射关系。其唯一标识符（hash）由规范 JSON 的 SHA256 决定，因此相同内容始终生成相同的 hash。

## 合集结构

```json
{
  "version": 1,
  "friendly_name": "my-collection",
  "entries": [
    {"path": "dir/file.txt", "hash": "a1b2c3..."},
    {"path": "dir/other.txt", "hash": "d4e5f6..."}
  ],
  "created_at": "2026-04-25T12:00:00Z"
}
```

## API

### POST /anon/collections

创建匿名合集。

**请求体：**
```json
{
  "friendly_name": "my-collection",
  "entries": [
    {"path": "relative/path", "hash": "64-hex-chars"}
  ]
}
```

**校验规则：**
- 所有 `path` 必须是相对路径，不能以 `/` 开头，不能包含 `..`
- 所有 `hash` 必须是 64 位小写十六进制字符串
- 条目会在服务端按 `path` 字典序排序（保证确定性）

**成功响应（201 Created）：**
```json
{"hash": "sha256-of-the-json"}
```

**错误响应（400 Bad Request）：**
```json
{"error": "invalid path: ../etc/passwd"}
```

### GET /anon/collections/:hash

获取已创建的合集 JSON 内容。

**成功响应（200 OK）：**
```json
{
  "version": 1,
  "entries": [...],
  "created_at": "2026-04-25T12:00:00Z"
}
```

**错误响应（404 Not Found）：**
```json
{"error": "collection not found"}
```

### GET /anon/collections/:hash/entries/*path

从匿名合集中下载指定路径的文件。

**参数：**
- `:hash` — 合集 hash
- `*path` — 文件在合集 entries 中的路径

**成功响应（200 OK）：**
文件内容流，附带 `Content-Disposition` 头。

**错误响应：**
- 合集不存在 → 404
- 路径在合集中不存在 → 404
- 文件数据不可用 → 404

### POST /anon/collections/fork

基于已有合集创建一个新合集，可增删条目。

**请求体：**
```json
{
  "source_hash": "sha256-of-source-collection",
  "add_entries": [{"path": "...", "hash": "..."}],
  "remove_paths": ["path/to/remove"]
}
```

**成功响应（201 Created）：**
```json
{"hash": "sha256-of-the-new-collection"}
```

## 执行流程

### 创建流程
1. 校验每个 entry 的 `path` 和 `hash` 格式
2. 按 `path` 字典序排序 entries
3. 构建 `AnonCollection` 对象（version=1, created_at=当前 UTC 时间）
4. `json.MarshalIndent` 序列化为规范 JSON
5. 计算 SHA256 -> hash
6. 写文件到 `storage/{hash[:2]}/{hash}`
7. 注册 `file_meta`（type=anon_collection, mime=application/json）
8. 注册 `file_providers`（provider_type=local, path=相对路径）
9. 返回 hash

### 读取流程
1. 按 hash 从 `storage/{hash[:2]}/{hash}` 读取文件
2. `json.Unmarshal` 解析为 `AnonCollection`
3. 校验 `version == 1`
4. 返回解析后的 JSON

### 下载文件流程
1. 读取合集 JSON
2. 匹配 entries 中的 `path`
3. 通过 `Downloader.GetFileStream(entry.Hash)` 获取文件流
4. 流式返回给客户端

## 响应头
- 通过 `GET /sha256sum/:hash` 下载合集 JSON 时，若 `file_meta.type == anon_collection`，响应头增加 `X-Peerdrive-Collection: true`

## 与下载层的关系
- 合集 JSON 本身是一个 SHA256 寻址的普通文件，可通过 `/sha256sum/:hash` 直接下载
- 合集内文件的下载最终依赖 `/sha256sum/:hash` 相同的代码路径（复用 P2P 回退、多副本重试等）

## 涉及文件

```
internal/model/anon.go             — AnonCollection / AnonCollectionEntry
internal/service/anon_service.go   — 校验、排序、创建、读取
internal/controller/anon.go        — HTTP 入口
internal/repository/anon_repo.go   — 底层存储
internal/controller/download.go    — X-Peerdrive-Collection 响应头
```

## 测试

执行 `test_anon_collection.sh` 验证：
- 创建合集（201）
- 路径遍历拦截（400）
- 合集 JSON 读取（version=1）
- 通过 sha256sum 下载合集
- 从合集条目中下载文件
- 复刻合集并移除条目
