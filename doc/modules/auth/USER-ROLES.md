# Peerdrive 用户角色模型

## 四个角色

| # | 角色 | 认证 | 本地节点 | 权限 |
|---|------|------|----------|------|
| 1 | 匿名访客 | ❌ | ❌ | 浏览公开合集、通过 WebRTC 下载 |
| 2 | 认证用户 | ✅ JWT | ❌ | 创建合集、上传文件、使用远程 relay |
| 3 | 匿名+节点 | ❌ | ✅ API URL | P2P 分享、管理本地文件 |
| 4 | 认证+节点 | ✅ JWT | ✅ API URL | 全部权限 |

## UI 差异

| 功能 | 匿名访客 | 认证用户 | 匿名+节点 | 认证+节点 |
|------|---------|---------|----------|----------|
| 浏览公开合集 | ✅ | ✅ | ✅ | ✅ |
| WebRTC 下载 | ✅ | ✅ | ✅ | ✅ |
| Hash/URL 搜索 | ✅ | ✅ | ✅ | ✅ |
| 创建合集 | ❌ | ✅ | ✅ | ✅ |
| 上传文件 | ❌ | ✅ | ❌ | ✅ |
| P2P 面板 | ❌ | ❌ | ✅ | ✅ |
| 文件管理 | ❌ | ❌ | ✅ | ✅ |
| 节点设置 | ❌ | ❌ | ✅ | ✅ |
| BT/IPFS 控制 | ❌ | ❌ | ✅ | ✅ |
| 管理面板 | ❌ | ❌ | ❌ | ✅ |
| 注册服务器管理 | ❌ | ❌ | ❌ | ✅ (admin) |

## 检测逻辑（前端）

```javascript
// 匿名访客: 什么都没配
// 认证用户: localStorage 有 auth_key, 无 api_base
// 匿名+节点: localStorage 有 api_base, 无 auth_key  
// 认证+节点: 两者都有

const hasAuth = !!localStorage.getItem('peerdrive_auth_key');
const hasNode = !!localStorage.getItem('peerdrive_api_base') && 
                localStorage.getItem('peerdrive_api_base') !== DEFAULT_API;

if (!hasAuth && !hasNode) role = 'anonymous';
else if (hasAuth && !hasNode) role = 'authenticated';
else if (!hasAuth && hasNode) role = 'node-owner';
else role = 'full';
```

## WebRTC 匿名下载流程

```
Anon Browser                    Node Owner
    │                               │
    │── ws://relay/ws/signal ──→    │  (join room by file hash)
    │                               │
    │  ← SDP offer ─────────────    │  (node creates offer)
    │                               │
    │  ──── ICE candidates ───→    │
    │                               │
    │  ═══ WebRTC Data Channel ═══ │
    │  ← file chunks (16KB each) ─ │
    │                               │
    │  download complete            │
```

匿名用户从未接触 HTTP API，只通过 WebRTC Data Channel 接收文件。

## 无本地节点：获取文件的途径

没有 local node 的用户，必须"各显神通"从以下来源获取合集：

```
无本地节点用户
  ├── P2P DHT 网络      → 通过 hash 查找 → 发现 peer → 下载合集
  ├── Registration Server → 获取公开 peer 列表 → 连接 → 下载
  ├── WebRTC            → 连接到有节点的用户 → Data Channel 传输
  ├── 公开 Relay        → 直接 HTTP 下载 (如果 relay 开放)
  └── URL/Hash 粘贴     → 从 P2P 网络定位文件
```

| 来源 | 需要什么 | 延迟 | 可靠性 |
|------|---------|------|--------|
| P2P DHT | 文件 hash | 高 (DHT 查找) | 中 (peer 可能不在线) |
| Reg Server | 知道 reg server URL | 低 | 高 |
| WebRTC | 信令服务器 + peer 在线 | 低 | 中 (需要 NAT 打洞) |
| Relay HTTP | relay URL 开放 | 低 | 高 |
| Hash 粘贴 | 知道 hash 或合集 URL | 高 | 中 |

## 有本地节点：直接管理

有 local node 的用户直接操作本地文件系统：

```
有本地节点用户
  ├── 本地文件注册    → /files/register_local
  ├── 文件夹注册      → /files/register_folder
  ├── HTTP 上传       → /files/upload
  ├── URL 注册        → /files/register_url
  ├── P2P 分享        → /p2p/announce + /p2p/dual/announce
  ├── 合集创建        → /anon/collections
  └── 节点管理        → /p2p/status, /p2p/bt/status, etc.
