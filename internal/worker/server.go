package worker

import (
	"context"
	"errors"
	"net/http" // 需要导入 http 以检查 ErrServerClosed

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"

	// 导入内部包
	"collaborative-blackboard/internal/repository"
	"collaborative-blackboard/internal/tasks"
)

// WorkerServer 封装了 Asynq Worker Server 的启动和关闭逻辑
type WorkerServer struct {
	server *asynq.Server
	log    *logrus.Entry
	actionRepo repository.ActionRepository // 将 actionRepo 存储在结构体中
}

// NewWorkerServer 创建一个新的 WorkerServer 实例
func NewWorkerServer(redisOpt asynq.RedisClientOpt, actionRepo repository.ActionRepository, logger *logrus.Logger) *WorkerServer {
	logEntry := logger.WithField("component", "worker_server")

	server := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: 10, // 可以从配置读取
			Queues: map[string]int{
				"critical": 6,
				"default":  3,
				"low":      1,
			},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				// *** 修正：尝试从 Task 对象获取信息 ***
                taskID := ""
                taskQueue := "default" // 默认值
                if rw := task.ResultWriter(); rw != nil {
                    taskID = rw.TaskID()
                }
				retryCount, _ := asynq.GetRetryCount(ctx)
				maxRetry, _ := asynq.GetMaxRetry(ctx)
				logEntry.WithFields(logrus.Fields{ // 使用 logEntry
					"task_id":   taskID,
					"task_type": task.Type(),
					"queue":     taskQueue,
                    "retries":   retryCount,
                    "max_retry": maxRetry,
				}).Errorf("Task failed: %v", err)
			}),
			// Logger: logEntry, // 可以适配 logger
		},
	)

	return &WorkerServer{
		server: server,
		log:    logEntry,
		actionRepo: actionRepo, // 存储 actionRepo
	}
}

// Start 运行 Worker Server
// 它应该在一个单独的 goroutine 中调用
func (ws *WorkerServer) Start() {
	mux := asynq.NewServeMux()

	// 注册任务处理器
	actionPersistenceHandler := NewActionPersistenceHandler(ws.actionRepo)
	mux.HandleFunc(tasks.TypeActionPersistence, actionPersistenceHandler.ProcessTask)

	// TODO: 注册其他任务处理器

	ws.log.Info("Worker server starting...")
	if err := ws.server.Run(mux); err != nil {
		// 检查是否是正常关闭错误
		if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, asynq.ErrServerClosed) {
			ws.log.Fatalf("Could not run worker server: %v", err)
		} else {
			ws.log.Info("Worker server stopped.")
		}
	}
}

// Shutdown 优雅地关闭 Worker Server
func (ws *WorkerServer) Shutdown() {
	ws.log.Info("Shutting down worker server...")
	ws.server.Shutdown()
	ws.log.Info("Worker server shut down complete.")
}