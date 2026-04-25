# 匿名合集层 (Stage 1)

## 接口
- **POST /anon/collections** – 创建匿名合集
- **GET /anon/collections/:hash** – 获取合集 JSON
- **GET /anon/collections/:hash/entries/*path** – 下载合集内文件
- **POST /anon/collections/fork** – 复刻合集（增删条目）

## 合集结构
```json
{
  "version": 1,
  "entries": [
    {"path": "relative/path", "hash": "sha256hex"}
  ],
  "created_at": "2025-01-01T00:00:00Z"
}
```

## 校验规则
- 所有 path 必须是相对路径，且不包含 `..`，否则返回 400。
- hash 必须为 64 位十六进制字符串。
- 条目按路径字典序排序保证确定性。

## 存储
- 合集 JSON 本身作为内容寻址对象存储，类型为 `anon_collection`，MIME 为 `application/json`。
- 下载合集 JSON 时，响应头增加 `X-Peerdrive-Collection: true`。

## 涉及文件
```
internal/model/anon.go             — AnonCollection / AnonCollectionEntry 结构体
internal/service/anon_service.go   — 业务逻辑：校验、创建、读取
internal/controller/anon.go        — HTTP 入口
internal/repository/anon_repo.go   — 底层存储（写文件 + file_meta/file_providers）
internal/controller/download.go    — 下载时添加 X-Peerdrive-Collection 头
```

## 测试
执行 `test_anon_collection.sh` 验证创建、路径安全、下载和复刻功能。
