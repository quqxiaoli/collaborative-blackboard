package main

import (
    "collaborative-blackboard/config"
    "github.com/gin-gonic/gin"
)

func main() {
    // 初始化数据库并迁移表结构
    config.InitDB()
    config.MigrateDB()
    // 初始化 Redis
    config.InitRedis()

    r := gin.Default()
    r.GET("/ping", func(c *gin.Context) {
        c.JSON(200, gin.H{
            "message": "pong",
        })
    })
    r.Run(":8080")
}