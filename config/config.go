package config

import (
	"collaborative-blackboard/models"
	"context"
	"os"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	_ "github.com/joho/godotenv/autoload"
)

var DB *gorm.DB
var Redis *redis.Client

func InitDB() {
    // 从环境变量获取 MySQL 配置，默认值作为 fallback
    mysqlHost := os.Getenv("MYSQL_HOST")
    if mysqlHost == "" {
        mysqlHost = "127.0.0.1"
    }
    mysqlPort := os.Getenv("MYSQL_PORT")
    if mysqlPort == "" {
        mysqlPort = "3306"
    }
    mysqlUser := os.Getenv("MYSQL_USER")
    if mysqlUser == "" {
        mysqlUser = "root"
    }
    mysqlPassword := os.Getenv("MYSQL_PASSWORD")
    if mysqlPassword == "" {
        mysqlPassword = "Yb1756062305."
    }
    mysqlDB := os.Getenv("MYSQL_DB")
    if mysqlDB == "" {
        mysqlDB = "blackboard_db"
    }

    // 构造 DSN
    dsn := mysqlUser + ":" + mysqlPassword + "@tcp(" + mysqlHost + ":" + mysqlPort + ")/" + mysqlDB + "?charset=utf8mb4&parseTime=True&loc=Local"
    var err error
    DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
    if err != nil {
        panic("Failed to connect to MySQL: " + err.Error())
    }

    // 配置 MySQL 连接池
    sqlDB, err := DB.DB()
    if err != nil {
        panic("Failed to get sql.DB: " + err.Error())
    }
    sqlDB.SetMaxOpenConns(100)        // 最大打开连接数
    sqlDB.SetMaxIdleConns(10)         // 最大空闲连接数
    sqlDB.SetConnMaxLifetime(time.Hour) // 连接最大存活时间
}

func MigrateDB() {
    DB.AutoMigrate(&models.User{}, &models.Room{}, &models.Action{})
}

func InitRedis() {
    // 从环境变量获取 Redis 配置，默认值作为 fallback
    redisHost := os.Getenv("REDIS_HOST")
    if redisHost == "" {
        redisHost = "127.0.0.1"
    }
    redisPort := os.Getenv("REDIS_PORT")
    if redisPort == "" {
        redisPort = "6379"
    }
    redisPassword := os.Getenv("REDIS_PASSWORD") // 支持密码，默认为空
    redisDBStr := os.Getenv("REDIS_DB")
    redisDB := 0
    if redisDBStr != "" {
        if dbNum, err := strconv.Atoi(redisDBStr); err == nil {
            redisDB = dbNum
        }
    }

    // 初始化 Redis 客户端
    Redis = redis.NewClient(&redis.Options{
        Addr:         redisHost + ":" + redisPort,
        Password:     redisPassword,
        DB:           redisDB,
        PoolSize:     50,           // 连接池大小
        MinIdleConns: 10,           // 最小空闲连接数
        MaxConnAge:   time.Hour,    // 连接最大存活时间
    })
    _, err := Redis.Ping(context.Background()).Result()
    if err != nil {
        panic("Failed to connect to Redis: " + err.Error())
    }
}

// ResetDB 重置数据库表和自增 ID
func ResetDB() {
    InitDB()
    MigrateDB()
    // 使用 TRUNCATE 重置表
    DB.Exec("TRUNCATE TABLE actions")
    DB.Exec("TRUNCATE TABLE rooms")
    DB.Exec("TRUNCATE TABLE users")
    // 重置 Redis
    InitRedis()
    Redis.FlushAll(context.Background())
}