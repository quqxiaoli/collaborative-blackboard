package config

import (
	"collaborative-blackboard/models"
	"context"

	"github.com/go-redis/redis/v8"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB
var Redis *redis.Client

func InitDB() {
	dsn := "root:Yb1756062305.@tcp(127.0.0.1:3306)/blackboard_db?charset=utf8mb4&parseTime=True&loc=Local"
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("Failed to connect to MySQL: " + err.Error())
	}
}

func InitRedis() {
	Redis = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
	_, err := Redis.Ping(context.Background()).Result()
	if err != nil {
		panic("Failed to connect to Redis: " + err.Error())
	}
}

// MigrateDB 自动创建或更新数据库表
func MigrateDB() {
	err := DB.AutoMigrate(&models.User{}, &models.Room{}, &models.Action{})
	if err != nil {
		panic("Failed to migrate database: " + err.Error())
	}
}
