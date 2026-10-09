# 更新分发提示通道架构选型与协议规范

> 对应 Issue: #138  
> 状态: 调研与选型完成，决策明确（按 Issue 边界：确定通道形态与协议，待后续 PR 实现）  
> 关联 Issue: #103 (配置下发可观测性), #105 (模块镜像同步), #107 (ECH 出口), #115 (架构总纲)

---

## 1. 业务动机与版本生态现状

Peerdrive 拥有多元的分发矩阵：
- **主二进制**：由 `release.yml` 构建发布，版本号由 `back/internal/version` 通过 `-ldflags -X` 注入；
- **前端 Web 站点**：由 `pages.yml` 持续部署至 Cloudflare / GitHub Pages；
- **独立消费端包**：
  - `packages/peerdrive-client`（单文件公共面板 `dist/panel.html`）；
  - `packages/peerdrive-media`；
- **独立 Go 模块**：`go-peerjs`、`signalserver` (`go-peersignal`)、`p2p_bt`。

**当前痛点**：本地节点、运维与用户缺乏任何新版本可用感知通道，只能手动关注 GitHub Releases。

---

## 2. 候选形态深度评估与裁定

| 候选形态 | 核心机制 | 优势 | 缺陷与风险 | 最终裁定 |
|---|---|---|---|---|
| **形态 A：泛化现有配置下发协议** | 复用 `exhentai/remote.go` 的 ETag/304、TTL、Jitter、失败回退机制 | 协议成熟（PR #103 已验证），零请求浪费，自动支持离线容错 | ⚠️ 原文档存在携带鉴权 Cookie 的风险，**必须严格隔离载体** | **采纳（独立端点模式）** |
| **形态 B：信令与发现通道附加** | 在自托管信令 `/discover/status` 携带 `version` | 客户端心跳复用，零额外 HTTP 请求 | 仅对自托管信令有效；直连公共 PeerJS 云或单机使用时完全失效 | **辅助采纳（内网信令透传）** |
| **形态 C：直接轮询 GitHub API** | 定期请求 `api.github.com/repos/...` | 零服务端自建成本 | 强依赖外网连接；国内网络环境下超时严重；极易触发 60次/小时 API Rate Limit | **否决** |
| **形态 D：前端/CLI 纯展示位** | 在 Settings、面板与 `peerdrive version` 展示徽标 | 用户感知最直观 | 只是消费展示端，需依赖后端提供数据源 | **作为展示层采纳** |

### 裁定结论：
**采用「形态 A 泛化协议（独立公开端点）作为主干，形态 D 作为前端/CLI 消费端」的设计方案。**

---

## 3. 核心设计规范

### 3.1 载体独立与绝对安全原则（严格隔离 Cookie）
- **硬性安全防线**：更新检查是一项**完全公开、匿名、只读**的操作。
- 严禁将更新元数据与业务授权配置（如 ECH / ExHentai Cookie 凭据）合并在同一文档或同一 HTTP 客户端。
- 更新检查客户端使用纯净无状态的标准 `http.Client`，不发送任何 Authorization 头、Cookie 或 Session 信息。

### 3.2 远程更新元数据 Schema 规范
发布于公开 CDN / 静态存储（如 `https://peerdrive.moonchan.xyz/dist/version.json`）：
```json
{
  "schema": 1,
  "latest_version": "v0.3.0",
  "release_date": "2026-10-15T00:00:00Z",
  "components": {
    "peerdrive": "v0.3.0",
    "peerdrive-client": "v0.2.2",
    "peerdrive-media": "v0.1.5",
    "signalserver": "v0.1.2"
  },
  "changelog_url": "https://github.com/Hana-ame/peerdrive/releases/tag/v0.3.0",
  "critical_security_update": false,
  "announcement": "优化了 WebRTC 对称 NAT 打洞与节点发现稳定性。"
}
```

### 3.3 缓存协商与轻量同步状态机
复用 `remote.go` 模式：
1. **HTTP 条件请求**：
   - 携带 `If-None-Match: <ETag>` 与 `If-Modified-Since`；
   - 服务端返回 `304 Not Modified` 时，仅续期本地 `CacheTTL`，不重新反序列化 JSON，网络消耗 $< 300$ 字节。
2. **刷新节奏与抖动（Jitter）**：
   - 默认检查间隔 `CacheTTL = 24h`；
   - 引入 $\pm 10\%$ 随机 Jitter，防止海量节点在整点瞬时向更新服务器发起雷鸣风暴。
3. **容错与静默失败**：
   - 网络断开或解析失败时，保留本地上一次缓存的版本信息，不重试爆破，不记录刷屏错误日志（遵循 #103 可观测性原则，仅记录 Debug 日志并更新内部统计状态）。

---

## 4. 开关策略与静默原则

遵循 Peerdrive 「默认不主动向外网报送、不产生无谓外部连接」的隐私设计哲学：
1. **默认状态**：
   ```bash
   # 默认关闭更新自检，完全离线运行
   PEERDRIVE_UPDATE_CHECK_ENABLE=false
   PEERDRIVE_UPDATE_FEED_URL="https://peerdrive.moonchan.xyz/dist/version.json"
   ```
2. **免打扰模式**：
   - 发现新版本时，**绝不在标准输出刷屏或抛出警告**；
   - 仅通过管理状态 API（`GET /status` 或 WebSocket admin）暴露 `update_available: true` 标记。

---

## 5. 消费端呈现（形态 D）

1. **Web 管理后台（`front/src/pages/Settings.jsx`）**：
   - 在底部或关于卡片展示：
     `当前版本: v0.2.0`
     若检测到更新：显示黄色轻量徽标 `发现新版本 v0.3.0 (点击查看更新日志)`。
2. **公共单文件面板（`packages/peerdrive-client`）**：
   - `dist/panel.html` 嵌入版本元数据比对组件，若当前拉取器对应的节点版本落后，提供友好升级指引。
3. **CLI 终端（`peerdrive version`）**：
   - 执行 `peerdrive version` 时，读取本地已缓存的版本状态，输出：
     ```text
     peerdrive v0.2.0 (linux/amd64)
     * A new version v0.3.0 is available. See: https://github.com/Hana-ame/peerdrive/releases/tag/v0.3.0
     ```

---

## 6. 实施规划

- **PR 1（后端通道）**：在 `back/internal/update` 落地基于 ETag/304 的轻量版本拉取器与状态存储，接入 `serverapp` 状态端点；
- **PR 2（前端/CLI 消费）**：在 Settings 页面与 `peerdrive version` CLI 输出中接入新版本提示。
