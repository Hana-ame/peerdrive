# Peerdrive P2P 协议测试矩阵

> 2026-05-03

## 协议

P2P 层上有两个 libp2p stream handler，没有独立端口：

| Protocol ID | 用途 | 状态 |
|-------------|------|------|
| `/peerdrive/collections/list/1.0.0` | 查询对端 Collections 列表及存在性 | ✅ 已实现 |
| `/peerdrive/exchange/1.0.0` | 按 SHA256 查询/下载文件 | ✅ 已实现 |

---

## 1. Collections List — `/peerdrive/collections/list/1.0.0`

### 1.1 列出所有公开 Collections

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| L-01 | 空查询列出所有公开合集 | 发送 `\n` (空 query) | `OK <size>\n` + JSON，`user_collections` 全部 visibility=public | 每条 visibility 字段为 "public" |
| L-02 | 对端无公开合集 | 向空节点发送 `\n` | `OK <size>\n` + `{"user_collections":[],"anon_collections":[]}` | JSON 空数组 |
| L-03 | 对端有匿名合集无用户合集 | 发送 `\n` | `anon_collections` 非空, `user_collections` 可能空 | anon 列表展示全部存储的匿名合集 |
| L-04 | 响应 JSON 结构完整性 | 解析所有字段 | id, username, collection_name, current_hash, visibility, tags, created_at | 字段类型正确，无遗漏 |
| L-05 | 大响应不截断 | 对端有 100+ 合集 | 完整接收全部 JSON | JSON 解析后 length 匹配 OK 声明 |

### 1.2 按名称查询 Collection

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| L-06 | 按 collection_name 模糊查询 | 发送 `my-coll\n` | 返回 collection_name 包含 "my-coll" 的合集 | LIKE 语义：返回匹配条目 |
| L-07 | 按 username 模糊查询 | 发送 `alice\n` | 返回 username 包含 "alice" 的合集 | 用户名部分匹配 |
| L-08 | 组合匹配 (name + user) | 发送 `test\n` | username 或 collection_name 任一匹配即返回 | OR 语义 |
| L-09 | 查询无匹配 | 发送 `zzzznonexistent\n` | 空结果 `user_collections:[]` | 不返回 ERR |
| L-10 | 查询特殊字符 | 发送 `test@#$%\n` | 正确处理，无 SQL 注入风险 | 不崩溃，返回空或安全结果 |
| L-11 | 长查询字符串 | 发送 1000 字符 query | 截断或正常处理，不崩溃 | 服务端稳定 |

### 1.3 检查 Collection 存在性

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| L-12 | 已知存在合集 | 查询已知 collection_name | 结果数 > 0 | 精确匹配 |
| L-13 | 不存在合集 | 查询随机不存在的名称 | 结果数 = 0 | 判断非 ERR |
| L-14 | 空字符串查询判存在 | 发送 `\n` 并判断 count > 0 | 若对端有任意公开合集则 count > 0 | 布尔判断 |

### 1.4 可见性过滤扩展 (待实现)

> 当前 **仅返回 visibility=public** 的集合。未来需要支持按可见性过滤，
> 允许查询 unlisted 和 private 级别（需认证）。

| 测试ID | 测试项 | 预期结果 |
|--------|--------|----------|
| L-15 | 请求指定 visibility=unlisted | 返回 unlisted 合集 |
| L-16 | 请求指定 visibility=private (有权限) | 返回 private 合集 |
| L-17 | 请求指定 visibility=private (无权限) | 返回 ERR 或仅含公开结果 |
| L-18 | 请求 visibility=all (有权限) | 返回全部可见性 |
| L-19 | 无认证请求 private → 只返回 public | 权限隔离 |

### 1.5 边界/安全

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| L-20 | 未连接到对端就查询 | 不 connect 直接 NewStream | 连接错误 | 返回 error，不 panic |
| L-21 | 对端断开后查询 | connect → 对端断开 → 再查询 | stream 错误 | 超时或连接重置 |
| L-22 | 查询超大 payload | 构造超大 JSON 响应 (如有) | OK 声明与实际 size 一致 | 读满 size 字节 |
| L-23 | ERR 格式响应 | 若对端返回 `ERR xxx\n` | 客户端识别 ERR 前缀 | 不尝试读 JSON |

---

## 2. Exchange — `/peerdrive/exchange/1.0.0`

### 2.1 文件查询

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| E-01 | 查询存在的文件 (小) | 发送 `<64hex>\n`，已知 <1KB | `OK <size>\n<data>` | data 的 SHA256 匹配请求 hash |
| E-02 | 查询存在的文件 (大) | 发送 `<64hex>\n`，已知 >10MB | `OK <size>\n<data>` | 完整接收，SHA256 匹配 |
| E-03 | 查询不存在的 hash | 发送 `000...001\n` | `ERR not found\n` | 错误前缀 ERR |
| E-04 | 无效 hash 长度 (短) | 发送 `abc\n` | `ERR invalid hash length ...\n` | 错误消息含长度信息 |
| E-05 | 无效 hash 长度 (长) | 发送 128 位 hex | `ERR invalid hash length ...\n` | 同上 |
| E-06 | 空请求 | 发送 `\n` | `ERR bad request\n` 或 `ERR invalid hash length 0\n` | 不崩溃 |
| E-07 | 特殊字符 | 发送 `not-a-hex!\n` | `ERR invalid hash length ...\n` | 按长度校验 |

### 2.2 SIZE 命令

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| E-08 | 查询存在文件的 SIZE | 发送 `SIZE <64hex>\n` | `OK <size>\n` | size 为正整数，等于文件实际大小 |
| E-09 | 查询不存在文件的 SIZE | 发送 `SIZE 000...001\n` | `ERR not found\n` | 错误响应 |
| E-10 | SIZE 无效 hash | 发送 `SIZE abc\n` | `ERR invalid hash length ...\n` | 长度校验 |
| E-11 | SIZE 边界 (0 字节文件) | 发送 `SIZE <empty-file-hash>\n` | `OK 0\n` | size=0 |

### 2.3 可靠性与性能

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| E-12 | 并发下载多个文件 | goroutine 并发请求不同 hash | 全部成功且内容匹配 | SHA256 逐一验证 |
| E-13 | 大文件超时保护 | 请求 >100MB 文件，设置短超时 | 读取部分后超时，不 OOM | 客户端控制内存 |
| E-14 | 二进制数据完整性 | 请求二进制文件 (.bin, .zip) | `OK <size>\n<data>` 中 data 包含所有字节包括 null | 完整读取 size 字节 |
| E-15 | 重复请求相同 hash | 连续 2 次请求同一 hash | 两次响应相同 | data 完全一致 |

---

## 3. 连接层测试

| 测试ID | 测试项 | 操作 | 预期结果 | 验证方式 |
|--------|--------|------|----------|----------|
| N-01 | 直连已知对端 | `/ip4/<IP>/tcp/<port>/p2p/<peerID>` | connect 成功 | no error |
| N-02 | 通过中继连接 | `/ip4/<relay>/tcp/<port>/p2p/<relayID>/p2p-circuit/p2p/<targetID>` | connect 成功 | relay 转发 |
| N-03 | 连接无效地址 | `/ip4/1.2.3.4/tcp/9999/p2p/<randomID>` | 连接超时/拒绝 | 合理时间内返回错误 |
| N-04 | 连接后发送协议 | connect → NewStream | stream 打开成功 | 可读写 |
| N-05 | 多个顺序请求 | 同一连接串行多次 collections/list + exchange | 全部成功 | 不受上一次影响 |
| N-06 | 对端版本不兼容 | 连接无此 handler 的对端 | stream 拒绝/协议不支持 | 不 panic |

---

## 4. 测试拓扑

```
┌──────────────────────────────────────┐
│  p2p-test (本工具)                    │
│  ─────────────                       │
│  裸 go-libp2p host                   │
│  无 DHT / 无 Relay / 无 Storage      │
│  仅 Stream 层协议交互                 │
│                                      │
│  CLI 参数:                            │
│    --peer  目标 peer multiaddr        │
│    --op    操作 (list/query/exist/    │
│             get/size)                 │
│    --query 查询字符串                  │
│    --hash  SHA256 hex                 │
│    --out   下载输出路径               │
└──────────┬───────────────────────────┘
           │ libp2p stream
           ▼
┌──────────────────────────────────────┐
│  目标 Peerdrive 节点                  │
│  ────────────────                     │
│  Handler: /peerdrive/collections/list │
│  Handler: /peerdrive/exchange         │
└──────────────────────────────────────┘
```

## 5. 快速运行

```bash
# 列出对端所有公开合集
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op list

# 按名称查询
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op query --query "test"

# 判断集合是否存在
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op exists --query "my-collection"

# 获取文件
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op get --hash <64hex> --out /tmp/downloaded

# 查询文件大小
go run -tags nosqlite ./cmd/p2p-test/main.go \
  --peer "/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
  --op size --hash <64hex>
```
