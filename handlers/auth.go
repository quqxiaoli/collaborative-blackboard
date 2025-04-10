package handlers

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"errors" // 导入 errors 包
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4" // 使用 v4 或 v5，保持一致
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm" // 导入 gorm 包以使用 gorm.ErrRecordNotFound
)

// Register 处理用户注册请求
func Register(c *gin.Context) {
	var user models.User
	// 1. 绑定并验证输入 JSON
	if err := c.ShouldBindJSON(&user); err != nil {
		logrus.WithError(err).Warn("Register: Invalid input format")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input format"})
		return
	}

	// 在这里可以添加更严格的验证逻辑，例如用户名、密码、邮箱格式和长度

	// 2. 哈希用户密码
	hashedPassword, err := hashPassword(user.Password)
	if err != nil {
		logrus.WithError(err).Error("Register: Failed to hash password")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process registration"}) // 避免暴露服务器内部错误细节
		return
	}
	user.Password = hashedPassword // 使用哈希后的密码

	// 3. 创建用户记录到数据库
	if err := config.DB.Create(&user).Error; err != nil {
		// 检查是否是唯一约束冲突错误 (用户名或邮箱已存在)
		// 注意：这种检查方式依赖于具体的数据库错误信息，可能不够健壮
		// 更好的方式是尝试分别查询用户名和邮箱是否存在
		// 例如，对于 MySQL，错误信息可能包含 "Error 1062: Duplicate entry"
		// 对于 PostgreSQL，可能包含 "duplicate key value violates unique constraint"
		// 对于 SQLite，可能包含 "UNIQUE constraint failed"
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			logrus.WithError(err).Warn("Register: Username or email already exists")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username or email already exists"})
		} else {
			logrus.WithError(err).Error("Register: Database error during user creation")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register user"}) // 通用错误信息
		}
		return
	}

	// 4. 注册成功响应
	logrus.WithField("user_id", user.ID).Info("User registered successfully")
	c.JSON(http.StatusOK, gin.H{"message": "User registered successfully", "user_id": user.ID}) // 返回 user_id 而不是 id
}

// Login 处理用户登录请求
func Login(c *gin.Context) {
	var input struct {
		Username string `json:"username" binding:"required"` // 添加 binding:"required" 确保字段存在
		Password string `json:"password" binding:"required"`
	}
	// 1. 绑定并验证输入 JSON
	if err := c.ShouldBindJSON(&input); err != nil {
		logrus.WithError(err).Warn("Login: Invalid input format or missing fields")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input format or missing fields"})
		return
	}

	// 2. 根据用户名查找用户
	user, err := findUserByUsername(input.Username)
	if err != nil {
		// 如果用户不存在或查询出错
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logrus.WithField("username", input.Username).Warn("Login: User not found")
		} else {
			// 对于其他数据库错误，记录详细信息
			logrus.WithError(err).Error("Login: Database error finding user")
		}
		// 统一返回认证失败信息，避免泄露用户是否存在
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// 3. 验证密码
	if !checkPassword(input.Password, user.Password) {
		logrus.WithField("username", input.Username).Warn("Login: Invalid password attempt")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"}) // 统一返回认证失败信息
		return
	}

	// 4. 生成 JWT Token
	token, err := generateJWT(user.ID)
	if err != nil {
		logrus.WithError(err).Error("Login: Failed to generate JWT token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Login failed due to server error"}) // 通用错误信息
		return
	}

	// 5. 登录成功响应
	logrus.WithField("user_id", user.ID).Info("User logged in successfully")
	c.JSON(http.StatusOK, gin.H{"message": "Login successful", "token": token})
}

// hashPassword 使用 bcrypt 对密码进行哈希处理
func hashPassword(password string) (string, error) {
	// 使用 bcrypt 的默认成本因子，通常足够安全
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		// 包装错误，提供更多上下文
		return "", fmt.Errorf("failed to generate hash from password: %w", err)
	}
	return string(bytes), nil
}

// checkPassword 验证提供的密码是否与存储的哈希匹配
func checkPassword(password, hash string) bool {
	// CompareHashAndPassword 会比较哈希值和明文密码
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	// 如果 err 为 nil，表示密码匹配
	return err == nil
}

// generateJWT 为指定用户 ID 生成 JWT Token
func generateJWT(userID uint) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// !!! 安全警告：JWT_SECRET 必须设置 !!!
		logrus.Error("CRITICAL: JWT_SECRET environment variable not set during token generation!")
		// 返回明确的错误，阻止生成不安全的 Token
		return "", fmt.Errorf("server configuration error: JWT secret not set")
	}

	// 创建一个新的 Token 对象，指定签名方法和 Claims
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,                                  // 用户 ID
		"exp":     time.Now().Add(24 * time.Hour).Unix(), // Token 过期时间 (24 小时后)
		"iat":     time.Now().Unix(),                     // Token 签发时间 (Issued At)
		// "nbf": time.Now().Unix(), // Not Before 时间 (可选)
		// "iss": "your-issuer", // 签发者 (可选)
		// "aud": "your-audience", // 接收者 (可选)
	})

	// 使用密钥对 Token 进行签名，生成最终的 Token 字符串
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		// 包装签名错误
		return "", fmt.Errorf("failed to sign token: %w", err)
	}
	return tokenString, nil
}

// findUserByUsername 通过用户名从数据库查找用户
func findUserByUsername(username string) (models.User, error) {
	var user models.User
	// 使用 GORM 的 First 方法查找匹配的第一个用户
	// 如果没有找到记录，会返回 gorm.ErrRecordNotFound 错误
	err := config.DB.Where("username = ?", username).First(&user).Error
	if err != nil {
		// 将 gorm 错误包装一下，方便上层判断
		// 不需要检查 ErrRecordNotFound，让调用者处理
		return user, fmt.Errorf("database query failed for username '%s': %w", username, err)
	}
	return user, nil
}
