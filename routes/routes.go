package routes

import (
	"collaborative-blackboard/handlers"
	"collaborative-blackboard/middleware"
	"github.com/gin-gonic/gin"
)

func SetupRouter(r *gin.Engine) {
    r.POST("/register", handlers.Register)
    r.POST("/login", handlers.Login)
    // 受保护路由组
    auth := r.Group("/api") // 改为明确分组，避免路径混淆
    auth.Use(middleware.Auth())
    {
        auth.POST("/room", handlers.CreateRoom)
        auth.POST("/room/join", handlers.JoinRoom)
        auth.GET("/ws/:roomId", handlers.WSHandler)
        auth.GET("/room/:id/replay", handlers.GetReplay)
        auth.POST("/room/:id/upload", handlers.UploadImage)
        auth.GET("/room/:id/export", handlers.ExportBoard)
    }


}