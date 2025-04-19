//处理用户注册相关的业务逻辑

package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/golang-jwt/jwt/v4" // 或者 v5
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	// "gorm.io/gorm" // 暂时不需要 GORM 错误，因为我们依赖 repo 接口
)

// AuthService 负责用户认证相关的业务逻辑。
type AuthService struct {
	userRepo  repository.UserRepository
	jwtSecret []byte // 存储密钥的字节形式
}

// NewAuthService 创建 AuthService 实例。
// jwtSecretKey 应从安全配置中获取。
func NewAuthService(userRepo repository.UserRepository, jwtSecretKey string) (*AuthService, error) {
	if jwtSecretKey == "" {
		// 强制要求设置密钥
		return nil, fmt.Errorf("JWT secret key cannot be empty")
	}
	return &AuthService{
		userRepo:  userRepo,
		jwtSecret: []byte(jwtSecretKey),
	}, nil
}

// Register 处理用户注册。
func (s *AuthService) Register(ctx context.Context, username, password, email string) (*domain.User, error) {
	logCtx := logrus.WithFields(logrus.Fields{"username": username, "email": email})

	// 1. 基本验证 (可以在 Handler 层做更详细的格式验证)
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password are required") // 或者返回更具体的业务错误
	}

	// 2. 哈希密码
	hashedPassword, err := hashPassword(password)
	if err != nil {
		logCtx.WithError(err).Error("Failed to hash password during registration")
		return nil, ErrInternalServer // 不暴露内部细节
	}

	// 3. 创建用户对象
	user := &domain.User{
		Username: username,
		Password: hashedPassword,
		Email:    email, // 假设邮箱是可选的或在 Handler 验证
	}

	// 4. 保存用户 (调用 Repository 接口)
	err = s.userRepo.Save(ctx, user)
	if err != nil {
		// 尝试判断是否是唯一约束错误
		// 注意: 依赖错误字符串不够健壮，更好的方法是让 Repository 实现返回特定错误
		// 或者在 Service 层先检查用户名/邮箱是否存在。
		// 这里我们暂时保留原有的字符串检查方式，但标记为待改进。
		// TODO: Refactor repository error handling for unique constraints
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			logCtx.WithError(err).Warn("Registration failed: Username or email already exists")
			return nil, ErrRegistrationFailed
		}
		// 其他数据库错误
		logCtx.WithError(err).Error("Database error during user creation")
		return nil, ErrInternalServer
	}

	logCtx.WithField("user_id", user.ID).Info("User registered successfully")
	// 返回创建的用户（可能包含 ID 和时间戳）
	return user, nil
}

// Login 处理用户登录。
func (s *AuthService) Login(ctx context.Context, username, password string) (string, error) {
	logCtx := logrus.WithField("username", username)

	// 1. 查找用户 (调用 Repository 接口)
	user, err := s.userRepo.FindByUsername(ctx, username)
	if err != nil {
		// Repository 实现应该在找不到用户时返回一个可识别的错误
		// 假设 repo 返回了类似 repository.ErrUserNotFound 的错误
		// if errors.Is(err, repository.ErrUserNotFound) { // 理想情况
		if err != nil { // 简化处理：任何错误都视为认证失败
			logCtx.WithError(err).Warn("Login attempt failed: User not found or repo error")
			return "", ErrAuthenticationFailed // 统一返回认证失败
		}
	}
    // 如果用户为空但错误也为空（在良好的存储库实现中不应发生这种情况，但进行防御性检查）。
    if user == nil {
        logCtx.Warn("Login attempt failed: User not found (repo returned nil user without error)")
        return "", ErrAuthenticationFailed
    }


	// 2. 验证密码
	if !checkPassword(password, user.Password) {
		logCtx.Warn("Login attempt failed: Invalid password")
		return "", ErrAuthenticationFailed // 统一返回认证失败
	}

	// 3. 生成 JWT Token
	token, err := s.generateJWT(user.ID)
	if err != nil {
		logCtx.WithError(err).Error("Failed to generate JWT token during login")
		return "", ErrInternalServer
	}

	logCtx.WithField("user_id", user.ID).Info("User logged in successfully")
	return token, nil
}

// --- 私有辅助函数 ---

// hashPassword 使用 bcrypt 对密码进行哈希处理 (与原 handlers/auth.go 中相同)
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to generate hash from password: %w", err)
	}
	return string(bytes), nil
}

// checkPassword 验证提供的密码是否与存储的哈希匹配 (与原 handlers/auth.go 中相同)
func checkPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// generateJWT 为指定用户 ID 生成 JWT Token (与原 handlers/auth.go 中类似，但使用注入的密钥)
func (s *AuthService) generateJWT(userID uint) (string, error) {
	// 创建一个新的 Token 对象，指定签名方法和 Claims
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(24 * time.Hour).Unix(), // Token 过期时间 (24 小时)
		"iat":     time.Now().Unix(),                     // Token 签发时间
	})

	// 使用 Service 中存储的密钥进行签名
	tokenString, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, nil
}