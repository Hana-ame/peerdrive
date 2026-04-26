package router

import (
	"peerdrive-registration/internal/controller"
	"peerdrive-registration/internal/middleware"
	"peerdrive-registration/internal/service"

	"github.com/gin-gonic/gin"
)

func SetupRouter(authCtrl *controller.AuthController, authSvc *service.AuthService) *gin.Engine {
	r := gin.Default()

	auth := r.Group("/auth")
	{
		auth.POST("/register", authCtrl.Register)
		auth.POST("/login", authCtrl.Login)
		auth.GET("/whoami", middleware.AuthRequired(authSvc), authCtrl.WhoAmI)
	}

	protected := r.Group("/api")
	protected.Use(middleware.AuthRequired(authSvc))
	{
		protected.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"status": "ok", "username": c.GetString("username")})
		})
	}

	return r
}
