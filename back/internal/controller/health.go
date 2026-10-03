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

package controller

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// startedAt is the process start time, used by /health to return uptime (container orchestrators
// use this to determine if the container was repeatedly restarted).
var startedAt = time.Now()

// dbPing is the "is the database alive?" probe injected by the assembly layer (router.SetupRouter).
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
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"uptime_sec": int64(time.Since(startedAt).Seconds()),
	})
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
	if dbPing == nil {
		// If not wired up, treat as not ready: better to let the probe go red than pretend everything is
		// fine — a readiness that always returns 200 is more dangerous than no readiness at all (it
		// fools the orchestration system).
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": "health check not wired"})
		return
	}
	// Ping actually takes a connection and executes a query; only when the database file is deleted,
	// permissions are lost, or handles are exhausted will this go red.
	// No separate timeout: database/sql's Ping uses context, and this probe's timeout is controlled
	// by the caller (the orchestration system).
	if err := dbPing(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": "database ping failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
