# Peerdrive Dual-Stack P2P TODO

> Branch: feat/bt-dht · Status: 🚧 IN PROGRESS

## 🔴 P0 — 基础功能 Bug

- [ ] P2P exchange 后文件未写入 storage（Node B 下载了数据但 `GET /sha256sum/:hash` 404）
- [ ] BT Find 在 Node A 上返回 0（可能是因为同一节点查找自己）
- [ ] Dual Find IPFS 侧始终返回 null（DHT 孤立节点，需验证 bootstrap 后是否恢复）

## 🔴 P0 — 全局日志系统

- [ ] 统一 log 包（`internal/log/`）— 所有 service 和 controller 共用
- [ ] 支持 `PEERDRIVE_LOG_LEVEL=DEBUG|INFO|WARN|ERROR` 环境变量
- [ ] 每个关键函数入口/出口打 DEBUG 日志 + 耗时
- [ ] 所有错误路径打 ERROR 日志
- [ ] 需要加日志的文件清单：
  - [ ] `internal/service/p2p.go` — NewP2PService, handleExchange, AnnounceHash, FindProviders, FetchFile, SyncFiles, BroadcastRequest
  - [ ] `internal/service/p2p_connection.go` — ConnectToPeer, heartbeatLoop, checkAndReconnect
  - [ ] `internal/service/p2p_transfer.go` — DownloadFile, requestChunk, handleChunkRequest
  - [ ] `internal/p2p_bt/bt_dht.go` — NewBTDHT, Announce, FindProviders
  - [ ] `internal/p2p_bt/bt_bridge.go` — ShareFile, FetchFile
  - [ ] `internal/service/p2p_dual.go` — Announce, FindProviders, FetchFile
  - [ ] `internal/controller/p2p.go` — 所有 handler
  - [ ] `internal/controller/file.go` — UploadFile, RegisterLocalFile, RegisterFolder
  - [ ] `internal/service/file_service.go` — RegisterLocal, RegisterFolder, Verify
  - [ ] `internal/router/router.go` — 启动日志、路由注册计数

## 🟡 P1 — 测试网格

- [ ] 分协议测试矩阵 (HTML + curl 脚本)
  - [ ] IPFS/libp2p: connect, ping, announce, find, exchange, sync
  - [ ] BT DHT: status, announce, find, node count
  - [ ] Dual: announce, find
  - [ ] File: upload, register, browse, verify, delete
  - [ ] Collection: create, get, list, fork, sync
- [ ] 每个协议独立测试，清晰 pass/fail 输出
- [ ] 生成 test-grid.html 发布到 upload.moonchan.xyz
- [ ] curl 测试脚本 `test/dual-stack-test.sh`

## 🟡 P1 — Test Peers 实测

- [ ] 在 VPS 上运行 ipfs-peer.py — 验证 /ping, /p2p/node, /files/upload, /files/:hash
- [ ] 在 VPS 上运行 bt-peer.py — 验证 UDP DHT ping/find_node/get_peers/announce_peer
- [ ] 运行 dual-stack-test.py 集成测试 3 个 scenario
- [ ] 修复 test peers 中发现的问题

## 🟡 P1 — P2P Exchange 保存到 Storage

- [ ] `handleExchange` 收到文件后应调用 `repository.InsertFileMeta` + `InsertFileProvider`
- [ ] `SyncFiles` 下载后应确保文件从 storageDir 可达（已有但需验证）
- [ ] `requestData` 成功后缓存到本地 storage（去重）

## 🟢 P2 — 前端完善

- [ ] P2PStatus BT 标签页连接状态实时刷新
- [ ] FileManager 🧲 按钮 toast 通知可见性
- [ ] 双栈宣告结果卡片在 UI 中显示
- [ ] Settings 页面添加 BT DHT 状态

## 🟢 P2 — Relay 节点监控

- [ ] VPS relay systemd 服务健康检查脚本
- [ ] 定期 curl 测试 → 失败自动重启
- [ ] 节点数、内存、uptime metrics

## ⚪ P3 — 文档

- [ ] dual-stack 架构图
- [ ] BT DHT 协议文档 (go/docs/specs/bt-dht.md)
- [ ] API 参考更新 (含 /p2p/bt/* 和 /p2p/dual/*)
- [ ] README.md 更新双栈说明

## 已验证

- ✅ IPFS/libp2p 双节点连接 (A↔B)
- ✅ BT DHT 连接全球网络 (127+ 节点)
- ✅ BT DHT announce
- ✅ BT DHT find (Node B 查到 Node A 宣告的文件)
- ✅ P2P exchange (B→A 下载 10B)
- ✅ Dual announce (双网同时宣告)
- ✅ 单二进制编译 (51MB)
- ✅ systemd 持久化 relay
