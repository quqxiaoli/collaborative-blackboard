package middleware

import (
    "github.com/gin-gonic/gin"
    "github.com/golang-jwt/jwt/v4"
    "github.com/sirupsen/logrus"
    "net/http"
    "os"
    "strings"
)

func Auth() gin.HandlerFunc {
    return func(c *gin.Context) {
        tokenStr, err := extractToken(c)
        if err != nil {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "Token not found"})
            c.Abort()
            return
        }

        claims, err := validateToken(tokenStr)
        if err != nil {
            c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
            c.Abort()
            return
        }

        userID := uint(claims["user_id"].(float64))
        logrus.WithField("user_id", userID).Info("User authenticated")
        c.Set("user_id", userID)
        c.Next()
    }
}

func extractToken(c *gin.Context) (string, error) {
    authHeader := c.GetHeader("Authorization")
    if authHeader == "" {
        return "", nil // 改为 nil 或自定义错误
    }
    parts := strings.Split(authHeader, " ")
    if len(parts) != 2 || parts[0] != "Bearer" {
        return "", jwt.ErrTokenMalformed
    }
    return parts[1], nil
}
func validateToken(tokenStr string) (jwt.MapClaims, error) {
    secret := os.Getenv("JWT_SECRET")
    if secret == "" {
        secret = "" // 安全的默认值
    }

    token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
        if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
            return nil, jwt.ErrSignatureInvalid
        }
        return []byte(secret), nil
    })
    if err != nil {
        return nil, err
    }
    if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
        return claims, nil
    }
    return nil, nil
}