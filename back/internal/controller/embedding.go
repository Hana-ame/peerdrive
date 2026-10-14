// embedding.go: HTTP surface for the standalone EmbeddingGemma service.
//
// The four endpoints are deliberately small: one POST per operation, no state
// on the node, no caching of vectors. peerdrive is a pass-through — the model
// lives in the operator's own llama-server / Ollama process, so nothing here
// owns an embedding lifecycle.
//
// Wire paths follow the repo's existing convention of bare route groups
// (/collections, /files, /iwara, /twitterpic). There is no /api/v1 prefix
// anywhere in this router — grep for it returns nothing — so /embedding/v1/* is
// the consistent choice rather than inventing the first /api/v1 in the tree.
//
// Auth: every endpoint is authRequired. Embedding consumes the compute of a
// third-party inference process, so an unauthenticated caller hitting this
// endpoint could pin the operator's GPU with large requests. The module is
// opt-in; opting in means accepting the auth gate.
//
// Upstream errors are returned as 502 with the upstream's own message. This is
// the same treatment controller.TwitterPicUsers gives its upstream, and it is
// deliberate rather than a slip: the caller is an authenticated operator asking
// about *their own* embedding service, so "model 'nope' does not exist" is
// information they want, not a leak. The red-team R2 rule in errors.go about
// not echoing driver text covers SQLite internals over unauthenticated routes.
package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/embedding"
	"peerdrive/internal/log"
)

// embeddingClient is injected by serverapp; nil means the module is disabled
// (PEERDRIVE_EMBEDDING_ENABLE=false, the default) and none of the routes are
// registered at all.
var embeddingClient *embedding.Client

// InitEmbeddingClient injects the client (nil = endpoints off).
func InitEmbeddingClient(c *embedding.Client) {
	log.LogDebug("ctrl-embedding: InitEmbeddingClient")
	embeddingClient = c
}

// maxEmbedInputBytes caps the request body. A single 512 KiB text is well past
// any reasonable embedding unit and covers the full context window of the
// models this module targets, so anything larger is almost certainly a mistake
// (or a caller dumping a whole file and expecting the model to pay for it).
const maxEmbedInputBytes = 512 * 1024

// maxEmbedBatch caps how many texts one batch request may contain. Bounds the
// per-request forward pass so one request cannot monopolise the service.
const maxEmbedBatch = 512

// embedRequest is the request body for both POST endpoints: {text} for the
// single form, {texts:[...]} for the batch form. Both fields are present so a
// caller can read one struct for both routes.
type embedRequest struct {
	Text  string   `json:"text"`
	Texts []string `json:"texts"`
}

// embedResponse is the single-vector reply.
type embedResponse struct {
	Model     string    `json:"model"`
	Dim       int       `json:"dim"`
	Embedding []float32 `json:"embedding"`
	ElapsedMs int64     `json:"elapsed_ms"`
}

// embedBatchResponse is the multi-vector reply.
type embedBatchResponse struct {
	Model      string      `json:"model"`
	Dim        int         `json:"dim"`
	Count      int         `json:"count"`
	Embeddings [][]float32 `json:"embeddings"`
	ElapsedMs  int64       `json:"elapsed_ms"`
}

// pingResponse reports one probe round trip.
type pingResponse struct {
	Model     string `json:"model"`
	URL       string `json:"url"`
	OK        bool   `json:"ok"`
	ElapsedMs int64  `json:"elapsed_ms"`
	Detail    string `json:"detail,omitempty"`
}

// readEmbedBody decodes and size-limits one request body. On failure it writes
// the 4xx response itself and returns nil, so the caller can
// `req, ok := readEmbedBody(c); if !ok { return }`.
func readEmbedBody(c *gin.Context) (*embedRequest, bool) {
	r := io.LimitReader(c.Request.Body, maxEmbedInputBytes+1)
	raw, err := io.ReadAll(r)
	if err != nil {
		// The body was unreadable; report it as a bad request rather than
		// silently treating it as empty.
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read request body: " + err.Error()})
		return nil, false
	}
	if len(raw) > maxEmbedInputBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error":    "request body too large",
			"limit":    maxEmbedInputBytes,
			"received": len(raw),
		})
		return nil, false
	}
	var req embedRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body: " + err.Error()})
		return nil, false
	}
	return &req, true
}

// EmbedText handles POST /embedding/v1/embed with {"text": "..."}.
func EmbedText(c *gin.Context) {
	if embeddingClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding module not enabled"})
		return
	}
	req, ok := readEmbedBody(c)
	if !ok {
		return
	}
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": `"text" is required, or use /embedding/v1/embed/batch with "texts"`})
		return
	}

	start := time.Now()
	vec, err := embeddingClient.Embed(c.Request.Context(), req.Text)
	if err != nil {
		log.LogDebug("ctrl-embedding: embed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, embedResponse{
		Model:     embeddingClient.Model(),
		Dim:       len(vec),
		Embedding: vec,
		ElapsedMs: time.Since(start).Milliseconds(),
	})
}

// EmbedBatch handles POST /embedding/v1/embed/batch with {"texts": ["a", "b"]}.
func EmbedBatch(c *gin.Context) {
	if embeddingClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding module not enabled"})
		return
	}
	req, ok := readEmbedBody(c)
	if !ok {
		return
	}
	if len(req.Texts) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": `"texts" must be a non-empty array, or use /embedding/v1/embed with "text"`})
		return
	}
	if len(req.Texts) > maxEmbedBatch {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": "too many texts in one batch",
			"limit": maxEmbedBatch,
			"count": len(req.Texts),
		})
		return
	}

	start := time.Now()
	vecs, err := embeddingClient.EmbedBatch(c.Request.Context(), req.Texts)
	if err != nil {
		log.LogDebug("ctrl-embedding: embed batch: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	dim := 0
	if len(vecs) > 0 {
		dim = len(vecs[0])
	}
	c.JSON(http.StatusOK, embedBatchResponse{
		Model:      embeddingClient.Model(),
		Dim:        dim,
		Count:      len(vecs),
		Embeddings: vecs,
		ElapsedMs:  time.Since(start).Milliseconds(),
	})
}

// EmbedPing handles POST /embedding/v1/ping: one tiny embedding, so the operator
// can check reachability at runtime for the price of a single forward pass.
func EmbedPing(c *gin.Context) {
	if embeddingClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding module not enabled"})
		return
	}
	start := time.Now()
	err := embeddingClient.Ping(c.Request.Context())
	resp := pingResponse{
		Model:     embeddingClient.Model(),
		URL:       embeddingClient.URL(),
		OK:        err == nil,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		resp.Detail = err.Error()
		log.LogDebug("ctrl-embedding: ping: %v", err)
		c.JSON(http.StatusBadGateway, resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// EmbedStatus handles GET /embedding/v1/status: configuration only, no
// inference, so it is cheap to poll.
func EmbedStatus(c *gin.Context) {
	if embeddingClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "embedding module not enabled"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"client": embeddingClient.Status()})
}
