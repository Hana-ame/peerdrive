# DeepSeek P2P 文件分享系统方案

## 一、libp2p 集成方案

在 Gin 框架中整合 libp2p 节点，提供以下 API：

- `GET /p2p/node` - 查看节点信息（PeerID、地址）
- `GET /p2p/peers` - 查看已连接的对等点
- `GET /p2p/ping/:peer_id` - ping 指定节点

### 依赖安装

```bash
go get github.com/libp2p/go-libp2p@latest
go get github.com/libp2p/go-libp2p/p2p/protocol/ping
```

### libp2p 服务层 (service/p2p.go)

```go
type P2PService struct {
    Host host.Host
    Ping *ping.PingService
}

func NewP2PService(ctx context.Context) (*P2PService, error) {
    h, err := libp2p.New(
        libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"),
    )
    pingService := ping.NewPingService(h)
    return &P2PService{Host: h, Ping: pingService}, nil
}
```

---

## 二、技术架构选择

| 方案 | 特点 | 适用场景 |
|------|------|----------|
| **WebRTC** | 浏览器可直接运行，无安装 | 网页分享 |
| **libp2p** | 完整 P2P 网络栈，DHT/NAT穿透 | 完整客户端 |
| **混合** | Web前端 + 服务端信令 | 兼容更多网络 |

---

## 三、核心技术问题

1. **Peer Discovery** - 客户端之间如何互相找到
2. **NAT 穿透 + 信令** - 如何建立直接连接
3. **文件传输** - 分块、加密、断点续传
4. **权限控制** - 匿名房间/令牌/角色

---

## 四、参考项目

- **sharedrop**: https://github.com/szimek/sharedrop - WebRTC + Firebase
- **pcp**: https://github.com/dennis-tra/pcp - Go + libp2p
- **file-transfer-go**: https://github.com/matrixji/file-transfer-go - Go 信令
- **Hyperswarm**: https://github.com/holepunchto/hyperswarm - DHT 发现

---

## 五、开发路线

### Phase 1: 最小可行原型
- 同一局域网内文件传输
- WebRTC + STUN/TURN
- 验证核心传输流程

### Phase 2: 完善传输
- 分块传输与断点续传
- 大文件支持
- 进度显示

### Phase 3: 去中心化发现
- DHT 方案
- 房间名发现机制

### Phase 4: 权限与安全
- 安全令牌机制
- 端到端加密
- 身份验证