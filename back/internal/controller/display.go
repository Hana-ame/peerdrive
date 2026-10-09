// Package controller provides HTTP handlers for remote display and screen synchronization.
package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/transport"
)

// displayPeer holds the active PeerJSService instance for screen casting and display control (Issue #243).
var displayPeer *transport.PeerJSService

// InitDisplayController injects the PeerJSService into the display controller.
func InitDisplayController(svc *transport.PeerJSService) {
	displayPeer = svc
}

// ListDisplayScreens handles GET /display/screens, returning active display receivers.
func ListDisplayScreens(c *gin.Context) {
	channel := c.Query("channel")
	if displayPeer == nil || displayPeer.DisplayManager() == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	screens := displayPeer.DisplayManager().ListScreens(channel)
	if screens == nil {
		screens = []*transport.DisplayScreen{}
	}
	c.JSON(http.StatusOK, screens)
}

// GetDisplayStatus handles GET /display/status, returning the playback and media state of a channel.
func GetDisplayStatus(c *gin.Context) {
	channel := c.Query("channel")
	if displayPeer == nil || displayPeer.DisplayManager() == nil {
		c.JSON(http.StatusOK, gin.H{"channel": channel})
		return
	}
	st := displayPeer.DisplayManager().GetState(channel)
	c.JSON(http.StatusOK, st)
}

// CastDisplay handles POST /display/cast, commanding a file or media to be shown on target screen(s).
func CastDisplay(c *gin.Context) {
	var req struct {
		TargetSessionID string   `json:"targetSessionId"`
		Channel         string   `json:"channel"`
		MediaType       string   `json:"mediaType"`
		Hash            string   `json:"hash"`
		URL             string   `json:"url"`
		Title           string   `json:"title"`
		MimeType        string   `json:"mimeType"`
		Autoplay        bool     `json:"autoplay"`
		Loop            bool     `json:"loop"`
		Volume          *float64 `json:"volume"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if displayPeer == nil || displayPeer.DisplayManager() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "display service not available"})
		return
	}
	cmd := transport.DisplayFrame{
		Type:      "display",
		Action:    "show",
		SessionID: req.TargetSessionID,
		Channel:   req.Channel,
		MediaType: req.MediaType,
		Hash:      req.Hash,
		URL:       req.URL,
		Title:     req.Title,
		MimeType:  req.MimeType,
		Autoplay:  req.Autoplay,
		Loop:      req.Loop,
		Volume:    req.Volume,
	}
	if err := displayPeer.DisplayManager().Cast(nil, req.TargetSessionID, req.Channel, cmd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ControlDisplay handles POST /display/control, adjusting playback on target screen(s).
func ControlDisplay(c *gin.Context) {
	var req struct {
		TargetSessionID string   `json:"targetSessionId"`
		Channel         string   `json:"channel"`
		ControlAction   string   `json:"controlAction"`
		Position        float64  `json:"position"`
		Volume          *float64 `json:"volume"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if displayPeer == nil || displayPeer.DisplayManager() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "display service not available"})
		return
	}
	cmd := transport.DisplayFrame{
		Type:          "display",
		Action:        "control",
		SessionID:     req.TargetSessionID,
		Channel:       req.Channel,
		ControlAction: req.ControlAction,
		Position:      req.Position,
		Volume:        req.Volume,
	}
	if err := displayPeer.DisplayManager().Control(nil, req.TargetSessionID, req.Channel, cmd); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ClearDisplay handles POST /display/clear, resetting target screen(s) or channel.
func ClearDisplay(c *gin.Context) {
	var req struct {
		TargetSessionID string `json:"targetSessionId"`
		Channel         string `json:"channel"`
	}
	_ = c.ShouldBindJSON(&req)
	if displayPeer == nil || displayPeer.DisplayManager() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "display service not available"})
		return
	}
	if err := displayPeer.DisplayManager().Clear(nil, req.TargetSessionID, req.Channel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
