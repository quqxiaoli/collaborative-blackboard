package routes

import (
    "collaborative-blackboard/handlers"
    "github.com/gin-gonic/gin"
)

func SetupRouter(r *gin.Engine) {
    r.POST("/register", handlers.Register)
    r.POST("/login", handlers.Login)
    r.POST("/room", handlers.CreateRoom)      // 创建房间
    r.POST("/room/join", handlers.JoinRoom)   // 加入房间
}