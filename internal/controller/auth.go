// Package controller 提供所有 HTTP API 处理函数。每个文件对应一组相关端点，
// 通过独立的 Init* 函数注入服务依赖。
package controller

import (
	"net/http"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthController struct {
	authSvc *service.AuthService
}

func NewAuthController(authSvc *service.AuthService) *AuthController {
	return &AuthController{authSvc: authSvc}
}

func (c *AuthController) Register(ctx *gin.Context) {
	var req model.RegisterRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	resp, err := c.authSvc.Register(req)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, resp)
}

func (c *AuthController) Login(ctx *gin.Context) {
	var req model.LoginRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	resp, err := c.authSvc.Login(req)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, resp)
}

func (c *AuthController) Logout(ctx *gin.Context) {
	authKey := ctx.GetHeader("Authorization")
	if len(authKey) > 7 && authKey[:7] == "Bearer " {
		authKey = authKey[7:]
	}

	if authKey == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "missing authorization header"})
		return
	}

	if err := c.authSvc.Logout(authKey); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}

func (c *AuthController) Me(ctx *gin.Context) {
	authKey := ctx.GetHeader("Authorization")
	if len(authKey) > 7 && authKey[:7] == "Bearer " {
		authKey = authKey[7:]
	}

	if authKey == "" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
		return
	}

	user, err := c.authSvc.ValidateKey(authKey)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired auth key"})
		return
	}

	ctx.JSON(http.StatusOK, user)
}
