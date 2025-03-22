package routes

import (
    "collaborative-blackboard/handlers"
    "collaborative-blackboard/middleware"
    "github.com/gin-gonic/gin"
)

func SetupRouter(r *gin.Engine) {
    r.Use(middleware.RateLimit(10)) // 每秒最多 10 次请求
    r.POST("/register", handlers.Register)
    r.POST("/login", handlers.Login)
    r.POST("/room", handlers.CreateRoom)
    r.POST("/room/join", handlers.JoinRoom)
    r.GET("/ws/:roomId", handlers.WSHandler) // WebSocket 端点
    r.GET("/room/:id/replay", handlers.GetReplay) // 回放路由
    r.POST("/room/:id/upload", handlers.UploadImage) // 图片上传
    r.GET("/room/:id/export", handlers.ExportBoard)  // 黑板导出
}