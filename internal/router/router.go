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
		auth.GET("/group/:username", middleware.AuthRequired(authSvc), authCtrl.GetUserGroups)
		auth.POST("/group/:username", middleware.AuthRequired(authSvc), authCtrl.AddUserToGroup)
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
