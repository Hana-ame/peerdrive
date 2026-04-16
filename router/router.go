package router

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"peerdrive/controller"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()

	// API Routes
	r.GET("/ping", controller.Ping)

	// Swagger Route
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}
