package main

import (
    "collaborative-blackboard/config"
    "github.com/gin-gonic/gin"
)

// main 是程序入口
func main() {
    // 初始化数据库和 Redis
    config.InitDB()
    config.InitRedis()

    // 初始化 Gin
    r := gin.Default()

    // 定义 /ping 端点
    r.GET("/ping", func(c *gin.Context) {
        c.JSON(200, gin.H{
            "message": "pong",
        })
    })

    // 启动服务
    r.Run(":8080")
}