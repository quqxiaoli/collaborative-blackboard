package service

import (
	"context"
	"encoding/json"
	//"errors"
	"fmt"
	"time"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/sirupsen/logrus"
	// "collaborative-blackboard/internal/ot"
)

// CollaborationService 负责处理实时的白板协作逻辑。
type CollaborationService struct {
	actionRepo   repository.ActionRepository   // 用于计划的后台持久化
	stateRepo    repository.StateRepository    // 读写 Redis 状态/缓存/历史/PubSub
	//snapshotRepo repository.SnapshotRepository // 读写 DB 快照 
	//roomRepo     repository.RoomRepository     // 获取房间信息 
	//快照逻辑移到和DB持久化已移出，不需要这两个Repo了
	// pubsubRepo repository.PubSubRepository // 如果将发布进一步抽象
	// otTransformer *ot.Transformer
}

// NewCollaborationService 创建 CollaborationService 实例。(修正签名)
func NewCollaborationService(
	actionRepo repository.ActionRepository,
	stateRepo repository.StateRepository,
	//snapshotRepo repository.SnapshotRepository, // *** 恢复参数 ***
	//roomRepo repository.RoomRepository,     // *** 恢复参数 ***
	// pubsubRepo repository.PubSubRepository,
) *CollaborationService {
	// 添加 nil 检查
	if actionRepo == nil || stateRepo == nil  {
		panic("All repositories must be non-nil for CollaborationService")
	}
	return &CollaborationService{
		actionRepo:   actionRepo,
		stateRepo:    stateRepo,
		//snapshotRepo: snapshotRepo, // *** 赋值 ***
		//roomRepo:     roomRepo,     // *** 赋值 ***
		// pubsubRepo: pubsubRepo,
	}
}

// ProcessIncomingAction 处理从客户端 WebSocket 接收到的操作。(修正方法调用)
// 返回处理后的 Action（可能是 noop）以及是否需要广播和持久化。
func (s *CollaborationService) ProcessIncomingAction(ctx context.Context, roomID uint, userID uint, drawDataJSON []byte) (*domain.Action, bool, error) {
	logCtx := logrus.WithFields(logrus.Fields{"room_id": roomID, "user_id": userID})

	// 1. 解析输入数据
	var drawData domain.DrawData
	if err := json.Unmarshal(drawDataJSON, &drawData); err != nil {
		logCtx.WithError(err).Warn("Failed to unmarshal draw data from client")
		return nil, false, ErrInvalidAction // 返回业务错误
	}

	// 2. 获取新版本号
	newVersionInt, err := s.stateRepo.IncrementVersion(ctx, roomID)
	if err != nil {
		logCtx.WithError(err).Error("Failed to increment version in Redis")
		return nil, false, ErrInternalServer
	}
	newVersion := newVersionInt // 类型转换
	logCtx = logCtx.WithField("version", newVersion)

	// 3. 创建 Action 对象
	action := &domain.Action{ // 使用指针
		RoomID:     roomID,
		UserID:     userID,
		ActionType: "draw", // TODO: 根据 drawData 判断 ActionType (e.g., color vs empty/background color for erase)
		Timestamp:  time.Now().UTC(),
		Version:    newVersion,
	}
	if err := action.SetData(drawData); err != nil {
		logCtx.WithError(err).Error("Failed to set action data")
		return nil, false, ErrInternalServer
	}

	// 4. [可选] 应用 OT/转换
	//    目前 basicTransform 只是直接返回，实际应用需要替换
	finalAction := s.basicTransform(ctx, *action, logCtx) // 传递值

	// 5. 如果操作未被转换为 noop
	shouldBroadcastAndPersist := false
	if finalAction.ActionType != "noop" {
		logCtx.Debug("Action not noop, applying state update")
		shouldBroadcastAndPersist = true // 标记需要广播和持久化

		// 5a. 更新 Redis 状态 (调用 Repository 接口 - 使用 ApplyActionToState)
		if err := s.stateRepo.ApplyActionToState(ctx, roomID, finalAction); err != nil { // *** 修正方法名 ***
			logCtx.WithError(err).Error("Failed to update board state in repository")
			// 关键步骤失败，可以考虑是否仍要广播（可能导致不一致）或返回错误
			// 暂时返回错误，让调用者 (Hub) 决定如何处理
			return nil, false, fmt.Errorf("failed to update board state: %w", err)
		} else {
			logCtx.Debug("Successfully updated board state in repository")
		}

		// 5b. 记录 Action 到 Redis 历史
		if err := s.stateRepo.PushActionToHistory(ctx, roomID, finalAction); err != nil {
			logCtx.WithError(err).Error("Failed to push action to Redis history")
			// 记录错误，但不中断流程
		}

		// 5c. 递增操作计数
		if err := s.stateRepo.IncrementOpCount(ctx, roomID); err != nil {
			logCtx.WithError(err).Error("Failed to increment op count in Redis")
			// 记录错误
		}

		// 5d. **直接发布** Action 到 Redis Pub/Sub (用于广播)
		if err := s.stateRepo.PublishAction(ctx, roomID, finalAction); err != nil {
			logCtx.WithError(err).Error("Failed to publish action via repository")
			// 发布失败是严重问题，记录下来，可能需要监控
		} else {
			logCtx.Debug("Action published successfully via repository")
		}
	} else {
			logCtx.Debug("Action transformed to noop")
	}

	// 返回最终的 Action (可能是 noop) 和是否需要广播/持久化的标志
	return &finalAction, shouldBroadcastAndPersist, nil
}

// basicTransform ... (保持不变)
func (s *CollaborationService) basicTransform(ctx context.Context, currentAction domain.Action, logCtx *logrus.Entry) domain.Action {
	logCtx.Debug("Skipping complex OT, returning original action for now")
	return currentAction
	/* ... (OT 逻辑骨架不变) ... */
}

