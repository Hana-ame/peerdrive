package router

import (
	"peerdrive-registration/internal/controller"
	"peerdrive-registration/internal/middleware"
	"peerdrive-registration/internal/service"

	"github.com/gin-gonic/gin"
)

func SetupRouter(authCtrl *controller.AuthController, authSvc *service.AuthService) *gin.Engine {
	r := gin.Default()

	r.GET("/ping", authCtrl.Ping)

	auth := r.Group("/auth")
	{
		auth.POST("/register", authCtrl.Register)
		auth.POST("/login", authCtrl.Login)
		auth.GET("/whoami", middleware.AuthRequired(authSvc), authCtrl.WhoAmI)
		auth.GET("/list", middleware.AuthRequired(authSvc), middleware.AdminRequired(), authCtrl.ListUsers)

		// Group membership
		auth.GET("/groups", middleware.AuthRequired(authSvc), authCtrl.GetAllGroups)
		auth.GET("/group/:username", middleware.AuthRequired(authSvc), authCtrl.GetUserGroups)
		auth.POST("/group/:username", middleware.AuthRequired(authSvc), authCtrl.AddUserToGroup)
		auth.DELETE("/group/:username/:groupname", middleware.AuthRequired(authSvc), authCtrl.RemoveUserFromGroup)
		auth.GET("/groups/:groupname/members", middleware.AuthRequired(authSvc), authCtrl.GetGroupMembers)

		// Service policy
		auth.GET("/service-policy/:username", middleware.AuthRequired(authSvc), authCtrl.GetServicePolicy)
		auth.POST("/service-policy/:username", middleware.AuthRequired(authSvc), authCtrl.SetServicePolicy)

		// Storage tracking
		auth.GET("/storage/:username", middleware.AuthRequired(authSvc), authCtrl.GetStorage)
		auth.POST("/storage/:username", middleware.AuthRequired(authSvc), authCtrl.UpdateStorage)

		// User's operated relay nodes
		auth.GET("/relays/:username", middleware.AuthRequired(authSvc), authCtrl.ListUserRelays)

		// Node registration (user binds their node to their account)
		auth.POST("/node/register", middleware.AuthRequired(authSvc), authCtrl.RegisterNode)
		auth.POST("/node/heartbeat", middleware.AuthRequired(authSvc), authCtrl.NodeHeartbeat)
		auth.POST("/node/stats", middleware.AuthRequired(authSvc), authCtrl.ReportNodeStats)
		auth.GET("/nodes/:username", middleware.AuthRequired(authSvc), authCtrl.ListUserNodes)
	}

	protected := r.Group("/api")
	protected.Use(middleware.AuthRequired(authSvc))
	{
		protected.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"status": "ok", "username": c.GetString("username")})
		})
	}

	// Relay node registration and discovery (no auth required)
	p2pRelay := r.Group("/p2p/relay")
	{
		p2pRelay.POST("/register", authCtrl.RegisterRelay)
		p2pRelay.GET("/list", authCtrl.ListRelays)
		p2pRelay.POST("/heartbeat", authCtrl.RelayHeartbeat)
		p2pRelay.GET("/:peer_id/operator", middleware.AuthRequired(authSvc), authCtrl.GetRelayOperator)
		p2pRelay.POST("/:peer_id/operator", middleware.AuthRequired(authSvc), authCtrl.SetRelayOperator)
	}

	// Node operator query (public — anyone can check who operates a node)
	p2pNode := r.Group("/p2p/node")
	{
		p2pNode.GET("/:peer_id/operator", authCtrl.GetNodeOperator)
	}

	// Comments on collections
	comments := r.Group("/comments")
	{
		comments.GET("/:hash", authCtrl.GetComments)
		comments.POST("/:hash", middleware.AuthRequired(authSvc), authCtrl.PostComment)
	}

	// Stats
	r.GET("/stats", authCtrl.GetStats)

	return r
}
