# Storage 模块

文件存储、上传、注册、下载、合集逻辑、数据库。

## 源码

| 文件 | 说明 |
|------|------|
| [service/file_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/file_service.go) | 文件服务 (存储/URL解析/MIME嗅探 ~607行) |
| [service/anon_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/anon_service.go) | 匿名合集服务 |
| [service/universal_downloader.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/universal_downloader.go) | 多协议下载编排 |
| [service/downloader.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/downloader.go) | 基础下载器 |
| [service/webdav.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/webdav.go) | WebDAV 文件系统 |
| [repository/file_repo.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/repository/file_repo.go) | 文件元数据仓库 |
| [repository/collection_repo.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/repository/collection_repo.go) | 合集仓库 |
| [repository/db.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/repository/db.go) | 数据库初始化 + 迁移 |
| [controller/file.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go) | 文件管理 handlers |
| [controller/download.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go) | 下载 handlers |
| [controller/anon.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go) | 匿名合集 handlers |
| [controller/collection.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go) | 用户合集 handlers |

## 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/files` | 列出文件 |
| POST | `/files/upload` | 上传文件 |
| POST | `/files/register_local` | 注册本地文件 |
| POST | `/files/register_url` | 注册 URL 文件 |
| POST | `/files/register_folder` | 注册文件夹 |
| GET | `/files/verify/:hash` | 验证文件 |
| GET | `/files/browse` | 浏览文件系统 |
| DELETE | `/files/:hash` | 删除文件 |
| POST | `/files/copy` | 复制文件 |
| GET | `/sha256sum/:hash` | SHA256 下载 |
| GET | `/download/:hash` | 通用下载 |
| ALL | `/webdav/*path` | WebDAV 挂载 |

## 子文档

- [api-reference.md](api-reference.md) — API 参考
- [API-DESIGN.md](API-DESIGN.md) — API 设计
- [COLLECTION-LOGIC.md](COLLECTION-LOGIC.md) — 合集逻辑追踪
- [database.md](database.md) — 数据库设计
