# 模块 01：config 配置模块

- **代码位置**：`back/internal/config`
- **功能一句话**：进程启动时一次性从环境变量（`PEERDRIVE_*` 系列及 `PORT`）读取全部运行配置为只读 `*Config`，并做「快速失败」的启动期校验（`back/internal/config/config.go:1-3`、`back/internal/config/config.go:186-246`、`back/internal/config/config.go:254-282`）。
- **依赖**：仅 Go 标准库 `fmt`/`os`/`runtime`/`strconv`/`strings`（`back/internal/config/config.go:5-11`），无任何第三方依赖、不读写文件、不依赖数据库。
- **被依赖**（均经 `back/cmd/server/main.go` 注入）：
  - `back/cmd/server/main.go:52-71`：`config.Load()` + `config.Validate(cfg)`（校验失败 `stdlog.Fatalf`）+ 卷根检查/`os.Root` 探测（main 包内实现，作用在 config 值上）；
  - `back/internal/router`：`SetupRouter(cfg)`（`back/internal/router/router.go:44`）、`SetPeerJSConfig(cfg)`（`back/internal/router/peerjs_routes.go:50-52`）；
  - `back/internal/transport`：`NewPeerJSService(cfg, storageDir)`（`back/internal/transport/peerjs_service.go:113`）、PSK 门禁读 `cfg.PeerPSK`（`back/internal/transport/psk.go:54,66`）、跨节点拉取上限读 `cfg.MaxUploadBytes`（`back/internal/transport/pull.go:79-80`）；
  - `back/internal/service`：`AnonService{config: cfg}`（`back/internal/service/anon_service.go:25-28`）、`NewNodeShare(cfg, storageDir)`（`back/cmd/server/main.go:134`）、`NewNodeDirectory(storageDir, cfg.DiscoverURL)`（`back/cmd/server/main.go:120`）、`FileService` 上传限额/目录深度（`back/internal/service/file_service.go:309,836-841`）；
  - `back/internal/controller`：`WebRTCInfoHandler(cfg)`（`back/internal/controller/webrtc.go:12-23`）；
  - `back/internal/source`：URL 源模板经 main 注册（`back/cmd/server/main.go:224-228`）；
  - 测试：`back/test/integration/*`、`back/internal/.../*_test.go`（构造 cfg 供被测组件）。

## 1. 逻辑

**职责**：配置的唯一装配点。包注释明确写出「从环境变量加载全部配置项（端口、存储目录、PeerJS/WebRTC、BT DHT、转发等）。`Load()` 读取 `PEERDRIVE_*` 系列环境变量并返回 `*Config`」（`back/internal/config/config.go:1-3`）。全部事实以真实代码为据，无配置文件、无配置中心。

**核心类型**：

- `Config` 结构体（`back/internal/config/config.go:25-149`）：扁平的约 40 个字符串/布尔/数值字段，覆盖监听端口、DB 路径、存储目录、CORS 白名单、上传限额、BT DHT、IPFS 网关、WebRTC STUN/TURN、PeerJS 信令、PSK、MQTT、节点发现、URL 源模板、下载、转发规则、HTTP 加固、共享范围。每个字段注释都标注对应环境变量名与默认值（默认值注释见 `back/internal/config/config.go:27,51-59,72-76,94,99,102-125` 等）。
- 默认常量（`back/internal/config/config.go:13-23`）：`DefaultSignalHost`/`DefaultSignalPort`/`DefaultSignalKey`/`DefaultDiscoverURL`——项目公共信令的默认值，带历史教训注释：早先默认 PeerJS 公共云 `0.peerjs.com/peerjs` 导致「照默认跑」的节点与面板分属两个信令而互相找不到（`back/internal/config/config.go:14-16`）。

**主要流程**：

1. `Load()`（`back/internal/config/config.go:186-246`）：逐字段 `getEnv*` 读取环境变量并填默认值，返回全新的 `*Config`。
2. `Validate()`（`back/internal/config/config.go:254-282`）：启动期校验，把「配错了但不会报错」的配置一次性挡下；聚合全部错误后一次返回（`back/internal/config/config.go:278-281`）。
3. 调用流（`back/cmd/server/main.go:52-71`）：`cfg := config.Load()` → `config.Validate(cfg)`（非 nil 即 `stdlog.Fatalf`）→ `checkUnsafeRoots(cfg)`（卷根配置拒绝，main 包实现，`back/cmd/server/main.go:316-333`）→ `warnUnsupportedRoots(cfg)`（`os.Root` 安全边界探测，`back/cmd/server/main.go:340-354`）。
4. 只读分发（`back/cmd/server/main.go:73-241`）：`cfg.DBPath` → `repository.InitDB`；`cfg.StorageDir` → Gin Context/存储；`cfg.PeerJSEnable` 为真时 `NewPeerJSService(cfg, storageDir)`；`router.SetPeerJSConfig(cfg)`/`SetupRouter(cfg)`；`cfg.URLSourceTemplate` 非空才注册 URL 源（`back/cmd/server/main.go:224-228`）。

**辅助读取函数语义**（`back/internal/config/config.go:284-329`）：`getEnv` 未设置→默认值；`getEnvBool` 用 `strconv.ParseBool`；`getEnvInt`/`getEnvInt64` 要求解析成功且值 `> 0`；`getEnvFloat` 要求解析成功且值 `>= 0`；任何解析失败都**静默回退默认值**。

**方法**：

- `IsOriginAllowed`（`back/internal/config/config.go:151-176`）：CORS 与 WS 本地会话的 Origin 白名单判定。支持精确匹配（大小写不敏感）、`*` 全放行、`*.example.com` 与 `https://*.example.com` 子域名通配；`AllowedOrigins` 为空或 `*` 时放行一切（`back/internal/config/config.go:153-154`）。行为测试见 `back/internal/config/config_test.go:67-106`。
- `DefaultRootPath`（`back/internal/config/config.go:178-184`）：Windows 返回 `C:\`，其他平台返回 `/`。

**生命周期**：进程级单例——`main` 局部变量 `cfg` 构造后经参数/注入传给各模块，运行期只读（生产代码中未发现对 `cfg` 字段的写入；测试内局部赋值除外，如 `back/internal/transport/share_test.go:25`）。「运行时可改」的只有下游 NodeShare 的共享范围运行时状态（`back/internal/service/nodeshare.go` 文件头注释），不是本模块。

## 2. 如何储存

**不持久化——纯内存态**。储存介质 = 进程环境变量（`os.LookupEnv`，`back/internal/config/config.go:284-289`）→ 内存中的 `*Config` 结构体。`Load()` 每次启动从零构造（`back/internal/config/config.go:186-246`）；本包**没有任何写出路径**：无配置文件、不写数据库、不落盘（整个 config 包只有 `os.LookupEnv`/`os.Getenv` 读取，唯一启动后再读环境变量的点在 main 包的逃生阀检查，`back/cmd/server/main.go:317`）。

**内存态构成与生命周期**：

- 唯一实例是 `back/cmd/server/main.go:52` 的 `cfg` 局部变量；同一 `cfg` 被引用/传参给各模块：
  - `PeerJSService.cfg`（`back/internal/transport/peerjs_service.go:120`，`s.cfg == cfg` 同一指针）；
  - `AnonService.config`（`back/internal/service/anon_service.go:25-28`）；
  - router 包级 `peerjsCfg`（`back/internal/router/peerjs_routes.go:22,50-52`）与 `SetupRouter(cfg)`（`back/internal/router/router.go:44`）；
  - 部分字段被拆开传参：`cfg.DBPath`、`cfg.DownloadDir`（`back/cmd/server/main.go:75-77,167`）等。
- 生命周期与进程一致：重启后 `os.LookupEnv` 重新读取环境变量，配置不跨重启保留任何状态。

**下游持久化边界（委托关系）**：共享范围运行时状态由 `service.NodeShare` 写入 `storageDir` 下的 `share_scope.json`（`back/cmd/server/main.go:132-134` 注释「环境变量只是首次启动的初值」；`back/internal/service/nodeshare.go` 文件头注释）——那是 **service 模块自身的持久化**，config 只提供「首次启动初值」（`PEERDRIVE_SHARE_*`，`back/internal/config/config.go:127-148`），不参与写入。

## 3. 何时储存

本模块是「加载」而非「储存」：值只在进程启动时被装配一次，之后不再与外部同步。具体时机：

1. **进程启动最开始**：`main()` 里日志之后立即 `cfg := config.Load()`（`back/cmd/server/main.go:50-52`）——环境变量只在这一个点被批量读取。
2. **紧随其后的启动校验**（同一启动流程）：`config.Validate(cfg)` 失败即 `Fatalf` 退出（`back/cmd/server/main.go:55-57`）；main 包对配置目录做卷根检查与 `os.Root` 探测（`back/cmd/server/main.go:68-71`）。
3. **装配期**（仍在启动流程内）：DB 初始化读取 `cfg.DBPath`（`back/cmd/server/main.go:75-77`）；PeerJS 服务创建、信令拨号参数、自动互联对端、发现链路读取各字段（`back/internal/transport/peerjs_service.go:113-133,190-233,257-282`）；路由注册 `SetupRouter(cfg)` 读取中间件参数（`back/internal/router/router.go:44,51,57,90,112`）。
4. **运行期**：无定时任务、无写回、无热更新。但「读」是逐请求发生的，且都来自同一内存结构、不再查环境变量：CORS 中间件每请求调 `cfg.IsOriginAllowed(origin)`（`back/internal/router/router.go:90`）；WS 本地会话建立时 `peerjsCfg.IsOriginAllowed`（`back/internal/router/peerjs_routes.go:171-174`）；限流中间件每请求读 `RateLimitRPS`（`back/internal/router/router.go:51`）。
5. **唯一的例外**：`main.checkUnsafeRoots` 在启动检查时才读 `PEERDRIVE_ALLOW_UNSAFE_ROOT` 环境变量（`back/cmd/server/main.go:317`）。
6. **运行期想改配置的正确路径**：共享范围走管理台/PUT `/peerjs/share` 修改 NodeShare 运行时状态（写 `share_scope.json`），改环境变量**不会**回灌已选范围（`back/internal/service/nodeshare.go` 文件头注释）；其余配置项修改必须重启进程。

## 4. 储存什么

`Config` 结构体字段清单（按 `Load()` 装配顺序，`back/internal/config/config.go:186-246`；字段定义及注释 `back/internal/config/config.go:25-149`）：

| Config 字段 | 环境变量 | 默认值 | 约束 / 说明 |
|---|---|---|---|
| `Port` | `PORT`（注意：**无** `PEERDRIVE_` 前缀） | `"3000"` | 监听端口；Validate 要求 1-65535（`config.go:189,257-259`） |
| `DBPath` | `PEERDRIVE_DB_PATH` | `./peerdrive.db` | SQLite 元数据库路径；Validate 要求非空（`config.go:27,190,260-262`） |
| `StorageDir` | `PEERDRIVE_STORAGE` | `./storage` | 存储目录；Validate 要求非空（`config.go:29,191,263-265`） |
| `StorageEnable` | `PEERDRIVE_STORAGE_ENABLE` | `true` | 内容寻址存储开关（`config.go:30,192`） |
| `AllowedOrigins` | `PEERDRIVE_ALLOWED_ORIGINS` | `http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev,https://*.pages.dev` | 逗号分隔；空串/`*` 全放行，支持子域通配（`config.go:33,193,151-176`） |
| `PublicAccessDomain` | `PEERDRIVE_PUBLIC_DOMAIN` | `""` | **已加载但全仓库被测代码无消费方（未核实用途，疑似遗留/规划字段，`config.go:33,194`）** |
| `RegistrationServer` | `PEERDRIVE_REG_SERVER` | `""` | 非空时启用 Bearer 注册服务器鉴权中间件（`config.go:34,195`；`router.go:112-115`） |
| `NodeAuthToken` | `PEERDRIVE_AUTH_TOKEN` | `""` | 节点持久身份 token；**已加载但全仓库被测代码无消费方（未核实，`config.go:35,196`）** |
| `RegServerURL` | `PEERDRIVE_REG_SERVER_URL` | `""` | **已加载但全仓库被测代码无消费方（未核实，疑似与 `RegistrationServer` 重复的遗留字段，`config.go:43,197`）** |
| `MaxUploadBytes` | `PEERDRIVE_MAX_UPLOAD_BYTES` | `100*1024*1024`（100MB） | 注释「0 = unlimited」（`config.go:45,198`）；跨节点拉取上限经 `pullMaxBytes` 兜底（`pull.go:29,79-80`），认证用户上传上限（`file_service.go:836-841`） |
| `MaxUploadBytesAnon` | `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` | `10*1024*1024`（10MB） | 匿名用户上传上限（`config.go:46,199`） |
| `BTDHTEnabled` | `PEERDRIVE_BT_DHT_ENABLE` | `false` | 默认禁用：DHT 初始化阻塞启动（`config.go:37,200`） |
| `BTDHTListenAddr` | `PEERDRIVE_BT_DHT_LISTEN` | `:6881` |（`config.go:38,202`） |
| `IPFSGatewayEnable` | `PEERDRIVE_IPFS_GATEWAY_ENABLE` | `true` |（`config.go:40,203`） |
| `IPFSGateways` | `PEERDRIVE_IPFS_GATEWAYS` | `https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link` | 逗号分隔（`config.go:41,204`） |
| `WebRTCSTUNServer` | `PEERDRIVE_WEBRTC_STUN` | `stun:stun.l.google.com:19302` |（`config.go:48,206`；`controller/webrtc.go:14-19`） |
| `WebRTCTURNServer` | `PEERDRIVE_WEBRTC_TURN` | `""` | 空则不返回 turn_server（`config.go:49,207`） |
| `PeerJSEnable` | `PEERDRIVE_PEERJS_ENABLE` | `true` |（`config.go:51,209`） |
| `PeerJSHost` | `PEERDRIVE_PEERJS_HOST` | `DefaultSignalHost`（`peersignal.moonchan.xyz`） | 默认指向项目自建公共信令（`config.go:17-18,54,210`；空值在 `peerjs_service.go:195-197` 兜底） |
| `PeerJSPort` | `PEERDRIVE_PEERJS_PORT` | `443` | Validate：非空时要求 1-65535（`config.go:55,211,269-273`） |
| `PeerJSKey` | `PEERDRIVE_PEERJS_KEY` | `DefaultSignalKey`（`pd-signal-b9447b406828e500`） |（`config.go:20,56,212`） |
| `PeerJSID` | `PEERDRIVE_PEERJS_ID` | `""` | 空则生成 `peerdrive-<随机hex>`（`config.go:57,213`；`peerjs_service.go:114-117`） |
| `PeerJSSecure` | `PEERDRIVE_PEERJS_SECURE` | `true` |（`config.go:58,214`） |
| `PeerJSPeers` | `PEERDRIVE_PEERJS_PEERS` | `""` | 逗号分隔对端 peer id，启动自动互联（`config.go:59,215`；`peerjs_service.go:226-233`） |
| `PeerPSK` | `PEERDRIVE_PSK` | `""` | 空=开放模式；验「知不知道密钥」不验「是谁」（`config.go:61-70,216`；`transport/psk.go:3-29`） |
| `MQTTEnable` | `PEERDRIVE_MQTT_ENABLE` | `false` | MQTT 分片房间发现开关（`config.go:72,218`） |
| `MQTTBroker` | `PEERDRIVE_MQTT_BROKER` | `tcp://broker.emqx.io:1883` |（`config.go:73,219`） |
| `MQTTTopicPref` | `PEERDRIVE_MQTT_TOPIC_PREFIX` | `peerdrive/v1` |（`config.go:74,220`） |
| `MQTTCollections` | `PEERDRIVE_MQTT_COLLECTIONS` | `""` | 逗号分隔关注的 collection hash 分片（`config.go:75,221`；`peerjs_service.go:390`） |
| `DiscoverURL` | `PEERDRIVE_DISCOVER_URL` | `DefaultDiscoverURL`（`https://peersignal.moonchan.xyz`） | 自托管信令发现 API（announce + 节点列表）；设置后优先于 MQTT（`config.go:22,76,222`；`peerjs_service.go:257-272`） |
| `DiscoverPresence` | `PEERDRIVE_DISCOVER_PRESENCE` | `true` | 节点级「存在房间」发现；开=发现服务端与同房间节点可见本节点在线与 peerId，关=仅共享集合可见（隐私取舍，`config.go:78-85,223`；`peerjs_service.go:370`） |
| `URLSourceTemplate` | `PEERDRIVE_URL_SOURCE_TEMPLATE` | `""` | 空则不注册 url source；`%s`=sha256 hash，含 `%d`（依次 offset,size）声明 `CapStream`，否则 `CapFile`（`config.go:87-91,224`；`main.go:224-228`） |
| `DownloadDir` | `PEERDRIVE_DOWNLOAD_DIR` | `./downloads` | 下载/落盘目录；Validate 要求非空（`config.go:93,226,266-268`） |
| `FolderMaxDepth` | `PEERDRIVE_FOLDER_MAX_DEPTH` | `0` | `0=不限制`；**注意**：结构体注释写「默认 1=只扫当前目录」（`config.go:94`），而 `Load()` 实际默认 `0=不限制`（`config.go:227`），`>0` 才限深（`file_service.go:309-310`）——以实际代码为准，注释疑似过期 |
| `MaxPeers` | `PEERDRIVE_MAX_PEERS` | `8` | Validate 要求 > 0（`config.go:95,228,274-276`；`peerjs_service.go:359-362`） |
| `DownloadOrder` | `PEERDRIVE_DOWNLOAD_ORDER` | `local,ipfs,ipfsgw,btdht,http` | 下载源顺序（`config.go:96,235`；`router.go:203-204`） |
| `DownloadTimeoutSecs` | `PEERDRIVE_DOWNLOAD_TIMEOUT` | `30` |（`config.go:97,236`；`router.go:200`） |
| `ForwardRules` | `PEERDRIVE_FORWARD_RULES` | `""` | 格式 `key1:8080,key2:8443`；key 即凭证（服务端 HMAC 验证），配置建议 chmod 600；语法解析在 main（坏条目仅 Warn 并忽略，`config.go:99,238`；`main.go:194-213`） |
| `RateLimitRPS` | `PEERDRIVE_RATE_LIMIT_RPS` | `30` | 每 IP 请求速率上限，0=不限（`config.go:102-105,240`；`router.go:51`） |
| `DisableCSP` | `PEERDRIVE_CSP` | `== "off"` 时关 | **特殊字符串哨兵**而非布尔解析（`config.go:106-108,241`） |
| `DisableSwagger` | `PEERDRIVE_SWAGGER` | `== "off"` 时关 | 同上；默认开会把全部端点与参数结构公开（`config.go:109-111,242`；`router.go:387`） |
| `Host` | `PEERDRIVE_HOST` | `""` | 空=监听所有网卡；管理面无账号体系，`Host` 即边界（`config.go:112-117,243`；`main.go:238-241`） |
| `TrustedProxies` | `PEERDRIVE_TRUSTED_PROXIES` | `""` | 逗号分隔 IP/CIDR；空=只认 `RemoteAddr`；gin 默认信任所有代理，XFF 可伪造（`config.go:119-125,244`；`router.go:55-73`） |
| `ShareEnable` | `PEERDRIVE_SHARE_ENABLE` | `false` | 共享总开关，默认关（防隐私事故，`config.go:129-132,230`；`nodeshare.go:252`） |
| `ShareCollections` | `PEERDRIVE_SHARE_COLLECTIONS` | `""` | 64hex 合集 hash 或 `"all"`=所有 public 合集；restricted/private 合集会被跳过（`config.go:133-136,231`） |
| `ShareDirs` | `PEERDRIVE_SHARE_DIRS` | `""` | 对外共享目录，路径前缀匹配；越权读由 `file_index.IsPathAllowed` 兜底（`config.go:137-141,232`；`main.go:106-108`） |
| `ShareFriends` | `PEERDRIVE_SHARE_FRIENDS` | `""` | 好友节点 ID，private 内容放行；peer id 对端自报，仅 PSK 准入连接上才有意义；「只是运行时状态的初值，之后在管理台改」（`config.go:142-148,233`） |

**读取语义**（`back/internal/config/config.go:284-329`）：

| 函数 | 语义 |
|---|---|
| `getEnv` | 未设置→默认值；已设置→原样字符串 |
| `getEnvBool` | `strconv.ParseBool` 成功才用，否则回退默认 |
| `getEnvInt` / `getEnvInt64` | 解析成功**且 `> 0`** 才用，否则回退默认（无法显式设 0） |
| `getEnvFloat` | 解析成功**且 `>= 0`** 才用，否则回退默认 |
| `DisableCSP` / `DisableSwagger` | `os.Getenv("PEERDRIVE_CSP") == "off"` / `os.Getenv("PEERDRIVE_SWAGGER") == "off"`（`config.go:241-242`） |

**Validate 启动期校验清单**（`back/internal/config/config.go:254-282`；只覆盖以下几项，非全量）：

- `PORT` 必须为 1-65535 的整数（`config.go:257-259`）；
- `PEERDRIVE_DB_PATH` / `PEERDRIVE_STORAGE` / `PEERDRIVE_DOWNLOAD_DIR` TrimSpace 后非空（`config.go:260-268`）；
- `PEERDRIVE_PEERJS_PORT` 非空时须为 1-65535 的整数（`config.go:269-273`）；
- `PEERDRIVE_MAX_PEERS` 必须为正数（`config.go:274-276`）；
- 错误聚合成一条带 `  - ` 前缀的多行错误一次返回（`config.go:278-281`）。

## 5. 边界与坑

- **快速失败原则**（`back/internal/config/config.go:248-253`）：环境变量是字符串，拼错一个字符不会让进程失败，只会让行为跑偏（`PORT=300o` → 监听失败；`PEERDRIVE_STORAGE=` 空 → 文件落进当前工作目录），所以启动期一次说清。但 **Validate 只覆盖上表 4 项**，`PeerJSHost`/`PeerJSKey`/`PeerPSK`/`Share*`/`AllowedOrigins` 等不校验。
- **Validate 之外的容错式检查**（在 main/router，配错不阻断）：转发规则坏条目仅 `LogWarn` 并忽略（`back/cmd/server/main.go:198-208`）；`PEERDRIVE_TRUSTED_PROXIES` 坏值仅 Warn（`back/internal/router/router.go:67-68`）。
- **getEnv 系列的静默回退**（`back/internal/config/config.go:291-329`）：解析失败一律回退默认值且不报错——这正是 Validate 存在的原因。注意整型项无法显式设 0（`n > 0` 才生效，`config.go:304,324`），`FolderMaxDepth=0` 与未设置效果相同（都是默认 0=不限制）；float 项允许 0（`RateLimitRPS=0` = 不限，`config.go:314`）。
- **字符串哨兵**：`DisableCSP`/`DisableSwagger` 只在值**恰为** `"off"` 时生效（`config.go:241-242`），`"OFF"`/`"0"` 均无效。
- **默认信令必须保持项目公共信令这对常量**（`back/internal/config/config.go:13-16`）：历史教训——曾默认 PeerJS 公共云导致节点与面板互相找不到；`peerjs_service.go:195-203` 对 `Host`/`Port`/`Key` 的空值再兜底一次常量。
- **`AllowedOrigins` 空或 `*` = 全放行**（`back/internal/config/config.go:153-154`）：默认值已含 `*.pages.dev` 子域通配（为 Cloudflare Pages 预览部署，`config_test.go:64-66,90-93`）。
- **`Host` 默认空 = 监听所有网卡**：管理面 `/ws/peer` 无账号体系，「谁能连到这个端口」就是边界，同局域网可当管理员（`config.go:112-117`；`main.go:238-241`）。
- **`TrustedProxies` 空 = 只认 `RemoteAddr`**：gin 默认信任所有代理、`ClientIP()` 取可伪造的 `X-Forwarded-For`，限流与日志里的 IP 全由攻击者填；反代后必须显式配信任的那一跳（`config.go:119-125`；`router.go:55-73`）。
- **`PeerPSK` 的边界**：验「对端知不知道这个密钥」不是「对方是谁」，无身份、无授权分级，所有持钥者对节点同等访问权；好友名单（`ShareFriends`）在无 PSK 时会被自报的 peer id 冒充（`config.go:61-70,142-148`；`transport/psk.go:3-29`；`nodeshare.go` 文件头注释）。
- **`ShareEnable` 默认关**：开共享等于对外公开内容清单（`config.go:129-131`）；`ShareDirs` 仅路径前缀匹配，真正的越权读由 `file_index.IsPathAllowed` 兜底（`config.go:138-141`）。
- **卷根配置启动拒绝**：`PEERDRIVE_STORAGE`/`PEERDRIVE_DOWNLOAD_DIR`/`PEERDRIVE_SHARE_DIRS` 配成 `/`（Windows `C:\`）时启动 `Fatalf` 拒绝，逃生阀 `PEERDRIVE_ALLOW_UNSAFE_ROOT=1`（`back/cmd/server/main.go:61-70,287-333`）；`warnUnsupportedRoots` 探测 `os.Root` 支持，目录首次不存在时只会 Info 提示自动创建（`back/cmd/server/main.go:340-354`）。
- **`FolderMaxDepth` 注释矛盾**：结构体注释写「默认 1=只扫当前目录」（`config.go:94`），`Load()` 实际默认 `0=不限制`（`config.go:227`），`> 0` 才限深（`file_service.go:309-310`）——以实际代码为准（未核实哪一方是作者本意）。
- **三个无消费方字段**：`PublicAccessDomain`/`NodeAuthToken`/`RegServerURL` 在 `config.go:33,35,43` 定义并加载（`config.go:194,196-197`），全仓库被测代码均无消费方（未核实用途，疑似遗留/规划中）。
- **环境变量启动后不可改**：共享范围想改必须走管理台/PUT `/peerjs/share`（写 `share_scope.json`），改环境变量不会回灌（`back/internal/service/nodeshare.go` 文件头注释）。
- **`BTDHTEnabled` 默认关**：DHT 初始化阻塞启动（`config.go:200`）；`DisableSwagger` 默认开 = 公网部署等于送攻击地图（`config.go:109-111`）。

## 6. 对外连接

- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md)：`cfg.AllowedOrigins` 经 router CORS 中间件（`back/internal/router/router.go:90`）与 WS 本地会话 Origin 白名单（`back/internal/router/peerjs_routes.go:171-174`、`SetPeerJSConfig` 注入）约束前端来源；方向 config → router → 前端 WS 会话。
- [../connections/02-router-controller.md](../connections/02-router-controller.md)：`SetupRouter(cfg)` 用 `RateLimitRPS`/`TrustedProxies`/`DisableCSP`/`RegistrationServer`/`Host` 等装配中间件与控制器（`back/internal/router/router.go:44-119`）；方向 config → router 装配参数 → 控制器。
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md)：`NewPeerJSService(cfg, storageDir)` 消费 `PeerJSEnable/Host/Port/Key/Secure/ID/Peers/PSK/STUN/TURN/DownloadDir/MaxPeers` 等（`back/internal/transport/peerjs_service.go:113-133,190-233,359-362`、`back/internal/transport/psk.go:54,66`）；方向 config → transport（PeerJS 信令 + WebRTC 参数）。
- [../connections/08-transport-signalserver.md](../connections/08-transport-signalserver.md)：`DiscoverURL`/`DiscoverPresence`/`MQTTEnable`/`MQTTBroker`/`MQTTTopicPref`/`MQTTCollections` 驱动自托管信令发现 API 与 MQTT 房间发现（`back/internal/transport/peerjs_service.go:257-282,370,390`）；方向 config → transport 发现链路（自托管信令/MQTT broker）。
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md)：前端获得本节点 peer id（`/peerjs/node`，`back/internal/router/peerjs_routes.go:78-90`）与 ICE 配置（`GET /p2p/webrtc/info`，`back/internal/router/router.go:242`、`back/internal/controller/webrtc.go:12-23`）后直连信令——这些端点值与前端最终指向的信令都源于同一份 cfg（`PeerJSKey`/STUN/TURN 等）；方向 config → 后端 HTTP 下发 → 前端 → 信令。
- 关联说明：[../connections/13-media-node-ech.md](../connections/13-media-node-ech.md) 与本模块**无依赖**：`back/cmd/media-node/main.go` 不 import `internal/config`，媒体节点走 `ech.InitDefault(ech.Config{ProxyURL: proxyURL})`（`back/cmd/media-node/main.go:178`），ech 包自带配置缓存，不属于本配置模块。