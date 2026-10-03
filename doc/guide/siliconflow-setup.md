# Silicon Flow Integration Guide

> Last updated: 2026-04-27

## Overview

Peerdrive uses Silicon Flow (SiliconFlow) as the LLM backend, accessed through `siliconflow.moonchan.xyz` reverse proxy, for AI collection naming and similar features.

Three components involved:
| Component | Path | Purpose |
|------|------|------|
| anthropic-sf-proxy | `/mnt/d/WorkPlace/anthropic-sf-proxy/` | Claude Code → Silicon Flow protocol conversion |
| siliconflow.moonchan.xyz | Cloudflare reverse proxy | Proxies `api.siliconflow.cn` to avoid direct connections |
| Peerdrive Settings | `react/src/pages/Settings.jsx` | Frontend LLM endpoint/model/key configuration |

---

## 1. siliconflow.moonchan.xyz Reverse Proxy

### Purpose

Peerdrive frontend directly calls `https://siliconflow.moonchan.xyz/v1/chat/completions` for AI collection naming (LLMAssistant, AnonCreator's 🤖 button).

### Principle

A Cloudflare reverse proxy that forwards requests from `siliconflow.moonchan.xyz` to `api.siliconflow.cn`. The frontend doesn't need to connect to Silicon Flow directly, avoiding cross-origin and key exposure issues.

### Usage in Peerdrive

**LLM endpoint configuration** (`react/src/api.js:143`):
```js
const DEFAULT_LLM_ENDPOINT = 'https://siliconflow.moonchan.xyz';
```

**Call pattern** (`react/src/pages/AnonCreator.jsx:7-16`):
```js
const LLM_URL = api.getLlmEndpoint() || 'https://siliconflow.moonchan.xyz';
const LLM_CHAT = `${LLM_URL}/v1/chat/completions`;

async function llmSuggest(names) {
  const res = await fetch(LLM_CHAT, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model: 'Qwen/Qwen3-8B',
      messages: [{ role: 'user', content: `Please give the following file set a concise collection name in 3-5 Chinese characters: ${names}` }],
      max_tokens: 20,
      stream: false,
    }),
  });
  return res.json().choices?.[0]?.message?.content?.trim();
}
```

**LLMAssistant function calling** (`react/src/components/LLMAssistant.jsx`):
- Uses the same OpenAI-compatible `/v1/chat/completions` endpoint
- Supports SSE streaming output
- Has 17 function call tools (navigation, search, collection operations, P2P status, etc.)
- Up to 5 tool call rounds
- Supports AbortController interruption

### Frontend Configuration Page

The `/settings` page (`react/src/pages/Settings.jsx`) provides a complete LLM configuration panel:

| Setting | localStorage Key | Default |
|--------|-----------------|--------|
| Endpoint | `peerdrive_llm_endpoint` | `https://siliconflow.moonchan.xyz` |
| Model | `peerdrive_llm_model` | `Qwen/Qwen3-8B` |
| API Key | `peerdrive_llm_apikey` | Empty |
| Body template | `peerdrive_llm_body_template` | OpenAI-compatible JSON |

**Body JSON template default value**:
```json
{
  "model": "Qwen/Qwen3-8B",
  "messages": [],
  "stream": true,
  "max_tokens": 4096,
  "temperature": 0.7
}
```

The `model` and `messages` fields in the template are replaced at runtime; other parameters (`stream`, `max_tokens`, `temperature`) can be customized.

---

## 2. Free Model List

13 free models available in Settings → LLM Configuration → Model dropdown:

| Model | Series | Description |
|------|------|------|
| `Qwen/Qwen3-8B` | Qwen 3 | Recommended, strong Chinese capability |
| `Qwen/Qwen3.5-4B` | Qwen 3.5 | Lightweight |
| `Qwen/Qwen2.5-7B-Instruct` | Qwen 2.5 | Stable version |
| `deepseek-ai/DeepSeek-R1-0528-Qwen3-8B` | DeepSeek R1 | Enhanced reasoning |
| `deepseek-ai/DeepSeek-R1-Distill-Qwen-7B` | DeepSeek R1 Distill | Distilled reasoning |
| `deepseek-ai/DeepSeek-OCR` | DeepSeek OCR | Text recognition |
| `THUDM/GLM-4-9B-0414` | Zhipu GLM-4 | General dialogue |
| `THUDM/GLM-Z1-9B-0414` | Zhipu GLM-Z1 | Enhanced reasoning |
| `THUDM/GLM-4.1V-9B-Thinking` | Zhipu GLM-4V | Multimodal thinking |
| `tencent/Hunyuan-MT-7B` | Tencent Hunyuan | Machine translation |
| `internlm/internlm2_5-7b-chat` | InternLM | General dialogue |
| `PaddlePaddle/PaddleOCR-VL` | Baidu PaddleOCR | Visual text recognition |
| `PaddlePaddle/PaddleOCR-VL-1.5` | Baidu PaddleOCR 1.5 | New visual text recognition |

Silicon Flow registration: https://cloud.siliconflow.cn/i/sRO0U8o0

---

## 3. Anthropic-SF-Proxy (Claude Code Proxy)

### Purpose

Enables Claude Code (Anthropic Messages API) to work through Silicon Flow models.

### Repository

`/mnt/d/WorkPlace/anthropic-sf-proxy/`

### Protocol Conversion

Claude Code → Anthropic Messages API → Proxy → OpenAI Chat Completions → Silicon Flow

| Conversion | Anthropic | OpenAI |
|------|-----------|--------|
| Endpoint | `/v1/messages` | `/v1/chat/completions` |
| Request body | `system` + `messages` | `messages` (system merged) |
| Streaming | SSE `message_start`/`content_block_delta`/`message_delta` | SSE `data: {"choices":[...]}` |
| Model | `claude-*` | `Qwen/*` etc. |

### Request Conversion (convert.py)

```python
def anthropic_to_openai(anthropic_request, model_map):
    """
    Convert Anthropic Messages API request to OpenAI Chat Completions
    - Merge system into messages
    - Map model name
    - Preserve tools/tool_choice
    """
    messages = []
    if 'system' in req:
        messages.append({'role': 'system', 'content': req['system']})
    messages.extend(req['messages'])
    
    # Model name mapping
    openai_model = model_map.get(req['model'], req['model'])
    
    openai_request = {
        'model': openai_model,
        'messages': messages,
        'max_tokens': req.get('max_tokens', 1024),
        'temperature': req.get('temperature', 1),
    }
    
    # Tools mapping
    if 'tools' in req:
        openai_tools = []
        for tool in req['tools']:
            openai_tools.append({
                'type': 'function',
                'function': {
                    'name': tool['name'],
                    'description': tool.get('description', ''),
                    'parameters': tool['input_schema'],
                }
            })
        openai_request['tools'] = openai_tools
    
    return openai_request
```

### Response Conversion

```python
def openai_to_anthropic(openai_response, is_stream):
    """
    Convert OpenAI response to Anthropic format
    - Non-stream: complete message object
    - Stream: SSE events (message_start, content_block_start/delta/stop, message_delta, message_stop)
    """
    if not is_stream:
        return {
            'id': f'msg_{uuid4().hex[:24]}',
            'type': 'message',
            'role': 'assistant',
            'model': openai_response.get('model'),
            'content': [
                {
                    'type': 'text',
                    'text': openai_response['choices'][0]['message']['content']
                }
            ],
            'stop_reason': openai_response['choices'][0]['finish_reason'],
            'stop_sequence': None,
            'usage': openai_response.get('usage', {}),
        }
    
    # Streaming: return SSE event sequence
    yield 'event: message_start\n...'
    yield 'event: content_block_start\n...'
    yield 'event: content_block_delta\n...'
    yield 'event: content_block_stop\n...'
    yield 'event: message_delta\n...'
    yield 'event: message_stop\n...'
```

### Configuration

```python
# config.py
class Config:
    # Port
    PORT = 8080
    
    # Silicon Flow API
    SILICONFLOW_API_BASE = 'https://api.siliconflow.cn/v1'
    
    # Model name mapping (Anthropic → Silicon Flow)
    MODEL_MAP = {
        'claude-3-5-sonnet-20241022': 'Qwen/Qwen3-8B',
        'claude-3-5-haiku-20241022': 'Qwen/Qwen3.5-4B',
        'claude-3-opus-20240229': 'deepseek-ai/DeepSeek-R1-0528-Qwen3-8B',
        # ...
    }
    
    # Request timeout (seconds)
    TIMEOUT = 60
```

### Model Mapping Table

| Claude Model | Silicon Flow Model | Notes |
|-------------|-------------------|-------|
| `claude-3-5-sonnet-20241022` | `Qwen/Qwen3-8B` | Main model, strong dialogue |
| `claude-3-5-haiku-20241022` | `Qwen/Qwen3.5-4B` | Lightweight fast |
| `claude-3-opus-20240229` | `deepseek-ai/DeepSeek-R1-0528-Qwen3-8B` | Complex reasoning |
| `claude-3-sonnet-20240229` | `Qwen/Qwen2.5-7B-Instruct` | Stable general |
| `claude-3-haiku-20240307` | `tencent/Hunyuan-MT-7B` | Lightweight fast response |
| (default fallback) | `Qwen/Qwen3-8B` | When no mapping |

### Server

```python
# server.py
from fastapi import FastAPI
from fastapi.responses import StreamingResponse
import httpx
from convert import anthropic_to_openai, openai_to_anthropic, openai_to_anthropic_stream
from config import Config

app = FastAPI()

@app.post("/v1/messages")
async def messages(request: Request):
    req = await request.json()
    stream = req.get('stream', False)
    
    # Convert request
    openai_req = anthropic_to_openai(req, Config.MODEL_MAP)
    
    if stream:
        return StreamingResponse(
            stream_anthropic(openai_req),
            media_type='text/event-stream'
        )
    else:
        async with httpx.AsyncClient() as client:
            resp = await client.post(
                f'{Config.SILICONFLOW_API_BASE}/chat/completions',
                json=openai_req,
                timeout=Config.TIMEOUT
            )
            return openai_to_anthropic(resp.json(), is_stream=False)
```

### Usage

```bash
# Start proxy
cd /mnt/d/WorkPlace/anthropic-sf-proxy
python server.py

# Configure Claude Code
export ANTHROPIC_BASE_URL=http://localhost:8080

# Then use Claude Code normally
claude
```

### Direct API Call Test

```bash
curl http://localhost:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "messages": [{"role":"user","content":"Hello"}],
    "max_tokens": 20
  }'
```

### Test Claude Code Proxy

```bash
# Non-streaming
NO_PROXY=localhost,127.0.0.1 curl -s http://localhost:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -d '{"model":"Qwen/Qwen2.5-7B-Instruct","max_tokens":50,"messages":[{"role":"user","content":"say hi"}]}'

# Health check
curl http://localhost:8080/health
```

---

## 6. Related Files Index

| File | Description |
|------|------|
| `react/src/api.js:143-177` | LLM configuration localStorage access + free model list |
| `react/src/pages/AnonCreator.jsx:7-17` | AI collection naming call |
| `react/src/pages/Settings.jsx:176-264` | LLM configuration UI panel |
| `react/src/components/LLMAssistant.jsx` | AI chat assistant (function calling) |
| `/mnt/d/WorkPlace/anthropic-sf-proxy/server.py` | Claude Code → SF protocol conversion service |
| `/mnt/d/WorkPlace/anthropic-sf-proxy/convert.py` | Request/response/streaming conversion logic |

#siliconflow #LLM #Qwen #ClaudeCode #protocol-conversion #proxy #peerdrive
