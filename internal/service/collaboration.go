package service

import (
	"context"
	"encoding/json"
	"time"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/sirupsen/logrus"
    // "collaborative-blackboard/internal/ot" // 如果有单独的 OT 包
)

// CollaborationService 负责处理实时的白板协作逻辑。
type CollaborationService struct {
	actionRepo repository.ActionRepository // 用于持久化 Action
	stateRepo  repository.StateRepository  // 用于读写 Redis 实时状态
	// snapshotRepo repository.SnapshotRepository // 快照持久化移到 SnapshotService
	// pubsubRepo repository.PubSubRepository     // 如果抽象了 PubSub
	// otTransformer *ot.Transformer            // 如果有单独的 OT 实现
}

// NewCollaborationService 创建 CollaborationService 实例。
func NewCollaborationService(
	actionRepo repository.ActionRepository,
	stateRepo repository.StateRepository,
	// snapshotRepo repository.SnapshotRepository,
	// pubsubRepo repository.PubSubRepository,
) *CollaborationService {
	return &CollaborationService{
		actionRepo: actionRepo,
		stateRepo:  stateRepo,
		// snapshotRepo: snapshotRepo,
		// pubsubRepo: pubsubRepo,
	}
}

// ProcessIncomingAction 处理从客户端 WebSocket 接收到的操作。
// 返回处理后的 Action（可能是 noop）以及是否需要广播和持久化。
func (s *CollaborationService) ProcessIncomingAction(ctx context.Context, roomID uint, userID uint, drawDataJSON []byte) (*domain.Action, bool, error) {
	logCtx := logrus.WithFields(logrus.Fields{"room_id": roomID, "user_id": userID})

	// 1. 解析输入数据
	var drawData domain.DrawData
	if err := json.Unmarshal(drawDataJSON, &drawData); err != nil {
		logCtx.WithError(err).Warn("Failed to unmarshal draw data from client")
		return nil, false, ErrInvalidAction // 返回特定业务错误
	}

	// 2. 获取新版本号 (调用 Repository 接口)
	// 注意：这里存在一个潜在的竞争条件，获取版本号和应用更新不是原子操作。
	// 更健壮的方式是让 ApplyActionToState 方法原子地处理版本和状态更新。
	// 暂时保留现有逻辑。
	newVersion, err := s.stateRepo.IncrementVersion(ctx, roomID)
	if err != nil {
		logCtx.WithError(err).Error("Failed to increment version in Redis")
		return nil, false, ErrInternalServer
	}
	logCtx = logCtx.WithField("version", newVersion)

	// 3. 创建 Action 对象
	action := domain.Action{
		RoomID:     roomID,
		UserID:     userID,
		ActionType: "draw", // 假设都是 draw，需要根据 drawData 判断是否是 erase?
		Timestamp:  time.Now().UTC(),
		Version:    newVersion,
		// ID 和 CreatedAt 由数据库生成
	}
	if err := action.SetData(drawData); err != nil {
		logCtx.WithError(err).Error("Failed to set action data")
		return nil, false, ErrInternalServer
	}

	// 4. [可选] 应用 OT/转换
	//    当前的 OT 逻辑非常基础，并且依赖于 applyOperationalTransform，
	//    这个函数又依赖于从 Redis 获取历史记录。我们将这个逻辑简化并标记为待改进。
	// TODO: Implement proper Operational Transformation or Conflict Resolution
	finalAction := s.basicTransform(ctx, action, logCtx) // 使用简化的转换逻辑

	// 5. 如果操作未被转换为 noop
	shouldBroadcastAndPersist := false
	if finalAction.ActionType != "noop" {
		logCtx.Debug("Action not noop, applying state update")
		shouldBroadcastAndPersist = true

		// 5a. 更新 Redis 状态 (调用 Repository 接口)
		if err := s.stateRepo.ApplyActionToState(ctx, roomID, finalAction); err != nil {
			logCtx.WithError(err).Error("Failed to apply action to Redis state")
			// 即使状态更新失败，我们可能仍然希望广播和尝试保存 Action，
			// 或者根据策略决定是否回滚/返回错误。暂时只记录错误。
			// return nil, false, ErrInternalServer
		} else {
            logCtx.Debug("Successfully applied action to Redis state")
        }

		// 5b. 记录 Action 到 Redis 历史 (调用 Repository 接口)
		if err := s.stateRepo.PushActionToHistory(ctx, roomID, finalAction); err != nil {
			logCtx.WithError(err).Error("Failed to push action to Redis history")
			// 记录错误，但不一定是致命的
		}

		// 5c. 递增操作计数 (调用 Repository 接口)
		if err := s.stateRepo.IncrementOpCount(ctx, roomID); err != nil {
			logCtx.WithError(err).Error("Failed to increment op count in Redis")
			// 记录错误
		}

		// 5d & 5e: 持久化和广播的触发将在调用此函数之后进行（例如在 Hub 中）

	} else {
		logCtx.Debug("Action transformed to noop")
	}

	// 返回最终的 Action (可能是 noop) 和是否需要广播/持久化的标志
	return &finalAction, shouldBroadcastAndPersist, nil
}


// basicTransform 实现了一个极其简化的冲突检测 (类似 LWW，但只比较版本)
// 仅作为示例，实际 OT 复杂得多。
func (s *CollaborationService) basicTransform(ctx context.Context, currentAction domain.Action, logCtx *logrus.Entry) domain.Action {
    // 简化逻辑：暂时不与 Redis 中的历史记录比较进行转换
    // 实际 OT 需要实现 transform(action1, action2) -> action1', action2'
    // 这里我们直接返回原始 Action，假设冲突由后续操作自然解决（LWW 效果）
    logCtx.Debug("Skipping complex OT, returning original action for now")
    return currentAction

    /* // 原先 hub.go 中的 OT 逻辑骨架 (需要 stateRepo.GetRecentActions)
	recentActions, err := s.stateRepo.GetRecentActions(ctx, currentAction.RoomID, 10) // 获取最近 10 个
	if err != nil {
		logCtx.WithError(err).Warn("OT: Failed to get recent actions from Redis, skipping transform")
		return currentAction
	}

	transformedAction := currentAction
	for _, historicalAction := range recentActions {
        // 实现真正的 transform 函数调用:
		// transformedAction, _ = transform(transformedAction, historicalAction)
        // 简单的 LWW 检查 (仅示例):
        if transformedAction.ActionType == "noop" {
            break // 如果已经被标记为 noop，停止转换
        }
        if areActionsConflicting(transformedAction, historicalAction) { // 需要实现冲突检测逻辑
            if historicalAction.Version >= transformedAction.Version { // 基于版本号的 LWW
                logCtx.WithFields(logrus.Fields{
                    "current_version": transformedAction.Version,
                    "historical_version": historicalAction.Version,
                    "historical_action_id": historicalAction.ID,
                }).Info("OT: Action becomes noop due to conflict with newer or equal version")
                transformedAction.ActionType = "noop"
				transformedAction.Data = ""
                break
            }
        }
	}
	return transformedAction
    */
}

// --- 其他潜在的协作方法 ---
// func (s *CollaborationService) HandleUserJoin(ctx context.Context, roomID uint, userID uint) error { ... }
// func (s *CollaborationService) HandleUserLeave(ctx context.Context, roomID uint, userID uint) error { ... }