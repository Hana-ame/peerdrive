// AuthService 处理用户注册、登录、登出和 authkey 验证，使用 bcrypt 密码哈希。
package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid username or password")

type AuthService struct {
	userRepo *repository.UserRepository
}

// NewAuthService 创建一个新的认证服务实例。
func NewAuthService(userRepo *repository.UserRepository) *AuthService {
	return &AuthService{userRepo: userRepo}
}

// Register 注册新用户，使用 bcrypt 哈希密码并生成 authkey。
func (s *AuthService) Register(req model.RegisterRequest) (*model.AuthResponse, error) {
	// L1：bcrypt 只取前 72 字节，超长密码的尾部被静默忽略——不同密码可能
	// 哈希相同（截断熵损失）。超限直接拒绝，避免"看似设置了强密码实际等效短密码"。
	if len(req.Password) > 72 {
		return nil, errors.New("password too long (bcrypt limit 72 bytes)")
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &model.User{
		Username:     req.Username,
		PasswordHash: string(hashedPassword),
	}

	if err := s.userRepo.CreateUser(user); err != nil {
		return nil, err
	}

	authKey, err := s.generateAuthKey()
	if err != nil {
		return nil, err
	}

	if err := s.userRepo.UpdateAuthKey(user.ID, authKey); err != nil {
		return nil, err
	}

	return &model.AuthResponse{
		AuthKey:  authKey,
		Username: user.Username,
	}, nil
}

// Login 验证用户名密码，成功时返回新生成的 authkey。
func (s *AuthService) Login(req model.LoginRequest) (*model.AuthResponse, error) {
	user, err := s.userRepo.GetByUsername(req.Username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	authKey, err := s.generateAuthKey()
	if err != nil {
		return nil, err
	}

	if err := s.userRepo.UpdateAuthKey(user.ID, authKey); err != nil {
		return nil, err
	}

	return &model.AuthResponse{
		AuthKey:  authKey,
		Username: user.Username,
	}, nil
}

// Logout 清除指定 authkey，使用户会话失效。
func (s *AuthService) Logout(authKey string) error {
	user, err := s.userRepo.GetByAuthKey(authKey)
	if err != nil {
		return err
	}
	return s.userRepo.ClearAuthKey(user.ID)
}

// ValidateKey 验证 authkey 并返回对应的用户信息。
func (s *AuthService) ValidateKey(authKey string) (*model.User, error) {
	return s.userRepo.GetByAuthKey(authKey)
}

func (s *AuthService) generateAuthKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
