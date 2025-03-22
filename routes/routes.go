package routes

import (
    "collaborative-blackboard/handlers"
    "github.com/gin-gonic/gin"
)

func SetupRouter(r *gin.Engine) {
    r.POST("/register", handlers.Register)
    r.POST("/login", handlers.Login)
    r.POST("/room", handlers.CreateRoom)
    r.POST("/room/join", handlers.JoinRoom)
    r.GET("/ws/:roomId", handlers.WSHandler) // WebSocket 端点
    r.GET("/room/:id/replay", handlers.GetReplay) // 回放路由
}