package transport

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPDiscoveryAnnounceIncludesNodeTypeAndPeers verifies that the announce request body
// carries peerId/collections/peers/nodeType, for the signaling server's discovery/graph.
func TestHTTPDiscoveryAnnounceIncludesNodeTypeAndPeers(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &got))
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	d := NewHTTPDiscovery(srv.URL, "go-node-1", []string{"media"}, nil, func() []string {
		return []string{"web-1", "go-2"}
	})
	d.announce()

	require.NotNil(t, got)
	assert.Equal(t, "go-node-1", got["peerId"])
	assert.Equal(t, "go-persistent", got["nodeType"])
	assert.ElementsMatch(t, []any{"media"}, got["collections"])
	assert.ElementsMatch(t, []any{"web-1", "go-2"}, got["peers"])
}
