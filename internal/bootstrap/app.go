package bootstrap

import (
	"context"
	"errors" // 导入 errors 包
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8" // 导入 redis
	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm" // 导入 gorm

	// --- 导入内部包 ---
	httpHandler "collaborative-blackboard/internal/handler/http"
	wsHandler "collaborative-blackboard/internal/handler/websocket"
	"collaborative-blackboard/internal/hub"
	gormpersistence "collaborative-blackboard/internal/infra/persistence/gorm"
	"collaborative-blackboard/internal/infra/setup"
	redisstate "collaborative-blackboard/internal/infra/state/redis"
	"collaborative-blackboard/internal/middleware"
	//"collaborative-blackboard/internal/repository" 
	"collaborative-blackboard/internal/service"
	"collaborative-blackboard/internal/worker"
	"collaborative-blackboard/internal/tasks"
)

// Config 结构体用于存储从环境变量或文件加载的配置
type Config struct {
	DBUser          string
	DBPassword      string
	DBHost          string
	DBPort          string
	DBName          string
	RedisAddr       string
	RedisPassword   string
	RedisDB         int
	JWTSecret       string
	ServerPort      string
	LogLevel        string
	RateLimitMax    int
	RateLimitWindow time.Duration
	JWTExpiryHours  int
	AppEnv          string // 新增: 应用环境 (development/production)
	KeyPrefix       string // 新增: Redis Key 前缀
}

// LoadConfig 从环境变量加载配置
func LoadConfig() (*Config, error) {
	// 优先加载 .env 文件 (如果存在)
	_ = godotenv.Load() // 忽略错误，允许只使用环境变量

	cfg := &Config{
		DBUser:          os.Getenv("DB_USER"),
		DBPassword:      os.Getenv("DB_PASSWORD"),
		DBHost:          os.Getenv("DB_HOST"),
		DBPort:          os.Getenv("DB_PORT"),
		DBName:          os.Getenv("DB_NAME"),
		RedisAddr:       os.Getenv("REDIS_ADDR"),
		RedisPassword:   os.Getenv("REDIS_PASSWORD"),
		JWTSecret:       os.Getenv("JWT_SECRET"),
		ServerPort:      os.Getenv("SERVER_PORT"),
		LogLevel:        os.Getenv("LOG_LEVEL"),
		AppEnv:          os.Getenv("APP_ENV"),
		KeyPrefix:       os.Getenv("REDIS_KEY_PREFIX"),
		// --- 设置默认值 ---
		RateLimitMax:    100,
		RateLimitWindow: 1 * time.Second,
		JWTExpiryHours:  24,
	}

	// 处理 Redis DB
	redisDBStr := os.Getenv("REDIS_DB")
	cfg.RedisDB, _ = strconv.Atoi(redisDBStr) // 忽略错误，默认为 0

	// --- 设置其他默认值和进行必要检查 ---
	if cfg.ServerPort == "" { cfg.ServerPort = "8080" }
	if cfg.LogLevel == "" { cfg.LogLevel = "info" }
	if cfg.AppEnv == "" { cfg.AppEnv = "development" } // 默认开发环境
	if cfg.KeyPrefix == "" { cfg.KeyPrefix = "bb:" } // 默认 key 前缀
	if cfg.RedisAddr == "" { return nil, fmt.Errorf("environment variable REDIS_ADDR must be set") }
	if cfg.JWTSecret == "" { return nil, fmt.Errorf("environment variable JWT_SECRET must be set") }
	// 可以添加数据库配置的检查

	// 验证日志级别
	if _, err := logrus.ParseLevel(cfg.LogLevel); err != nil {
		logrus.Warnf("Invalid LOG_LEVEL '%s', using default 'info'", cfg.LogLevel)
		cfg.LogLevel = "info" // 修正配置值
	}

	return cfg, nil
}

// App 结构体包含应用的所有组件和配置
type App struct {
	Config       *Config
	Log          *logrus.Logger
	DB           *gorm.DB
	RedisClient  *redis.Client
	AsynqClient  *asynq.Client
	AsynqServer  *worker.WorkerServer
	Hub          *hub.Hub
	HttpServer   *http.Server
	redisClientOpt asynq.RedisClientOpt
	// 可以存储其他组件以便 Shutdown 时访问，或者仅在 NewApp 中使用
	// actionRepo   repository.ActionRepository
}

// NewApp 创建并初始化应用的所有组件
func NewApp() (*App, error) {
	// 1. 加载配置
	cfg, err := LoadConfig()
	if err != nil {
		// 使用标准 log 记录启动时错误，因为 logrus 可能还未完全配置
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		return nil, err // 返回错误
	}

	// 2. 初始化 Logger
	log := logrus.New()
	if cfg.AppEnv == "production" {
		log.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339Nano})
	} else {
		log.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, ForceColors: true})
	}
	logLevel, _ := logrus.ParseLevel(cfg.LogLevel) // cfg.LogLevel 已被 LoadConfig 验证
	log.SetLevel(logLevel)
	log.SetOutput(os.Stdout) // 或 os.Stderr
	log.Infof("Logger initialized (Level: %s, Format: %T)", logLevel.String(), log.Formatter)
	log.Info("Configuration loaded successfully")

	// 3. 初始化基础设施
	log.Info("Initializing infrastructure...")
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
	log.Info("Infrastructure initialized successfully")

	// 4. 初始化 Repositories
	log.Info("Initializing repositories...")
	userRepo := gormpersistence.NewGormUserRepository(db)
	roomRepo := gormpersistence.NewGormRoomRepository(db)
	actionRepo := gormpersistence.NewGormActionRepository(db)
	snapshotRepo := gormpersistence.NewGormSnapshotRepository(db)
	stateRepo := redisstate.NewRedisStateRepository(redisClient, cfg.KeyPrefix) // 使用配置的前缀
	log.Info("Repositories initialized")

	// 5. 初始化 Services
	log.Info("Initializing services...")
	authService, err := service.NewAuthService(userRepo, cfg.JWTSecret, cfg.JWTExpiryHours)
	if err != nil { return nil, fmt.Errorf("failed to create AuthService: %w", err) }
	roomService := service.NewRoomService(roomRepo)
	// 注意：确保 Service 的依赖是最新的
	collabService := service.NewCollaborationService(actionRepo, stateRepo)
	snapshotService := service.NewSnapshotService(snapshotRepo, stateRepo, actionRepo)
	log.Info("Services initialized")

	// 6. 初始化 Hub (注入 Redis Client 和 Prefix)
	log.Info("Initializing hub...")
	hubInstance := hub.NewHub(collabService, snapshotService, asynqClient, redisClient, cfg.KeyPrefix)
	log.Info("Hub initialized")

	// 7. 初始化 Handlers
	log.Info("Initializing handlers...")
	authHandler := httpHandler.NewAuthHandler(authService)
	roomHandler := httpHandler.NewRoomHandler(roomService)
	wsHandler := wsHandler.NewWebSocketHandler(hubInstance, roomService)
	log.Info("Handlers initialized")

	// 8. 初始化 Worker Server
	log.Info("Initializing worker server...")
	workerServer := worker.NewWorkerServer(redisClientOpt, actionRepo, hubInstance, snapshotService, log)
	log.Info("Worker server initialized")

	// 9. 初始化 Gin Engine 和路由
	log.Info("Setting up Gin router...")
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(LoggerMiddleware(log)) // 使用 App 的 logger

	// --- 应用其他中间件 ---
	router.Use(func(c *gin.Context) { /* CORS */
		allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN") // 从环境变量读取
		if allowedOrigin == "" {
			allowedOrigin = "http://localhost:3000" // 开发默认
		}
		c.Writer.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if c.Request.Method == "OPTIONS" { c.AbortWithStatus(http.StatusNoContent); return }
		c.Next()
	})
	router.Use(middleware.RateLimit(redisClient, cfg.RateLimitMax, cfg.RateLimitWindow))

	// --- 设置路由 ---
	api := router.Group("/api")
	authRoutes := api.Group("/auth")
	{
		authRoutes.POST("/register", authHandler.Register)
		authRoutes.POST("/login", authHandler.Login)
	}
	roomRoutes := api.Group("/rooms").Use(middleware.Auth(cfg.JWTSecret))
	{
		roomRoutes.POST("", roomHandler.CreateRoom)
		roomRoutes.POST("/join", roomHandler.JoinRoom)
	}
	wsRoutes := router.Group("/ws").Use(middleware.Auth(cfg.JWTSecret))
	{
		wsRoutes.GET("/room/:roomId", wsHandler.HandleConnection)
	}
	router.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "pong"}) })
	log.Info("Router setup complete")

	// 10. 初始化 HTTP Server
	log.Info("Initializing HTTP server...")
	httpServer := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,
		// ReadTimeout: 10 * time.Second, // 推荐设置超时
		// WriteTimeout: 10 * time.Second,
		// IdleTimeout:  120 * time.Second,
	}
	log.Info("HTTP server initialized")

	// 11. 组装 App 对象
	log.Info("Assembling application...")
	app := &App{
		Config:       cfg,
		Log:          log,
		DB:           db,
		RedisClient:  redisClient,
		AsynqClient:  asynqClient,
		AsynqServer:  workerServer,
		Hub:          hubInstance,
		HttpServer:   httpServer,
		// actionRepo: actionRepo // 如果 Shutdown 需要，可以存储
	}
	log.Info("Application assembled successfully")

	return app, nil
}

// Start 启动应用的所有后台 Goroutine 和 HTTP 服务器
func (a *App) Start() {
	a.Log.Info("Starting application background routines...")
	go a.Hub.Run()
	a.Log.Info("Hub routine started")

	go a.AsynqServer.Start() // Start 不再需要参数
	a.Log.Info("Asynq worker server routine started")

	a.registerPeriodicTasks()

	// 启动 HTTP 服务器
	go func() {
		a.Log.Infof("HTTP server starting to listen on %s", a.HttpServer.Addr)
		if err := a.HttpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// 使用 App 的 logger 记录 Fatal 错误
			a.Log.Fatalf("Failed to start HTTP server: %v", err)
		}
		a.Log.Info("HTTP server stopped listening.")
	}()
}


func (a *App) registerPeriodicTasks() {
    scheduler := asynq.NewScheduler(a.redisClientOpt, &asynq.SchedulerOpts{
        // Location: time.UTC, // 可以设置时区
    })

    // 创建周期性快照检查任务
    taskPayload, err := tasks.NewSnapshotPeriodicCheckTask()
    if err != nil {
        a.Log.Errorf("Failed to create snapshot periodic check task payload: %v", err)
        return // 或者 panic?
    }
    task := asynq.NewTask(tasks.TypeSnapshotPeriodicCheck, taskPayload)

    schedule := "@every 5m"
    // --- 使用 _ 忽略 entryID，或者在日志中使用它 ---
    entryID, err := scheduler.Register(schedule, task, asynq.Queue("default")) // <--- 使用 _ 或者 entryID
    if err != nil {
        a.Log.Errorf("Could not register periodic snapshot check task: %v", err)
    } else {
        // --- 在日志中使用 entryID ---
        a.Log.Infof("Periodic snapshot check task registered with schedule '%s' (EntryID: %s)", schedule, entryID)
    }

    // 启动 Scheduler (需要在一个 goroutine 中运行)
    go func() {
        a.Log.Info("Asynq scheduler starting...")
        if err := scheduler.Run(); err != nil {
            // 通常 scheduler.Run() 在正常关闭时也会返回错误
             if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, asynq.ErrServerClosed){ // 检查是否是关闭错误
                 a.Log.Errorf("Asynq scheduler Run() failed: %v", err)
             } else {
                 a.Log.Info("Asynq scheduler stopped.")
             }
        }
    }()
    // 注意：优雅关闭时可能也需要停止 scheduler
}

// Shutdown 优雅地关闭应用
func (a *App) Shutdown() {
	a.Log.Info("Shutting down application...")

	// 1. 停止 Hub 的订阅
	// Hub 的 Run 方法会在其 channel 关闭时退出，我们还需要停止订阅
	if a.Hub != nil { // 检查 Hub 是否已初始化
		a.Hub.StopAllSubscriptions() // 调用停止订阅方法
	}

	// 2. 优雅关闭 Worker Server
	if a.AsynqServer != nil { // 检查 Worker 是否已初始化
		a.AsynqServer.Shutdown()
	}

	// 3. 优雅关闭 HTTP 服务器
	a.Log.Info("Shutting down HTTP server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // 增加超时时间
	defer cancel()
	if err := a.HttpServer.Shutdown(ctx); err != nil {
		a.Log.Errorf("Error shutting down HTTP server: %v", err)
	} else {
		a.Log.Info("HTTP server shut down gracefully.")
	}

	// 4. 关闭 Asynq Client
	if a.AsynqClient != nil { // 检查 Asynq Client 是否已初始化
		if err := a.AsynqClient.Close(); err != nil {
			a.Log.Errorf("Error closing Asynq client: %v", err)
		} else {
			a.Log.Info("Asynq client closed.")
		}
	}

	// 5. 关闭 Redis 连接
	if a.RedisClient != nil { // 检查 Redis Client 是否已初始化
		if err := a.RedisClient.Close(); err != nil {
			a.Log.Errorf("Error closing Redis connection: %v", err)
		} else {
			a.Log.Info("Redis connection closed.")
		}
	}

	// 6. 关闭数据库连接 (GORM V2 通常不需要显式关闭连接池)
	// sqlDB, err := a.DB.DB()
	// if err == nil && sqlDB != nil {
	//     if err := sqlDB.Close(); err != nil {
	//         a.Log.Errorf("Error closing database connection: %v", err)
	//     } else {
	//         a.Log.Info("Database connection closed.")
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
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		entry := log.WithFields(logrus.Fields{
			"status_code": statusCode, // 更标准的字段名
			"latency_ms":  latency.Milliseconds(), // 记录毫秒数
			"client_ip":   clientIP,
			"method":      method,
			"path":        path,
		})

		if errorMessage != "" {
			entry.Error(errorMessage)
		} else {
			// 区分状态码记录日志级别
			if statusCode >= 500 {
				entry.Error("Server error")
			} else if statusCode >= 400 {
				entry.Warn("Client error")
			} else {
				entry.Info("Request handled")
			}
		}
	}
}