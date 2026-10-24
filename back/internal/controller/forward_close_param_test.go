package controller

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// newForwardCloseRouter wires the three close spellings the way router.go does
// (minus authRequired): the legacy POST body endpoint plus the two RESTful DELETE
// aliases added for Issue #354.
func newForwardCloseRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	p := r.Group("/p2p")
	p.POST("/forward/close", CloseForwardSession)
	p.DELETE("/forward/listeners/:key", CloseForwardSession)
	p.DELETE("/forward/tunnels/:peer_id", CloseForwardSession)
	return r
}

func requestForwardClose(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCloseForwardSession_BodyKeyStillHonoured(t *testing.T) {
	w := requestForwardClose(newForwardCloseRouter(), http.MethodPost, "/p2p/forward/close", `{"key":"absent-key"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "no matching forward session")
}

func TestCloseForwardSession_BodyPeerIDStillHonoured(t *testing.T) {
	w := requestForwardClose(newForwardCloseRouter(), http.MethodPost, "/p2p/forward/close", `{"peer_id":"peer1"}`)
	// A peer_id counts as a match, so the handler reaches the 200 branch even with
	// zero local listeners (forwardPeer is nil here, so no stream is dropped).
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"closed_listeners":0`)
}

func TestCloseForwardSession_EmptyBodyStillRejected(t *testing.T) {
	w := requestForwardClose(newForwardCloseRouter(), http.MethodPost, "/p2p/forward/close", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid request")
}

func TestCloseForwardSession_DeleteListenerAliasCloses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listen unavailable: %v", err)
	}
	defer ln.Close()
	const key = "peer1:9"
	fwdListenersMu.Lock()
	fwdListeners[key] = &fwdListener{key: key, targetPeer: "peer1", ln: ln}
	fwdListenersMu.Unlock()
	t.Cleanup(func() {
		fwdListenersMu.Lock()
		delete(fwdListeners, key)
		fwdListenersMu.Unlock()
	})

	w := requestForwardClose(newForwardCloseRouter(), http.MethodDelete, "/p2p/forward/listeners/"+key, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"closed"`)
	assert.Contains(t, w.Body.String(), `"closed_listeners":1`)

	fwdListenersMu.Lock()
	_, stillThere := fwdListeners[key]
	fwdListenersMu.Unlock()
	assert.False(t, stillThere, "the DELETE alias must have removed the listener")
}

func TestCloseForwardSession_DeleteTunnelAliasTargetsPeer(t *testing.T) {
	w := requestForwardClose(newForwardCloseRouter(), http.MethodDelete, "/p2p/forward/tunnels/peer1", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"closed_listeners":0`)
}

func TestCloseForwardSession_AliasReachesTheHandlerNotGin404(t *testing.T) {
	// Negative control: the 404 above is the handler's JSON answer, not the
	// router's generic "404 page not found" for a path that does not exist.
	w := requestForwardClose(newForwardCloseRouter(), http.MethodDelete, "/p2p/forward/listeners/none", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "no matching forward session")
}
