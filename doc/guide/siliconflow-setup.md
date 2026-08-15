# Silicon Flow 接入指南

> 最后更新: 2026-04-27

## 概述

Peerdrive 项目使用 Silicon Flow（硅基流动）作为 LLM 后端，通过 `siliconflow.moonchan.xyz` 反向代理访问，用于 AI 合集命名等功能。

涉及三个组件：
| 组件 | 路径 | 用途 |
|------|------|------|
| anthropic-sf-proxy | `/mnt/d/WorkPlace/anthropic-sf-proxy/` | Claude Code → Silicon Flow 协议转换 |
| siliconflow.moonchan.xyz | Cloudflare 反向代理 | 代理 `api.siliconflow.cn`，避免直连 |
| Peerdrive Settings | `react/src/pages/Settings.jsx` | 前端 LLM 端点/模型/Key 配置 |

---

## 一、siliconflow.moonchan.xyz 反向代理

### 用途

Peerdrive 前端直接调用 `https://siliconflow.moonchan.xyz/v1/chat/completions` 进行 AI 合集命名（LLMAssistant、AnonCreator 的 🤖 按钮）。

### 原理

Cloudflare 上配置的反向代理，将 `siliconflow.moonchan.xyz` 的请求转发到 `api.siliconflow.cn`。前端无需直连硅基流动，避免跨域和 Key 暴露问题。

### Peerdrive 中的使用

**LLM 端点配置**（`react/src/api.js:143`）：
```js
const DEFAULT_LLM_ENDPOINT = 'https://siliconflow.moonchan.xyz';
```

**调用方式**（`react/src/pages/AnonCreator.jsx:7-16`）：
```js
const LLM_URL = api.getLlmEndpoint() || 'https://siliconflow.moonchan.xyz';
const LLM_CHAT = `${LLM_URL}/v1/chat/completions`;

async function llmSuggest(names) {
  const res = await fetch(LLM_CHAT, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model: 'Qwen/Qwen3-8B',
      messages: [{ role: 'user', content: `请用3-5个中文字为以下文件集取一个简洁的合集名称: ${names}` }],
      max_tokens: 20,
      stream: false,
    }),
  });
  return res.json().choices?.[0]?.message?.content?.trim();
}
```

**LLMAssistant 函数调用**（`react/src/components/LLMAssistant.jsx`）：
- 使用相同的 OpenAI 兼容 `/v1/chat/completions` 端点
- 支持 SSE 流式输出
- 有 17 个 function call 工具（导航、搜索、集合操作、P2P 状态等）
- 最多 5 轮工具调用循环
- 支持 AbortController 中断

### 前端配置页面

`/settings` 页面（`react/src/pages/Settings.jsx`）提供完整的 LLM 配置面板：

| 配置项 | localStorage Key | 默认值 |
|--------|-----------------|--------|
| Endpoint | `peerdrive_llm_endpoint` | `https://siliconflow.moonchan.xyz` |
| Model | `peerdrive_llm_model` | `Qwen/Qwen3-8B` |
| API Key | `peerdrive_llm_apikey` | 空 |
| Body 模板 | `peerdrive_llm_body_template` | OpenAI 兼容 JSON |

**Body JSON 模板默认值**：
```json
{
  "model": "Qwen/Qwen3-8B",
  "messages": [],
  "stream": true,
  "max_tokens": 4096,
  "temperature": 0.7
}
```

模板中的 `model` 和 `messages` 会被运行时替换，其余参数（`stream`、`max_tokens`、`temperature`）可自定义。

---

## 二、免费模型列表

在 Settings → LLM 配置 → 模型下拉中可选的 13 个免费模型：

| 模型 | 系列 | 说明 |
|------|------|------|
| `Qwen/Qwen3-8B` | 通义千问 3 | 推荐，中文能力强 |
| `Qwen/Qwen3.5-4B` | 通义千问 3.5 | 轻量 |
| `Qwen/Qwen2.5-7B-Instruct` | 通义千问 2.5 | 稳定版 |
| `deepseek-ai/DeepSeek-R1-0528-Qwen3-8B` | DeepSeek R1 | 推理增强 |
| `deepseek-ai/DeepSeek-R1-Distill-Qwen-7B` | DeepSeek R1 Distill | 蒸馏推理 |
| `deepseek-ai/DeepSeek-OCR` | DeepSeek OCR | 文字识别 |
| `THUDM/GLM-4-9B-0414` | 智谱 GLM-4 | 通用对话 |
| `THUDM/GLM-Z1-9B-0414` | 智谱 GLM-Z1 | 推理增强 |
| `THUDM/GLM-4.1V-9B-Thinking` | 智谱 GLM-4V | 多模态思考 |
| `tencent/Hunyuan-MT-7B` | 腾讯混元 | 机器翻译 |
| `internlm/internlm2_5-7b-chat` | 书生浦语 | 通用对话 |
| `PaddlePaddle/PaddleOCR-VL` | 百度 PaddleOCR | 视觉文字识别 |
| `PaddlePaddle/PaddleOCR-VL-1.5` | 百度 PaddleOCR 1.5 | 视觉文字识别新版 |

硅基流动注册: https://cloud.siliconflow.cn/i/sRO0U8o0

---

## 三、Anthropic-SF-Proxy（Claude Code 代理）

### 用途

让 Claude Code（Anthropic Messages API）通过 Silicon Flow 模型工作。

### 仓库

`/mnt/d/WorkPlace/anthropic-sf-proxy/`

### 协议转换

Claude Code → Anthropic Messages API → Proxy → OpenAI Chat Completions → Silicon Flow

| 转换 | Anthropic | OpenAI |
|------|-----------|--------|
| 端点 | `/v1/messages` | `/v1/chat/completions` |
| 系统提示 | 顶层 `system` | `messages[0] role=system` |
| 内容 | `content` 数组 | `content` 字符串/多模态数组 |
| 工具定义 | `input_schema` | `function.parameters` |
| 工具调用 | `tool_use` 块 | `tool_calls[]` |
| 工具结果 | `tool_result` 块 | `role=tool` |
| 停止原因 | `stop_reason` | `finish_reason` |
| 流式 | `content_block_delta` | `choices[0].delta.content` |

### 安装

```bash
cd /mnt/d/WorkPlace/anthropic-sf-proxy
pip install -r requirements.txt   # FastAPI + httpx
cp .env.example .env              # 填入 SILICONFLOW_API_KEY=sk-xxx
python server.py                  # 监听 0.0.0.0:8080
```

### Claude Code 配置

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080/v1
export ANTHROPIC_API_KEY=any-value
export NO_PROXY=localhost,127.0.0.1
```

然后用 `/model` 指定 Silicon Flow 模型名。

### 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/messages` | 消息接口（支持 stream） |
| POST | `/v1/messages/count_tokens` | Token 估算（启发式） |
| GET | `/health` | 健康检查 |

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SILICONFLOW_API_KEY` | — | Silicon Flow API Key（必需） |
| `SILICONFLOW_BASE_URL` | `https://api.siliconflow.cn/v1` | API 地址 |
| `PROXY_PORT` | `8080` | 代理端口 |
| `PROXY_HOST` | `0.0.0.0` | 监听地址 |
| `DEBUG` | `false` | 打印完整请求/响应 |

### 已知限制

- Extended Thinking / Prompt Caching 无 OpenAI 等价物，会丢失
- Token 计数仅启发式估算（CJK ~1.5 token/char，英文 ~0.3 token/char），误差 ±30%
- 图片仅支持 base64，不支持 URL 源
- 代理不校验 API Key，`ANTHROPIC_API_KEY` 可填任意值

---

## 四、架构图

```
┌──────────────────────────────────────────────────┐
│  Peerdrive React 前端 (localhost:5173)            │
│  ├── AnonCreator 🤖 llmSuggest()                 │
│  ├── LLMAssistant (function calling)             │
│  └── Settings (LLM 配置面板)                      │
│        │                                          │
│        ▼ POST /v1/chat/completions               │
│  https://siliconflow.moonchan.xyz                │
│        │ Cloudflare 反向代理                       │
│        ▼                                          │
│  https://api.siliconflow.cn/v1                   │
│        │                                          │
│        ▼                                          │
│  Silicon Flow 模型 (Qwen/GLM/DeepSeek...)         │
└──────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────┐
│  Claude Code                                     │
│        │ POST /v1/messages (Anthropic 格式)       │
│        ▼                                          │
│  anthropic-sf-proxy (localhost:8080)              │
│        │ 协议转换 Anthropic → OpenAI               │
│        ▼                                          │
│  Silicon Flow API                                 │
└──────────────────────────────────────────────────┘
```

---

## 五、测试

### 测试前端 LLM 调用

```bash
# 直接测试 siliconflow 代理（AI 命名）
curl -s https://siliconflow.moonchan.xyz/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "Qwen/Qwen3-8B",
    "messages": [{"role":"user","content":"say hello"}],
    "max_tokens": 20
  }'
```

### 测试 Claude Code 代理

```bash
# 非流式
NO_PROXY=localhost,127.0.0.1 curl -s http://localhost:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{"model":"Qwen/Qwen2.5-7B-Instruct","max_tokens":50,"messages":[{"role":"user","content":"say hi"}]}'

# 健康检查
curl http://localhost:8080/health
```

---

## 六、相关文件索引

| 文件 | 说明 |
|------|------|
| `react/src/api.js:143-177` | LLM 配置 localStorage 存取 + 免费模型列表 |
| `react/src/pages/AnonCreator.jsx:7-17` | AI 合集命名调用 |
| `react/src/pages/Settings.jsx:176-264` | LLM 配置 UI 面板 |
| `react/src/components/LLMAssistant.jsx` | AI 聊天助手（function calling） |
| `/mnt/d/WorkPlace/anthropic-sf-proxy/server.py` | Claude Code → SF 协议转换服务 |
| `/mnt/d/WorkPlace/anthropic-sf-proxy/convert.py` | 请求/响应/流式转换逻辑 |

#siliconflow #LLM #Qwen #ClaudeCode #协议转换 #代理 #peerdrive
