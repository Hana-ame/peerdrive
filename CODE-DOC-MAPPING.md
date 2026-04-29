# Peerdrive 代码-文档映射

> 每个 API 端点 ↔ Go handler 源码 ↔ 文档 的完整对应关系
> GitHub: [Hana-ame/peerdrive](https://github.com/Hana-ame/peerdrive)
> Go 源码分支: `feat/node-auth` | 文档分支: `master`

## 端点 → Handler 源码速查

### System
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/ping` | `Ping` | [ping.go:24](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/ping.go#L24) |

### Files
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/files` | `ListFiles` | [file.go:329](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L329) |
| POST | `/files/upload` | `UploadFile` | [file.go:41](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L41) |
| POST | `/files/register_local` | `RegisterLocalFile` | [file.go:129](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L129) |
| POST | `/files/register_url` | `RegisterURL` | [file.go:95](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L95) |
| POST | `/files/register_folder` | `RegisterFolder` | [file.go:153](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L153) |
| GET | `/files/verify/:hash` | `VerifyFile` | [file.go:176](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L176) |
| GET | `/files/browse` | `BrowseDir` | [file.go:345](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L345) |
| DELETE | `/files/:hash` | `DeleteFile` | [file.go:207](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L207) |
| POST | `/files/copy` | `CopyFile` | [file.go:230](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L230) |

### Downloads
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/sha256sum/:sha256` | `DownloadBySHA256` | [download.go:55](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go#L55) |
| GET | `/ipfs/:cid` | `DownloadByCID` | [download.go:130](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go#L130) |
| GET | `/download/:hash` | `UniversalDownload` | [download.go:263](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go#L263) |
| GET | `/download/:hash/sources` | `UniversalDownloadSources` | [download.go:286](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go#L286) |
| POST | `/download/:hash/refresh` | `UniversalDownloadRefresh` | [download.go:305](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go#L305) |

### Anonymous Collections
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| POST | `/anon/collections` | `CreateAnonCollection` | [anon.go:33](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go#L33) |
| GET | `/anon/collections` | `ListAnonCollections` | [anon.go:65](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go#L65) |
| POST | `/anon/collections/commit` | `CommitAnonCollection` | [anon.go:237](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go#L237) |
| GET | `/anon/collections/:hash` | `GetAnonCollection` | [anon.go:83](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go#L83) |
| GET | `/anon/collections/:hash/*filepath` | `DownloadAnonFile` | [anon.go:103](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go#L103) |
| POST | `/anon/collections/fork` | `ForkAnonCollection` | [anon.go:172](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go#L172) |

### User Collections
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| POST | `/collections` | `CreateCollection` | [collection.go:50](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L50) |
| GET | `/collections/public` | `ListPublicCollections` | [collection.go:525](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L525) |
| GET | `/collections/search` | `SearchCollections` | [collection.go:107](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L107) |
| GET | `/collections/:username` | `ListCollections` | [collection.go:86](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L86) |
| GET | `/collections/:username/:coll` | `GetCollection` | [collection.go:134](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L134) |
| POST | `/collections/:username/:coll/entries` | `AddEntry` | [collection.go:189](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L189) |
| DELETE | `/collections/:username/:coll/entries/*path` | `RemoveEntry` | [collection.go:232](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L232) |
| POST | `/collections/:username/:coll/commit` | `CommitCollection` | [collection.go:320](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L320) |
| GET | `/collections/:username/:coll/log` | `GetVersionLog` | [collection.go:403](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L403) |
| POST | `/collections/:username/:coll/rollback/:vid` | `RollbackCollection` | [collection.go:438](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L438) |
| POST | `/collections/:username/:coll/visibility` | `SetCollectionVisibility` | [collection.go:491](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L491) |
| POST | `/collections/:username/:coll/tags` | `UpdateCollectionTags` | [collection.go:565](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L565) |
| POST | `/collections/diff` | `DiffVersions` | [file.go:267](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L267) |
| GET | `/:username/:coll/*filepath` | `DownloadCollectionFile` | [collection.go:263](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go#L263) |

### Collaboration
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| POST | `/actions/merge` | `MergeFromSource` | [merge.go:41](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/merge.go#L41) |
| POST | `/actions/fork` | `ForkCollection` | [fork.go:32](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/fork.go#L32) |
| POST | `/actions/pull` | `PullCollection` | [fork.go:98](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/fork.go#L98) |

### P2P Network
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/p2p/status` | `P2PStatus` | [p2p.go:299](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L299) |
| GET | `/p2p/node` | `GetNodeInfo` | [p2p.go:100](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L100) |
| GET | `/p2p/peers` | `GetPeers` | [p2p.go:111](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L111) |
| GET | `/p2p/discovered` | `GetDiscoveredPeers` | [p2p.go:123](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L123) |
| GET | `/p2p/peers/detail` | `GetPeersDetail` | [p2p.go:588](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L588) |
| GET | `/p2p/peers/detail/:peer_id` | `GetPeerDetail` | [p2p.go:600](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L600) |
| GET | `/p2p/stats` | `GetP2PStats` | [p2p.go:617](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L617) |
| GET | `/p2p/connections` | `GetConnections` | [p2p.go:629](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L629) |
| GET | `/p2p/topology` | `GetTopology` | [p2p.go:656](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L656) |
| GET | `/p2p/quality` | `GetConnectionQuality` | [p2p.go:671](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L671) |
| GET | `/p2p/ping/:peer_id` | `PingPeer` | [p2p.go:142](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L142) |
| POST | `/p2p/connect` | `ConnectPeer` | [p2p.go:167](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L167) |
| POST | `/p2p/announce` | `AnnounceHash` | [p2p.go:187](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L187) |
| POST | `/p2p/fetch` | `FetchCollection` | [p2p.go:207](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L207) |
| POST | `/p2p/sync` | `SyncFromPeer` | [p2p.go:233](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L233) |
| POST | `/p2p/push` | `PushSync` | [p2p.go:341](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L341) |
| POST | `/p2p/request-file` | `RequestFile` | [p2p.go:398](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L398) |
| GET | `/p2p/ws/info` | `WSInfo` | [p2p.go:449](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L449) |
| GET | `/p2p/webrtc/info` | `WebRTCInfoHandler` | [p2p.go:1605](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1605) |
| POST | `/p2p/dual/announce` | `DualAnnounce` | [p2p.go:533](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L533) |
| POST | `/p2p/dual/find` | `DualFindProviders` | [p2p.go:558](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L558) |
| POST | `/p2p/download/resume` | `ResumeDownload` | [p2p_download.go:19](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go#L19) |
| GET | `/p2p/download/progress/:hash` | `DownloadProgress` | [p2p_download.go:59](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go#L59) |
| POST | `/p2p/download/cancel/:hash` | `CancelDownload` | [p2p_download.go:100](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go#L100) |
| POST | `/p2p/download/multipeer` | `MultiPeerDownload` | [p2p_download.go:129](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go#L129) |
| GET | `/p2p/download/sources/:hash` | `DownloadSources` | [p2p_download.go:169](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go#L169) |
| GET | `/p2p/download/multipeer/progress/:hash` | `MultiPeerProgress` | [p2p_download.go:215](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go#L215) |
| POST | `/p2p/forward/create` | `CreateForwardSession` | [p2p.go:1066](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1066) |
| POST | `/p2p/forward/connect` | `ConnectForwardSession` | [p2p.go:1100](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1100) |
| GET | `/p2p/forward/list` | `ListForwardSessions` | [p2p.go:1143](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1143) |
| POST | `/p2p/forward/close` | `CloseForwardSession` | [p2p.go:1165](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1165) |

### BitTorrent
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/bt/status` | `BTDHTStatus` | [p2p.go:461](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L461) |
| POST | `/bt/announce` | `BTAnnounce` | [p2p.go:476](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L476) |
| POST | `/bt/find` | `BTFindProviders` | [p2p.go:501](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L501) |
| POST | `/bt/bep44/put` | `BEP44Put` | [p2p.go:699](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L699) |
| POST | `/bt/bep44/get` | `BEP44Get` | [p2p.go:750](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L750) |
| GET | `/bt/bep51/sample` | `BEP51Sample` | [p2p.go:792](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L792) |
| POST | `/bt/dht/get` | `BTDHTGet` | [p2p.go:1536](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1536) |
| POST | `/bt/torrent` | `BTTorrentUpload` | [p2p.go:821](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L821) |
| POST | `/bt/magnet` | `BTMagnetResolve` | [p2p.go:868](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L868) |
| GET | `/bt/download/:infohash` | `BTDownloadProgress` | [p2p.go:907](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L907) |
| GET | `/bt/downloads` | `BTDownloadList` | [p2p.go:1048](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1048) |
| POST | `/bt/download/:infohash/pause` | `BTPauseDownload` | [p2p.go:926](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L926) |
| POST | `/bt/download/:infohash/resume` | `BTResumeDownload` | [p2p.go:945](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L945) |
| POST | `/bt/download/:infohash/seed` | `BTSeedTorrent` | [p2p.go:1010](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1010) |
| POST | `/bt/download/:infohash/unseed` | `BTStopSeed` | [p2p.go:1029](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1029) |
| DELETE | `/bt/download/:infohash` | `BTRemoveDownload` | [p2p.go:964](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L964) |
| GET | `/bt/stats` | `BTGlobalStats` | [p2p.go:983](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L983) |

### IPFS Compat
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/ipfs` | `IPFSCompatStatus` | [p2p.go:1230](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1230) |
| POST | `/ipfs/toggle` | `IPFSCompatToggle` | [p2p.go:1248](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1248) |
| POST | `/ipfs/pin/:cid` | `PinCID` | [p2p.go:1282](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1282) |
| DELETE | `/ipfs/pin/:cid` | `UnpinCID` | [p2p.go:1355](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1355) |
| GET | `/ipfs/pins` | `ListPins` | [p2p.go:1385](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1385) |
| GET | `/ipfs/gateways` | `IPFSGatewayStatus` | [p2p.go:1407](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1407) |
| POST | `/ipfs/dht/get` | `IPFSDHTGet` | [p2p.go:1568](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1568) |

### Node Identity
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/node/operator` | `GetNodeOperator` | [p2p.go:1449](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1449) |
| POST | `/node/register` | `RegisterNode` | [p2p.go:1460](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1460) |

### Auth & Shares
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| GET | `/p2p/auth/status` | `AuthStatus` | [p2p.go:1194](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1194) |
| POST | `/shares` | `CreateShare` | [share.go:14](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/share.go#L14) |
| GET | `/shares` | `ListShares` | [share.go:70](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/share.go#L70) |
| GET | `/s/:token` | `AccessShare` | [share.go:46](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/share.go#L46) |

### Local Sync & Tasks
| 方法 | 路径 | Handler | 源码 |
|------|------|---------|------|
| POST | `/local/save` | `SaveLocal` | [sync.go:19](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/sync.go#L19) (NewSyncController) |
| GET | `/local/status/:hash` | `GetStatus` | [sync.go:19](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/sync.go#L19) (NewSyncController) |
| GET | `/tasks` | `ListTasks` | [sync.go:95](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/sync.go#L95) |
| GET | `/tasks/:id` | `GetTaskStatus` | [sync.go:69](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/sync.go#L69) |

### WebSocket / Other
| 方法 | 路径 | 说明 | 源码 |
|------|------|------|------|
| GET | `/ws/transfer` | P2P 文件传输 WS | [p2p_ws.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_ws.go) |
| GET | `/ws/signal` | WebRTC 信令 WS | [signaling.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/signaling.go) |
| GET | `/relay/proxy` | Relay 代理下载 | [relay.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/relay.go) |
| ALL | `/webdav/*path` | WebDAV 挂载 | [webdav.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/webdav.go) |
| GET | `/swagger/*any` | Swagger UI | [router.go:410](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/router.go#L410) |

---

## Controller 源文件 → 文档

| 源文件 | 行数 | 功能 | 相关文档 |
|--------|------|------|----------|
| [p2p.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go) | ~1615 | P2P/BT/IPFS/Node/Forward/Dual/WebRTC handlers | [API-REFERENCE.md](spec/API-REFERENCE.md) §4-10, [modules/p2p/](modules/p2p/), [modules/bt/](modules/bt/), [modules/ipfs/](modules/ipfs/) |
| [p2p_download.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go) | ~250 | P2P 断点续传/多源下载 | [API-REFERENCE.md](spec/API-REFERENCE.md) §4 |
| [anon.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go) | ~290 | 匿名合集 CRUD + Fork + Commit | [API-REFERENCE.md](spec/API-REFERENCE.md) §3, [COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) |
| [collection.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go) | ~590 | 用户合集 CRUD + 版本管理 | [API-REFERENCE.md](spec/API-REFERENCE.md) §3, [COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) |
| [file.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go) | ~380 | 文件上传/注册/删除/Diff | [API-REFERENCE.md](spec/API-REFERENCE.md) §2, [upload.md](spec/backend/upload.md) |
| [download.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go) | ~330 | SHA256/CID/Universal 下载 | [API-REFERENCE.md](spec/API-REFERENCE.md) §2, [sha256-download.md](spec/backend/sha256-download.md) |
| [fork.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/fork.go) | ~120 | Fork + Pull | [API-REFERENCE.md](spec/API-REFERENCE.md) §3 |
| [merge.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/merge.go) | ~100 | 三路合并 | [API-REFERENCE.md](spec/API-REFERENCE.md) §3 |
| [auth.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/auth.go) | ~50 | Auth controller 工厂 | [modules/auth/](modules/auth/) |
| [share.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/share.go) | ~90 | 分享链接 | [API-REFERENCE.md](spec/API-REFERENCE.md) §12 |
| [sync.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/sync.go) | ~100 | 本地同步 + 任务状态 | [API-REFERENCE.md](spec/API-REFERENCE.md) §13, §16 |
| [ping.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/ping.go) | ~30 | 健康检查 | [API-REFERENCE.md](spec/API-REFERENCE.md) §1 |

## Service 源文件 → 文档

| 源文件 | 功能 | 相关文档 |
|--------|------|----------|
| [p2p.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p.go) | libp2p 主机 + DHT + 流处理 | [modules/p2p/p2p.md](modules/p2p/p2p.md) |
| [file_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/file_service.go) | 文件存储 + URL 解析 + MIME 嗅探 | [modules/storage/](modules/storage/) |
| [anon_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/anon_service.go) | 匿名合集创建/验证/Provider 处理 | [COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) |
| [ipfs_compat.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/ipfs_compat.go) | IPFS Bitswap + CID 转换 | [modules/ipfs/](modules/ipfs/) |
| [universal_downloader.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/universal_downloader.go) | 多协议下载编排 | [API-REFERENCE.md](spec/API-REFERENCE.md) §2 |
| [p2p_resume.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_resume.go) | 断点续传管理 | [API-REFERENCE.md](spec/API-REFERENCE.md) §4 |
| [p2p_multipeer.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_multipeer.go) | 多源并行下载 | [API-REFERENCE.md](spec/API-REFERENCE.md) §4 |
| [p2p_dual.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_dual.go) | IPFS+BT 双栈宣告/查找 | [dual-stack-protocol.md](modules/p2p/dual-stack-protocol.md) |
| [signaling.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/signaling.go) | WebRTC 信令 Hub | [webrtc-architecture.md](modules/ipfs/webrtc-architecture.md) |
| [forward.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/forward.go) | P2P 端口转发 | [API-REFERENCE.md](spec/API-REFERENCE.md) §10 |
| [auth_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/auth_service.go) | JWT 验证 + 注册服务器通信 | [modules/auth/](modules/auth/) |
| [webdav.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/webdav.go) | WebDAV 文件系统 | [API-REFERENCE.md](spec/API-REFERENCE.md) §14 |
| [relay.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/relay.go) | Relay 代理 | [API-REFERENCE.md](spec/API-REFERENCE.md) §15 |
| [peer_tracker.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/peer_tracker.go) | 对等节点状态追踪 | [modules/p2p/](modules/p2p/) |
| [peer_scanner.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/peer_scanner.go) | 主动节点发现 | [modules/p2p/](modules/p2p/) |
| [downloader.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/downloader.go) | 基础下载器接口 | [sha256-download.md](spec/backend/sha256-download.md) |
| [sync_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/sync_service.go) | 本地同步逻辑 | [API-REFERENCE.md](spec/API-REFERENCE.md) §13 |
| [nodestate/nodestate.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/nodestate/nodestate.go) | 节点运营者状态 | [API-REFERENCE.md](spec/API-REFERENCE.md) §8 |

## Router 入口

| 源文件 | 说明 |
|--------|------|
| [router.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/router.go) | 全部路由注册 (~510行)，含中间件、relay bootstrap、BT完成回调 |
| [auth_middleware.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/auth_middleware.go) | JWT 认证中间件 |

## BT DHT 独立模块

| 源文件 | 功能 | 文档 |
|--------|------|------|
| [p2p_bt/bt_dht.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/p2p_bt/bt_dht.go) | Mainline DHT 节点 | [bt-dht-protocol.md](modules/bt/bt-dht-protocol.md) |
| [p2p_bt/bep44.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/p2p_bt/bep44.go) | BEP44 不可变存储 | [API-DESIGN.md](modules/bt/API-DESIGN.md) |
| [p2p_bt/bt_client.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/p2p_bt/bt_client.go) | BT 下载客户端 | [API-DESIGN.md](modules/bt/API-DESIGN.md) |

## 文档 → 源文件

| 文档 | 源文件 |
|------|--------|
| [spec/API-REFERENCE.md](spec/API-REFERENCE.md) | [router.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/router.go), [p2p.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go), [file.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go), [download.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go), [anon.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go), [collection.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go), [fork.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/fork.go), [merge.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/merge.go), [share.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/share.go), [sync.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/sync.go) |
| [API-USAGE.md](API-USAGE.md) | 同上 + [modules/](modules/) 各模块 README |
| [spec/COLLECTION-LOGIC.md](spec/COLLECTION-LOGIC.md) | [collection.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/collection.go), [anon.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/anon.go), [anon_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/anon_service.go), [collection_repo.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/repository/collection_repo.go) |
| [spec/backend/BACKEND_DOC.md](spec/backend/BACKEND_DOC.md) | [main.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/cmd/server/main.go), [config.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/config/config.go), [router.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/router.go) |
| [spec/backend/database.md](spec/backend/database.md) | [db.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/repository/db.go) |
| [spec/backend/upload.md](spec/backend/upload.md) | [file.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/file.go#L41), [file_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/file_service.go) |
| [spec/REQUIREMENTS.md](spec/REQUIREMENTS.md) | 全部 `internal/**/*.go`, `react/src/**/*.jsx` |
| [spec/USER-ROLES.md](spec/USER-ROLES.md) | [auth_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/auth_service.go), [auth_middleware.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/auth_middleware.go) |
| [modules/p2p/](modules/p2p/) | [p2p.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p.go), [p2p_connection.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_connection.go), [p2p_transfer.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_transfer.go), [p2p_ws.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_ws.go), [p2p_helpers.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_helpers.go), [peer_tracker.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/peer_tracker.go), [peer_scanner.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/peer_scanner.go) |
| [modules/bt/](modules/bt/) | [p2p_bt/*.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/p2p_bt/) (11 files) |
| [modules/ipfs/](modules/ipfs/) | [ipfs_compat.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/ipfs_compat.go), [signaling.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/signaling.go) |
| [modules/storage/](modules/storage/) | [file_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/file_service.go), [file_repo.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/repository/file_repo.go) |
| [modules/auth/](modules/auth/) | [auth_service.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/auth_service.go), [auth_middleware.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/router/auth_middleware.go) |
| [testing/TEST-MATRIX.md](testing/TEST-MATRIX.md) | `go/test/*.sh`, `go/*_test.go` |
| [testing/TEST-PIPELINE.md](testing/TEST-PIPELINE.md) | `go/test/*.sh` |
| [testing/TESTING-HANDBOOK.md](testing/TESTING-HANDBOOK.md) | `go/test/*.sh` |

---

> 更新: 2026-04-29 · Go 源码分支 `feat/node-auth` · 文档分支 `master`
