package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// newInboxApproveRouter wires the two approve spellings the way router.go does
// (minus authRequired and any store), so both parameter surfaces of Issue #353
// can be exercised through a real gin engine.
func newInboxApproveRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/files")
	g.POST("/inbox/approve", ApproveInboxFile)
	g.POST("/inbox/:hash/approve", ApproveInboxFile)
	return r
}

const inboxApproveHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func postToRouter(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if body != "" {
		req = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestApproveInboxFile_BodyHashStillAccepted(t *testing.T) {
	w := postToRouter(newInboxApproveRouter(), "/files/inbox/approve", `{"hash":"`+inboxApproveHash+`"}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"`+inboxApproveHash+`"`)
}

func TestApproveInboxFile_PathHashAliasApproves(t *testing.T) {
	w := postToRouter(newInboxApproveRouter(), "/files/inbox/"+inboxApproveHash+"/approve", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hash":"`+inboxApproveHash+`"`)
}

func TestApproveInboxFile_PathHashWinsOverBody(t *testing.T) {
	other := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	w := postToRouter(newInboxApproveRouter(), "/files/inbox/"+inboxApproveHash+"/approve", `{"hash":"`+other+`"}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), inboxApproveHash)
	assert.NotContains(t, w.Body.String(), other)
}

func TestApproveInboxFile_BodyWithoutHashStillRejected(t *testing.T) {
	w := postToRouter(newInboxApproveRouter(), "/files/inbox/approve", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "hash is required")
}

func TestApproveInboxFile_BadPathHashRejected(t *testing.T) {
	w := postToRouter(newInboxApproveRouter(), "/files/inbox/not-a-sha/approve", "")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid sha256 hash")
}

func TestApproveInboxFile_AliasReachesTheHandlerNotGin404(t *testing.T) {
	// Negative control: the 400s above come from ApproveInboxFile, not from the
	// router answering "404 page not found" for an unknown path.
	w := postToRouter(newInboxApproveRouter(), "/files/inbox/not-a-sha/nope", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "404 page not found")
}
