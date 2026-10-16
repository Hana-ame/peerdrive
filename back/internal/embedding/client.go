// client.go: OpenAI-compatible embeddings client for a standalone EmbeddingGemma
// service.
//
// Scope (deliberately narrow):
//
//   - peerdrive never loads a model, downloads a weight, or runs inference. The
//     service is always standalone and operator-managed: llama-server
//     `--embedding`, or Ollama through its OpenAI-compatible layer. Everything
//     this package does is one HTTP POST.
//   - No mmproj / multimodal projector: text only, which is what the 768-dim
//     EmbeddingGemma text vectors are.
//   - One POST per call is the whole protocol surface.
//
// Wire format is the OpenAI /v1/embeddings shape:
//
//	POST {URL} {"model":"embeddinggemma-2","input":"text"}
//	→ {"data":[{"embedding":[768 floats],"index":0}],"model":"...","usage":{...}}
//
// Config.TimeoutSecs bounds one request. Embedding is a real forward pass, so
// the default is generous (30s) but finite: without it a wedged service hangs
// the caller forever, and the HTTP endpoint in front of this client would turn
// into a slot machine of stuck connections.
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxErrorBody is how much of a non-2xx response body is folded into the error.
// Enough for the useful part of llama-server's message, not enough to leak a
// large payload or blow up a log line.
const maxErrorBody = 512

// pingText is what Ping sends. One short token keeps the probe cheap: it costs
// a single forward pass, which is the price of validating the whole path
// (TCP → auth → model name → dimension) end to end rather than only the socket.
const pingText = "ping"

// request is the OpenAI-compatible request body. Input is `any` so a single
// string and a []string share one type: servers that only accept arrays reject
// the string form, which EmbedBatch detects and falls back on.
type request struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

// item is one entry of the response's data array.
type item struct {
	Object    string    `json:"object,omitempty"`
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// usage mirrors the token counters some servers return. Omitted when absent.
type usage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// response is the OpenAI-compatible response body.
type response struct {
	Object string  `json:"object"`
	Model  string  `json:"model"`
	Data   []item  `json:"data"`
	Usage  *usage  `json:"usage,omitempty"`
	Error  *apiErr `json:"error,omitempty"`
}

// apiErr is the OpenAI error envelope, returned with a non-2xx status when the
// server rejects a request (unknown model, oversized input, ...).
type apiErr struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// Config describes the target service. Every field except URL has a sane
// default: an empty APIKey means "no auth header", TimeoutSecs <= 0 means the
// caller's context alone bounds the request, and ExpectedDim <= 0 disables the
// dimension check.
type Config struct {
	// URL is the *full* embeddings endpoint. It is not a base URL: the client
	// POSTs to exactly this string. That matches llama-server's route layout
	// and keeps a gateway that mounts embeddings under another prefix usable
	// without a code change.
	URL string
	// Model is sent verbatim as the "model" field. llama-server usually accepts
	// the model's alias or empty string, but sending it explicitly is what lets
	// a multi-model server pick the right one.
	Model string
	// TimeoutSecs bounds a single request. 0 means "no deadline beyond the
	// caller's context".
	TimeoutSecs int
	// APIKey, when non-empty, is sent as `Authorization: Bearer <key>`.
	// llama-server and Ollama ignore it; gateways (LiteLLM, vLLM with auth)
	// need it.
	APIKey string
	// ExpectedDim, when > 0, is validated against every returned vector. A
	// mismatch is an error: returning the wrong width silently would poison
	// every caller downstream (index build, retrieval, ...).
	ExpectedDim int
	// HTTPClient may be injected for tests. nil → a fresh client.
	//
	// The default transport uses http.ProxyFromEnvironment: that is the
	// idiomatic Go choice, and a loopback llama-server behind a configured
	// proxy needs NO_PROXY=127.0.0.1,localhost set, exactly like every other
	// Go program talking to a local service.
	HTTPClient *http.Client
}

// Normalize fills in the defaults an operator leaves unset.
func (c *Config) Normalize() {
	if c.URL == "" {
		c.URL = "http://127.0.0.1:8080/v1/embeddings"
	}
	if c.Model == "" {
		c.Model = "embeddinggemma-2"
	}
	if c.TimeoutSecs <= 0 {
		c.TimeoutSecs = 30
	}
}

// Client talks to one standalone embedding service. It is safe for concurrent
// use: all state is immutable after New.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client. It never dials the service: construction is allocation
// only, so enabling the module costs nothing when the service is down.
func New(cfg Config) *Client {
	cfg.Normalize()
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
		}
	}
	return &Client{cfg: cfg, http: hc}
}

// Config returns the normalized config this client was built with.
func (c *Client) Config() Config { return c.cfg }

// URL returns the endpoint this client POSTs to.
func (c *Client) URL() string { return c.cfg.URL }

// Model returns the model name sent in every request.
func (c *Client) Model() string { return c.cfg.Model }

// Status is the operator-facing view of this client, returned by the
// /embedding/v1/status endpoint. APIKeySet is a boolean on purpose: never echo
// the credential, and never echo the query string either (see URLForStatus).
type Status struct {
	Model       string `json:"model"`
	URL         string `json:"url"`
	ExpectedDim int    `json:"expected_dim"`
	TimeoutSecs int    `json:"timeout_secs"`
	APIKeySet   bool   `json:"api_key_set"`
}

// URLForStatus strips the query string: operators occasionally paste a token
// into the URL rather than using the API key field.
func URLForStatus(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

// Status returns the operator-facing view.
func (c *Client) Status() Status {
	return Status{
		Model:       c.cfg.Model,
		URL:         URLForStatus(c.cfg.URL),
		ExpectedDim: c.cfg.ExpectedDim,
		TimeoutSecs: c.cfg.TimeoutSecs,
		APIKeySet:   c.cfg.APIKey != "",
	}
}

// Embed embeds one text and returns its vector.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embedding: empty input")
	}
	vecs, err := c.do(ctx, text, 1)
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedBatch embeds every text and returns vectors in the same order.
//
// It issues one request with an array input — the fast path, since the model
// embeds a batch in a single forward pass. Older llama-server builds reject
// array input with a 4xx; when that happens and there is more than one text,
// the client falls back to one request per text so a caller never has to know
// which build it pointed at.
func (c *Client) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	for i, t := range texts {
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("embedding: text[%d] is empty", i)
		}
	}
	if len(texts) == 1 {
		v, err := c.Embed(ctx, texts[0])
		if err != nil {
			return nil, err
		}
		return [][]float32{v}, nil
	}

	vecs, err := c.do(ctx, texts, len(texts))
	if err == nil {
		return vecs, nil
	}

	// Single-request path failed. If it was a hard server error (5xx, transport)
	// the per-text path is unlikely to help, but it costs little to try and the
	// error message is much more useful when it does.
	vecs = make([][]float32, len(texts))
	for i, t := range texts {
		v, e := c.Embed(ctx, t)
		if e != nil {
			return nil, fmt.Errorf("embedding: batch (array input) failed: %w; per-text fallback text[%d]: %v", err, i, e)
		}
		vecs[i] = v
	}
	return vecs, nil
}

// Ping probes reachability. It embeds one tiny string, so it exercises the whole
// path — TCP, auth, model resolution, dimension — for the cost of a single
// forward pass. Used at startup: a failure is a WARN, never a crash, because
// the operator may legitimately start peerdrive before the service.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Embed(ctx, pingText)
	return err
}

// do issues one request whose input may be a string or a []string and returns
// the vectors, ordered and dimension-checked. expect is the number of inputs
// the caller sent; the server must return exactly that many, and a short reply
// is an error rather than a quietly truncated result.
func (c *Client) do(ctx context.Context, input any, expect int) ([][]float32, error) {
	ctx, cancel := c.deadline(ctx)
	defer cancel()

	payload, err := json.Marshal(request{Model: c.cfg.Model, Input: input})
	if err != nil {
		return nil, fmt.Errorf("embedding: encode: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("embedding: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding: %s unreachable: %w", URLForStatus(c.cfg.URL), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("embedding: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding: %s returned HTTP %d: %s",
			URLForStatus(c.cfg.URL), resp.StatusCode, truncate(body, maxErrorBody))
	}

	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("embedding: bad JSON from %s: %v (body: %s)",
			URLForStatus(c.cfg.URL), err, truncate(body, maxErrorBody))
	}
	if r.Error != nil && r.Error.Message != "" {
		return nil, fmt.Errorf("embedding: %s rejected request: %s", URLForStatus(c.cfg.URL), r.Error.Message)
	}
	if len(r.Data) == 0 {
		return nil, fmt.Errorf("embedding: %s returned an empty data array", URLForStatus(c.cfg.URL))
	}
	if len(r.Data) != expect {
		return nil, fmt.Errorf("embedding: %s returned %d vectors for %d inputs",
			URLForStatus(c.cfg.URL), len(r.Data), expect)
	}

	want := c.cfg.ExpectedDim
	got, err := collect(r.Data, want, c.cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("embedding: %s: %w", URLForStatus(c.cfg.URL), err)
	}
	return got, nil
}

// collect maps response items back onto the input order and checks the
// dimension. It refuses both a missing index and a duplicated one: silently
// filling a hole with a neighbour's vector would be far worse than failing.
func collect(data []item, want int, model string) ([][]float32, error) {
	if want < 0 {
		want = 0
	}
	out := make([][]float32, len(data))
	seen := make([]bool, len(data))
	for _, it := range data {
		if len(it.Embedding) == 0 {
			return nil, fmt.Errorf("empty vector at index %d", it.Index)
		}
		if it.Index < 0 || it.Index >= len(data) {
			return nil, fmt.Errorf("index %d out of range for %d vectors", it.Index, len(data))
		}
		if seen[it.Index] {
			return nil, fmt.Errorf("duplicate index %d", it.Index)
		}
		seen[it.Index] = true
		if want > 0 && len(it.Embedding) != want {
			return nil, fmt.Errorf("dimension mismatch: model %q returned %d, expected %d (check PEERDRIVE_EMBEDDING_DIM or the server model)",
				model, len(it.Embedding), want)
		}
		out[it.Index] = it.Embedding
	}
	return out, nil
}

// deadline applies Config.TimeoutSecs on top of the caller's context. The
// caller's deadline still wins if it is shorter.
func (c *Client) deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.cfg.TimeoutSecs <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(c.cfg.TimeoutSecs)*time.Second)
}

// truncate renders at most n bytes of a body for an error message, marking a
// truncation instead of silently cutting.
func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
