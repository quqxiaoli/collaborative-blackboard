package config

import (
	"collaborative-blackboard/models" // 确保导入 models 包
	"context"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB
var Redis *redis.Client

func InitDB() {
	err := godotenv.Load()
	if err != nil {
		logrus.Fatal("Error loading .env file")
	}

	dsn := getDSN()
	var errDB error
	DB, errDB = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if errDB != nil {
		logrus.Fatal("Failed to connect to MySQL: ", err)
	}

	sqlDB, _ := DB.DB()
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	logrus.Info("MySQL connected")
}

func getDSN() string {
	mysqlUser := os.Getenv("MYSQL_USER")
	if mysqlUser == "" {
		mysqlUser = "root"
	}
	mysqlPassword := os.Getenv("MYSQL_PASSWORD")
	if mysqlPassword == "" {
		mysqlPassword = "Yb1756062305."
	}
	mysqlHost := os.Getenv("MYSQL_HOST")
	if mysqlHost == "" {
		mysqlHost = "127.0.0.1"
	}
	mysqlPort := os.Getenv("MYSQL_PORT")
	if mysqlPort == "" {
		mysqlPort = "3306"
	}
	mysqlDB := os.Getenv("MYSQL_DB")
	if mysqlDB == "" {
		mysqlDB = "blackboard_db"
	}
	return mysqlUser + ":" + mysqlPassword + "@tcp(" + mysqlHost + ":" + mysqlPort + ")/" + mysqlDB + "?charset=utf8mb4&parseTime=True&loc=Local"
}

func MigrateDB() {
	DB.AutoMigrate(&models.User{}, &models.Room{}, &models.Action{}, &models.Snapshot{})
	logrus.Info("Database migrated")
}

func InitRedis() {
	err := godotenv.Load()
	if err != nil {
		logrus.Fatal("Error loading.env file")
	}
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "127.0.0.1"
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
		MaxConnAge:   30 * time.Minute,
	})
	if _, err := Redis.Ping(context.Background()).Result(); err != nil {
		logrus.Fatal("Failed to connect to Redis: ", err)
	}
	logrus.Info("Redis connected")
}
