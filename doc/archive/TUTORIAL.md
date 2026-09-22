# Peerdrive 使用教程

> 2026-05-03

---

## 目录

- [1. 多后端切换](#1-多后端切换)
- [2. P2P 协议测试工具](#2-p2p-协议测试工具)
- [3. 常见操作](#3-常见操作)

---

## 1. 多后端切换

前端支持配置多个后端地址，在设置页面一键切换。

### 1.1 打开设置

点击导航栏右上角的齿轮图标 ⚙ 进入设置页。

### 1.2 切换后端

在"节点连接"区顶部，可以看到后端切换按钮组：

```
┌──────────────────────────────────────────────┐
│  后端快速切换                                  │
│                                              │
│  ┌──────┐  ┌──────┐  ┌──────────┐           │
│  │ WSL ✓│  │ BWH  │  │ + 添加   │           │
│  └──────┘  └──────┘  └──────────┘           │
│                                              │
│  ┌ 当前连接 ──────────────────────────────┐  │
│  │ 🟢 已连接                              │  │
│  │ https://wsl-3000.moonchan.xyz          │  │
│  │                                        │  │
│  │ Peer ID: 12D3...                       │  │
│  │ P2P: 启用                              │  │
│  │ Relay: server                          │  │
│  │ Peers: 3                               │  │
│  └────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
```

- **点击按钮** 切换到对应后端
- **当前后端** 高亮显示并带 ✓ 标记
- **hover 自定义后端** 右上角出现 × 可删除

### 1.3 添加新后端

点击 "+ 添加" 按钮，在弹出的对话框中填写：

```
┌──────────────────────────────┐
│  添加后端                     │
│                              │
│  [名称（如：VPS）           ] │
│  [URL（如：http://host:3000)] │
│                              │
│        [取消]    [添加]      │
└──────────────────────────────┘
```

添加后自动切换到新后端，并继承当前 STUN/TURN 配置。

### 1.4 临时连接

如果只是想临时测试一个地址而不保存为后端，展开"临时连接"输入框：

```
▸ 临时连接其他地址
```

输入 URL 后点"连接"，会直接切换过去，不会保存到后端列表。

### 1.5 STUN/TURN 配置

STUN/TURN 配置是按后端存储的。切换后端时自动加载对应配置：

1. 在"节点连接"区找到 STUN/TURN 输入框
2. 修改后点击"保存"
3. 配置会写入当前后端，下次切换回来时自动恢复

### 1.6 预设后端

| 名称 | URL | 说明 |
|------|-----|------|
| WSL | `https://wsl-3000.moonchan.xyz` | 本地开发环境（WSL 端口转发） |
| BWH | `http://97.64.30.221:3000` | 公网 VPS 服务器 |

---

## 2. P2P 协议测试工具

`p2p-test` 是一个独立的 P2P 协议测试工具，使用裸 go-libp2p 直接操作 stream 层，不依赖 peerdrive 内部包。

### 2.1 编译

```bash
cd back
go build -tags nosqlite -o p2p-test ./cmd/p2p-test/
```

### 2.2 通用参数

| 参数 | 必填 | 说明 |
|------|------|------|
| `--peer` | ✅ | 目标 peer multiaddr |
| `--op` | ❌ (默认 list) | 操作: list / query / exists / get / size |
| `--query` | 部分 | 查询字符串 (用于 query/exists) |
| `--hash` | 部分 | SHA256 hex (用于 get/size) |
| `--out` | 否 | 下载输出路径 (用于 get) |
| `--timeout` | 否 | 超时秒数 (默认 30) |

### 2.3 操作详解

#### `list` — 列出所有公开 Collections

```
p2p-test --peer <multiaddr> --op list
```

输出示例：
```
=== 列出所有公开 Collections ===
用户 Collections: 0
匿名 Collections: 6
  - e9fcb693...02f3ae41 (name="", entries=0)
  - e1e85077...f0476775 (name="Relay Test", entries=1)

✅ 完成
```

#### `query` — 按名称查询 Collections

```
p2p-test --peer <multiaddr> --op query --query "关键字"
```

模糊匹配 username 或 collection_name，OR 语义。

#### `exists` — 判断 Collection 是否存在

```
p2p-test --peer <multiaddr> --op exists --query "Collection名"
```

返回 `✅ 存在` 或 `❌ 不存在`，适用于脚本判断。

#### `size` — 查询文件大小

```
p2p-test --peer <multiaddr> --op size --hash <64位SHA256>
```

返回文件字节数，自动换算 KB/MB。

#### `get` — 下载文件

```
p2p-test --peer <multiaddr> --op get --hash <64位SHA256> --out /tmp/output
```

自动验证 SHA256，匹配则显示 `✅ SHA256 验证通过`。

不指定 `--out` 时打印前 256 字节的 hex 预览。

### 2.4 完整示例

```bash
# VPS relay 节点地址
PEER="/ip4/97.64.30.221/tcp/4001/p2p/12D3KooWSbj9NsY2bBY1BeorgWWmqZypjiqpEHTLNSVNMhWuMVRZ"

# 列出公开合集
p2p-test --peer "$PEER" --op list

# 查询包含 "test" 的合集
p2p-test --peer "$PEER" --op query --query "test"

# 判断合集是否存在
p2p-test --peer "$PEER" --op exists --query "Relay Test"

# 查询文件大小
p2p-test --peer "$PEER" --op size --hash 3a0e8592b5d7d40136d35fa3f33175a9731f8a817244d7143dc36dea172417ce

# 下载文件
p2p-test --peer "$PEER" --op get --hash 3a0e8592b5d7d40136d35fa3f33175a9731f8a817244d7143dc36dea172417ce --out /tmp/relay.txt
```

### 2.5 使用 `go run` 直接运行

```bash
cd back
go run -tags nosqlite ./cmd/p2p-test/ --peer "$PEER" --op list
```

---

## 3. 常见操作

### 启动本地后端

```bash
cd back
PEERDRIVE_BOOTSTRAP_PEER="/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
PEERDRIVE_RELAY_MODE=client \
PEERDRIVE_P2P_ENABLE=true \
  go run -tags nosqlite ./cmd/server/main.go
```

### 启动前端

```bash
cd front
npm run dev
```

前端默认 `localhost:5173`，设置中切换到 WSL 后端 (`https://wsl-3000.moonchan.xyz`) 或 BWH 后端 (`http://97.64.30.221:3000`)。

### 构建并推送 CI

```bash
git add <files>
git commit -m "描述"
git push origin main
```

GitHub Actions 自动触发：
- **Peerdrive CI**: Go 后端测试 + 前端测试 + 构建
- **Go Build Matrix**: 5 平台交叉编译
