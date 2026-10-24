// Health check controller: liveness probe /health and readiness probe /ready.
//
// Why not just /ping: there are two types of probes with different semantics, and mixing
// them causes two kinds of incidents —
//   liveness (/health): is the process still alive. If it fails, restart the container.
//     So it **checks no dependencies**, otherwise a database blip would kill a healthy
//     service, which then restarts, then blips again... (restart storm)
//   readiness (/ready): can it serve traffic. If it fails, drain traffic and wait for recovery.
//     So it must actually query dependencies (here, the metadata database).
// /ping is retained: a legacy endpoint that load balancers and old scripts still use.
//
// The response bodies themselves live in internal/httpd (PingHandler /
// LivenessHandler / ReadinessHandler): the shell package owns the probe bytes
// so the three probes cannot drift apart in Content-Type or JSON key order.
// This file keeps the dependency wiring (InitHealth), the swag tags and the
// documentation of the semantics above.

package controller

import (
	"github.com/gin-gonic/gin"

	"peerdrive/internal/httpd"
)

// dbPing is the "is the database alive?" probe injected by the assembly layer (Router.Engine in the router package).
//
// Why not directly import repository: the controller layer not touching persistence is a hard
// implementation convention (service layer → repository layer; controllers only see services).
// The probe just wants to know "can it reach the DB" — breaking the rule once leads to a second
// time. Injecting a func() error is sufficient.
var dbPing func() error

// InitHealth wires dependencies (called from router, same pattern as InitFileController etc.).
func InitHealth(ping func() error) {
	dbPing = ping
}

// Health godoc
// @Summary Liveness probe
// @Description Process liveness probe, checks no dependencies
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func Health(c *gin.Context) {
	httpd.LivenessHandler(c.Writer, c.Request)
}

// Ready godoc
// @Summary Readiness probe
// @Description Readiness probe: the metadata database must be reachable to be considered ready
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /ready [get]
func Ready(c *gin.Context) {
	// If not wired up, treat as not ready: better to let the probe go red than pretend everything is
	// fine — a readiness that always returns 200 is more dangerous than no readiness at all (it
	// fools the orchestration system).
	httpd.ReadinessHandler(dbPing)(c.Writer, c.Request)
}
