// client_test.go: unit tests for the OpenAI-compatible embeddings client.
//
// Everything runs against httptest servers on the loopback interface — no real
// inference service is needed and no test touches the network. This is also
// what makes the "default off" contract checkable: a caller that never
// constructs a Client has no code path to take at all (the config tests
// cover that half).
package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dim768 = 768

// vecJSON renders a length-`dim` vector of 0.1s as JSON.
func vecJSON(dim int) string {
	parts := make([]string, dim)
	for i := range parts {
		parts[i] = "0.1"
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// embBody renders one OpenAI-compatible embeddings response for the given
// indices, each with a vector of length dim.
func embBody(dim int, indices ...int) string {
	items := make([]string, 0, len(indices))
	for _, i := range indices {
		items = append(items, fmt.Sprintf(`{"object":"embedding","index":%d,"embedding":%s}`, i, vecJSON(dim)))
	}
	return fmt.Sprintf(`{"object":"list","model":"m","data":[%s],"usage":{"prompt_tokens":1,"total_tokens":1}}`,
		strings.Join(items, ","))
}

// recording is an httptest handler that answers with a fixed status/body and
// keeps the last request it saw so tests can assert on what was actually sent.
type recording struct {
	mu     sync.Mutex
	status int
	body   string
	model  string
	input  any
	count  int
}

func (r *recording) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var payload map[string]any
		_ = json.Unmarshal(b, &payload)
		r.mu.Lock()
		r.count++
		if payload != nil {
			if m, ok := payload["model"].(string); ok {
				r.model = m
			}
			r.input = payload["input"]
		}
		r.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(r.status)
		_, _ = w.Write([]byte(r.body))
	}
}

func (r *recording) seen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *recording) seenModel() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.model
}

func (r *recording) seenInput() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.input
}

// --- Config ---------------------------------------------------------------

func TestConfigNormalizeFillsDefaults(t *testing.T) {
	cfg := Config{}
	cfg.Normalize()
	assert.Equal(t, "http://127.0.0.1:8080/v1/embeddings", cfg.URL)
	assert.Equal(t, "embeddinggemma-2", cfg.Model)
	assert.Equal(t, 30, cfg.TimeoutSecs)
}

func TestConfigNormalizeKeepsOverrides(t *testing.T) {
	cfg := Config{URL: "http://10.0.0.2:8080/v1/embeddings", Model: "ollama", TimeoutSecs: 5}
	cfg.Normalize()
	assert.Equal(t, "http://10.0.0.2:8080/v1/embeddings", cfg.URL)
	assert.Equal(t, "ollama", cfg.Model)
	assert.Equal(t, 5, cfg.TimeoutSecs)
}

func TestNewNeverDials(t *testing.T) {
	// Building against an unroutable address must not fail: reachability is
	// Ping's job, not New's, so enabling the module costs nothing at boot.
	c := New(Config{URL: "http://192.0.2.1:1/v1/embeddings", TimeoutSecs: 1})
	require.NotNil(t, c)
	assert.Equal(t, "http://192.0.2.1:1/v1/embeddings", c.URL())
	assert.Equal(t, "embeddinggemma-2", c.Model())
}

func TestURLForStatusStripsQuery(t *testing.T) {
	assert.Equal(t, "http://x/v1/embeddings", URLForStatus("http://x/v1/embeddings"))
	assert.Equal(t, "http://x/v1/embeddings", URLForStatus("http://x/v1/embeddings?token=***"))
}

func TestStatusNeverEchoesCredential(t *testing.T) {
	c := New(Config{URL: "http://x/v1/embeddings?token=***", APIKey: "sekret", ExpectedDim: dim768, TimeoutSecs: 7})
	b, err := json.Marshal(c.Status())
	require.NoError(t, err)
	s := string(b)
	assert.False(t, strings.Contains(s, "sekret"))
	assert.False(t, strings.Contains(s, "token=***"))
	assert.Equal(t, "http://x/v1/embeddings", c.Status().URL)
	assert.Equal(t, dim768, c.Status().ExpectedDim)
	assert.Equal(t, 7, c.Status().TimeoutSecs)
	assert.True(t, c.Status().APIKeySet)
}

// --- Embed ----------------------------------------------------------------

func TestEmbedReturns768Vector(t *testing.T) {
	r := &recording{status: 200, body: embBody(dim768, 0)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "embeddinggemma-2", ExpectedDim: dim768})
	v, err := c.Embed(context.Background(), "hello")
	require.NoError(t, err)
	require.Len(t, v, dim768)
	assert.InDelta(t, 0.1, v[0], 1e-6)
	assert.InDelta(t, 0.1, v[dim768-1], 1e-6)
}

func TestEmbedSendsModelAndText(t *testing.T) {
	r := &recording{status: 200, body: embBody(dim768, 0)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "embeddinggemma-2", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "the quick brown fox")
	require.NoError(t, err)

	assert.Equal(t, "embeddinggemma-2", r.seenModel())
	assert.Equal(t, "the quick brown fox", r.seenInput())
}

func TestEmbedSendsBearerKeyWhenSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		_, _ = w.Write([]byte(embBody(dim768, 0)))
	}))
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", APIKey: "secret", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.NoError(t, err)
	assert.Equal(t, "Bearer secret", gotAuth)
}

func TestEmbedOmitsKeyWhenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		_, _ = w.Write([]byte(embBody(dim768, 0)))
	}))
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.NoError(t, err)
	assert.Empty(t, gotAuth)
}

func TestEmbedRejectsEmptyInput(t *testing.T) {
	c := New(Config{URL: "http://127.0.0.1:1/v1/embeddings", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty input")
}

func TestEmbedRejectsDimensionMismatch(t *testing.T) {
	r := &recording{status: 200, body: embBody(3, 0)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "mxbai-embed-large", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dimension mismatch")
	assert.Contains(t, err.Error(), "returned 3")
	assert.Contains(t, err.Error(), "expected 768")
}

func TestEmbedSkipsDimensionCheckWhenZero(t *testing.T) {
	r := &recording{status: 200, body: embBody(1024, 0)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: 0})
	v, err := c.Embed(context.Background(), "x")
	require.NoError(t, err)
	assert.Len(t, v, 1024)
}

func TestEmbedRejectsWrongCount(t *testing.T) {
	// The server claims to answer one input but returns two items.
	r := &recording{status: 200, body: embBody(dim768, 0, 1)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned 2 vectors for 1 inputs")
}

// --- Batch ----------------------------------------------------------------

func TestEmbedBatchReturnsInOrder(t *testing.T) {
	r := &recording{status: 200, body: embBody(4, 0, 1, 2)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: 4})
	got, err := c.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, v := range got {
		assert.Len(t, v, 4)
	}
	assert.Equal(t, []any{"a", "b", "c"}, r.seenInput())
}

func TestEmbedBatchEmpty(t *testing.T) {
	c := New(Config{URL: "http://127.0.0.1:1/v1/embeddings", ExpectedDim: dim768})
	got, err := c.EmbedBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestEmbedBatchRejectsEmptyElement(t *testing.T) {
	c := New(Config{URL: "http://127.0.0.1:1/v1/embeddings", ExpectedDim: dim768})
	_, err := c.EmbedBatch(context.Background(), []string{"a", "", "c"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text[1] is empty")
}

// TestEmbedBatchFallsBackToPerText covers older llama-server builds that
// reject array input. The array request fails with a 400 and three single
// requests succeed.
func TestEmbedBatchFallsBackToPerText(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var payload map[string]any
		_ = json.Unmarshal(b, &payload)
		array, _ := payload["input"].([]any)
		mu.Lock()
		calls++
		isArray := array != nil
		mu.Unlock()
		if isArray {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"array input not supported"}}`))
			return
		}
		_, _ = w.Write([]byte(embBody(dim768, 0)))
	}))
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	got, err := c.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	require.NoError(t, err)
	require.Len(t, got, 3)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 4, calls, "1 array attempt + 3 per-text retries")
}

// --- Errors ---------------------------------------------------------------

func TestEmbedSurfacesHTTPError(t *testing.T) {
	r := &recording{status: 500, body: `{"error":{"message":"boom"}}`}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
	assert.Contains(t, err.Error(), "boom")
}

func TestEmbedSurfacesUnknownModel(t *testing.T) {
	r := &recording{status: 404, body: `{"error":{"message":"model 'nope' does not exist"}}`}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "nope", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
	assert.Contains(t, err.Error(), "does not exist")
}

func TestEmbedHandlesAPIErrorEnvelopeIn200(t *testing.T) {
	r := &recording{status: 200, body: `{"error":{"message":"invalid input"}}`}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected request")
	assert.Contains(t, err.Error(), "invalid input")
}

func TestEmbedHandlesMalformedJSON(t *testing.T) {
	r := &recording{status: 200, body: "<html>not json</html>"}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad JSON")
}

func TestEmbedHandlesEmptyData(t *testing.T) {
	r := &recording{status: 200, body: `{"object":"list","model":"m","data":[]}`}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty data array")
}

func TestEmbedHandlesOutOfRangeIndex(t *testing.T) {
	r := &recording{status: 200, body: embBody(4, 5)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: 4})
	_, err := c.EmbedBatch(context.Background(), []string{"a", "b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestEmbedHandlesDuplicateIndex(t *testing.T) {
	r := &recording{status: 200, body: `{"object":"list","model":"m","data":[` +
		fmt.Sprintf(`{"index":0,"embedding":%s},{"index":0,"embedding":%s}`, vecJSON(4), vecJSON(4)) + `]}`}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: 4})
	_, err := c.EmbedBatch(context.Background(), []string{"a", "b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate index")
}

func TestEmbedUnreachable(t *testing.T) {
	c := New(Config{URL: "http://127.0.0.1:9/v1/embeddings", Model: "m", ExpectedDim: dim768, TimeoutSecs: 1})
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
}

// --- Timeouts -------------------------------------------------------------

func TestEmbedHonoursCallerContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(embBody(dim768, 0)))
	}))
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768, TimeoutSecs: 60})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := c.Embed(ctx, "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
}

func TestEmbedHonoursConfigTimeout(t *testing.T) {
	// The handler must not answer on its own. Note it cannot wait on
	// req.Context(): when only the *client's* deadline expires the TCP
	// connection stays open (kept alive for reuse) and the server never sees a
	// disconnect, so waiting on req.Context() here would make httptest.Server.
	// Close() block forever and the test binary hang. A release channel that the
	// test always closes is deterministic.
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-stop
	}))
	defer srv.Close() // runs 2nd: waits for the handler to return
	defer close(stop) // runs 1st: releases the handler, so Close() cannot stall

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768, TimeoutSecs: 1})
	start := time.Now()
	_, err := c.Embed(context.Background(), "x")
	require.Error(t, err)
	assert.Less(t, time.Since(start), 3*time.Second, "the per-request deadline must fire")
	assert.Contains(t, err.Error(), "unreachable")
}

// --- Ping -----------------------------------------------------------------

func TestPingSucceedsAgainstLiveServer(t *testing.T) {
	r := &recording{status: 200, body: embBody(dim768, 0)}
	srv := httptest.NewServer(r.handler())
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "m", ExpectedDim: dim768})
	require.NoError(t, c.Ping(context.Background()))
	assert.Equal(t, pingText, r.seenInput())
}

func TestPingFailsWhenDown(t *testing.T) {
	c := New(Config{URL: "http://127.0.0.1:9/v1/embeddings", Model: "m", ExpectedDim: dim768, TimeoutSecs: 1})
	err := c.Ping(context.Background())
	require.Error(t, err)
}
