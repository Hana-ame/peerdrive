# 文件上传层分析

## 涉及文件

```
internal/controller/file.go    — HTTP 入口 /files/upload
internal/model/file.go         — FileMetadata（含 metadata JSON）
internal/repository/file_repo.go — files 表 INSERT
internal/repository/db.go      — files 表 schema
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
        └─ 7. repository.InsertFile(FileMetadata{
               Hash: hash,
               ProviderType: "local",
               Path: "{hash[:2]}/{hash}",
               Filename: 原文件名,
               Metadata: JSON 字符串,
               Available: true,
             })
             └─ type 列走 schema 默认值 'blob'
             └─ 同一 hash 可有多条记录（不同 provider/path）
        │
        └─ 返回 {"hash": "a1b2c3...", "filename": "xxx"}
```

## 数据流

```
multipart → SHA256 → storage/{h[:2]}/{h} → files INSERT
                              ↑                       ↑
                      磁盘持久化（同名覆盖）     type='blob'(默认)
                                               available=1
                                               可重复 hash
```

## 设计决策

| 决策 | 方案 | 原因 |
|------|------|------|
| 存储路径 | storage/{hash[:2]}/{hash} | 前两位分目录防单个目录文件过多 |
| 多副本 | hash 无 UNIQUE 约束，同一 hash 可有多行 | 同一内容多位置存储（本地/HTTP/P2P） |
| 可用性标记 | available=1/0 | 文件被删或 provider 不可达时置位，不影响其他副本 |
| filename 保留 | 原文件名存入 Filename 字段 | 下载时 Content-Disposition 使用原始名 |
| metadata JSON | 上传后检测 gzip 存入 metadata | 可扩展，不新增专用列 |
