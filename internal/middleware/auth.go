package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4" // 或者 v5，与 AuthService 保持一致
	"github.com/sirupsen/logrus"
	// "os" // 不再需要 os.Getenv
)

// Auth 返回一个 Gin 中间件，用于验证 JWT token。
// jwtSecret: 用于验证签名的密钥，必须提供。
func Auth(jwtSecret string) gin.HandlerFunc {
	// 在创建中间件时就进行检查，避免运行时 panic
	if jwtSecret == "" {
		panic("JWT secret cannot be empty for Auth middleware")
	}

	return func(c *gin.Context) {
		// 1. 从请求头提取 Token
		tokenStr, err := extractToken(c)
		if err != nil {
			// 根据错误类型返回不同的响应
			if errors.Is(err, ErrMissingAuthHeader) {
				logrus.Warn("Auth middleware: Missing Authorization header")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			} else if errors.Is(err, jwt.ErrTokenMalformed) { // 使用标准库错误
				logrus.Warnf("Auth middleware: Malformed token format: %v", err)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token format"})
			} else {
				// 对于 extractToken 返回的其他潜在错误
				logrus.WithError(err).Warn("Auth middleware: Error extracting token")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Could not process token"})
			}
			c.Abort() // 终止请求处理链
			return
		}

		// 2. 验证 Token (传入 secret)
		claims, err := validateToken(tokenStr, jwtSecret)
		if err != nil {
			// 先创建一个带有错误上下文的日志条目
			logCtx := logrus.WithError(err)

			// 使用这个 logCtx 记录主要错误信息
			logCtx.Warn("Auth middleware: Invalid token")

			// 然后基于同样的 logCtx 记录更详细的原因 (如果需要)
			// 根据 JWT 错误类型提供更具体的日志，但对客户端返回通用错误
			var validationError *jwt.ValidationError
			if errors.As(err, &validationError) {
				if validationError.Errors&jwt.ValidationErrorExpired != 0 {
					logCtx.Warn("Reason: Token is expired")
				}
				if validationError.Errors&jwt.ValidationErrorSignatureInvalid != 0 {
					logCtx.Warn("Reason: Token signature is invalid")
				}
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}

		// 3. 从 Claims 中提取用户信息并设置到 Context
		userIDClaim, ok := claims["user_id"]
		if !ok {
			logrus.Error("Auth middleware: 'user_id' claim missing in token")
			// 这是服务端或 Token 配置问题
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token processing error: missing user_id"})
			c.Abort()
			return
		}

		// JWT 数字默认为 float64，需要安全转换为 uint
		userIDFloat, ok := userIDClaim.(float64)
		if !ok || userIDFloat <= 0 || userIDFloat != float64(uint(userIDFloat)) {
			logrus.Errorf("Auth middleware: 'user_id' claim is not a valid positive integer number: %v", userIDClaim)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token processing error: invalid user_id type or value"})
			c.Abort()
			return
		}
		userID := uint(userIDFloat)

		// 将 user_id 存储在 Gin 上下文中，供后续处理程序使用
		c.Set("user_id", userID)
		logrus.WithField("user_id", userID).Debug("Auth middleware: User authenticated via JWT") // 使用 Debug 级别

		c.Next() // 继续处理请求链
	}
}

// ErrMissingAuthHeader 定义一个自定义错误，用于表示缺少 Authorization 头
var ErrMissingAuthHeader = errors.New("missing Authorization header")

// extractToken 从 Gin 上下文中提取 Bearer Token
func extractToken(c *gin.Context) (string, error) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return "", ErrMissingAuthHeader
	}
	// Authorization header 格式应为 "Bearer <token>"
	parts := strings.Split(authHeader, " ")
	// 使用 EqualFold 忽略 "Bearer" 的大小写
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		// 返回标准库定义的格式错误
		return "", jwt.ErrTokenMalformed
	}
	return parts[1], nil // 返回提取到的 token 字符串
}

// validateToken 解析并验证 JWT token 字符串
// secret: 用于验证签名的密钥
func validateToken(tokenStr string, secret string) (jwt.MapClaims, error) {
	// secret 已在 Auth 函数中检查非空

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// 验证签名方法是否为 HMAC (HS256)
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		// 返回用于验证签名的密钥字节
		return []byte(secret), nil
	})

	if err != nil {
		// 解析或验证过程中发生错误 (例如，格式错误、签名无效、过期等)
		// jwt-go 库会将过期等错误包装在 ValidationError 中
		// 返回原始错误，让调用者检查具体类型
		return nil, fmt.Errorf("token validation failed: %w", err) // 包装错误
	}

	// 检查 Token 是否有效，并且 Claims 是否为 MapClaims 类型
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		// Token 有效，返回 Claims
		return claims, nil
	}

	// 如果 Token 无效 (例如，验证通过但 token.Valid 为 false) 或 Claims 类型不匹配
	return nil, errors.New("invalid token or claims type")
}