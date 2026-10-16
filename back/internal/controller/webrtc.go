// Package controller provides HTTP handlers for WebRTC-related endpoints.
package controller

import (
	"net/http"

	"peerdrive/internal/config"

	"github.com/gin-gonic/gin"
)

// WebRTCInfoHandler returns a Gin handler for GET /p2p/webrtc/info, providing STUN/TURN configuration.
//
// #144: TURN is only usable when its static-auth credentials travel with the server
// URL. Emitting just the URL made every browser client issue an unauthenticated
// Allocate that a real TURN server rejects — the relay candidate never appeared and
// symmetric-NAT peers could not connect. The username/password pair is therefore
// forwarded alongside turn_server whenever credentials are configured.
//
// The response deliberately reports `turn_configured` so a client (and the operator
// reading /status) can tell "TURN not set up" from "TURN set up but unusable"; the
// old behaviour silently returned no turn_server at all in both cases.
//
// Note the credentials are readable by any caller of this endpoint: they are static
// TURN credentials, whose whole purpose is to be handed to untrusted peers, so this
// exposes nothing a TURN server does not already expose by design.
func WebRTCInfoHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp := gin.H{
			"stun_server":     cfg.WebRTCSTUNServer,
			"turn_configured": cfg.WebRTCTURNServer != "",
		}
		if cfg.WebRTCTURNServer != "" {
			resp["turn_server"] = cfg.WebRTCTURNServer
			// Only emit the credential fields when a username is actually set,
			// keeping the wire format of anonymous-TURN deployments unchanged.
			if cfg.WebRTCTURNUsername != "" {
				resp["turn_username"] = cfg.WebRTCTURNUsername
				resp["turn_password"] = cfg.WebRTCTURNPassword
			}
		}
		c.JSON(http.StatusOK, resp)
	}
}
