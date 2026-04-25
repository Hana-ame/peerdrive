# 文件注册层分析

## 涉及文件

```
internal/controller/file.go    — HTTP 入口 /files/register_local, /files/register_folder
internal/model/file.go         — FileMeta + FileProvider
internal/repository/file_repo.go — file_meta + file_providers INSERT
internal/repository/db.go      — schema
```

## 请求生命周期

### 注册单个文件

```
POST /files/register_local
  body: {"path": "relative/path", "filename": "display_name"}
        │
        ▼
controller.RegisterLocalFile
        │
        ├─ 1. os.Open(storageDir + req.Path)
        ├─ 2. SHA256 → hash
        ├─ 3. 检测 gzip 魔数
        ├─ 4. repository.InsertFile(hash, "local", req.Path, req.Filename, metadata, available=true)
        │      └─ 不检查 hash 是否已存在，直接插入新记录
        │      └─ 不复制文件，直接引用已有路径
        └─ 返回 {"hash": "...", "filename": "..."}
```

### 批量注册文件夹

```
POST /files/register_folder
  body: {"folder_path": "subdir"}
        │
        ▼
controller.RegisterFolder
        │
        ├─ os.ReadDir(storageDir + folder_path)
        ├─ 遍历每个文件:
        │    ├─ SHA256 → hash
        │    ├─ 检测 gzip
        │    └─ repository.InsertFile(hash, "local", relPath, 文件名, metadata, true)
        │
        └─ 返回 {"registered": [{"filename":"...","hash":"..."}, ...]}
```

## 注册 vs 上传

| 特性 | 上传 (upload) | 注册 (register) |
|------|--------------|-----------------|
| 文件来源 | HTTP multipart | 磁盘已有文件 |
| 是否复制 | 复制到 storage/{h[:2]}/{h} | 不复制，直接引用原路径 |
| hash 已存在 | 允许，新记录可用标记 1 | 允许，同位置不同记录 |
| 典型用途 | 客户端上传 | 服务端预导入或同步 |

## 设计决策

| 决策 | 方案 | 原因 |
|------|------|------|
| 不复制 | 仅写 DB，不写文件 | 避免重复存储，适合预导入场景 |
| 不检查唯一性 | 直接 INSERT，不查 hash | 同一文件可从不同位置注册多份 |
| 批量注册 | 遍历文件夹逐个注册 | 批量导入时减少请求次数 |
