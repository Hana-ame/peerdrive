# Embedding Module Docs

Standalone EmbeddingGemma embeddings over the OpenAI-compatible
`/v1/embeddings` wire format. Issue #371.

peerdrive does **not** load a model, does not download weights, and does not run
inference. It only speaks an HTTP client protocol against a service the operator
runs themselves. That is deliberate: a GGUF loader in the peerdrive binary would
add hundreds of MB and a whole dependency tree (ggml, CUDA/Vulkan backends,
tokenizers) for a feature that llama.cpp already ships as a standalone binary.

---

## Why default off

`PEERDRIVE_EMBEDDING_ENABLE` defaults to `false`. Enabling it:

1. makes the node **ship its text** to an operator-configured inference service —
   a data-egress decision the operator must make explicitly, not one that should
   happen because a config file existed; and
2. registers `/embedding/v1/*`, which spends that service's compute. An
   unauthenticated caller hitting it could pin the operator's GPU.

Default off keeps an unconfigured node byte-identical to one without the module:
no client is constructed, no route is registered, and no network call is ever
made. `TestLoad_EmbeddingDefaults` pins this.

---

## Running the service yourself

peerdrive has no local inference path, so you point it at something else.

**llama.cpp** — the direct target:

```bash
llama-server -m embeddinggemma-2-UD-Q4_K_XL.gguf --embedding -c 8192 -t 8 \
             --host 127.0.0.1 --port 8080
```

Windows one-liner: [`scripts/start-embeddinggemma.bat`](../../scripts/start-embeddinggemma.bat)
(takes physical core count via WMI, checks both files exist before launching).

**Ollama** — through its OpenAI-compatible layer:

```bash
ollama serve
# then: PEERDRIVE_EMBEDDING_URL=http://127.0.0.1:11434/v1/embeddings
#       PEERDRIVE_EMBEDDING_MODEL=nomic-embed-text
```

Note the port: llama.cpp's `--port` **default is 9931**, not 8080. peerdrive's
`DefaultEmbeddingURL` points at 8080, so either use llama.cpp's default binding
or set `PEERDRIVE_EMBEDDING_URL`.

Model weights: <https://huggingface.co/ggml-org/embeddinggemma-2-GGUF>.
`embeddinggemma-2-UD-Q4_K_XL.gguf` is ~168 MB and produces 768-dimensional
vectors.

There is no multimodal projector (`mmproj`) support. This module is text in,
vector out.

---

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `PEERDRIVE_EMBEDDING_ENABLE` | `false` | Master switch |
| `PEERDRIVE_EMBEDDING_URL` | `http://127.0.0.1:8080/v1/embeddings` | Full endpoint, POSTed to verbatim |
| `PEERDRIVE_EMBEDDING_MODEL` | `embeddinggemma-2` | Sent as `"model"` in the body |
| `PEERDRIVE_EMBEDDING_TIMEOUT_SECS` | `30` | Per-request deadline |
| `PEERDRIVE_EMBEDDING_API_KEY` | `` (empty) | `Authorization: Bearer ***`; empty = no header |
| `PEERDRIVE_EMBEDDING_DIM` | `768` | Expected width; `0` disables the check |

`PEERDRIVE_EMBEDDING_DIM` is parsed with `getEnvIntAllowZero`, not `getEnvInt`:
`0` is a real value here (opt out of width validation) and `getEnvInt` would
silently fall back to 768 — the opposite of what the operator asked for.
`TestLoad_EmbeddingZeroDimDisablesCheck` pins this.

`PEERDRIVE_EMBEDDING_DIM` exists because the failure mode without it is silent.
A model whose width you did not intend returns a *plausible-looking* 512- or
384-wide vector and downstream cosine similarity scores still look reasonable.
The check is a 3-line compare, and the cost of getting the width wrong is one
forward pass.

---

## HTTP API

All four endpoints are behind `authRequired`. There is no anonymous path: these
routes consume third-party inference compute.

### `POST /embedding/v1/embed`

```json
{"text": "hello"}
```

```json
{"model": "embeddinggemma-2", "dim": 768, "elapsed_ms": 12, "embedding": [0.01, ...]}
```

### `POST /embedding/v1/embed/batch`

```json
{"texts": ["a", "b", "c"]}
```

```json
{"model": "embeddinggemma-2", "dim": 768, "count": 3, "elapsed_ms": 31, "embeddings": [[...], [...], [...]]}
```

`maxEmbedBatch = 512` caps the per-request forward pass so one request cannot
monopolise the service.

### `POST /embedding/v1/ping`

One tiny embedding. Runtime reachability check for the price of a single forward
pass. Returns `200` with `{"ok": true, "elapsed_ms": N}`, or `502` with
`{"ok": false, "detail": "..."}`.

### `GET /embedding/v1/status`

Configuration only, **no inference** — cheap to poll. Returns model name, URL
with credentials stripped, expected dim, timeout, and an `api_key_set` boolean.
The URL goes through `URLForStatus()`, which strips the query string: a bearer
token passed as `?token=...` must not be echoed back. `TestStatusNeverEchoesCredential`
pins this.

### Request size limits

`maxEmbedInputBytes = 512 * 1024` caps the body. 512 KiB of text is far past any
reasonable embedding unit and covers the full context window of the models this
module targets, so anything larger is almost certainly a mistake — or a caller
dumping a whole file and expecting the model to pay for it.

---

## CLI

```bash
peerdrive embed "some text"            # one vector, JSON
peerdrive embed "a" "b" "c"            # batch
cat corpus.txt | peerdrive embed       # one vector per stdin line
peerdrive embed --ping                 # reachability probe
peerdrive embed "t" -format vec        # space-separated, for piping
```

Flag > env > compiled-in default. `-dim 0` disables the width check; `-dim -1`
means "use the env value". Exit 0 = ok, 1 = failure (disabled, unreachable, or
bad input) — one code on purpose, since this is a building block for pipelines
rather than a diagnostic.

---

## Upstream error handling

Upstream failures return `502` **with the upstream's own message**. That is the
same treatment `controller.TwitterPicUsers` gives its upstream, and it is
deliberate rather than a slip: the caller is an authenticated operator asking
about *their own* embedding service, so `model 'nope' does not exist` is
information they want. The red-team rule in `controller/errors.go` about not
echoing driver text covers SQLite internals over unauthenticated routes — a
different surface.

`Embed()` and `EmbedBatch()` also reject, before returning:

- empty or whitespace-only input (400 at the controller, `embed: empty input`)
- a response with the wrong vector count for the number of inputs
- a vector whose width differs from `EmbeddingExpectedDim`
- out-of-range and duplicate `index` fields in the response array
- an `error` envelope even when the HTTP status is 200

---

## Files

| File | Role |
| --- | --- |
| `back/internal/embedding/client.go` | OpenAI-compatible client: `Embed`, `EmbedBatch`, `Ping`, `Status` |
| `back/internal/embedding/client_test.go` | 26 tests, all against `httptest` servers — no live service needed |
| `back/internal/controller/embedding.go` | The four HTTP handlers |
| `back/internal/router/router.go` | Route registration, gated on `deps.EmbeddingClient != nil` |
| `back/internal/cli/embed.go` | `peerdrive embed` subcommand |
| `scripts/start-embeddinggemma.bat` | Windows one-click service launcher |

## Startup behaviour

When enabled, the builder probes once with a 5-second cap and logs a `WARN` if
the service is down. It does **not** fail startup: the operator may
legitimately boot peerdrive before the embedding service, and a wedged
third-party process should not take the node down with it. The route reports the
failure per request.
