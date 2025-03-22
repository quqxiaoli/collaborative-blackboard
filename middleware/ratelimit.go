package middleware

import (
    "collaborative-blackboard/config"
    "github.com/gin-gonic/gin"
    "net/http"
    "time"
)

// RateLimit 限制每秒请求次数
func RateLimit(maxRequests int) gin.HandlerFunc {
    return func(c *gin.Context) {
        key := "ratelimit:" + c.ClientIP()
        count, _ := config.Redis.Get(c.Request.Context(), key).Int()
        if count >= maxRequests {
            c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
            c.Abort()
            return
        }
        config.Redis.Incr(c.Request.Context(), key)
        config.Redis.Expire(c.Request.Context(), key, time.Second)
        c.Next()
    }
}