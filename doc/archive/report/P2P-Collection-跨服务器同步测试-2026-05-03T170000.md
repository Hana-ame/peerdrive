# Peerdrive P2P Collection 跨服务器同步测试

> 2026-05-03T17:00:00 · BWH → WSL P2P Collection 拉取成功

---

## 测试目标

验证 BWH 服务器上的匿名合集可以通过 P2P 网络被 WSL 服务发现并拉取。

## 测试环境

| 节点 | 角色 | API 地址 | Peer ID | P2P 模式 |
|------|------|----------|---------|----------|
| BWH | 数据源 | `https://bwh.moonchan.xyz:3000` | `12D3KooWSbj9NsY2bBY1BeorgWWmqZypjiqpEHTLNSVNMhWuMVRZ` | relay: server |
| WSL | 拉取端 | `https://wsl-3000.moonchan.xyz` | `12D3KooWRwbdNdQccHaxCutcxJHjsGQkk9YgZhyGoknNy7T7vAA9` | relay: client |

## 测试数据

### Sample Collections on BWH

```
5012f4407b9e76816687... | bwh-test.txt             | 1 entry  | p2p,bwh,test
e754f7ad2544bd5cdc61... | bwh-test.txt             | 1 entry  | p2p,bwh,test
7120aaa131ea3b9e3eb5... | hello.txt                | 1 entry
e1e85077c1b5746b880f... | Relay Test               | 1 entry  | relay,test
99a3e98be84c943c5617... | DHT Test                 | 1 entry
```

## 测试流程

### Step 1: BWH 创建测试文件并上传

```bash
echo "Peerdrive P2P cross-server test file from BWH" > /tmp/bwh-test.txt
curl -F "file=@/tmp/bwh-test.txt" http://localhost:3000/files/upload
# → {"hash":"58cd9276a0bd438666638c5fce6d1fcf1e45fbdb056636a38bfaa5af476cea8b"}
```

### Step 2: BWH 创建匿名合集

```bash
curl -X POST http://localhost:3000/anon/collections \
  -H "Content-Type: application/json" \
  -d '{"name":"BWH-P2P-Test","entries":[{"path":"bwh-test.txt","hash":"58cd9276..."}],"tags":["p2p","bwh","test"]}'
# → {"hash":"5012f4407b9e76816687e8e81b837d1d90f33754743714f5d88046504f9bdc08"}
```

### Step 3: BWH 在 P2P 网络宣布合集

```bash
curl -X POST http://localhost:3000/p2p/dual/announce \
  -H "Content-Type: application/json" \
  -d '{"hash":"5012f4407b9e76816687e8e81b837d1d90f33754743714f5d88046504f9bdc08"}'
# → {"status":"announced on both networks"}
```

### Step 4: WSL 通过 IPFS DHT 查找合集提供者

```bash
curl -X POST https://wsl-3000.moonchan.xyz/p2p/dual/find \
  -H "Content-Type: application/json" \
  -d '{"hash":"5012f4407b9e76816687e8e81b837d1d90f33754743714f5d88046504f9bdc08"}'
# → 找到 BWH peer (4 multiaddrs)
```

### Step 5: WSL 通过 P2P 拉取合集

```bash
curl -X POST https://wsl-3000.moonchan.xyz/p2p/fetch \
  -H "Content-Type: application/json" \
  -d '{"hash":"5012f4407b9e76816687e8e81b837d1d90f33754743714f5d88046504f9bdc08"}'
# → 返回完整合集数据 (entries + providers)
```

### Step 6: WSL 保存拉取的合集到本地

```bash
curl -X POST https://wsl-3000.moonchan.xyz/anon/collections \
  -H "Content-Type: application/json" \
  -d '{"name":"BWH-P2P-Test","entries":[...],"tags":["p2p","bwh","test"]}'
# → {"hash":"091c700fc491031dab6360a4a4bbc424ba3f192a79d00e7fc5cfcb855f54ae96"}
```

### Step 7: WSL 验证文件内容

```bash
curl https://wsl-3000.moonchan.xyz/sha256sum/58cd9276a0bd438666638c5fce6d1fcf1e45fbdb056636a38bfaa5af476cea8b
# → "Peerdrive P2P cross-server test file from BWH"
```

## 测试结果

| 步骤 | 操作 | 结果 |
|------|------|------|
| 1 | BWH 上传文件 | ✅ HTTP 200, 返回 hash |
| 2 | BWH 创建合集 | ✅ 合集创建成功 |
| 3 | BWH 双栈 announce | ✅ announced on both networks |
| 4 | WSL dual find | ✅ 找到 BWH peer (4 addrs) |
| 5 | WSL fetch collection | ✅ 完整合集数据返回 |
| 6 | WSL 保存合集 | ✅ 本地存储成功 |
| 7 | WSL 下载文件 | ✅ 文件内容一致 |

**结论**: P2P Collection 跨服务器同步功能正常工作。BWH 作为 relay server 宣告，WSL 作为 relay client 发现并拉取，文件内容端到端验证一致。
