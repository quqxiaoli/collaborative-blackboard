package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"

	// --- 导入内部包 ---
	httpHandler "collaborative-blackboard/internal/handler/http"
	wsHandler "collaborative-blackboard/internal/handler/websocket"
	"collaborative-blackboard/internal/hub"
	gormpersistence "collaborative-blackboard/internal/infra/persistence/gorm"
	"collaborative-blackboard/internal/infra/setup"
	redisstate "collaborative-blackboard/internal/infra/state/redis"
	"collaborative-blackboard/internal/middleware"

	//"collaborative-blackboard/internal/repository" // 需要导入 repository
	"collaborative-blackboard/internal/service"
	"collaborative-blackboard/internal/worker"

	"github.com/go-redis/redis/v8" // 需要导入 redis
	"gorm.io/gorm"                 // 需要导入 gorm
)

// Config 结构体用于存储从环境变量或文件加载的配置
type Config struct {
	DBUser         string
	DBPassword     string
	DBHost         string
	DBPort         string
	DBName         string
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
	JWTSecret      string
	ServerPort     string
	LogLevel       string // 例如 "debug", "info", "warn"
	// 可以添加更多配置项...
	RateLimitMax   int
	RateLimitWindow time.Duration
    JWTExpiryHours int
}

// LoadConfig 从环境变量加载配置
func LoadConfig() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		logrus.Warn("Error loading .env file, relying on system environment variables")
	}

	cfg := &Config{
		DBUser:         os.Getenv("DB_USER"),
		DBPassword:     os.Getenv("DB_PASSWORD"),
		DBHost:         os.Getenv("DB_HOST"),
		DBPort:         os.Getenv("DB_PORT"),
		DBName:         os.Getenv("DB_NAME"),
		RedisAddr:      os.Getenv("REDIS_ADDR"),
		RedisPassword:  os.Getenv("REDIS_PASSWORD"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		ServerPort:     os.Getenv("SERVER_PORT"),
		LogLevel:       os.Getenv("LOG_LEVEL"),
        RateLimitMax:   100, // 默认值
        RateLimitWindow: 1 * time.Second, // 默认值
        JWTExpiryHours: 24, // 默认值
	}

    // 处理 Redis DB
    redisDBStr := os.Getenv("REDIS_DB")
    cfg.RedisDB, _ = strconv.Atoi(redisDBStr) // 忽略错误，默认为 0

    // 设置默认值和进行必要检查
	if cfg.ServerPort == "" { cfg.ServerPort = "8080" }
	if cfg.LogLevel == "" { cfg.LogLevel = "info" }
    if cfg.RedisAddr == "" { return nil, fmt.Errorf("REDIS_ADDR must be set") }
    if cfg.JWTSecret == "" { return nil, fmt.Errorf("JWT_SECRET must be set") }
    // 可以添加更多验证...

	// 解析日志级别
	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		logrus.Warnf("Invalid LOG_LEVEL '%s', using default 'info'", cfg.LogLevel)
		cfg.LogLevel = "info"//将配置中的字符串修正为有效的默认值 "info"
		level = logrus.InfoLevel// 将要设置的级别常量也设为默认 InfoLevel
	}
	logrus.SetLevel(level) // 设置全局日志级别

	return cfg, nil
}

// App 结构体包含应用的所有组件和配置
type App struct {
	Config       *Config
	Log          *logrus.Logger
	DB           *gorm.DB
	RedisClient  *redis.Client
	AsynqClient  *asynq.Client
	AsynqServer  *worker.WorkerServer // 使用封装后的 WorkerServer
	Hub          *hub.Hub
	HttpServer   *http.Server
	// 可以添加其他需要的组件...
}

// NewApp 创建并初始化应用的所有组件
func NewApp() (*App, error) {
	log := logrus.New() // 创建一个新的 logger 实例给 App
	log.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	// 1. 加载配置
	cfg, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	log.Info("Configuration loaded") // 这条日志会根据 LoadConfig 设置的全局级别输出

	// 2. 初始化基础设施
	db, err := setup.InitDB(cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)
	if err != nil { return nil, fmt.Errorf("failed to init DB: %w", err) }
	log.Info("Database initialized")

	err = setup.MigrateDB(db)
	if err != nil { return nil, fmt.Errorf("failed to migrate DB: %w", err) }
	log.Info("Database migrated")

	redisClient, err := setup.InitRedis(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil { return nil, fmt.Errorf("failed to init Redis: %w", err) }
	log.Info("Redis client initialized")

	redisClientOpt := asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
	asynqClient := asynq.NewClient(redisClientOpt)
	log.Info("Asynq client initialized")

	// 3. 初始化 Repositories
	userRepo := gormpersistence.NewGormUserRepository(db)
	roomRepo := gormpersistence.NewGormRoomRepository(db)
	actionRepo := gormpersistence.NewGormActionRepository(db) // 需要 actionRepo 给 worker
	snapshotRepo := gormpersistence.NewGormSnapshotRepository(db)
	stateRepo := redisstate.NewRedisStateRepository(redisClient, "bb:") // 使用 redisClient

	// 4. 初始化 Services
	authService, err := service.NewAuthService(userRepo, cfg.JWTSecret, cfg.JWTExpiryHours)
	if err != nil { return nil, fmt.Errorf("failed to create AuthService: %w", err) }
	roomService := service.NewRoomService(roomRepo)
	collabService := service.NewCollaborationService(actionRepo, stateRepo)
	snapshotService := service.NewSnapshotService(snapshotRepo, stateRepo, actionRepo)

	// 5. 初始化 Hub
	hubInstance := hub.NewHub(collabService, snapshotService, asynqClient)

	// 6. 初始化 Handlers
	authHandler := httpHandler.NewAuthHandler(authService)
	roomHandler := httpHandler.NewRoomHandler(roomService)
	wsHandler := wsHandler.NewWebSocketHandler(hubInstance, roomService)

    // 7. 初始化 Worker Server
    workerServer := worker.NewWorkerServer(redisClientOpt, actionRepo, log) // 将 actionRepo 注入


	// 8. 初始化 Gin Engine 和路由
	gin.SetMode(gin.ReleaseMode) // 根据环境设置模式
	router := gin.New()          // 使用 New() 而不是 Default() 以便自定义中间件
	router.Use(gin.Recovery())   // 添加 Recovery 中间件
	router.Use(LoggerMiddleware(log)) // 添加自定义的 Logger 中间件 (见下文)
	// --- 应用其他中间件 ---
	router.Use(func(c *gin.Context) { /* CORS */
        allowedOrigin := "http://localhost:3000" // TODO: 从配置读取
        c.Writer.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if c.Request.Method == "OPTIONS" { c.AbortWithStatus(http.StatusNoContent); return }
		c.Next()
    })
	router.Use(middleware.RateLimit(redisClient, cfg.RateLimitMax, cfg.RateLimitWindow)) // 注入 redisClient

	// --- 设置路由 ---
	api := router.Group("/api")
	authRoutes := api.Group("/auth")
	{
		authRoutes.POST("/register", authHandler.Register)
		authRoutes.POST("/login", authHandler.Login)
	}
	roomRoutes := api.Group("/rooms").Use(middleware.Auth(cfg.JWTSecret)) // 注入 jwtSecret
	{
		roomRoutes.POST("", roomHandler.CreateRoom)
		roomRoutes.POST("/join", roomHandler.JoinRoom)
	}
	wsRoutes := router.Group("/ws").Use(middleware.Auth(cfg.JWTSecret)) // 注入 jwtSecret
	{
		wsRoutes.GET("/room/:roomId", wsHandler.HandleConnection)
	}
	router.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "pong"}) })


    // 9. 初始化 HTTP Server
    httpServer := &http.Server{
        Addr:    ":" + cfg.ServerPort,
        Handler: router,
        // 可以设置 ReadTimeout, WriteTimeout 等
    }

	// 10. 组装 App 对象
	app := &App{
		Config:       cfg,
		Log:          log,
		DB:           db,
		RedisClient:  redisClient,
		AsynqClient:  asynqClient,
        AsynqServer:  workerServer, // 存储 WorkerServer 实例
		Hub:          hubInstance,
		HttpServer:   httpServer,
	}

	return app, nil
}

// Start 启动应用的所有后台 Goroutine 和 HTTP 服务器
func (a *App) Start() {
	a.Log.Info("Starting application components...")
	// 启动 Hub
	go a.Hub.Run() // Hub 现在不接收 repo
	a.Log.Info("Hub routine started")

	// 启动 Worker Server (需要 actionRepo，但 App 中没有直接存储 repo，可以从参数传入或修改 App 结构)
    // 我们修改 App 结构来存储 actionRepo
    // go a.AsynqServer.Start(a.actionRepo) // 假设 actionRepo 已添加到 App
    // 或者在 NewApp 时就获取 actionRepo 并传递给 NewWorkerServer
    // 修正：在 NewApp 中已经将 actionRepo 传给 NewWorkerServer 了，这里直接启动
    go a.AsynqServer.Start() // WorkerServer 的 Start 方法现在不需要参数了
	a.Log.Info("Asynq worker server routine started")

	// 启动 HTTP 服务器
	go func() {
		a.Log.Infof("HTTP server starting on %s", a.HttpServer.Addr)
		if err := a.HttpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()
}

// Shutdown 优雅地关闭应用
func (a *App) Shutdown() {
	a.Log.Info("Shutting down application...")

	// 关闭 Worker Server
	a.AsynqServer.Shutdown()

	// 关闭 HTTP 服务器
	a.Log.Info("Shutting down HTTP server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.HttpServer.Shutdown(ctx); err != nil {
		a.Log.Errorf("Error shutting down HTTP server: %v", err)
	}

	// 关闭 Asynq Client
	if err := a.AsynqClient.Close(); err != nil {
		a.Log.Errorf("Error closing Asynq client: %v", err)
	}

	// 关闭 Redis 连接
	if err := a.RedisClient.Close(); err != nil {
		a.Log.Errorf("Error closing Redis connection: %v", err)
	}

	// 关闭数据库连接 (GORM V2 通常不需要)
	// sqlDB, err := a.DB.DB()
	// if err == nil {
	//     if err := sqlDB.Close(); err != nil {
	//         a.Log.Errorf("Error closing database connection: %v", err)
	//     }
	// }

	a.Log.Info("Application shutdown complete.")
}

// LoggerMiddleware 创建一个 Gin 中间件用于记录请求日志
func LoggerMiddleware(log *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()
		c.Next() // 处理请求
		latency := time.Since(startTime)
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		path := c.Request.URL.Path
		if c.Request.URL.RawQuery != "" {
			path = path + "?" + c.Request.URL.RawQuery
		}
        errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String() // 获取 Gin 错误

		entry := log.WithFields(logrus.Fields{
			"statusCode": statusCode,
			"latency":    latency,
			"clientIP":   clientIP,
			"method":     method,
			"path":       path,
		})

        if errorMessage != "" {
             entry.Error(errorMessage)
        } else {
		    entry.Info("Handled request")
        }
	}
}