package routes

import (
	"collaborative-blackboard/handlers"
	"collaborative-blackboard/handlers/ws"
	"collaborative-blackboard/middleware"

	"github.com/gin-gonic/gin"
)

func SetupRouter(r *gin.Engine) {
	r.POST("/register", handlers.Register)
	r.POST("/login", handlers.Login)

	api := r.Group("/api")
	api.Use(middleware.Auth())
	{
		api.POST("/room", handlers.CreateRoom)
		api.POST("/room/join", handlers.JoinRoom)
		api.GET("/ws/:roomId", ws.WSHandler)
	}
}
