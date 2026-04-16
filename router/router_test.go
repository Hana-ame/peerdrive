package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPingRoute(t *testing.T) {
	// Setup router
	r := SetupRouter()

	// Create a request
	req, _ := http.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()

	// Perform request
	r.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"message":"pong"}`, w.Body.String())
}
