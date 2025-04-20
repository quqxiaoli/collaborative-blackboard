package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"

	// --- 内部包导入 ---
	httpHandler "collaborative-blackboard/internal/handler/http"
	wsHandler "collaborative-blackboard/internal/handler/websocket"
	"collaborative-blackboard/internal/hub"
	gormpersistence "collaborative-blackboard/internal/infra/persistence/gorm"
	"collaborative-blackboard/internal/infra/setup"
	redisstate "collaborative-blackboard/internal/infra/state/redis"
	"collaborative-blackboard/internal/middleware"
	"collaborative-blackboard/internal/service"
)

var log = logrus.New()

func init() { // 初始化日志设置 (可选)
	log.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(logrus.DebugLevel) // 开发时使用 Debug，生产环境用 Info 或 Warn }
}

func main() {
	log.Info("Starting collaborative blackboard server...")

	// 1. 加载环境变量 ... (不变)
	if err := godotenv.Load(); err != nil {
		log.Warn("Error loading .env file, relying on system environment variables")
	}

	// 2. 加载配置 ... (不变)
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_NAME")
	redisAddr := os.Getenv("REDIS_ADDR")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	jwtSecret := os.Getenv("JWT_SECRET")
	serverPort := os.Getenv("SERVER_PORT")
	if serverPort == "" {
		serverPort = "8080"
	}
	if jwtSecret == "" {
		log.Fatal("CRITICAL: JWT_SECRET environment variable is not set!")
	}

	// --- 3. 初始化基础设施连接 (修正错误处理) ---
	// 初始化数据库连接
	db, err := setup.InitDB(dbUser, dbPassword, dbHost, dbPort, dbName)
	if err != nil {
		// InitDB 内部已 Fatal，这里理论上不会执行，但保持检查
		log.Fatalf("Failed to initialize database: %v", err)
	}
	log.Info("Database connection initialized")

	// 执行数据库迁移
	// 将 db 连接传递给迁移函数，并检查错误
	err = setup.MigrateDB(db)
	if err != nil {
		log.Fatalf("Failed to run database migrations: %v", err)
	}
	log.Info("Database migrations completed")

	// 初始化 Redis 连接
	// 接收返回值 rdb 和 err
	rdb, err := setup.InitRedis(redisAddr, redisPassword, 0) // DB 0
	if err != nil {
		// InitRedis 内部已 Fatal
		log.Fatalf("Failed to initialize Redis: %v", err)
	}
	log.Info("Redis connection initialized")
	// 优雅关闭 Redis 连接
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Errorf("Error closing Redis connection: %v", err)
		} else {
			log.Info("Redis connection closed")
		}
	}()

	// --- 4. 依赖注入 (不变) ---
	log.Info("Initializing application components...")
	userRepo := gormpersistence.NewGormUserRepository(db)
	roomRepo := gormpersistence.NewGormRoomRepository(db)
	actionRepo := gormpersistence.NewGormActionRepository(db)
	snapshotRepo := gormpersistence.NewGormSnapshotRepository(db)
	stateRepo := redisstate.NewRedisStateRepository(rdb, "bb:")

	jwtExpiryHours := 24
	authService, err := service.NewAuthService(userRepo, jwtSecret, jwtExpiryHours)
	if err != nil {
		log.Fatalf("Failed to create AuthService: %v", err)
	}
	roomService := service.NewRoomService(roomRepo)
	collabService := service.NewCollaborationService(actionRepo, stateRepo, snapshotRepo, roomRepo)
	snapshotService := service.NewSnapshotService(snapshotRepo, stateRepo, actionRepo)

	hubInstance := hub.NewHub(collabService, snapshotService)

	authHandler := httpHandler.NewAuthHandler(authService)
	roomHandler := httpHandler.NewRoomHandler(roomService)
	wsHandler := wsHandler.NewWebSocketHandler(hubInstance, roomService)

	// --- 5. 启动核心后台 Goroutine (不变) ---
	log.Info("Starting background routines...")
	go hubInstance.Run()
	log.Info("Hub routine started")
	// TODO: 启动其他后台任务

	// --- 6. 创建并配置 Gin 引擎 (不变) ---
	log.Info("Setting up Gin router...")
	r := gin.Default()

	// --- 7. 应用中间件 (修正 RateLimit 和 Auth 调用) ---
	r.Use(func(c *gin.Context) { /* ... (CORS 不变) ... */ })

	// 应用速率限制中间件 (传入 rdb)
	rateLimitWindow := 1 * time.Second
	r.Use(middleware.RateLimit(rdb, 100, rateLimitWindow)) // 传入 rdb

	// --- 8. 设置路由 (修正 Auth 中间件调用) ---
	api := r.Group("/api")
	authRoutes := api.Group("/auth")
	{
		authRoutes.POST("/register", authHandler.Register)
		authRoutes.POST("/login", authHandler.Login)
	}

	// 传入 jwtSecret 到 Auth 中间件
	roomRoutes := api.Group("/rooms").Use(middleware.Auth(jwtSecret))
	{
		roomRoutes.POST("", roomHandler.CreateRoom)
		roomRoutes.POST("/join", roomHandler.JoinRoom)
	}

	// 传入 jwtSecret 到 Auth 中间件
	wsRoutes := r.Group("/ws").Use(middleware.Auth(jwtSecret))
	{
		wsRoutes.GET("/room/:roomId", wsHandler.HandleConnection)
	}

	r.GET("/ping", func(c *gin.Context) { /* ... (不变) ... */ })

	// --- 9. 启动 HTTP 服务器 (不变) ---
	serverAddr := ":" + serverPort
	log.Infof("Server starting on %s", serverAddr)
	srv := &http.Server{Addr: serverAddr, Handler: r}
	go func() { /* ... (启动服务器不变) ... */ }()

	// --- 10. 实现优雅关闭 (不变) ---
	log.Info("Setting up graceful shutdown...")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutdown signal received, shutting down server gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Info("Server exiting")
}
