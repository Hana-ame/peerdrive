# 上传层 (Upload Layer)

## 概述
上传功能通过 `POST /files/upload` 接口实现，将文件存入内容寻址存储系统。  
文件以 SHA256 哈希命名，存储在配置的存储目录下，并自动注册元数据和提供者信息。

## 配置
- `PEERDRIVE_STORAGE`：存储根目录，默认 `./storage`
- `PEERDRIVE_STORAGE_ENABLE`：是否启用存储写操作，默认 `true`
  - 设为 `false` 时，上传、注册本地文件等写操作均返回 403 Forbidden

## 接口

### POST /files/upload
- Content-Type: `multipart/form-data`
- 表单字段: `file` (文件)
- 成功响应 (新文件):
  - 状态码: `201 Created`
  - 体: `{"hash": "...", "size": ..., "mime": "...", "filename": "...", "already_exists": false}`
- 文件已存在响应:
  - 状态码: `200 OK`
  - 体: `{"hash": "...", "size": ..., "mime": "...", "filename": "...", "already_exists": true}`
- 存储禁用时:
  - 状态码: `403 Forbidden`
  - 体: `{"error": "storage is disabled"}`

## 执行流程
1. 检查 `StorageEnable`，若为 `false` 则立即返回 403。
2. 将上传流写入临时文件，同时计算 SHA256 和文件大小。
3. 检测 MIME（基于内容前 512 字节 + 扩展名回退）。
4. 查询 `file_meta` 表，若已存在相同哈希的记录：
   - 直接返回元数据，并设置 `already_exists: true`；**不重复存储文件**。
5. 若不存在：
   - 创建目录 `storage/{hash[:2]}/`，将临时文件移动为 `storage/{hash[:2]}/{hash}`。
   - 在 `file_meta` 表中插入记录（hash, size, mime_type, filename, gziped=false, type=blob）。
   - 在 `file_providers` 表中插入记录，`provider_type` 为 `"local"`，`path` 为 `"{hash[:2]}/{hash}"`（相对存储根目录）。
6. 返回 `201 Created` 及元数据。

## 与下载层的衔接
- `LocalProvider` 基于 `provider.path` 拼接 `StorageDir` 读取文件。
- 上传后，立即可通过 `GET /sha256sum/{hash}` 下载文件。
