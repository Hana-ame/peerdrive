# 文件注册层 (Register Layer)

## 涉及文件

```
internal/controller/file.go      — HTTP 入口 RegisterLocalFile, RegisterFolder
internal/service/file_service.go — 业务逻辑：哈希计算、元数据提取、数据库写入
internal/model/file.go           — FileMeta + FileProvider
internal/repository/file_repo.go — file_meta + file_providers INSERT / GET
internal/repository/db.go        — schema
internal/provider/local.go       — 文件读取（绝对/相对路径自适应）
internal/config/config.go        — PEERDRIVE_STORAGE_ENABLE 开关
```

## API

### POST /files/register_local
注册单个本地文件。

**请求体：**
```json
{"path": "/absolute/path/to/file", "filename": "display_name.txt"}
```

**处理流程：**
1. 检查 `StorageEnable`，关闭则返回 403。
2. 打开 `path` 指定的文件。
3. 计算 SHA256 哈希。
4. 获取文件 `Size`（通过 `os.Stat`）。
5. 检测 MIME 类型（`http.DetectContentType` 读取前 512 字节 + 扩展名回退）。
6. 写入 `file_meta`（幂等：hash 已存在则跳过）。
7. 写入 `file_providers`，`path` 为传入的绝对路径。
8. 返回 `{"hash": "...", "filename": "..."}`。

### POST /files/register_folder
批量注册文件夹内所有文件（递归）。

**请求体：**
```json
{"folder_path": "/absolute/path/to/folder"}
```

**处理流程：**
1. 检查 `StorageEnable`，关闭则返回 403。
2. 使用 `filepath.Walk` 递归遍历目录。
3. 对每个非目录文件调用 `RegisterLocal`（传入绝对路径）。
4. 返回 `{"registered": [{"filename":"...","hash":"..."}, ...]}`。

## 注册 vs 上传

| 特性 | 上传 (Upload) | 注册 (Register) |
|------|--------------|-----------------|
| 文件来源 | HTTP multipart 流 | 磁盘已有文件 |
| 是否复制 | 复制到 `storage/{h[:2]}/{h}` | 不复制，直接引用原路径 |
| hash 已存在 | 返回 `already_exists: true` (200) | 幂等，返回相同 hash |
| 路径存储 | 相对路径 `{h[:2]}/{h}` | 绝对路径 |

## 元数据写入

注册时自动计算并写入以下元数据：
- `Size`：文件大小（字节）
- `MimeType`：HTTP 内容类型检测
- `Gziped`：固定为 `false`
- `Type`：固定为 `blob`

## 配置开关

环境变量 `PEERDRIVE_STORAGE_ENABLE` 控制写操作的可用性：
- `true`（默认）：允许注册和上传
- `false`：所有写操作返回 403 Forbidden

## 设计要点

| 决策 | 方案 | 原因 |
|------|------|------|
| 不复制文件 | 仅写 DB，不写文件 | 避免重复存储，适合预导入场景 |
| 幂等插入 | hash 存在则跳过 | 重复注册不破坏已有元数据 |
| 绝对路径存储 | 写入完整路径 | 支持任意位置的文件注册 |
| 递归遍历 | 使用 filepath.Walk | 支持嵌套目录深度注册 |
