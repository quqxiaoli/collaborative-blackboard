package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/golang-jwt/jwt/v4" // 或者 v5
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// AuthService 负责用户认证相关的业务逻辑。
type AuthService struct {
	userRepo  repository.UserRepository
	jwtSecret []byte        // 存储密钥的字节形式
	jwtExpiry time.Duration // JWT 过期时间
}

// NewAuthService 创建 AuthService 实例。
// jwtSecretKey 应从安全配置中获取。
// jwtExpiryHours 定义 token 过期的小时数。
func NewAuthService(userRepo repository.UserRepository, jwtSecretKey string, jwtExpiryHours int) (*AuthService, error) {
	if userRepo == nil {
		panic("UserRepository cannot be nil for AuthService")
	}
	if jwtSecretKey == "" {
		return nil, fmt.Errorf("JWT secret key cannot be empty")
	}
	if jwtExpiryHours <= 0 {
		jwtExpiryHours = 24 // 默认 24 小时
	}
	return &AuthService{
		userRepo:  userRepo,
		jwtSecret: []byte(jwtSecretKey),
		jwtExpiry: time.Duration(jwtExpiryHours) * time.Hour,
	}, nil
}

// Register 处理用户注册。
func (s *AuthService) Register(ctx context.Context, username, password, email string) (*domain.User, error) {
	logCtx := logrus.WithFields(logrus.Fields{"username": username, "email": email})

	// 1. 基本验证
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password are required") // 或者使用 ErrInvalidInput
	}
	// TODO: 添加更严格的用户名、密码、邮箱格式和长度验证

	// 2. 哈希密码
	hashedPassword, err := hashPassword(password)
	if err != nil {
		logCtx.WithError(err).Error("Failed to hash password during registration")
		return nil, ErrInternalServer
	}

	// 3. 创建用户对象
	user := &domain.User{
		Username: username,
		Password: hashedPassword,
		Email:    email,
	}

	// 4. 保存用户 (调用 Repository 接口)
	err = s.userRepo.Save(ctx, user)
	if err != nil {
		// 优先检查是否是仓库层返回的特定错误
		if errors.Is(err, repository.ErrDuplicateEntry) {
			logCtx.WithError(err).Warn("Registration failed: Username or email already exists (repo error)")
			return nil, ErrRegistrationFailed // 返回业务错误
		} else if isDuplicateEntryErrorString(err) { // 临时的字符串检查作为后备
			logCtx.WithError(err).Warn("Registration failed: Username or email already exists (string check)")
			return nil, ErrRegistrationFailed
		}
		// 其他数据库错误
		logCtx.WithError(err).Error("Database error during user creation")
		return nil, ErrInternalServer
	}

	logCtx.WithField("user_id", user.ID).Info("User registered successfully")
	user.Password = "" // 清除密码哈希再返回
	return user, nil
}

// Login 处理用户登录。
func (s *AuthService) Login(ctx context.Context, username, password string) (string, error) {
	logCtx := logrus.WithField("username", username)

	// 1. 查找用户
	user, err := s.userRepo.FindByUsername(ctx, username)
	if err != nil {
		// 检查是否是用户未找到的特定错误
		if errors.Is(err, repository.ErrUserNotFound) {
			logCtx.WithError(err).Warn("Login attempt failed: User not found")
		} else {
			// 其他仓库层错误
			logCtx.WithError(err).Warn("Login attempt failed: Error finding user")
		}
		return "", ErrAuthenticationFailed // 对客户端统一返回认证失败
	}
	// 防御性检查，以防仓库实现返回 nil, nil
	if user == nil {
		logCtx.Warn("Login attempt failed: User not found (repo returned nil user without error)")
		return "", ErrAuthenticationFailed
	}

	// 2. 验证密码
	if !checkPassword(password, user.Password) {
		logCtx.Warn("Login attempt failed: Invalid password")
		return "", ErrAuthenticationFailed
	}

	// 3. 生成 JWT Token
	token, err := s.generateJWT(user.ID) // 内部使用 s.jwtExpiry
	if err != nil {
		logCtx.WithError(err).Error("Failed to generate JWT token during login")
		return "", ErrInternalServer
	}

	logCtx.WithField("user_id", user.ID).Info("User logged in successfully")
	return token, nil
}

// --- 私有辅助函数 ---

// hashPassword 使用 bcrypt 对密码进行哈希处理
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to generate hash from password: %w", err)
	}
	return string(bytes), nil
}

// checkPassword 验证提供的密码是否与存储的哈希匹配
func checkPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// generateJWT 为指定用户 ID 生成 JWT Token
func (s *AuthService) generateJWT(userID uint) (string, error) {
	// s.jwtSecret 和 s.jwtExpiry 在 NewAuthService 时已初始化和检查
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(s.jwtExpiry).Unix(), // 使用结构体字段
		"iat":     time.Now().Unix(),
	})
	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		// 包装签名错误
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, nil
}

// isDuplicateEntryErrorString (临时的字符串检查)
// TODO: 当 GormUserRepository 返回 repository.ErrDuplicateEntry 后移除此函数
func isDuplicateEntryErrorString(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "duplicate key value violates unique constraint")
}