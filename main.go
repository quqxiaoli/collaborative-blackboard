package main

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/handlers/ws"
	"collaborative-blackboard/routes"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv" // 导入 godotenv
	"github.com/sirupsen/logrus"
)

// 全局日志实例，可以在这里配置日志格式、级别等
var log = logrus.New()

func main() {
	// 1. 加载环境变量 (应在所有配置初始化之前)
	// godotenv.Load() 会加载项目根目录下的 .env 文件
	// 如果文件不存在或加载失败，它会返回错误，但我们通常允许这种情况 (例如在生产环境中直接使用环境变量)
	if err := godotenv.Load(); err != nil {
		log.Warn("Error loading .env file, relying on system environment variables")
	}

	// 2. 初始化数据库连接
	config.InitDB()
	// 3. 执行数据库迁移
	config.MigrateDB()
	// 4. 初始化 Redis 连接
	config.InitRedis()

	// 5. 创建 Gin 引擎
	r := gin.Default() // Default() 包含了 Logger 和 Recovery 中间件

	// 6. 应用中间件
	r.Use(corsMiddleware()) // 添加 CORS 中间件

	// 7. 设置路由
	routes.SetupRouter(r)

	// 8. 启动后台任务
	// 启动定时生成快照的任务 (在单独的 goroutine 中运行)
	go ws.RunSnapshotTask()
	// 启动处理 WebSocket 动作的后台任务 (在 ws 包的 init 函数中启动)
	// go ws.processActions() // 这个在 ws.init() 中启动了
	// go ws.flushActionBatch() // 这个在 ws.init() 中启动了
	// go ws.cleanupClients() // 这个在 ws.init() 中启动了

	// 9. 启动 HTTP 服务器
	serverAddr := ":8080" // 可以考虑从环境变量配置端口
	log.Infof("Server starting on %s", serverAddr)
	if err := r.Run(serverAddr); err != nil {
		// 如果服务器启动失败，记录致命错误并退出
		log.Fatalf("Failed to start server on %s: %v", serverAddr, err)
	}
}

// corsMiddleware 创建一个处理跨域请求的 Gin 中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// !! 注意：生产环境应配置具体的允许来源，而不是 "*" 或开发服务器地址 !!
		// 可以从环境变量读取允许的源
		// allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")
		// if allowedOrigin == "" {
		// 	allowedOrigin = "http://localhost:3000" // 开发默认值
		// }
		allowedOrigin := "http://localhost:3000" // 暂时保留开发设置

		c.Writer.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true") // 如果前端需要发送凭证 (如 Cookie)
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS") // 允许的方法
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With") // 允许的请求头

		// 处理 OPTIONS 预检请求
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent) // 返回 204 No Content
			return
		}

		c.Next() // 继续处理请求链
	}
}
