package controller

import (
	"net/http"

	"peerdrive-registration/internal/model"
	"peerdrive-registration/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthController struct {
	svc *service.AuthService
}

func NewAuthController(svc *service.AuthService) *AuthController {
	return &AuthController{svc: svc}
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
