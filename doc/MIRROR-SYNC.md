# 独立子模块镜像同步与状态盘点（Independent Module Mirror Sync Audit）

> 依据：`doc/REFACTOR.md` §3.14 及 Issue #105。
> 本仓包含独立 `go.mod` 子模块，主仓通过 `replace` 指向本地目录。外部消费者通过独立仓库获取模块依赖。

---

## 1. 独立模块基线与映射关系

| 独立模块仓库 | 本仓对应目录 | 模块路径 (`go.mod`) | 基线 Tag | 状态 |
|---|---|---|---|---|
| `github.com/Hana-ame/go-peerjs` | `back/peerjs/` | `github.com/Hana-ame/go-peerjs` | `v0.1.0` | 待推送增量镜像至独立仓库 |
| `github.com/Hana-ame/go-peerserver` | `back/signalserver/` | `github.com/Hana-ame/go-peerserver` | `v0.1.0` | 待推送增量镜像至独立仓库 |

---

## 2. 自 `v0.1.0` 基线以来的增量提交盘点

### 2.1 `back/peerjs/` (`github.com/Hana-ame/go-peerjs`)

自基线 `v0.1.0` 以来，`back/peerjs/` 目录在主仓的演进提交：
1. `859eca6` — `fix(peerjs): wait for fake server client in test harness`（修复测试桩竞态）
2. `79f764a` — `feat(peerjs): XOR-encrypt DataChannel data plane (default off)`（数据面轻量混淆支持）
3. `4ad1bf0` — `refactor(peerjs): 信令传输拆到 signalling 子包，独立测试 + 等价性验证`（信令层重构与拆分）
4. `1352fe2` — `refactor(signal): extract shared signaling frame into back/signalframe`
5. `ebd353f` — 信令 key 权威默认值收敛与对齐
6. `d2d837d` — 注释与文档国际化翻译规范落地
7. `524533f` — 默认公共信令服务配置更新

### 2.2 `back/signalserver/` (`github.com/Hana-ame/go-peerserver`)

自基线 `v0.1.0` 以来，`back/signalserver/` 目录在主仓的演进提交：
1. `859eca6` — `fix(peerjs): wait for fake server client in test harness`
2. `edb5d89` / `1cdb037` — BitTorrent HTTP tracker 服务与公共 tracker 注入
3. `65da196` / `4ad1bf0` — 信令拆分与测试
4. `e618f87` — `refactor(signalserver): split signalserver.go into focused modules by functional area`
5. `1352fe2` — extract shared signaling frame
6. `00c0a98` / `f17491e` / `aa5188e` — 信令 key 安全防泄露与限流端点补齐
7. `bfeca03` / `dadfc45` / `e6b3ee3` — 注册 ID 保留名校验与 ops token 门禁
8. `6a22ed5` / `c669cf5` — CORS 配置与名单端点权限收紧
9. `d2d42df` — 统一 module 名为 `go-peerserver`
10. `d2d837d` — 注释与文档英文翻译规范落地

---

## 3. 镜像同步执行流程（Mirror Sync Procedure）

当需要将主仓的子模块目录同步至对应独立 Git 仓库时，遵循以下规范流程：

### 3.1 预备检查
- 确保本仓所有单元测试与交叉编译通过：
  - `cd back/peerjs && go test ./...`
  - `cd back/signalserver && CGO_ENABLED=0 go test ./...`
- 严禁移动或覆盖已发布的 `v0.1.0` 语义 tag。

### 3.2 使用 `git subtree` 或子目录提取推送
以 `go-peerjs` 为例：
```bash
# 1. 添加独立仓库远端
git remote add mirror-peerjs git@github.com:Hana-ame/go-peerjs.git

# 2. 将 back/peerjs 分支推送到独立仓库主干
git subtree push --prefix=back/peerjs mirror-peerjs main

# 3. 在独立仓库打增量 release tag（例如 v0.2.0）
# 保持 v0.1.0 不动
```

以 `go-peerserver` 为例：
```bash
# 1. 添加独立仓库远端
git remote add mirror-peerserver git@github.com:Hana-ame/go-peerserver.git

# 2. 将 back/signalserver 分支推送到独立仓库主干
git subtree push --prefix=back/signalserver mirror-peerserver main
```

### 3.3 验证外部依赖
验证在干净目录下拉取独立包可独立编译：
```bash
go get github.com/Hana-ame/go-peerjs@<new-tag>
```

---

## 4. 边界与纪律
- 主仓 `back/go.mod` 保持 `replace` 本地相对路径，确保主仓开发与 CI 零网络摩擦；
- 独立模块自身零内嵌对 `peerdrive/internal` 的非法引用。
