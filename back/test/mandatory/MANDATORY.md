# mandatory 测试类别

> 本文件是 mandatory 类别的策略定义。修改本文件的语义同样需要人工核验。

## 是什么

mandatory 是 peerdrive 里**唯一不允许 AI agent 自行修改的测试类别**。

它和普通测试的差别不在写法，在**治理**：

| | 普通测试 | mandatory 测试 |
|---|---|---|
| 跑法 | `go test ./...` | `go test -tags "nosqlite mandatory" ./test/mandatory/` |
| CI gate | `backend` job，自动 | `mandatory` job，**需人工 Approve 环境** |
| 改动谁批 | 代码 review 即可 | 必须人工核验，且要同步更新 `manifest.sha256` |
| 被改动的可发现性 | 静默 | 哈希不匹配 → CI 红 |
| AI agent 能否自行修改 | 可以 | **不可以** |

## 一次性人工设置（首次启用必做）

`mandatory` job 需要 GitHub Environment 的 `Required reviewers` 才能真正形成「人工核验」
关口。**这一步必须由人在 UI 里做**，agent 用 REST API 设置被拒
（`PUT /environments/mandatory-tests/required_reviewers` 返回 404，本仓库的
environment protection rules 无法通过 API 写）。

路径：**Settings → Environments → `mandatory-tests` → Add protection rule →
Required reviewers → 选人 → Save**。environment 本身已经建好，只差这一条规则。

在此之前 `mandatory` job 会正常跑但**不需要批准**，只有 `manifest` job 在提供
防篡改保障。

## 哈希的语义：内容，不是字节布局

**哈希按 LF 归一化后计算** —— `CRLF` 先折叠成 `LF` 再算 sha256。

为什么必须有这一步：防篡改的语义应该是「**内容**没变」，不该被 checkout 的
行尾策略绑架。Windows 贡献者用 `autocrlf=true`、或 GitHub Actions 的 Windows
runner 把文本文件 checkout 成 CRLF 时，内容一字未动但字节全变，逐字节哈希
就会全线误报。

2026-10-26 第一次在 `go-build.yml` 的 `windows-latest` 格子上跑就命中了这个：
**6 个条目全部 hash mismatch**。加了归一化之后 Linux/Windows 判定一致；
而且真实篡改仍然会被检出（归一化后再比，剩下的差异只能是真实内容），
失败信息还会顺带打印「文件里 N 个 CRLF；说明差异不是行尾」，省掉下一轮排查。

## 什么该进这个类别

只放**关掉就会出事、且弱化时看起来完全无害**的断言：

- 安全默认（`SSRF 闸门默认开着`、`JSON body 上限默认开着`）
- 用户显式钉住的行为契约（`invariant`）
- 破坏性变更的防线（`file_index 是唯一真相源`、`镜像默认开启保证可回滚`）

**不要**放进去：
- 常规单元测试 —— 普通套件已经覆盖，进这个类别只会稀释它
- 性能/时序相关（flaky，会训练出「红了就跳过」的坏习惯）
- 需要外部网络或人工准备数据的验收

## 现在有哪些

| 文件 | 钉住什么 |
| | |
| `test/mandatory/security_defaults_mandatory_test.go` | 安全默认：SSRF 闸门默认拒绝 12 个内网/元数据目标；URL scheme 白名单；`NewSafeClient` 一定带 Transport + Timeout；`MaxJSONBodyBytes` 默认 > 0 |
| `internal/transport/peerjs_fetch_mandatory_test.go` | **产品的根**：对端经 P2P 连接发 `req` 帧、传 sha256，必须取回真实文件的字节（整文件 + 分片两个用例） |
| `internal/transport/visibility_matrix_mandatory_test.go` | **权限语义**：公/不列出/私有三档在「列不列」和「给不给」上的分工——陌生人清单只见 public；unlisted 不列出但可取；private 陌生人拒、好友给；不存在的 hash 拒；PSK 是准入不是文件私钥；合集 PSK 未带 token 必须锁住条目 |

> 注意第二条不在 `test/mandatory/` 目录下 —— mandatory 类别是**由 build tag + manifest 定义**的，
> 不绑定目录。这条测试要用 `internal/transport` 的包内符号（`serveFile`、`dispatchFrame`、
> `fileIndex`）才能真正端到端，所以它就住在被测包的旁边；manifest 是它进这个类别的凭证。

## 怎么加一条

```bash
# 1. 写测试，文件名以 _mandatory_test.go 结尾，文件头必须带：
#    //go:build mandatory
#    // 🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS: ...

# 2. 生成哈希，追加到 manifest
cd back
sha256sum test/mandatory/你的文件_mandatory_test.go >> test/mandatory/manifest.sha256

# 3. 两端都跑一遍
go test -tags nosqlite ./test/mandatory/ -run TestMandatory_ManifestIntegrity -count=1 -v
go test -tags "nosqlite mandatory" ./test/mandatory/ -count=1 -v
```

## 怎么改 / 删一条

**必须由人做**。步骤和上面一样，但 PR 里要写清楚为什么原有的 invariant 不再成立。

AI agent 不得自行编辑以下任何内容：
- `test/mandatory/*_mandatory_test.go`（除新增）
- `internal/transport/peerjs_fetch_mandatory_test.go` —— 也是 mandatory 成员，只是住在被测包旁边
- `internal/transport/visibility_matrix_mandatory_test.go` —— 同上，权限语义的 mandatory 成员
- `test/mandatory/manifest.sha256`
- `test/mandatory/verify_test.go`
- `test/mandatory/MANDATORY.md`
- `.github/workflows/mandatory.yml`

## 本机制的边界（诚实声明）

哈希只能证明「文件没变」，不能证明「改动是合法的」。以下情形本机制挡不住：

1. **同一个 PR 里同时改测试 + 改 manifest** —— 哈希会匹配，检查通过。
   挡这一层的不是哈希，是 `mandatory-tests` 环境的 `required_reviewers`：
   那个 job 需要人工点 Approve，reviewer 就是这一层的核验人。
   而这个 workflow 文件本身也在 manifest 里，所以「把 gate 删掉」也会被检出。
2. **把整个目录删掉** —— `go test` 会报「package not found」而不是哈希失败，
   但 `.github/workflows/mandatory.yml` 仍在 manifest 里，那条会红。
3. **agent 在本地改了文件但不提交** —— 工作区层面无法强制，只能靠 review。

所以准确的说法是：**mandatory 类别把「改动是否合法」从代码问题变成了一道
需要人签字的关口**，而不是用哈希把篡改完全封死。

## 相关文件

- `test/mandatory/verify_test.go` —— 看门狗，默认套件每次都会跑
- `test/mandatory/manifest.sha256` —— 哈希清单，防篡改的唯一凭证
- `.github/workflows/mandatory.yml` —— 两个 CI gate
- `test/user_invariants/` —— 用户更早建立的 invariant 测试，已在 manifest 里受保护
  （尚未迁到 `//go:build mandatory` 标签下；是否迁移待人工决定）
