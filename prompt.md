# Peerdrive 项目规划

## 项目概述

形成**完整可运行**的 Peerdrive 项目，包含：

1.  **Gin 框架** + **libp2p 节点**（带 `/p2p/*` 管理接口）
2.  **内容寻址下载**：`GET /sha256sum/:sha256`
3.  **SQLite 数据库**（`files` 表，自增主键 + hash 唯一）
4.  **Provider 模式**：支持 `local` 和 `http` 两种内容源
5.  **单元测试/集成测试**（基础示例）

---

## 📁 项目目录结构（按层）

```
peerdrive/
├── cmd/
│   └── server/
│       └── main.go                 # 程序入口
├── internal/
│   ├── controller/
│   │   ├── download.go             # GET /sha256sum/:sha256
│   │   ├── p2p.go                  # GET /p2p/node, /p2p/peers, /p2p/ping/:peer_id
│   │   └── ping.go                 # GET /ping (健康检查)
│   ├── service/
│   │   ├── downloader.go           # 下载业务逻辑
│   │   └── p2p.go                  # libp2p 服务封装
│   ├── provider/
│   │   ├── provider.go             # ContentProvider 接口
│   │   ├── local.go                # 本地文件提供者
│   │   ├── http.go                 # 远程 URL 提供者
│   │   └── manager.go              # 根据 provider_type 路由
│   ├── repository/
│   │   ├── file_repo.go            # files 表 CRUD
│   │   └── db.go                   # 数据库连接、初始化
│   └── model/
│       └── file.go                 # FileMetadata 结构体
├── pkg/
│   └── hashutil/
│       └── hashutil.go             # SHA256 合法性校验
├── storage/                        # 本地文件存储示例目录
│   └── .gitkeep
├── testdata/                       # 测试用文件
│   └── test.txt
├── go.mod
├── go.sum
└── README.md
```

---

## 待完成

### 1. 种一条测试数据

在 `./storage/` 下放一个 `test.txt` 文件，并在数据库中注册：

```bash
# 计算文件 SHA256
sha256sum storage/test.txt

# 插入数据库
```

---

## ✅ 运行与测试

1.  **下载依赖**：`go mod tidy`
2.  **启动服务**：`go run ./cmd/server/main.go`
3.  **测试脚本**：`bash test.sh`
4.  **测试 libp2p 节点信息**：
    ```bash
    curl -x "" http://localhost:3000/p2p/node
    ```