# 文件上传层分析

## 涉及文件

```
internal/controller/file.go    — HTTP 入口 /files/upload
internal/model/file.go         — FileMeta + FileProvider
internal/repository/file_repo.go — file_meta + file_providers INSERT
internal/repository/db.go      — schema
```

## 请求生命周期

```
POST /files/upload (multipart/form-data)
        │
        ▼
controller.UploadFile
        │
        ├─ 1. Request.FormFile("file")
        │
        ├─ 2. SHA256 计算文件内容 → hash(64 hex)
        │
        ├─ 3. file.Seek(0, 0)
        │
        ├─ 4. 写文件到 storage/{hash[:2]}/{hash}
        │
        ├─ 5. 检测 gzip 魔数 (isGzipFile)
        │
        ├─ 6. 构建 metadata JSON
        │      └─ {"is_gzip": true/false}
        │
        └─ 7. INSERT file_meta (hash, gziped, filename, type=blob)
               INSERT file_providers (hash, 'local', path)
               └─ 同一 hash 可多条 provider 记录
        │
        └─ 返回 {"hash": "a1b2c3...", "filename": "xxx"}
```

## 数据流

```
multipart → SHA256 → storage/{h[:2]}/{h} → INSERT file_meta + file_providers
                              ↑                       ↑
                      磁盘持久化（同名覆盖）     file_meta(type=blob)
```

## 设计决策

| 决策 | 方案 | 原因 |
|------|------|------|
| 存储路径 | storage/{hash[:2]}/{hash} | 前两位分目录防单个目录文件过多 |
| 多副本 | file_providers 允许多行同一 hash | 同一内容多位置存储（本地/HTTP/P2P） |
| filename 保留 | 原文件名存入 FileMeta.Filename | 下载时 Content-Disposition 使用原始名 |
| 新增属性 | file_meta 已有专用列（gziped、mime_type、size） | 新增属性直接加列或使用现有列 |
