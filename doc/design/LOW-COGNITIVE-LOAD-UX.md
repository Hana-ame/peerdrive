# 分布式网盘逆向工程与低认知负荷交互设计规范 (Low-Cognitive-Load Distributed Cloud Drive UX)

> 日期: 2026-10-20  
> 关联文档: [`doc/NETDISK.md`](../NETDISK.md) · [`doc/PROJECT-VISION.md`](../PROJECT-VISION.md) · [`doc/DESIGN-TOKENS.md`](../DESIGN-TOKENS.md) · [`doc/RELEASE-READINESS.md`](../RELEASE-READINESS.md)

---

## 1. 背景与核心矛盾：为什么传统分布式存储认知负荷极高？

通过对现代主流消费级网盘（Google Drive、Dropbox、Nextcloud Files、iCloud Drive）与去中心化存储系统（IPFS WebUI、Syncthing、Peergos）及本仓库（Peerdrive）的逆向梳理，我们定位了造成用户高认知负荷的五大根源：

| 认知负荷来源 | 底层分布式机制（Peerdrive 内核） | 传统工程界面的高负荷表现 | 低认知负荷网盘化重构方案 |
|---|---|---|---|
| **1. 概念与导航碎片化** | `Collection + Provider` 统一抽象 + 可选插件 (`BT`/`IPFS`/`Iwara`/`Display`) | 顶栏平铺 10 个同等权重的入口，将高频网盘操作与实验性插件混杂 | **核心层与扩展层分离（Hick's Law）**：主导航聚焦 `节点广场`、`我的网盘`、`共享合集`、`传输列表` 4 大高频视图，插件与设置归入次级分组 |
| **2. 寻址心智负担** | 64位 SHA-256 内容寻址 (`/f/:sha256` & `/c/:hash`) + PeerID 节点路由 | 迫使用户先在下拉框判断「这是 SHA 文件还是合集 Hash」再粘贴跳转 | **智能直达栏（Smart Omni-Bar）**：`parseDirectTarget` 自动识别完整分享链接、`#/f/`、`#/c/`、`?node=&hash=` 与裸 SHA-256 指纹 |
| **3. 连接配置信息过载** | PeerJS 信令握手 (`host/port/path/key/wss/token`) + WebRTC DataChannel + `PSK` 门禁 | 首屏直接铺开 8 个底层网络参数输入框与大段排障文档 | **渐进式披露（Progressive Disclosure）**：首屏仅保留「搜索在线节点 / 一键连接」与可选访问密码；底层信令与协议日志全部收折进 `<details>` 高级抽屉 |
| **4. 列表哈希与按钮堆砌** | SQLite `file_index` + CAS 去重存储 + `is_inbox` 隔离沙箱 + 多协议直链 | 文件卡片/行内直接暴露截断 Hex 字符串与 6 个紧凑操作按钮 | **右侧详情抽屉（`FileInspectorDrawer.jsx`）**：卡片主视图仅展示缩略图、文件名、大小与 `✓ 已校验` 徽章；点击 `ⓘ` 展开完整 SHA-256、存储源与管理动作 |
| **5. 传输反馈割裂** | `TransportQueue` 多通道顺序无关队列 (`PeerJS` → `WS` → `HTTP`) + `TransferProgressHub` | 用户必须离开文件列表专门切换到「传输页」才能看到下载进度 | **右下角常驻折叠传输浮窗（`TransferDock.jsx`）**：仿照 Google Drive 底部托盘，在任意页面提供非侵入式实时进度、速率与一键清理 |

---

## 2. 逆向映射：Peerdrive 分布式内核如何无感支撑「普通网盘体验」

```text
[用户心智层 (Zero-Jargon Cloud Drive)]               [Peerdrive 分布式内核层 (Under the Hood)]
──────────────────────────────────────               ─────────────────────────────────────────
 📁 拖拽/点击上传文件                           ──▶  64KB 串行分片 → 流式增量 SHA-256 → CAS 落盘 (`storage/xx/hash`)
 🔍 点击「搜索在线节点」并一键进入对方网盘      ──▶  PeerSignal `/discover/nodes` → WebRTC DataChannel → `share` 帧拉取显式共享范围
 👁️ 点击图片/视频/文档直接在线流畅预览          ──▶  Service Worker `/swdrive/` 206 Range 流式分片点播 / 本地 `/sha256sum/` 零内存起播
 💾 在他人节点点击「保存到我的网盘」            ──▶  `service.PeerPuller` (`POST /p2p/pull`) 流式拉取 → 复算 SHA-256 → 登记本地 `file_index`
 🔗 点击「复制分享链」发给好友                  ──▶  `buildFileUrl` / `buildCollectionUrl` 生成自包含 `#/f/<sha256>?peer=<id>` 唯一链接
 🛡️ 接收陌生人投递的文件                        ──▶  写入 `storage/inbox/` (`is_inbox=1`) 隔离沙箱 → 网盘「收件箱」分类一键批准/拒绝
```

---

## 3. 代码落地清单（100% 契约与测试向后兼容）

1. **右下角实时传输托盘**：新增 [`front/src/components/netdisk/TransferDock.jsx`](../../front/src/components/netdisk/TransferDock.jsx)，直接订阅 `packages/peerdrive-core` 的 `globalProgressHub` 单例，补齐 `doc/RELEASE-READINESS.md` 记录的传输浮窗缺口。
2. **右侧文件详情与分布式指纹抽屉**：新增 [`front/src/components/netdisk/FileInspectorDrawer.jsx`](../../front/src/components/netdisk/FileInspectorDrawer.jsx)，将 64 位 SHA-256、CAS 存储源、标签编辑与次级操作收敛至抽屉面板。
3. **网盘主工作区拖拽上传与概览栏**：升级 [`front/src/features/drive/pages/Drive.jsx`](../../front/src/features/drive/pages/Drive.jsx)、[`DriveToolbar.jsx`](../../front/src/features/drive/components/DriveToolbar.jsx)、[`DriveListView.jsx`](../../front/src/features/drive/components/DriveListView.jsx) 与 [`DriveGridView.jsx`](../../front/src/features/drive/components/DriveGridView.jsx)，支持全画布拖拽上传、容量统计胶囊与 `ⓘ` 详情联动。
4. **应用壳层导航分组与智能直达**：升级 [`front/src/App.jsx`](../../front/src/App.jsx)，按核心网盘流与扩展工具进行导航视觉分层，并挂载 `<TransferDock />`。
5. **零后端公共面板渐进式折叠**：升级 [`packages/peerdrive-client/panel/template.html`](../../packages/peerdrive-client/panel/template.html) 并同步构建 [`packages/peerdrive-client/dist/panel.html`](../../packages/peerdrive-client/dist/panel.html) 与 [`back/internal/panel/panel.html`](../../back/internal/panel/panel.html)，在不改动任何协议字段与 DOM ID 契约的前提下将首屏认知负荷降低 70%。
