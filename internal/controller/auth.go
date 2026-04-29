package controller

import (
	"net/http"

	"peerdrive-registration/internal/model"
	"peerdrive-registration/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthController struct {
	svc        *service.AuthService
	relaySvc   *service.RelayService
	commentSvc *service.CommentService
	nodeSvc    *service.NodeService
}

func NewAuthController(svc *service.AuthService, relaySvc *service.RelayService) *AuthController {
	return &AuthController{svc: svc, relaySvc: relaySvc}
}

func (c *AuthController) SetCommentService(cs *service.CommentService) {
	c.commentSvc = cs
}

func (c *AuthController) SetNodeService(ns *service.NodeService) {
	c.nodeSvc = ns
}

// Register godoc
// @Summary      Register new user
// @Description  Create a new user account. Returns JWT token on success.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.RegisterRequest  true  "Registration request"
// @Success      201  {object}  model.TokenResponse
// @Failure      409  {object}  map[string]string  "Username already exists"
// @Failure      400  {object}  map[string]string  "Invalid request"
// @Router       /auth/register [post]
func (c *AuthController) Register(ctx *gin.Context) {
	var req model.RegisterRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, err := c.svc.Register(req)
	if err != nil {
		switch err {
		case service.ErrUsernameTaken:
			ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	ctx.JSON(http.StatusCreated, token)
}

// Login godoc
// @Summary      Login
// @Description  Authenticate user and return JWT token.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  model.LoginRequest  true  "Login request"
// @Success      200  {object}  model.TokenResponse
// @Failure      401  {object}  map[string]string  "Invalid credentials"
// @Failure      400  {object}  map[string]string  "Invalid request"
// @Router       /auth/login [post]
func (c *AuthController) Login(ctx *gin.Context) {
	var req model.LoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, err := c.svc.Login(req)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, token)
}

// WhoAmI godoc
// @Summary      Get current user info
// @Description  Returns JWT claims for the authenticated user.
// @Tags         auth
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]string
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Router       /auth/whoami [get]
func (c *AuthController) WhoAmI(ctx *gin.Context) {
	username, _ := ctx.Get("username")
	role, _ := ctx.Get("role")

	ctx.JSON(http.StatusOK, gin.H{
		"username": username,
		"role":     role,
	})
}

func (c *AuthController) ListUsers(ctx *gin.Context) {
	users, err := c.svc.ListUsers()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, model.ListResponse{
		Users: users,
		Total: len(users),
	})
}

func (c *AuthController) Ping(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "peerdrive-registration",
	})
}

// ──────────────────────────────
//  Relay node registration
// ──────────────────────────────

type relayRegisterRequest struct {
	PeerID    string   `json:"peer_id" binding:"required"`
	Addrs     []string `json:"addrs"`
	StorageMB int      `json:"storage_mb"`
	Version   string   `json:"version"`
}

type relayHeartbeatRequest struct {
	PeerID  string  `json:"peer_id" binding:"required"`
	LoadPct float64 `json:"load_pct"`
}

// RegisterRelay handles POST /p2p/relay/register
func (c *AuthController) RegisterRelay(ctx *gin.Context) {
	var req relayRegisterRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.relaySvc.Register(model.RelayNode{
		PeerID:    req.PeerID,
		Addrs:     req.Addrs,
		StorageMB: req.StorageMB,
		Version:   req.Version,
	}); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "registered"})
}

// ListRelays handles GET /p2p/relay/list
func (c *AuthController) ListRelays(ctx *gin.Context) {
	relays, err := c.relaySvc.ListActive()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if relays == nil {
		relays = []model.RelayNode{}
	}
	ctx.JSON(http.StatusOK, gin.H{"relays": relays})
}

// RelayHeartbeat handles POST /p2p/relay/heartbeat
func (c *AuthController) RelayHeartbeat(ctx *gin.Context) {
	var req relayHeartbeatRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.relaySvc.Heartbeat(req.PeerID, req.LoadPct); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ──────────────────────────────
//  Group membership
// ──────────────────────────────

// GetUserGroups handles GET /auth/group/:username
func (c *AuthController) GetUserGroups(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	groups, err := c.svc.GetGroups(username)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if groups == nil {
		groups = []model.UserGroup{}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"username": username,
		"groups":   groups,
	})
}

// AddUserToGroup handles POST /auth/group/:username
func (c *AuthController) AddUserToGroup(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	var req struct {
		GroupName string `json:"group_name" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.svc.AddToGroup(username, req.GroupName); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "added to group", "username": username, "group": req.GroupName})
}

// ──────────────────────────────
//  Comments
// ──────────────────────────────

type postCommentRequest struct {
	Content string `json:"content" binding:"required"`
}

// PostComment handles POST /comments/:hash
func (c *AuthController) PostComment(ctx *gin.Context) {
	hash := ctx.Param("hash")
	if hash == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "hash is required"})
		return
	}

	// Require authentication
	username, exists := ctx.Get("username")
	if !exists || username == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req postCommentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	comment, err := c.commentSvc.Post(hash, username.(string), req.Content)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, comment)
}

// GetComments handles GET /comments/:hash (no auth required)
func (c *AuthController) GetComments(ctx *gin.Context) {
	hash := ctx.Param("hash")
	if hash == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "hash is required"})
		return
	}

	comments, err := c.commentSvc.ListByHash(hash)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if comments == nil {
		comments = []model.Comment{}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"hash":     hash,
		"comments": comments,
		"total":    len(comments),
	})
}

// ──────────────────────────────
//  Stats
// ──────────────────────────────

// GetStats handles GET /stats
func (c *AuthController) GetStats(ctx *gin.Context) {
	totalUsers, err := c.svc.CountUsers()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count users: " + err.Error()})
		return
	}

	activeRelays, err := c.relaySvc.CountActive()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count relays: " + err.Error()})
		return
	}

	totalComments := 0
	if c.commentSvc != nil {
		totalComments, err = c.commentSvc.CountAll()
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to count comments: " + err.Error()})
			return
		}
	}

	activeNodes := 0
	if c.nodeSvc != nil {
		activeNodes, _ = c.nodeSvc.CountActive()
	}

	ctx.JSON(http.StatusOK, model.StatsExt{
		TotalUsers:    totalUsers,
		ActiveRelays:  activeRelays,
		ActiveNodes:   activeNodes,
		TotalComments: totalComments,
	})
}

// ──────────────────────────────
//  Extended group management
// ──────────────────────────────

// GetAllGroups handles GET /auth/groups
func (c *AuthController) GetAllGroups(ctx *gin.Context) {
	groups, err := c.svc.GetAllGroups()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if groups == nil {
		groups = []model.GroupDetail{}
	}
	ctx.JSON(http.StatusOK, gin.H{"groups": groups})
}

// GetGroupMembers handles GET /auth/groups/:groupname/members
func (c *AuthController) GetGroupMembers(ctx *gin.Context) {
	groupName := ctx.Param("groupname")
	if groupName == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "group name is required"})
		return
	}

	members, err := c.svc.GetGroupMembers(groupName)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if members == nil {
		members = []string{}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"group":   groupName,
		"members": members,
		"total":   len(members),
	})
}

// RemoveUserFromGroup handles DELETE /auth/group/:username/:groupname
func (c *AuthController) RemoveUserFromGroup(ctx *gin.Context) {
	username := ctx.Param("username")
	groupName := ctx.Param("groupname")
	if username == "" || groupName == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username and group name are required"})
		return
	}

	if err := c.svc.RemoveFromGroup(username, groupName); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "removed from group", "username": username, "group": groupName})
}

// ──────────────────────────────
//  Service policy
// ──────────────────────────────

// GetServicePolicy handles GET /auth/service-policy/:username
func (c *AuthController) GetServicePolicy(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	policy, err := c.svc.GetServicePolicy(username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, policy)
}

// SetServicePolicy handles POST /auth/service-policy/:username
func (c *AuthController) SetServicePolicy(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	var req struct {
		AllowRelay *bool  `json:"allow_relay"`
		AllowP2P   *bool  `json:"allow_p2p"`
		Notes      string `json:"notes"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	policy := &model.ServicePolicy{Username: username, AllowRelay: true, AllowP2P: true, Notes: ""}
	if req.AllowRelay != nil {
		policy.AllowRelay = *req.AllowRelay
	}
	if req.AllowP2P != nil {
		policy.AllowP2P = *req.AllowP2P
	}
	if req.Notes != "" {
		policy.Notes = req.Notes
	}

	if err := c.svc.SetServicePolicy(policy); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, policy)
}

// ──────────────────────────────
//  Storage tracking
// ──────────────────────────────

// GetStorage handles GET /auth/storage/:username
func (c *AuthController) GetStorage(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	storage, err := c.svc.GetStorage(username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, storage)
}

// UpdateStorage handles POST /auth/storage/:username
func (c *AuthController) UpdateStorage(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	var req struct {
		UsedBytes  *int64 `json:"used_bytes"`
		LimitBytes *int64 `json:"limit_bytes"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Fetch current values to merge with request
	current, err := c.svc.GetStorage(username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	used := current.UsedBytes
	limit := current.LimitBytes
	if req.UsedBytes != nil {
		used = *req.UsedBytes
	}
	if req.LimitBytes != nil {
		limit = *req.LimitBytes
	}

	if err := c.svc.UpdateStorage(username, used, limit); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, &model.UserStorage{
		Username:   username,
		UsedBytes:  used,
		LimitBytes: limit,
	})
}

// ──────────────────────────────
//  Relay operator info
// ──────────────────────────────

// GetRelayOperator handles GET /p2p/relay/:peer_id/operator
func (c *AuthController) GetRelayOperator(ctx *gin.Context) {
	peerID := ctx.Param("peer_id")
	if peerID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "peer_id is required"})
		return
	}

	node, err := c.relaySvc.GetByPeerID(peerID)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "relay node not found"})
		return
	}

	ctx.JSON(http.StatusOK, node)
}

// ListUserRelays handles GET /auth/relays/:username
func (c *AuthController) ListUserRelays(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	nodes, err := c.relaySvc.ListByOperator(username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if nodes == nil {
		nodes = []model.RelayNodeDetail{}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"username": username,
		"relays":   nodes,
		"total":    len(nodes),
	})
}

// SetRelayOperator handles POST /p2p/relay/:peer_id/operator
func (c *AuthController) SetRelayOperator(ctx *gin.Context) {
	peerID := ctx.Param("peer_id")
	if peerID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "peer_id is required"})
		return
	}

	var req struct {
		Username string `json:"username" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.relaySvc.SetOperator(peerID, req.Username); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "operator set", "peer_id": peerID, "username": req.Username})
}

// ──────────────────────────────
//  Node registration & operator query
// ──────────────────────────────

type nodeRegisterRequest struct {
	PeerID  string   `json:"peer_id" binding:"required"`
	Addrs   []string `json:"addrs"`
	Version string   `json:"version"`
}

// RegisterNode handles POST /auth/node/register (authenticated)
func (c *AuthController) RegisterNode(ctx *gin.Context) {
	username, _ := ctx.Get("username")
	if username == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var req nodeRegisterRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.nodeSvc.Register(model.PeerNode{
		PeerID:   req.PeerID,
		Username: username.(string),
		Addrs:    req.Addrs,
		Version:  req.Version,
	}); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "registered", "peer_id": req.PeerID, "username": username})
}

// NodeHeartbeat handles POST /auth/node/heartbeat
func (c *AuthController) NodeHeartbeat(ctx *gin.Context) {
	var req struct {
		PeerID string `json:"peer_id" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.nodeSvc.Heartbeat(req.PeerID); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ReportNodeStats handles POST /auth/node/stats
func (c *AuthController) ReportNodeStats(ctx *gin.Context) {
	var req struct {
		PeerID        string `json:"peer_id" binding:"required"`
		UploadBytes   int64  `json:"upload_bytes"`
		DownloadBytes int64  `json:"download_bytes"`
	}
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := c.nodeSvc.AddTransferStats(req.PeerID, req.UploadBytes, req.DownloadBytes); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"status": "stats recorded"})
}

// GetNodeOperator handles GET /p2p/node/:peer_id/operator (public)
func (c *AuthController) GetNodeOperator(ctx *gin.Context) {
	peerID := ctx.Param("peer_id")
	if peerID == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "peer_id is required"})
		return
	}

	info, err := c.nodeSvc.GetByPeerID(peerID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if info == nil {
		ctx.JSON(http.StatusOK, gin.H{"peer_id": peerID, "operator": nil, "note": "anonymous node"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"peer_id": peerID, "operator": info})
}

// ListUserNodes handles GET /auth/nodes/:username
func (c *AuthController) ListUserNodes(ctx *gin.Context) {
	username := ctx.Param("username")
	if username == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}

	nodes, err := c.nodeSvc.ListByUsername(username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if nodes == nil {
		nodes = []model.UserNodeInfo{}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"username": username,
		"nodes":    nodes,
		"total":    len(nodes),
	})
}
