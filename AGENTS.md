# Peerdrive 项目 Agent 说明

> 全局规范见 `~/.config/opencode/AGENTS.md`（账户/代理/编码规范等）。

## 先读这些文档（按顺序）

1. **`doc/REFACTOR.md`** ← 重构记录：**所有新做的东西、架构决策、坑、帧协议、E2E 验证方式都在这**。动代码前必读。
2. `doc/LEGACY.md` — 旧代码清单（libp2p/BT/WebDAV/前端死代码，标注可删/待迁移/保留）
3. `README.md` — 项目概览（注意：**README 关于 p2p_bt "可独立使用" 的断言是错的**，以 REFACTOR.md 第 6 节为准）

## 核心事实（30 秒版）

- **互联层 = PeerJS 信令 + WebRTC DataChannel**：`back/peerjs/` 是独立模块
  （`github.com/Hana-ame/go-peerjs`，主 go.mod `replace` 引用）；`internal/service/peerjs_service.go`
  是业务用法（文件服务 + 节点互联）；发现：`service/mqtt_discovery.go`（公共 broker）或
  `service/http_discovery.go` + `internal/signalserver/`（自托管，`cmd/peerserver` 独立二进制，
  `PEERDRIVE_DISCOVER_URL` 设置后优先于 MQTT）
- **帧协议 verb**（WS/WebRTC 同一套）：`req/meta/data/done/err`（文件拉取）+ `create/upload/list/info/delete/sync`
  （文件索引：SQLite `file_index` 表持久化 sha256→绝对路径 + seq 游标增量同步），详见 REFACTOR.md 第 4 节
- 旧的 libp2p/BT DHT 栈是 legacy（待迁移/删除），**新代码禁止 import**
- 编码规范：关键/易错/非显然代码旁必须写「为什么这么写」的注释；测试函数必须标注「发现背景」（全局 AGENTS.md 硬性要求）

## 构建与验证

```bash
cd back
go build -tags nosqlite ./...     # 必须 -tags nosqlite（双 SQLite 驱动 CGO 冲突）
go test -tags nosqlite ./...      # 单元/包测试
cd peerjs && go test ./... -count=1 -race   # peerjs 模块（独立 go.mod）
# 集成测试（真实公共信令 0.peerjs.com + 公共 broker，需外网+代理）：
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v
#   ⚠️ 必须 -p 1 串行：公共信令上多组测试并行会互相干扰
# go 命令需代理：HTTPS_PROXY=http://172.29.80.1:10809 GOPROXY=https://goproxy.cn,direct
```

## 关键配置（env）

| 变量 | 默认 | 说明 |
|---|---|---|
| `PEERDRIVE_PEERJS_ENABLE/ID/PEERS` | true/-/- | PeerJS 信令；PEERS 逗号分隔对端自动互联 |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY` | 0.peerjs.com/443/peerjs | 可指向自托管 peerserver |
| `PEERDRIVE_DISCOVER_URL` | - | 自托管发现 API（优先于 MQTT） |
| `PEERDRIVE_MQTT_ENABLE/BROKER/COLLECTIONS` | false/broker.emqx.io/- | MQTT 分片房间发现 |

## 线上部署（cloudcone 自托管信令）

- 服务：`peerserver`（systemd）监听 `127.0.0.1:9000`，nginx 反代
- 域名：`wss://peersignal.moonchan.xyz/peerjs`（WS 信令）+ `https://peersignal.moonchan.xyz/discover/*`（发现 API）
- key：`pd-signal-b9447b406828e500`
- **DNS 决策（橙云，已用）**：peersignal.moonchan.xyz → 117.55.237.217 **proxied=true（橙云）**。
  橙云已验证可行：CF 按 A 记录 IP 回源到 cloudcone nginx（自有证书），CF 100s 空闲超时对信令
  无影响（心跳 5s 保活）。踩坑记录：加 A 记录前 peersignal 橙云路径下实测 404（nginx/1.18.0，
  非 cloudcone 的 1.22.1）——当时回源目标不确定，A 记录建立后回源即正确。
  **注意：cloudcone.moonchan.xyz 被 livekit 占用**（livekit.conf → 127.0.0.1:7880），不能复用。
- **代理注意**：cloudcone 443 **不走宿主机代理**（代理连 cloudcone 超时）——curl/测试无代理直连；
  0.peerjs.com 等公共服务则必须走代理。两者按目标域名区分。
- 线上测试：`PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v`（无代理跑）
- 节点配置：`PEERDRIVE_PEERJS_HOST=peersignal.moonchan.xyz PEERDRIVE_PEERJS_KEY=<key> PEERDRIVE_DISCOVER_URL=https://peersignal.moonchan.xyz`
- 部署更新：构建 `GOOS=linux CGO_ENABLED=0 go build -tags nosqlite -o /tmp/peerserver ./cmd/peerserver/`，
  上传 `bash ~/script/ssh/cloudcone.sh "cat > /root/peerserver.new" < /tmp/peerserver`，
  `mv` 后 `systemctl restart peerserver`（避免 Text file busy）
| `PEERDRIVE_P2P_ENABLE` | true | 旧 libp2p 栈（legacy，测试时设 false 加速） |
