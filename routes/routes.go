package routes

import (
    "collaborative-blackboard/handlers"
    "github.com/gin-gonic/gin"
)

// SetupRouter 配置所有路由
func SetupRouter(r *gin.Engine) {
    r.POST("/register", handlers.Register)
    r.POST("/login", handlers.Login)
}