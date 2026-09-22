# Peerdrive 教程

> 面向「想把这个项目跑起来用」的人，按章节推进；每一章都能独立跑完并自己验证。
> 详细设计与踩坑记录不在这里，需要时看 `doc/NETDISK.md` · `doc/REFACTOR.md` · `doc/NODE.md`。

| 章节 | 内容 | 需要编译吗 |
|---|---|---|
| [第一章：如何运行并连接自己的节点](01-run-and-connect.md) | 下载 release → 起信令 → 起节点 → 用面板 / 管理台连上它 → 连不上怎么查 | ❌ 下载二进制即可 |
| [第二章：从源码编译](02-build-from-source.md) | 什么时候必须自己编 · 编译节点/信令 · 一键端到端脚本 · 构建面板与管理台 · 跑测试 · 自己出发布包 | ✅ |

> ⚠️ 仓库里另有一份 `doc/TUTORIAL.md`，写于 2026-05，讲的是当时的 libp2p 链路
> （`p2p-test`、multiaddr、bootstrap peer）。**那些内容已随 libp2p 栈删除而失效**
> （2026-08-16 全删，见 `doc/LEGACY.md`）。当前互联层是 PeerJS + WebRTC，
> 请以本目录为准。
