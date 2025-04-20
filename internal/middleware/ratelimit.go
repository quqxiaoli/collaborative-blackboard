package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8" // 导入 Redis 客户端
	"github.com/sirupsen/logrus"
)

// RateLimit 返回一个 Gin 中间件，用于基于客户端 IP 地址进行速率限制。
// redisClient: 用于存储计数器的 Redis 客户端实例，必须提供。
// maxRequests: 在指定时间窗口内允许的最大请求数。
// window: 速率限制的时间窗口。
func RateLimit(redisClient *redis.Client, maxRequests int, window time.Duration) gin.HandlerFunc {
	// 启动时检查依赖
	if redisClient == nil {
		panic("Redis client cannot be nil for RateLimit middleware")
	}
	if maxRequests <= 0 {
		panic("maxRequests must be positive for RateLimit middleware")
	}
	if window <= 0 {
		panic("window duration must be positive for RateLimit middleware")
	}

	return func(c *gin.Context) {
		// 使用客户端 IP 作为限流键的一部分
		// 注意：如果服务在反向代理后面，需要确保获取到真实的客户端 IP
		// 例如，检查 X-Forwarded-For 或 X-Real-IP 头
		key := "ratelimit:" + c.ClientIP()

		// 使用 Redis Pipeline 执行 INCR 和 EXPIRE 以提高原子性（减少竞争条件风险）
		// 虽然 INCR 本身是原子的，但检查计数和设置过期之间有时间差
		// 更严格的原子性需要 Lua 脚本，但 Pipeline 是一个不错的折中
		var count int64
		var err error

		pipe := redisClient.Pipeline()
		incrCmd := pipe.Incr(c.Request.Context(), key) // 执行 INCR
		pipe.Expire(c.Request.Context(), key, window) // 设置/刷新过期时间
		_, err = pipe.Exec(c.Request.Context())       // 执行 Pipeline

		if err != nil {
			// 处理 Redis 错误
			logrus.WithError(err).Error("RateLimit: Redis Pipeline failed")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Rate limiting error"})
			c.Abort()
			return
		}

		// 获取 INCR 命令的结果
		count, err = incrCmd.Result()
		if err != nil {
			// 处理获取 INCR 结果的错误 (理论上不应发生，因为 Exec 成功了)
			logrus.WithError(err).Error("RateLimit: Failed to get INCR result after successful Exec")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Rate limiting error"})
			c.Abort()
			return
		}


		// 检查请求次数是否超过限制
		// 注意：这里比较 count 和 maxRequests，如果 count > maxRequests 则超限
		if count > int64(maxRequests) {
			// 可以在响应头中包含速率限制信息 (可选)
			// c.Header("X-RateLimit-Limit", strconv.Itoa(maxRequests))
			// c.Header("X-RateLimit-Remaining", "0")
			// c.Header("X-RateLimit-Reset", ...) // 计算重置时间戳

			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests"})
			c.Abort()
			return
		}

		// // 可选：设置速率限制信息响应头 (即使未超限)
		// c.Header("X-RateLimit-Limit", strconv.Itoa(maxRequests))
		// c.Header("X-RateLimit-Remaining", strconv.FormatInt(int64(maxRequests)-count, 10))
		// c.Header("X-RateLimit-Reset", ...)

		c.Next() // 未超限，继续处理请求
	}
}