package main

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/handlers/ws"
	"collaborative-blackboard/routes"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

var log = logrus.New()

func main() {
	config.InitDB()
	config.MigrateDB()
	config.InitRedis()

	r := gin.Default()
	r.Use(corsMiddleware()) // 添加 CORS
	routes.SetupRouter(r)

	go ws.RunSnapshotTask()

	log.Info("Server starting on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Failed to start server: ", err)
	}
}

func corsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Writer.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000")
        c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
        if c.Request.Method == "OPTIONS" {
            c.AbortWithStatus(204)
            return
        }
        c.Next()
    }
}