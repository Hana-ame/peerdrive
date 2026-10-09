// Package controller provides HTTP handlers for P2P live streaming and chunk distribution.
package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/transport"
)

// streamPeer holds the active PeerJSService instance for P2P live stream distribution (Issue #244).
var streamPeer *transport.PeerJSService

// InitStreamController injects the PeerJSService into the stream controller.
func InitStreamController(svc *transport.PeerJSService) {
	streamPeer = svc
}

// ListStreams handles GET /stream/list, returning active live streams.
func ListStreams(c *gin.Context) {
	if streamPeer == nil || streamPeer.StreamManager() == nil {
		c.JSON(http.StatusOK, []any{})
		return
	}
	streams := streamPeer.StreamManager().ListStreams()
	if streams == nil {
		streams = []*transport.StreamManifest{}
	}
	c.JSON(http.StatusOK, streams)
}

// GetStreamManifest handles GET /stream/:id/manifest, returning the live stream playlist and chunk sequence.
func GetStreamManifest(c *gin.Context) {
	id := c.Param("id")
	if streamPeer == nil || streamPeer.StreamManager() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stream service unavailable"})
		return
	}
	manifest, err := streamPeer.StreamManager().GetManifest(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, manifest)
}

// CreateStream handles POST /stream/create, creating a new live stream channel.
func CreateStream(c *gin.Context) {
	var req struct {
		StreamID string `json:"streamId"`
		Title    string `json:"title"`
		MimeType string `json:"mimeType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if streamPeer == nil || streamPeer.StreamManager() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stream service unavailable"})
		return
	}
	manifest, err := streamPeer.StreamManager().CreateStream(nil, req.StreamID, req.Title, req.MimeType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, manifest)
}

// PushStreamChunk handles POST /stream/:id/chunk, appending a media segment SHA to the stream.
func PushStreamChunk(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Hash      string  `json:"hash"`
		Size      int64   `json:"size"`
		Duration  float64 `json:"duration"`
		MimeType  string  `json:"mimeType"`
		Title     string  `json:"title"`
		Timestamp int64   `json:"timestamp"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body: " + err.Error()})
		return
	}
	if streamPeer == nil || streamPeer.StreamManager() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stream service unavailable"})
		return
	}
	chunk := transport.StreamChunk{
		Hash:      req.Hash,
		Size:      req.Size,
		Duration:  req.Duration,
		MimeType:  req.MimeType,
		Title:     req.Title,
		Timestamp: req.Timestamp,
	}
	res, err := streamPeer.StreamManager().PushChunk(nil, id, chunk)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

// CloseStream handles POST /stream/:id/close, ending a live stream channel.
func CloseStream(c *gin.Context) {
	id := c.Param("id")
	if streamPeer == nil || streamPeer.StreamManager() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stream service unavailable"})
		return
	}
	if err := streamPeer.StreamManager().CloseStream(nil, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "closed"})
}
