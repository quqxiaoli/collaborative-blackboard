package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"

	// 导入内部包
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"
	"collaborative-blackboard/internal/tasks"
)

// ActionPersistenceHandler 处理 Action 持久化任务
type ActionPersistenceHandler struct {
	actionRepo repository.ActionRepository
}

// NewActionPersistenceHandler 创建 Handler 实例
func NewActionPersistenceHandler(actionRepo repository.ActionRepository) *ActionPersistenceHandler {
	return &ActionPersistenceHandler{actionRepo: actionRepo}
}

// ProcessTask 实现 asynq.Handler 接口
func (h *ActionPersistenceHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	// *** 修正：尝试从 Task 对象获取信息，Context 用于重试计数 ***
    // 根据 Asynq v0.25.x 附近的行为，ID 和 Queue 通常通过 Task 对象本身的方法或字段获取
    // Type() 和 Payload() 是标准方法
    taskID := "" // 初始化为空
    taskQueue := "default" // 初始化为默认

    // 尝试获取 ID 和 Queue - Asynq v0.25.1 的文档似乎不明确，我们尝试最可能的方式
    // 通常 ID 可以通过 ResultWriter 获取
    if rw := t.ResultWriter(); rw != nil {
         taskID = rw.TaskID()
    }
	currentRetry, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	logCtx := logrus.WithFields(logrus.Fields{
		"task_id":   taskID,
		"task_type": t.Type(),
		"queue":     taskQueue,
        "retry":     currentRetry,
        "max_retry": maxRetry,
	})
	logCtx.Info("Processing action persistence task...")

	var payload tasks.ActionPersistencePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logCtx.WithError(err).Error("Failed to unmarshal task payload")
		return fmt.Errorf("failed to unmarshal payload: %v: %w", err, asynq.SkipRetry)
	}

	actionsToSave := []domain.Action{payload.Action}
	if err := h.actionRepo.SaveBatch(ctx, actionsToSave); err != nil {
		logCtx.WithError(err).Errorf("Failed to save action batch (size 1) for action version %d", payload.Action.Version)
		return fmt.Errorf("failed to save action %d: %w", payload.Action.Version, err)
	}

	logCtx.WithField("action_version", payload.Action.Version).Info("Action persistence task processed successfully")
	return nil
}