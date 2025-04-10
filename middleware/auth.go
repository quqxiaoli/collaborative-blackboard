package middleware

import (
	"errors" // 导入 errors 包
	"fmt"    // 导入 fmt 包
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4" // 使用 v4 或 v5，保持一致
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
)

// Auth 是一个 Gin 中间件，用于验证 JWT token
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 从请求头提取 Token
		tokenStr, err := extractToken(c)
		if err != nil {
			// 根据错误类型返回不同的响应
			if errors.Is(err, ErrMissingAuthHeader) {
				logrus.Warn("Auth middleware: Missing Authorization header")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			} else if errors.Is(err, jwt.ErrTokenMalformed) {
				logrus.Warnf("Auth middleware: Malformed token format: %v", err)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token format"})
			} else {
				// 对于 extractToken 返回的其他潜在错误（虽然目前没有）
				logrus.WithError(err).Warn("Auth middleware: Error extracting token")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Could not process token"})
			}
			c.Abort() // 终止请求处理链
			return
		}

		// 2. 验证 Token
		claims, err := validateToken(tokenStr)
		if err != nil {
			logrus.WithError(err).Warn("Auth middleware: Invalid token")
			// 根据具体的 JWT 错误类型可以提供更友好的信息，但为了安全，通常只返回通用错误
			// 例如：检查 err 是否是 jwt.ValidationError 类型，并查看其内部错误标志 (如 .Errors & jwt.ValidationErrorExpired)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
            c.Abort()
			return
		}

		// 3. 从 Claims 中提取用户信息并设置到 Context
		userIDClaim, ok := claims["user_id"]
		if !ok {
			logrus.Error("Auth middleware: user_id claim missing in token")
			// 这是一个服务端或 Token 配置问题，返回 500
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token processing error: missing user_id"})
			c.Abort()
			return
		}

		// JWT 数字默认为 float64，需要转换为 uint
		userIDFloat, ok := userIDClaim.(float64)
		if !ok {
			logrus.Error("Auth middleware: user_id claim is not a number")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token processing error: invalid user_id type"})
			c.Abort()
			return
		}
		// 检查转换是否会导致数据丢失 (虽然对于典型的用户 ID 不太可能)
		if userIDFloat <= 0 || userIDFloat != float64(uint(userIDFloat)) {
			logrus.Error("Auth middleware: user_id claim value is invalid")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token processing error: invalid user_id value"})
			c.Abort()
			return
		}
		userID := uint(userIDFloat)

		logrus.WithField("user_id", userID).Info("User authenticated via JWT")
		c.Set("user_id", userID) // 将 user_id 存储在 Gin 上下文中，供后续处理程序使用
		c.Next()                 // 继续处理请求链
	}
}

// 定义一个自定义错误，用于表示缺少 Authorization 头
var ErrMissingAuthHeader = errors.New("missing Authorization header")

// extractToken 从 Gin 上下文中提取 Bearer Token
func extractToken(c *gin.Context) (string, error) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		// 返回自定义错误，而不是 nil
		return "", ErrMissingAuthHeader
	}
	// Authorization header 格式应为 "Bearer <token>"
	parts := strings.Split(authHeader, " ")
	// 使用 EqualFold 忽略 "Bearer" 的大小写
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		// 返回标准库定义的错误
		return "", jwt.ErrTokenMalformed
	}
	return parts[1], nil // 返回提取到的 token 字符串
}

// validateToken 解析并验证 JWT token 字符串
func validateToken(tokenStr string) (jwt.MapClaims, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// !!! 安全警告：JWT_SECRET 环境变量必须设置 !!!
		logrus.Error("CRITICAL: JWT_SECRET environment variable not set!")
		// 返回错误，阻止不安全的验证
		return nil, fmt.Errorf("server configuration error: JWT secret not set")
	}

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// 验证签名方法是否为 HMAC
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			// 签名方法不匹配
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		// 返回用于验证签名的密钥
		return []byte(secret), nil
	})

	if err != nil {
		// 解析或验证过程中发生错误 (例如，格式错误、签名无效、过期等)
		// jwt-go 库会将过期等错误包装在 ValidationError 中
		// 可以通过 errors.As() 检查具体错误类型
		var validationError *jwt.ValidationError
		if errors.As(err, &validationError) {
			if validationError.Errors&jwt.ValidationErrorMalformed != 0 {
				return nil, fmt.Errorf("token is malformed: %w", err)
			} else if validationError.Errors&jwt.ValidationErrorExpired != 0 {
				return nil, fmt.Errorf("token is expired: %w", err)
			} else if validationError.Errors&jwt.ValidationErrorNotValidYet != 0 {
				return nil, fmt.Errorf("token not valid yet: %w", err)
			} else if validationError.Errors&jwt.ValidationErrorSignatureInvalid != 0 {
				return nil, fmt.Errorf("token signature is invalid: %w", err)
			}
		}
		// 其他解析错误
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	// 检查 Token 是否有效，并且 Claims 是否为 MapClaims 类型
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		// Token 有效，返回 Claims
		return claims, nil
	}

	// 如果 Token 无效 (例如，验证通过但 token.Valid 为 false) 或 Claims 类型不匹配
	return nil, fmt.Errorf("invalid token or claims type")
}
