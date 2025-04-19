package setup

import (
	"context"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	"fmt" // 导入 fmt 包用于错误格式化
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB     // DB 是全局数据库连接实例
var Redis *redis.Client // Redis 是全局 Redis 客户端实例

// InitDB 初始化数据库连接
func InitDB() {
	// .env 文件通常在 main 函数开始时加载一次即可
	// err := godotenv.Load()
	// if err != nil {
	// 	logrus.Warn("Error loading .env file, using environment variables directly")
	// }

	dsn, err := getDSN() // 获取数据库连接字符串
	if err != nil {
		logrus.Fatal("Failed to get DSN: ", err) // 如果无法构建 DSN，则程序无法继续
	}

	var errDB error
	DB, errDB = gorm.Open(mysql.Open(dsn), &gorm.Config{}) // 连接数据库
	if errDB != nil {
		logrus.Fatal("Failed to connect to MySQL: ", errDB) // 注意这里应该用 errDB
	}

	sqlDB, err := DB.DB() // 获取底层的 *sql.DB 对象
	if err != nil {
		logrus.Fatal("Failed to get underlying sql.DB: ", err) // 添加错误检查
	}
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	logrus.Info("MySQL connected")             // 记录数据库连接成功信息
}

// getDSN 从环境变量构建数据库连接字符串 (DSN)
// 返回 DSN 字符串和可能的错误
func getDSN() (string, error) {
	mysqlUser := os.Getenv("MYSQL_USER")
	if mysqlUser == "" {
		// 如果环境变量未设置，应该返回错误
		return "", fmt.Errorf("MYSQL_USER environment variable not set")
	}
	mysqlPassword := os.Getenv("MYSQL_PASSWORD")
	if mysqlPassword == "" {
		// !!! 安全警告：绝不应在代码中硬编码密码或设置不安全的默认值 !!!
		// 如果环境变量未设置，应该返回错误，强制要求配置。
		return "", fmt.Errorf("MYSQL_PASSWORD environment variable not set")
	}
	mysqlHost := os.Getenv("MYSQL_HOST")
	if mysqlHost == "" {
		mysqlHost = "127.0.0.1" // 可以保留本地开发默认值，但生产环境应显式设置
	}
	mysqlPort := os.Getenv("MYSQL_PORT")
	if mysqlPort == "" {
		mysqlPort = "3306"
	}
	mysqlDB := os.Getenv("MYSQL_DB")
	if mysqlDB == "" {
		mysqlDB = "blackboard_db" // 可以保留本地开发默认值
	}
	// 构建 DSN 字符串
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		mysqlUser, mysqlPassword, mysqlHost, mysqlPort, mysqlDB)
	return dsn, nil // 返回构建好的 DSN 和 nil 错误
}

// MigrateDB 自动迁移数据库模式 
// 注意：此函数现在由 migrations.go 中的同名函数替代，它使用自定义 SQL 来处理用户表的迁移
// 这解决了 TEXT/BLOB 列需要指定索引长度的问题
// func MigrateDB() {
// 	// 自动迁移 User, Room, Action, Snapshot 模型对应的表结构
// 	err := DB.AutoMigrate(&models.User{}, &models.Room{}, &models.Action{}, &models.Snapshot{})
// 	if err != nil {
// 		logrus.Fatal("Failed to migrate database: ", err) // 迁移失败是严重问题
// 	}
// 	logrus.Info("Database migrated") // 记录数据库迁移成功信息
// }

// InitRedis 初始化 Redis 连接
func InitRedis() {
	// .env 文件通常在 main 函数开始时加载一次即可
	// err := godotenv.Load()
	// if err != nil {
	// 	logrus.Warn("Error loading .env file, using environment variables directly")
	// }
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "127.0.0.1" // 本地开发默认值
	}
	redisPort := os.Getenv("REDIS_PORT")
	if redisPort == "" {
		redisPort = "6379"
	}
	Redis = redis.NewClient(&redis.Options{
		Addr:         redisHost + ":" + redisPort,
		Password:     os.Getenv("REDIS_PASSWORD"),
		DB:           0,
		PoolSize:     20,
		MinIdleConns: 5,
		MaxConnAge:   30 * time.Minute,      // 连接最大存活时间
	})
	// 使用后台上下文测试 Redis 连接
	if _, err := Redis.Ping(context.Background()).Result(); err != nil {
		logrus.Fatal("Failed to connect to Redis: ", err) // Redis 连接失败是严重问题
	}
	logrus.Info("Redis connected") // 记录 Redis 连接成功信息
}
