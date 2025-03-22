package config

import (
    "context"
    "github.com/go-redis/redis/v8"
    "gorm.io/driver/mysql"
    "gorm.io/gorm"
)

// DB 是全局 MySQL 连接
var DB *gorm.DB

// Redis 是全局 Redis 客户端
var Redis *redis.Client

// InitDB 初始化 MySQL 连接
func InitDB() {
    // DSN（数据源名称）格式：用户:密码@协议(地址:端口)/数据库?参数
    dsn := "root:Yb1756062305.@tcp(127.0.0.1:3306)/blackboard_db?charset=utf8mb4&parseTime=True&loc=Local"
    var err error
    DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
    if err != nil {
        panic("Failed to connect to MySQL: " + err.Error())
    }
}

// InitRedis 初始化 Redis 连接
func InitRedis() {
    Redis = redis.NewClient(&redis.Options{
        Addr:     "localhost:6379", // Redis 默认地址和端口
        Password: "",               // 无密码
        DB:       0,                // 默认数据库
    })
    // 测试连接
    _, err := Redis.Ping(context.Background()).Result()
    if err != nil {
        panic("Failed to connect to Redis: " + err.Error())
    }
}