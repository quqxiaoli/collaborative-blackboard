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
	routes.SetupRouter(r)

	go ws.RunSnapshotTask()

	log.Info("Server starting on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Failed to start server: ", err)
	}
}
