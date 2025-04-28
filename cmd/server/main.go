package main

import (
	//"context"
	"os"
	"os/signal"
	"syscall"

	// 导入 bootstrap 包
	"collaborative-blackboard/internal/bootstrap"
	"github.com/sirupsen/logrus" // 仍然需要 logrus
)

func main() {
	// 初始化并运行 App
	app, err := bootstrap.NewApp()
	if err != nil {
		logrus.Fatalf("Failed to initialize application: %v", err)
	}

	// 启动应用组件
	app.Start()

	// 设置优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrus.Info("Shutdown signal received...")

	// 关闭应用
	app.Shutdown()
}