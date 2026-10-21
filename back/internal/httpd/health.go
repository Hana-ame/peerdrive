package httpd

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

const jsonContentType = "application/json; charset=utf-8"

const textContentType = "text/plain; charset=utf-8"

// startedAt is the process start time, used by /health to report uptime.
// Container orchestrators read it to notice a process that keeps restarting.
var startedAt = time.Now()

// PingHandler answers the legacy load-balancer probe: 200 with the body
// "pong".
//
// Extracted from controller.Ping so the response bytes live in the shell that
// owns the port. The response must stay byte-identical: load balancers and old
// scripts compare the literal body, not the route.
func PingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", textContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "pong")
}

// LivenessHandler is the /health liveness probe: 200 with status and uptime,
// and it checks no dependency.
//
// The reason it checks nothing is deliberate and worth keeping intact: a
// liveness probe that depends on the database turns a database blip into a
// restart, which restarts the thing that was already struggling. Readiness
// (ReadinessHandler) is the probe that is allowed to fail on dependencies.
func LivenessHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"uptime_sec": int64(time.Since(startedAt).Seconds()),
	})
}

// ReadinessHandler is the /ready readiness probe. It fails closed:
//
//   - probe is nil  -> 503 "health check not wired";
//   - probe errors  -> 503 "database ping failed";
//   - otherwise     -> 200 "ready".
//
// An always-200 readiness is worse than none: it tells the orchestrator to keep
// sending traffic to a node that cannot serve it. Failing closed turns that
// into a visible 503.
//
// probe is injected rather than imported: the controller layer does not touch
// persistence, so the readiness dependency arrives as a func() error.
func ReadinessHandler(probe func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if probe == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"reason": "health check not wired",
			})
			return
		}
		if err := probe(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"reason": "database ping failed",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	}
}

// writeJSON writes a JSON response with the exact Content-Type gin's c.JSON
// sets. Bodies are marshalled from a map so encoding/json sorts the keys
// alphabetically — matching gin.H, which is also a map. Field order is part of
// the response bytes, so a struct literal would not be equivalent.
func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	data, err := json.Marshal(body)
	if err != nil {
		// body is built from literals; this cannot fail in practice. Writing
		// nothing is wrong anyway, so fail loudly with 500.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
