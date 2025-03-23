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

    dsn := mysqlUser + ":" + mysqlPassword + "@tcp(" + mysqlHost + ":" + mysqlPort + ")/" + mysqlDB + "?charset=utf8mb4&parseTime=True&loc=Local"
    var err error
    DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
    if err != nil {
        panic("Failed to connect to MySQL: " + err.Error())
    }

    sqlDB, err := DB.DB()
    if err != nil {
        panic("Failed to get sql.DB: " + err.Error())
    }
    sqlDB.SetMaxOpenConns(100)
    sqlDB.SetMaxIdleConns(10)
    sqlDB.SetConnMaxLifetime(time.Hour)
}

func MigrateDB() {
    // 添加 Snapshot 模型迁移
    DB.AutoMigrate(&models.User{}, &models.Room{}, &models.Action{}, &models.Snapshot{})
}

func InitRedis() {
    redisHost := os.Getenv("REDIS_HOST")
    if redisHost == "" {
        redisHost = "127.0.0.1"
    }
    redisPort := os.Getenv("REDIS_PORT")
    if redisPort == "" {
        redisPort = "6379"
    }
    redisPassword := os.Getenv("REDIS_PASSWORD")
    redisDBStr := os.Getenv("REDIS_DB")
    redisDB := 0
    if redisDBStr != "" {
        if dbNum, err := strconv.Atoi(redisDBStr); err == nil {
            redisDB = dbNum
        }
    }

    Redis = redis.NewClient(&redis.Options{
        Addr:         redisHost + ":" + redisPort,
        Password:     redisPassword,
        DB:           redisDB,
        PoolSize:     50,
        MinIdleConns: 10,
        MaxConnAge:   time.Hour,
    })
    _, err := Redis.Ping(context.Background()).Result()
    if err != nil {
        panic("Failed to connect to Redis: " + err.Error())
    }
}

func ResetDB() {
    InitDB()
    MigrateDB()
    DB.Exec("TRUNCATE TABLE actions")
    DB.Exec("TRUNCATE TABLE rooms")
    DB.Exec("TRUNCATE TABLE users")
    DB.Exec("TRUNCATE TABLE snapshots") // 添加 snapshots 表重置
    InitRedis()
    Redis.FlushAll(context.Background())
}