# Peerdrive API 参考

Base URL: `http://localhost:3000`

所有写操作可通过 `PEERDRIVE_STORAGE_ENABLE=false` 环境变量关闭（返回 403）。

---

## 文件管理

### 上传文件
```
POST /files/upload
Content-Type: multipart/form-data
Field: file
```
**响应 201（新文件）：**
```json
{"hash":"abc123...","size":1024,"mime":"text/plain","filename":"a.txt","already_exists":false}
```
**响应 200（重复文件）：**
```json
{"hash":"abc123...","size":1024,"mime":"text/plain","filename":"a.txt","already_exists":true}
```
**响应 403：** `{"error":"storage is disabled"}`

### 注册本地文件（零拷贝）
```
POST /files/register_local
Content-Type: application/json
```
**请求体：**
```json
{"path":"/absolute/path/to/file.txt","filename":"file.txt"}
```
**响应 200：** `{"hash":"abc123...","filename":"file.txt"}`

### 注册文件夹（递归）
```
POST /files/register_folder
Content-Type: application/json
```
**请求体：**
```json
{"folder_path":"/absolute/path/to/folder"}
```
**响应 200：**
```json
{"registered":[{"filename":"a.txt","hash":"abc..."},{"filename":"b.txt","hash":"def..."}]}
```

### 验证文件元数据
```
GET /files/verify/:hash
```
**响应 200：** `{"hash":"abc...","filename":"a.txt","size":1024,"mime":"text/plain"}`

### 删除文件
```
DELETE /files/:hash
```
**响应 200：** `{"message":"deleted"}`

---

## SHA256 下载

```
GET /sha256sum/:hash
```
**响应 200：** 文件流，附带 `Content-Disposition: attachment; filename=xxx`

---

## 匿名合集

### 创建合集
```
POST /anon/collections
Content-Type: application/json
```
**请求体：**
```json
{
  "entries": [
    {"path":"docs/readme.txt","hash":"abc123..."},
    {"path":"images/logo.png","hash":"def456..."}
  ]
}
```
- path 必须是相对路径，不能含 `..`
- hash 必须为 64 位十六进制
- 服务端按 path 字典序排序

**响应 201：** `{"hash":"sha256-of-the-collection-json"}`

### 获取合集
```
GET /anon/collections/:hash
```
**响应 200：**
```json
{
  "version": 1,
  "entries": [
    {"path":"docs/readme.txt","hash":"abc123..."},
    {"path":"images/logo.png","hash":"def456..."}
  ],
  "created_at": "2026-04-25T12:00:00Z"
}
```

### 下载合集内文件
```
GET /anon/collections/:hash/entries/path/to/file
```
**响应 200：** 文件流

### 复刻合集（Fork）
```
POST /anon/collections/fork
Content-Type: application/json
```
**请求体：**
```json
{
  "source_hash":"abc123...",
  "add_entries":[{"path":"new.txt","hash":"def..."}],
  "remove_paths":["old.txt"]
}
```
**响应 201：** `{"hash":"new-collection-hash"}`

---

## 前端调用示例

```js
// 注册文件夹
const res = await fetch('/files/register_folder', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({folder_path: '/data/my-folder'})
});
const {registered} = await res.json();
// registered = [{filename:"a.txt", hash:"abc..."}, ...]

// 用注册结果创建匿名合集
const entries = registered.map(f => ({path: f.filename, hash: f.hash}));
const coll = await fetch('/anon/collections', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({entries})
});
const {hash} = await coll.json();

// 下载合集内文件
window.open(`/anon/collections/${hash}/entries/${registered[0].filename}`);
```

---

## 响应头

| 头 | 说明 |
|---|------|
| `Access-Control-Allow-Origin` | 匹配请求 Origin（动态） |
| `Access-Control-Allow-Credentials` | `true` |
| `Content-Disposition` | 下载文件时附带文件名 |
| `X-Peerdrive-Collection` | 下载合集 JSON 时设为 `true` |
