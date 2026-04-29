package service

import (
	"errors"
	"fmt"
	"os"
	"time"

	"peerdrive-registration/internal/model"
	"peerdrive-registration/internal/repository"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUsernameTaken      = errors.New("username already exists")
)

type AuthService struct {
	repo     *repository.UserRepository
	jwtKey   []byte
	issuer   string
}

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func NewAuthService(repo *repository.UserRepository) *AuthService {
	key := os.Getenv("JWT_SECRET")
	if key == "" {
		key = "change-me-in-production"
	}

	issuer := os.Getenv("REGISTRATION_HOST")
	if issuer == "" {
		host := os.Getenv("HOST")
		if host == "" {
			host = "localhost"
		}
		port := os.Getenv("PORT")
		if port == "" {
			port = "4000"
		}
		issuer = fmt.Sprintf("https://%s:%s", host, port)
	}

	return &AuthService{
		repo:   repo,
		jwtKey: []byte(key),
		issuer: issuer,
	}
}

func (s *AuthService) Register(req model.RegisterRequest) (*model.TokenResponse, error) {
	exists, err := s.repo.Exists(req.Username)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrUsernameTaken
	}

	role := model.RoleUser
	if req.Role == "admin" {
		role = model.RoleAdmin
	}

	_, err = s.repo.Create(req.Username, req.Password, role)
	if err != nil {
		return nil, err
	}

	return s.issueToken(req.Username, string(role))
}

func (s *AuthService) ListUsers() ([]model.User, error) {
	return s.repo.ListAll()
}

func (s *AuthService) GetGroups(username string) ([]model.UserGroup, error) {
	return s.repo.GetGroupsByUsername(username)
}

func (s *AuthService) AddToGroup(username, groupName string) error {
	return s.repo.AddUserToGroup(username, groupName)
}

func (s *AuthService) RemoveFromGroup(username, groupName string) error {
	return s.repo.RemoveUserFromGroup(username, groupName)
}

func (s *AuthService) CountUsers() (int, error) {
	return s.repo.CountUsers()
}

// ──────────────────────────────
//  Group management (extended)
// ──────────────────────────────

func (s *AuthService) GetAllGroups() ([]model.GroupDetail, error) {
	return s.repo.GetAllGroups()
}

func (s *AuthService) GetGroupMembers(groupName string) ([]string, error) {
	return s.repo.GetGroupMembers(groupName)
}

// ──────────────────────────────
//  Service policy
// ──────────────────────────────

func (s *AuthService) GetServicePolicy(username string) (*model.ServicePolicy, error) {
	return s.repo.GetServicePolicy(username)
}

func (s *AuthService) SetServicePolicy(policy *model.ServicePolicy) error {
	return s.repo.SetServicePolicy(policy)
}

// ──────────────────────────────
//  Storage tracking
// ──────────────────────────────

func (s *AuthService) GetStorage(username string) (*model.UserStorage, error) {
	return s.repo.GetStorage(username)
}

func (s *AuthService) UpdateStorage(username string, usedBytes, limitBytes int64) error {
	return s.repo.UpdateStorage(username, usedBytes, limitBytes)
}

func (s *AuthService) Login(req model.LoginRequest) (*model.TokenResponse, error) {
	user, err := s.repo.GetByUsername(req.Username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if !s.repo.VerifyPassword(user, req.Password) {
		return nil, ErrInvalidCredentials
	}

	return s.issueToken(user.Username, string(user.Role))
}

func (s *AuthService) ValidateToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.jwtKey, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

func (s *AuthService) issueToken(username, role string) (*model.TokenResponse, error) {
	claims := &Claims{
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   username,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(72 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(s.jwtKey)
	if err != nil {
		return nil, err
	}

	return &model.TokenResponse{
		Token:    tokenStr,
		Username: username,
		Role:     role,
	}, nil
}
