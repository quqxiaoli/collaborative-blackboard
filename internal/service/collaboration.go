package service

import (
	"context"
	"encoding/json"

	//"errors"
	//"fmt"
	"time"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/dto"
	"collaborative-blackboard/internal/repository"

	"github.com/gin-gonic/gin/binding"
	"github.com/sirupsen/logrus"
	// "collaborative-blackboard/internal/ot"
)

// CollaborationService 负责处理实时的白板协作逻辑。
type CollaborationService struct {
	actionRepo repository.ActionRepository // 用于计划的后台持久化
	stateRepo  repository.StateRepository  // 读写 Redis 状态/缓存/历史/PubSub
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
	if actionRepo == nil || stateRepo == nil {
		panic("All repositories must be non-nil for CollaborationService")
	}
	return &CollaborationService{
		actionRepo: actionRepo,
		stateRepo:  stateRepo,
		//snapshotRepo: snapshotRepo, // *** 赋值 ***
		//roomRepo:     roomRepo,     // *** 赋值 ***
		// pubsubRepo: pubsubRepo,
	}
}

// ProcessIncomingAction 处理从客户端 WebSocket 接收到的操作。(修正方法调用)
// 返回处理后的 Action（可能是 noop）以及是否需要广播和持久化。
func (s *CollaborationService) ProcessIncomingAction(ctx context.Context, roomID uint, userID uint, drawDataJSON []byte) (*domain.Action, bool, error) {
	logCtx := logrus.WithFields(logrus.Fields{"room_id": roomID, "user_id": userID})

	// 1. 解析输入 drawDataJSON 为 IncomingAction DTO
	var incomingAction dto.IncomingAction
	if err := json.Unmarshal(drawDataJSON, &incomingAction); err != nil {
		logCtx.WithError(err).Warn("Failed to unmarshal draw data from client")
		return nil, false, ErrInvalidAction // 返回业务错误
	}
	if err := binding.Validator.ValidateStruct(incomingAction); err != nil {
		logCtx.WithError(err).Warn("Incoming action DTO validation failed")
		return nil, false, ErrInvalidAction
	}
	if incomingAction.Type != "draw" && incomingAction.Type != "erase" {
		logCtx.Warnf("Received unsupported action type in DTO: %s", incomingAction.Type)
		return nil, false, ErrInvalidAction
	}

	// 2. 从 DTO 创建 DrawData (用于原子操作)
	//    DTO 已经包含了 x, y, color
	drawData := domain.DrawData{
		X:     incomingAction.X,
		Y:     incomingAction.Y,
		Color: incomingAction.Color,
	}
	// 2. 获取新版本号
	newVersionInt, err := s.stateRepo.IncrementVersion(ctx, roomID)
	if err != nil {
		logCtx.WithError(err).Error("Failed to increment version in Redis")
		return nil, false, ErrInternalServer
	}
	newVersion := newVersionInt // 类型转换
	logCtx = logCtx.WithField("version", newVersion)

	// 3. 原子化应用状态并获取新版本 (LWW 方案)
	//    调用 Repository 方法，传入 DTO 中的 ActionType 和 drawData
	newVersion, err = s.stateRepo.ApplyActionDataAndIncrementVersionAtomically(ctx, roomID, incomingAction.Type, drawData)
	if err != nil {
		logCtx.WithError(err).Error("Failed to apply action data and increment version atomically")
		return nil, false, ErrInternalServer
	}
	logCtx = logCtx.WithField("version", newVersion)

	// 4. **创建** domain.Action 对象 (用于广播和持久化)
	//    现在我们需要根据 DTO 和服务器生成的信息来创建领域对象
	action := &domain.Action{
		RoomID:     roomID,
		UserID:     userID,
		ActionType: incomingAction.Type, // 来自 DTO
		Timestamp:  time.Now().UTC(),    // 服务器时间
		Version:    newVersion,          // 原子操作返回的新版本
		// Data 字段需要根据 drawData 设置
	}
	// 将 drawData 设置到 Action 的 Data 字段 (序列化为 JSON 字符串)
	if err := action.SetData(drawData); err != nil {
		logCtx.WithError(err).Error("Failed to set action data")
		return nil, false, ErrInternalServer
	}

	// 5. 将处理后的 Action 推送到后续流程
	shouldBroadcastAndPersist := true // 只要原子操作成功，就应该广播和持久化

	// 5a. 记录 Action 到 Redis 历史 (使用 action 对象)
	if err := s.stateRepo.PushActionToHistory(ctx, roomID, *action); err != nil {
		logCtx.WithError(err).Error("Failed to push action to Redis history (non-critical)")
		// 记录错误，但不中断流程
	}

	// 5b. 递增操作计数 (可选，如果仍需跟踪总操作数)
	if err := s.stateRepo.IncrementOpCount(ctx, roomID); err != nil {
		logCtx.WithError(err).Error("Failed to increment op count in Redis (non-critical)")
		// 记录错误
	}

	// 5c. 通过 Redis Pub/Sub 发布 Action (使用 action 对象)
	if err := s.stateRepo.PublishAction(ctx, roomID, *action); err != nil {
		logCtx.WithError(err).Error("Failed to publish action via repository")
		// 发布失败是较严重问题，但可能仍需尝试持久化，或者在这里返回错误？
		// 暂时只记录错误
	} else {
		logCtx.Debug("Action published successfully via repository")
	}

	// 6. 返回处理后的 Action 和标记，以触发 Hub 中的持久化任务
	return action, shouldBroadcastAndPersist, nil
}
