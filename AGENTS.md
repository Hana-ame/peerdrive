# Peerdrive 项目 Agent 说明

> 全局规范见 `~/.config/opencode/AGENTS.md`（账户/代理/编码规范等）。

## 先读这些文档（按顺序）

1. **`doc/REFACTOR.md`** ← 重构记录：**所有新做的东西、架构决策、坑、帧协议、E2E 验证方式都在这**。动代码前必读。
2. `doc/LEGACY.md` — 旧代码清单（libp2p/BT/WebDAV/前端死代码，标注可删/待迁移/保留）
3. `README.md` — 项目概览（注意：README 的 p2p_bt 旧断言「可独立使用」已于 2026-08-18 修正——依赖桥接层，以 REFACTOR.md 第 6 节为准）

## 核心事实（30 秒版）

- **信令服务器实现方式（2026-09 决策）**：信令可以用**多种方式实现**，只要兼容
  PeerJS 协议即可——公共 PeerJS 云、wintools 自托管 Go 信令、peerdrive
  `back/signalserver`（`github.com/Hana-ame/go-peersignal`）、Node.js `peerjs-server`
  等。当前线上优先使用 wintools 维护的 Go 自托管信令，peerdrive 通过
  `PEERDRIVE_PEERJS_HOST/PORT/KEY` 和 `PEERDRIVE_DISCOVER_URL` 连接；
  如后续需要在 peerdrive 内嵌信令，`back/signalserver` 是现成的 Go 实现。
- **互联层 = PeerJS 信令 + WebRTC DataChannel**：`back/peerjs/` 是独立模块
  （module 路径 `github.com/Hana-ame/go-peerjs`，主 go.mod `replace` 指向本地
  `./peerjs`；**独立 repo 已创建** `github.com/Hana-ame/go-peerjs`，tag=v0.1.0 同步，
  改动随主 repo 提交后需镜像同步）；`internal/service/peerjs_service.go`
  是业务用法（文件服务 + 节点互联，`internal/transport/peerjs_service.go`）；发现：
  `service/mqtt_discovery.go`（公共 broker）或
  `service/http_discovery.go` + `back/signalserver/`（独立模块
  `github.com/Hana-ame/go-peersignal`，tag=v0.1.0 同步，自托管信令 `cmd/peersignal` 独立二进制，
  `PEERDRIVE_DISCOVER_URL` 设置后优先于 MQTT；当前线上/本地优先连 wintools 的信令）
- **帧协议 verb**（WS/WebRTC 同一套）：`req/meta/data/done/err`（文件拉取）+ `create/upload/list/info/delete/sync`
  （文件索引：SQLite `file_index` 表持久化 sha256→绝对路径 + seq 游标增量同步）+ `fwd-open/challenge/auth/ok/err/data/close`
  （端口转发 v2，HMAC 质询认证 + 端口白名单，见 REFACTOR.md 第 3.9 节）+ **`admin/admin-resp/admin-bin`**
  （管理面 verb，2026-08-17 起前端全面迁移至此：**仅本地 WS 会话**可用，内部转发 gin engine
  复用全部 HTTP controller；WebRTC 不实现管理 verb 防权限暴露；二进制上传=声明帧+后续二进制帧，
  文件流响应=admin-bin 头+单二进制帧，详见 REFACTOR.md 第 3.10 节与 NODE-API.md §2.4），详见 REFACTOR.md 第 4 节
- 旧的 libp2p/BT DHT 栈已于 2026-08-16 全部删除（REFACTOR §8），**新代码禁止 import**；BT 能力经独立库 `github.com/Hana-ame/go-peerdrive-bt`（back/p2p_bt）
- **第三方独立包 `peerdrive-media`**（`packages/peerdrive-media/`，无独立 repo）：浏览器经
  PeerJS 信令 + WebRTC DataChannel 从 Node 端加载 URL 资源渲染 img/video。三入口：react /
  vanilla（IIFE+CDN）/ node（createPeerMediaServer）。npm 依赖用
  `github:Hana-ame/peerdrive#v0.1.0`（主 repo tag）。（`@v0.1.0` 语法 npm 不认）。
  **改动后必须**：`npm run build`（dist 入库）+ `npm test`（21）+
  浏览器 E2E（`node ~/.claude/skills/playwright-test/scripts/test-runner.mjs
  test/e2e-browser.mjs`，10 项，本机 Firefox）。
  协议：connection 级串行、raw 序列化、64KB 块、背压 4MB。坑与浏览器 E2E
  七连（含串行槽空占三入口）见 REFACTOR.md §3.11；keepalive（断线 5s/15s
  阈值）与排队 abort 立即 settle 见 REFACTOR.md §3.12 第 4/5 项。
- 编码规范：关键/易错/非显然代码旁必须写「为什么这么写」的注释；测试函数必须标注「发现背景」（全局 AGENTS.md 硬性要求）

## 构建与验证

```bash
cd back
go build -tags nosqlite ./...     # 必须 -tags nosqlite（双 SQLite 驱动 CGO 冲突）
go test -tags nosqlite ./...      # 单元/包测试
cd peerjs && go test ./... -count=1 -race   # peerjs 模块（独立 go.mod，改动需同步独立 repo）
# 集成测试（脱外网：TestMain 起全局自托管信令 + 同机 WebRTC，无需代理）：
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1
#   ⚠️ 必须 -p 1 串行：多组测试共享全局自托管信令，并行会互相干扰
#   外网测试显式门控：PEERDRIVE_MQTT_TEST=1（公共 broker）/ PEERDRIVE_LIVE_TEST=1（线上）
#   无 UDP 沙箱（docker 默认）跳过互联类：PEERDRIVE_SKIP_RTC=1
# go 命令需代理：HTTPS_PROXY=http://172.29.80.1:10809 GOPROXY=https://goproxy.cn,direct
# （cloudcone 443 例外：直连）
```

## 关键配置（env）

| 变量 | 默认 | 说明 |
|---|---|---|
| `PEERDRIVE_PEERJS_ENABLE/ID/PEERS` | true/-/- | PeerJS 信令；PEERS 逗号分隔对端自动互联 |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY` | 0.peerjs.com/443/peerjs | 可指向自托管 peersignal |
| `PEERDRIVE_DISCOVER_URL` | - | 自托管发现 API（优先于 MQTT） |
| `PEERDRIVE_MQTT_ENABLE/BROKER/COLLECTIONS` | false/broker.emqx.io/- | MQTT 分片房间发现 |

## 线上部署（cloudcone 自托管信令）

> 当前线上信令由 wintools 维护，peerdrive 作为客户端连接。
> 信令实现方式不限，部署时也可以选择 peerdrive `back/signalserver`
> 或任何 PeerJS 兼容信令。

- 服务：`peersignal`（systemd）监听 `127.0.0.1:9000`，nginx 反代
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
- 部署更新：当前从 wintools 仓库构建并部署 peersignal；若改用 peerdrive/back/signalserver，
  请同步更新本段并确保 `PEERDRIVE_DISCOVER_URL` 指向对应发现 API
- `PEERDRIVE_P2P_ENABLE` 已删除（2026-08-16 批2，libp2p 栈移除）
