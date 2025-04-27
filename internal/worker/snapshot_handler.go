package worker

import (
	"context"
	"fmt"
	"sync"

	// "encoding/json" // 不需要解析 payload
	"time" // 需要 time

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"

	// 导入依赖
	"collaborative-blackboard/internal/hub"     // 需要 Hub 获取活跃房间
	"collaborative-blackboard/internal/service" // 需要 SnapshotService
	// "collaborative-blackboard/internal/tasks" // 暂时不需要 tasks 包的 payload
	"collaborative-blackboard/internal/repository"
)

// SnapshotCheckHandler 处理周期性的快照检查任务
type SnapshotCheckHandler struct {
	hub             *hub.Hub             // 用于获取活跃房间 ID
	snapshotService *service.SnapshotService // 用于检查和生成快照
	stateRepo       repository.StateRepository // <--- 新增 StateRepository 依赖
}

// NewSnapshotCheckHandler 创建 Handler 实例
func NewSnapshotCheckHandler(hub *hub.Hub, snapshotService *service.SnapshotService,stateRepo repository.StateRepository,) *SnapshotCheckHandler {
	// 添加 nil 检查
    if hub == nil { panic("Hub cannot be nil for SnapshotCheckHandler") }
    if snapshotService == nil { panic("SnapshotService cannot be nil for SnapshotCheckHandler") }
	if stateRepo == nil { panic("StateRepository cannot be nil for SnapshotCheckHandler") }
	return &SnapshotCheckHandler{
		hub:             hub,
		snapshotService: snapshotService,
		stateRepo:       stateRepo,
	}
}

// ProcessTask 实现 asynq.Handler 接口
func (h *SnapshotCheckHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
    taskID := t.ResultWriter().TaskID()
	logCtx := logrus.WithFields(logrus.Fields{
		"task_id":   taskID,
		"task_type": t.Type(),
		"queue":     "default", // 假设在 default 队列
	})
	logCtx.Info("Processing periodic snapshot check task...")

	// 1. 获取当前活跃的房间 ID 列表
    // Hub 需要提供一个获取活跃房间 ID 的方法
	activeRoomIDs := h.hub.GetActiveRoomIDs() // 假设 Hub 有此方法
	if len(activeRoomIDs) == 0 {
		logCtx.Info("No active rooms found, skipping snapshot check.")
		return nil // 没有活跃房间，任务成功完成
	}
	logCtx.Infof("Found %d active rooms to check.", len(activeRoomIDs))

	// 2. 遍历活跃房间，检查并生成快照
	var wg sync.WaitGroup // 用于等待所有房间检查完成 (可选)
    errors := make([]error, 0) // 收集错误
    errMu := sync.Mutex{} // 保护 errors slice

	for _, roomID := range activeRoomIDs {
		wg.Add(1)
		go func(rID uint) { // 为每个房间启动一个 goroutine 进行检查
			defer wg.Done()
			roomLogCtx := logCtx.WithField("room_id", rID)

			checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second) // 为每个房间检查设置超时
            defer cancel()

			// --- 从 Redis 获取上次快照时间 ---
			lastTime, err := h.stateRepo.GetLastSnapshotTime(checkCtx, rID)
            if err != nil {
                // 获取失败，记录错误但可能继续尝试生成（当作首次）
                roomLogCtx.WithError(err).Error("Failed to get last snapshot time from repository")
                lastTime = time.Time{} // 使用零值时间
            }
            // --- 获取结束 ---

			// b. 调用 Service 检查并生成快照 
			newLastTime, err := h.snapshotService.CheckAndGenerateSnapshot(checkCtx, rID, lastTime)

			if err != nil {
                roomLogCtx.WithError(err).Error("Snapshot check/generation failed for room")
                // 记录错误，但不让整个周期任务失败
                errMu.Lock()
                errors = append(errors, fmt.Errorf("room %d: %w", rID, err))
                errMu.Unlock()
			} else if !newLastTime.Equal(lastTime) { // 如果时间戳更新了，说明生成了快照
                roomLogCtx.Info("Snapshot generated or check successful, updating last snapshot time.")
				// ---  将新时间戳存回 Redis ---
                // 设置一个合理的 TTL，例如 7 天
                ttl := 7 * 24 * time.Hour
				if err := h.stateRepo.SetLastSnapshotTime(checkCtx, rID, newLastTime, ttl); err != nil {
                    // 设置失败，只记录警告
                    roomLogCtx.WithError(err).Warn("Failed to set last snapshot time in repository")
                }
                // --- 设置结束 ---
			} else {
                 roomLogCtx.Debug("Snapshot check complete, no generation needed.")
            }
		}(roomID)
	}

	wg.Wait() // 等待所有房间检查完成

    // 检查是否有错误发生
    if len(errors) > 0 {
         // 返回一个聚合错误，Asynq 可能会重试整个周期任务
         // 或者只记录错误，返回 nil 表示周期检查完成（即使部分房间失败）
         // 我们选择后者，避免因单个房间问题导致所有房间重试
         logCtx.Errorf("Snapshot check completed with %d errors for some rooms.", len(errors))
         // for _, e := range errors { logCtx.Error(e) } // 可以记录详细错误
         return nil // 认为周期任务本身是完成的
    }


	logCtx.Info("Periodic snapshot check task completed successfully.")
	return nil // 任务成功
}