package setup // 确认包名是 setup

import (
	"context"
	"fmt"
	"time"

	// 使用正确的 Domain 模型路径 (如果迁移需要)
	// "collaborative-blackboard/internal/domain"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql" // 假设使用 MySQL
	"gorm.io/gorm"
	"gorm.io/gorm/logger" // 用于配置 GORM 日志
)

// 全局变量 DB 和 Redis 不再需要在这里定义和赋值
// var DB *gorm.DB
// var Redis *redis.Client

// InitDB 初始化数据库连接并返回 GORM DB 实例和错误
func InitDB(user, password, host, port, dbname string) (*gorm.DB, error) {
	// 构建 DSN (Data Source Name)
	// 注意：参数需要根据实际情况调整，例如 charset, parseTime 等
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		user, password, host, port, dbname)

	// 配置 GORM 日志级别
	gormLogger := logger.New(
		logrus.StandardLogger(), // 使用 logrus 作为日志输出
		logger.Config{
			SlowThreshold:             time.Second, // 慢 SQL 阈值
			LogLevel:                  logger.Warn, // 日志级别 (Warn, Error, Info, Silent)
			IgnoreRecordNotFoundError: true,        // 忽略 ErrRecordNotFound 错误
			Colorful:                  false,       // 是否启用彩色打印
		},
	)

	// 连接数据库
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: gormLogger, // 设置日志记录器
		// 可以添加其他 GORM 配置
	})

	if err != nil {
		// 连接失败，返回错误
		logrus.WithError(err).Fatal("Failed to connect to database")
		return nil, fmt.Errorf("failed to connect database: %w", err)
	}

	logrus.Info("Database connection established")
	// 返回连接实例
	return db, nil
}

// InitRedis 初始化 Redis 连接并返回 Redis 客户端实例和错误
func InitRedis(addr, password string, db int) (*redis.Client, error) {
	// 创建 Redis 客户端选项
	opts := &redis.Options{
		Addr:     addr,     // Redis 服务器地址和端口 (e.g., "localhost:6379")
		Password: password, // Redis 密码 (如果没有则为空字符串)
		DB:       db,       // 使用的 Redis 数据库编号 (默认 0)
	}

	// 创建 Redis 客户端
	client := redis.NewClient(opts)

	// 测试连接 (Ping)
	// 使用 context 设置超时
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Ping(ctx).Result()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to connect to Redis")
		return nil, fmt.Errorf("failed to connect redis: %w", err)
	}

	logrus.Info("Redis connection established")
	// 返回客户端实例
	return client, nil
}

// MigrateDB 函数现在移动到 migrations.go 中，并接收 *gorm.DB 参数
// func MigrateDB() { ... }