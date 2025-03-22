package main

import (
    "github.com/gin-gonic/gin"
)

// main 是程序入口
func main() {
    // 初始化 Gin，默认包含日志和恢复中间件
    r := gin.Default()

    // 定义 /ping 端点，返回 JSON 响应
    r.GET("/ping", func(c *gin.Context) {
        c.JSON(200, gin.H{
            "message": "pong",
        })
    })

    // 启动服务，监听 8080 端口
    r.Run(":8080") // 默认 localhost:8080
}