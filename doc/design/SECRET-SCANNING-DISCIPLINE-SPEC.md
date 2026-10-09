# GitHub Secret Scanning 启用与测试占位串防误报规范

> **关联 Issue**: [#207](https://github.com/Hana-ame/peerdrive/issues/207)  
> **文档定位**: 确立 Peerdrive 代码库与测试用例中的凭据安全扫描标准，制定防范假阳性（False Positives）的测试占位串写法纪律与 Allowlist 规则。

---

## 1. 现状核实与扫描器机制

### 1.1 扫描器启用状态
经 GitHub API（`GET /repos/Hana-ame/peerdrive`）核验，本仓库安全扫描配置现状如下：
```json
{
  "secret_scanning": {
    "status": "enabled"
  },
  "secret_scanning_push_protection": {
    "status": "enabled"
  }
}
```
同时，CI 工作流（`.github/workflows/ci.yml` 及 PR 门禁）已全量集成 **GitGuardian Security Checks** 针对每次 commit 和 PR 的自动化凭据扫描。

### 1.2 误报产生的根本机制
GitHub Secret Scanning 与 GitGuardian 等工具主要依靠两类规则引擎判定敏感凭据：
1. **熵值分析 (Entropy Calculation)**：高信息熵的长随机字符串（如连续 20~40 位的十六进制或 base64）；
2. **前缀/特征正则匹配 (Regex Heuristics)**：
   - 通用模式如 `(?i)(secret|token|api_key|password|jwt)[-_=:]+['"][a-zA-Z0-9_-]{16,}['"]`；
   - 带有 `secret-` 或 `token-` 前缀的字符串字面量极易击中扫描引擎的高危启发式规则，从而产生假阳性拦截（阻断正常 push 或导致 CI 变红）。

---

## 2. 测试占位串编写纪律 (Discipline Matrix)

为了在保持测试断言有效性的同时彻底根绝扫描假阳性，所有 `*_test.go`、`*.test.mjs` 以及辅助脚本必须遵守以下占位串编写纪律：

| 场景类型 | ❌ 严禁使用（高危易误报） | ✅ 推荐使用（确定性无害） | 设计原理说明 |
|---|---|---|---|
| **API / Bearer 认证 Token** | `"secret-token"`<br>`"token-123456789"` | `"test-credential-placeholder"`<br>`"mock-auth-key-A"` | 消除 `secret-`、`token-` 敏感前缀，带有明确 `test-` / `mock-` 语义 |
| **测试载荷 / 模拟内容** | `[]byte("token-preserving-upload")` | `[]byte("upload-preserve-sample-payload")` | 纯业务测试文本避免混入凭据关键词 |
| **XOR 混淆种子 / 临时密钥** | `"secret-a"`, `"secret-b"` | `"test-xor-seed-a"`, `"test-xor-seed-b"` | 标明专用算法用途与测试属性 |
| **JWT / 签名 Secret** | `"secret-a"`, `"secret-b"` | `"test-jwt-key-a"`, `"test-jwt-key-b"` | 明确标示非真实私钥，避免启发式匹配 |
| **未公开 Hash / 共享标识** | `"secret-hash"` | `"test-unlisted-hash"` | 贴合 `doc/design/SHA-ACL-INFERENCE-SPEC.md` 的 `unlisted` 命名体系 |
| **长伪造字符串 (≥32 字符)** | 手写随机字符（如 `a8f9c71b4e2...`） | `"fake_" + strings.Repeat("a", 32)` | 统一降低信息熵，防止熵分析告警 |

---

## 3. Allowlist 策略与自定义排除规则

对确实需要测试真实校验逻辑（如检测有效 JWT 格式、测试公私钥解析失败等），必须采用文件级或模式级排除规则：

### 3.1 路径级排除规则 (`.github/secret_scanning.yml`)
在仓库根目录可通过 `.github/secret_scanning.yml` 配置路径豁免，确保扫描器不对测试用例目录过度敏感：
```yaml
paths-ignore:
  - '**/test/**'
  - '**/*_test.go'
  - '**/testdata/**'
  - 'doc/archive/**'
```

### 3.2 行内指纹标注
若某个测试字面量无法修改且被 GitGuardian 提示，应采用官方批准的注释指纹进行就地豁免，严禁禁用全局扫描器：
```go
// gitguardian:ignore-path
// 或在 PR/Dashboard 中标记为 "Test Credential / False Positive"
```

---

## 4. 文档示例与规范检查清单

1. **`doc/` 文档中的示例命令**：
   - 示例环境变量统一使用 `PEERDRIVE_PSK="test-shared-psk"`、`JWT_SECRET="test-development-secret"`；
   - 严禁在非 `archive` 正式文档中出现真实的生产密钥格式（如 `pd-signal-...` 只在真实部署文档中使用经脱敏的公共前缀）。
2. **提交前自动化保障**：
   - 开发者可使用 `git grep -E '"(secret|token)-[a-zA-Z0-9_-]+"' back/` 自检新代码是否引入高危占位串；
   - CI 中的 GitGuardian Security Checks 保持为即时安全反馈门禁。
