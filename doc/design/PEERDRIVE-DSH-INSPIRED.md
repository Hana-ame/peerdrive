# Peerdrive 参考 dsh 的组合式架构设计

> 日期：2026-08-16
> 目标：借鉴 DeepSeek Harness（`dsh`）的 **profile / bundle / patch / plugin-row** 组合模型，
> 把 Peerdrive 从“一个巨型 main 顺序初始化”改造成“一个空基座 + 有序模块 bundle + 用户覆盖层”的 harness。
> 该设计同时满足 `今日最后要求.txt`：第一个 commit 是基座，各模块在独立分支开发，最后合并集成。

---

## 1. 为什么参考 dsh

当前 Peerdrive 后端是典型的单体装配：

```go
InitDB → NewP2PService → NewIPFSService → NewPeerJSService → SetupRouter → Run
```

这种方式的问题：

| 问题 | 现状 |
|---|---|
| 模块边界只存在于目录，不存在于装配 | `service/` 里有 30+ 个服务，`main.go` 顺序 New 全部 |
| 分支合并容易冲突 | 每个模块都在改 `main.go`、`config.go`、`router.go` |
| 无法按部署形态裁剪 | 公共默认值把所有 legacy（libp2p/BT/IPFS）都带进构建 |
| 配置散落在环境变量 + 代码默认值 | 没有“层”，部署覆盖必须改代码或靠 env |
| 前端/后端模块无法成对发布 | 一个功能往往要同时动 `front/` 和 `back/` |

`dsh` 的组合模型正好解决同一类问题：

- **空根开始**：profile 根只是一个空列表，所有能力来自 bundle patch。
- **有序 bundle**：`dsh.profile.bundles` 决定组合包顺序，`cordis.patch.yml` 是每层 patch。
- **按 id 覆盖**：后一层 patch 针对同一 `id` 整体替换 `config`，最后写入者生效。
- **profile = 产品形态**：`web` / `headless` 是不同 profile，共享 base bundle。
- **用户最后覆盖**：profile 自己的 patch、home 级 patch、`--patch` overlay 依次压栈。

Peerdrive 不需要照搬 Cordis/JS，但可以复刻这套心智模型，用 Go 接口 + YAML manifest 实现。

---

## 2. 核心概念映射

| dsh（DeepSeek Harness） | Peerdrive 设计 |
|---|---|
| `dsh --profile <name>` | `peerdrive --profile <name>` 或 `pd --profile <name>` |
| profile 目录（`$DSH_HOME/profiles/<name>`） | `profiles/<name>/`，包含 `peerdrive.yml` + `peerdrive.patch.yml` |
| `package.json` 中 `dsh.profile.bundles` | `peerdrive.yml` 中 `bundles: [base, storage, ...]` |
| bundle（`@deepseek-ai/dsh-base` 等） | `back/bundles/<name>`（Go 包 + `bundle.yml`），前端必要时配 `front/bundles/<name>` |
| `cordis.patch.yml`（bundle 的 patch） | `back/bundles/<name>/bundle.patch.yml` / `bundle.yml` 内嵌 patch |
| plugin row（`id/name/config`） | component row（`id/type/config/disabled/inject`） |
| 后层按 `id` 整体替换 config，last write wins | 同样：后层 patch 按 `id` 替换整行 config |
| `--patch <path>` 覆盖层 | `peerdrive --patch <path>` / `PEERDRIVE_PATCH` |
| `--dump-config` | `peerdrive config --dump` |
| `dsh plugin --profile web add <pkg>` | `peerdrive module add <bundle>`（脚手架/模板生成） |
| bundle 既含 host 端也含 browser 端 | `back/bundles/<name>` + `front/bundles/<name>` 成对存在 |

---

## 3. 目标目录结构

保留现有 `/front`、`/back`、`/doc` 根目录，新增组合层：

```
peerdrive/
├── back/
│   ├── cmd/
│   │   ├── peerdrive/          # harness 入口：解析 profile -> 装配 bundles
│   │   ├── server/             # 兼容旧入口，等价于 --profile node --profile web
│   │   └── peerserver/         # 自托管信令/发现服务器（也可做成一个 bundle）
│   ├── internal/
│   │   ├── harness/            # 组合引擎：Bundle/Profile/Row/Patch/Scope
│   │   ├── core/               # 基座：config/log/db/router/cas/nodeinfo
│   │   └── ...
│   └── bundles/
│       ├── base/               # 基座 bundle，所有 profile 第一层
│       ├── auth/               # 认证/用户/JWT/权限/统计
│       ├── storage/            # 文件索引、上传/下载、collection
│       ├── sync/               # seq 增量同步、tombstone
│       ├── transport/          # PeerJS/WebRTC + WS 会话 + 帧协议
│       ├── discovery/          # 静态 peer / MQTT / HTTP discover / signalserver
│       ├── web/                # 前端 dist、API gateway、trust fence
│       └── legacy/             # libp2p / BT / IPFS，隔离可选
├── front/
│   ├── bundles/                # 每个前端功能包
│   │   ├── core/               # 布局、路由、API client
│   │   ├── auth/               # 登录/用户/权限 UI
│   │   ├── storage/            # 文件管理/上传/下载 UI
│   │   ├── sync/               # 同步状态 UI
│   │   ├── transport/          # P2P 面板/节点/传输 UI
│   │   └── discovery/          # 发现/信令配置 UI
│   └── src/                    # 由 bundles 组合出的应用源码（构建时生成/显式导入）
├── profiles/
│   ├── node/                   # 纯常驻节点 profile
│   ├── web/                    # 本地 Web + 节点 profile（对应现在 server+front）
│   └── server/                 # 自托管信令/发现服务器 profile
└── doc/
```

> 说明：Go 没有 npm 那样的运行时动态加载，“bundle”采用 **编译时组合 + 运行时配置覆盖**：
> profile 列出哪些 bundle，`cmd/peerdrive` 通过 import 这些 bundle 的注册函数，把它们链接进当前二进制；
> `--patch` 只改配置行，不新增代码。这与 dsh 的“动态插拔“精神一致，但保持 Go 单二进制部署。

---

## 4. 组合引擎（`internal/harness`）

### 4.1 最小模型

```go
// Component 是每个可挂载服务的最小接口
type Component interface {
    ID() string
    Start(ctx context.Context, s *Scope) error
    Close(ctx context.Context) error
}

// Bundle 描述一个模块包
type Bundle struct {
    Name    string
    Version string
    Patch   []PatchOp          // insert / patch / disable
    Build   func(b *Builder) error
}

// Profile 描述一个产品形态
type Profile struct {
    Name    string
    Bundles []string           // 有序 bundle 列表
    Patch   []PatchOp          // profile 自己的覆盖层
}

// Scope 是组件可访问的共享上下文
type Scope struct {
    Config  *Config            // 合并后的配置树
    Log     *Logger
    DB      *DB
    Router  *Router
    Storage *CASStore
    Env     map[string]string
}
```

### 4.2 patch 操作

参考 dsh 的 `cordis.patch.yml`，Peerdrive 的 patch 也是“行操作”：

```yaml
# 插入新行
- insert:
    - id: transport
      type: peerjs-transport
      config:
        enable: true
        host: 0.peerjs.com

# 覆盖已有行（整个 config 替换，last write wins）
- id: transport
  config:
    enable: true
    host: peersignal.moonchan.xyz
    key: pd-signal-b9447b406828e500

# 禁用
- id: legacy
  disabled: true
```

### 4.3 装配顺序

```
空根
  → bundle[base]    的 patch
  → bundle[storage] 的 patch
  → bundle[transport] 的 patch
  → ...
  → profile/peerdrive.patch.yml
  → $PEERDRIVE_HOME/peerdrive.patch.yml
  → --patch 覆盖层
```

后层对同一个 `id` 整体替换 config，实现“默认值来自 bundle，部署/本地值来自用户层”。

### 4.4 Profile 示例

```yaml
# profiles/web/peerdrive.yml
name: web
bundles:
  - base
  - auth
  - storage
  - sync
  - transport
  - discovery
  - web
```

```yaml
# profiles/node/peerdrive.yml
name: node
bundles:
  - base
  - storage
  - sync
  - transport
  - discovery
```

```yaml
# profiles/server/peerdrive.yml
name: server
bundles:
  - base
  - discovery          # 只启用信令/发现部分
```

### 4.5 启动过程伪代码

```go
func main() {
    p := harness.LoadProfile(flag.Profile)
    b := harness.NewBuilder()

    for _, name := range p.Bundles {
        bundle := bundles.Get(name)          // 编译期注册表
        if err := bundle.Build(b); err != nil { fatal(err) }
    }
    if err := harness.ApplyPatches(b, p.Patch,
        homePatch(), flag.Patches...); err != nil { fatal(err) }

    scope := harness.NewScope(b.Compose())
    for _, c := range scope.Components() {   // 按依赖服务可用性启动
        go c.Start(ctx, scope)
    }
    harness.WaitSignal(ctx)
}
```

---

## 5. 模块 Bundle 设计（草案）

### 5.1 base（基座，第一个 commit 就有的部分）

职责：
- `config` 加载与分层合并
- 日志
- SQLite 初始化 / 迁移入口
- Gin Router 空壳 + `/healthz` + `/api/node/info`
- 内容寻址存储（sha256 -> storage/xx/hash）
- node 身份（peer id / 实例 id）

不包含：认证、文件索引、P2P、前端。

### 5.2 auth

插入组件：
- `auth-service`：用户 / JWT / OAuth / permission
- `auth-middleware`：HTTP 鉴权中间件
- `usage-stats`：上传下载量统计（防谎报见 `认证功能.txt`）

### 5.3 storage

插入组件：
- `file-index`：`file_index` 表、sha256->path、seq
- `upload-session`：分片上传、位图、断点续传
- `download`：本地文件流式发送
- `collection`：匿名 + 用户 collection CRUD

### 5.4 sync

插入组件：
- `sync-service`：`sync{seq}` 增量拉取、`ApplySync` 合并、tombstone
- `sync-routes`：HTTP/WS/DataChannel 同步接口

### 5.5 transport

插入组件：
- `peerjs`：PeerJS 信令 + WebRTC DataChannel
- `ws-session`：本地 WS 会话（`/ws/peer`）
- `frame-protocol`：`req/meta/data/done/err` + `create/upload/list/info/delete/sync`
- `rtc-session`：WebRTC 会话适配

### 5.6 discovery

插入组件：
- `discovery-static`：`PEERDRIVE_PEERJS_PEERS`
- `discovery-mqtt`：分片房间发现
- `discovery-http`：自托管 `/discover/*`
- `signalserver`：`cmd/peerserver` 或 bundle 内服务

### 5.7 web

插入组件：
- `web-api`：API gateway 路由（依赖其他 bundle 已注册的路由）
- `web-static`：`front/dist` 静态服务
- `web-trust`：浏览器信任栅栏（Origin/Host 白名单）
- 前端 bundle 编译产物注入

### 5.8 legacy

插入组件：
- `libp2p`（旧 P2P）
- `bt-dht`（BT DHT）
- `ipfs-compat`（IPFS 兼容层）
- 默认 `disabled: true`，只在需要兼容旧功能时打开

---

## 6. 分支与 CI 流程（对应“今日最后要求”）

### 6.1 第一个 commit：基座

- 创建 `back/bundles/base` + `front/bundles/core`
- `profiles/node` + `profiles/web` 只挂 base（web 可挂 web 外壳但不含业务）
- CI：base 单测 + 启动健康检查
- 主分支保持可运行、可部署

### 6.2 每个模块一个分支

以 `feat/transport` 为例：

```
git checkout -b feat/transport
# back/bundles/transport/*
# front/bundles/transport/*
# profiles/web/peerdrive.yml 加入 transport
# profiles/node/peerdrive.yml 加入 transport
# 本模块测试：单元 + transport 集成
# 本分支 CI 必须绿
```

- 模块分支只允许修改：
  - 自己的 `bundles/<name>/`
  - 自己的 `front/bundles/<name>/`
  - profile manifest 中启用自己的行
  - 共享接口（`internal/harness`、`Scope`）只增不改
- 禁止直接改其他 bundle 的源码；需要协作时先合入主分支再改。

### 6.3 合并集成

- 每个模块分支合入 `main` 后，CI 跑 **全 profile 集成测试**：
  - `node` profile：base + storage + sync + transport + discovery
  - `web` profile：node + web
  - `server` profile：base + discovery
- 合并本身可能是纯 manifest 变更（在 `peerdrive.yml` 的 `bundles` 列表加一行），
  因此冲突面很小。

### 6.4 测试资产

每个 bundle 至少包含：
- Go 单元测试（不依赖外网）
- 集成测试（依赖真实信令/公共 broker 时打 `integration` tag）
- 前端测试（Vitest，仅测试自己的组件）
- 一个 `TEST-MATRIX.md` 或直接在 README 写清测试范围

---

## 7. 迁移路径（从现状到目标）

| 阶段 | 动作 | 验收 |
|---|---|---|
| M0 | 在 `back/internal/harness` 实现最小组合引擎，先支持 `base` | `peerdrive --profile node` 可启动空基座 |
| M1 | 把 `config.Load` 改成分层配置；`main.go` 的初始化按 bundle 拆到 `base/storage/sync/transport/discovery/web` | 现有功能等价，测试全绿 |
| M2 | 把 auth/legacy 分别收进 bundle；legacy 默认 disabled | 默认二进制不含 legacy 依赖或至少不启动 |
| M3 | front 按 bundle 整理，`front/dist` 由 web bundle 提供 | web profile 完整可用 |
| M4 | 新增 `profiles/` 和 `peerdrive --profile` 命令，旧 `server` 命令保留兼容 | 新入口与旧入口行为一致 |

建议迁移顺序参照 `doc/REFACTOR.md` 第 7 节：
先固定 `internal/harness` 的接口，再逐个把现有 service 包装成 component，
不要一次性大爆炸重构。

---

## 8. 环境变量与配置分层

dsh 用 patch 层管理配置，Peerdrive 同样要减少“散装 env”：

| 优先级（低→高） | 来源 |
|---|---|
| 1 | bundle 默认配置（`bundle.yml`） |
| 2 | profile 默认配置（`profiles/<name>/peerdrive.yml`） |
| 3 | 仓库 `peerdrive.patch.yml`（可提交） |
| 4 | `$PEERDRIVE_HOME/peerdrive.patch.yml`（本机，不提交） |
| 5 | `--patch` 命令行覆盖 |
| 6 | 环境变量 `PEERDRIVE_*`（部署紧急覆盖，保持兼容） |

环境变量继续保留，但建议只作为“最顶层覆盖”，不再承担默认值唯一的来源。

---

## 9. 关键决策与理由

1. **不用运行时插件加载**
   - Go 的动态插件（`plugin`）跨平台/构建链复杂，且与 CGO/SQLite 冲突。
   - 采用编译期注册表 + YAML profile，既得到组合能力，又保持单二进制部署。

2. **profile 不是 bundle**
   - bundle 是能力包，profile 是部署形态。
   - `web` profile 包含 `web` bundle；`node` profile 不包含，但二者都包含 `base`。

3. **前端 bundle 成对存在**
   - 一个功能如果同时有后端和前端，必须在同一分支/同一 bundle 名下。
   - 后端 bundle 提供 API，前端 bundle 提供 UI；集成时由 web profile 同时挂载。

4. **“行整包替换”刻意仿照 dsh**
   - 避免 patch 的 merge 语义导致“这里加一个 key、那里删一个 key”的隐式行为。
   - 每个组件把自己的完整配置写在一行里，覆盖者必须写出完整新配置，行为可预测。

---

## 10. 待办 / 后续设计细化

- [ ] 定义 `internal/harness` 的完整 Go API 与错误语义
- [ ] 定义 `bundle.yml` schema（insert/patch/disable 的 YAML 规范）
- [ ] 定义 bundle 间 service 依赖声明（dsh 用 inject，Peerdrive 可简化为 Scope 字段）
- [ ] 设计 `peerdrive module add <name>` 脚手架（生成 back/front bundle + test + CI 模板）
- [ ] 明确 legacy 包的编译隔离方式（build tag? 独立 Go module?）
- [ ] 与 `doc/REFACTOR.md` 的目标包结构合并，避免两套分层打架
