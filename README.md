# Peerdrive — P2P Content-Addressed File Sharing

> SHA256 内容寻址 + Git 风格版本管理 + libp2p / BT DHT / IPFS 多协议 P2P

[![Peerdrive CI](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml/badge.svg)](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml)
[![Go Build Matrix](https://github.com/Hana-ame/peerdrive/actions/workflows/go-build.yml/badge.svg)](https://github.com/Hana-ame/peerdrive/actions/workflows/go-build.yml)

## 目录结构

```
peerdrive/              ← Monorepo
├── front/               React 前端 (Vite + TailwindCSS)
├── back/                Go 后端 (Gin + SQLite + libp2p)
├── doc/                 项目文档
└── .github/workflows/   CI/CD (backend + frontend 双轨)
```

## 端口

| 服务 | 端口 | 说明 |
|------|------|------|
| back (API) | `:3000` | Gin HTTP，文件/合集/P2P 全部端点 |
| front (Dev) | `:5173` | Vite 开发服务器 |
| registration-server | `:4000` | 独立用户认证服务（不在本仓库） |

## 快速开始

```bash
# 后端
cd back
go run ./cmd/server/
# → http://localhost:3000

# 前端
cd front
npm ci
npm run dev
# → http://localhost:5173
```

## 测试

| 层级 | 命令 | 数量 |
|------|------|------|
| Go 单元测试 | `cd back && go test ./... -count=1` | 150 测试函数 / 17 文件 |
| 集成 / E2E | `cd back && bash test/e2e-all.sh` | 32 脚本 |
| 前端 Vitest | `cd front && npm test` | 20 用例 / 5 文件 |

## 核心功能

- **文件管理** — 上传 / SHA256 下载 / 本地注册 / 文件夹递归导入
- **合集 (Collection)** — 匿名 & 命名合集、Commit / Rollback / Fork / Merge
- **P2P** — libp2p DHT 发现、Relay、NAT 打洞、WebSocket 传输
- **BT DHT** — Mainline DHT、BEP44 put/get、torrent / magnet
- **IPFS** — IPFS 兼容层、CID pin/取消、Bitswap 互通
- **WebRTC** — 信令服务、浏览器直连
- **断点续传** — 多对等点分片下载、进度追踪

## 技术栈

| 层 | 技术 |
|----|------|
| HTTP 路由 | Gin |
| 数据库 | SQLite (`peerdrive.db`) |
| P2P | libp2p (DHT / Relay / AutoNAT / mDNS) |
| BT | anacrolix/dht |
| 前端 | React 18 + Vite + TailwindCSS |
| 测试 | Go testing + Vitest + Shell E2E |

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `3000` | 后端端口 |
| `PEERDRIVE_STORAGE` | `./storage` | 文件存储目录 |
| `PEERDRIVE_P2P_ENABLE` | `true` | 启用 libp2p |
| `PEERDRIVE_BT_DHT_ENABLE` | `true` | 启用 BT DHT |
| `PEERDRIVE_RELAY_ENABLE` | `false` | Relay 模式 |
| `PEERDRIVE_MDNS_ENABLE` | `true` | 局域网发现 |
| `CORS_MODE` | 白名单 | `all` / `localhost` |

## 文档

完整文档见 [doc/](doc/)：

| 入口 | 内容 |
|------|------|
| [doc/INDEX.md](doc/INDEX.md) | 完整文档索引 |
| [doc/spec/API-REFERENCE.md](doc/spec/API-REFERENCE.md) | API 参考 (105 端点) |
| [doc/testing/](doc/testing/) | 测试体系文档 |
| [doc/guide/VPS_DEPLOY.md](doc/guide/VPS_DEPLOY.md) | VPS 部署指南 |
