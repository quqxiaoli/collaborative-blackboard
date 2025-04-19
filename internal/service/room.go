package service

import (
	"context"
	"crypto/rand"
	"fmt"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/sirupsen/logrus"
	// "time" // 暂时不需要 time
)

// RoomService 负责房间管理相关的业务逻辑。
type RoomService struct {
	roomRepo repository.RoomRepository
	// stateRepo repository.StateRepository // 如果创建房间时需要初始化 Redis 状态，则注入 stateRepo
}

// NewRoomService 创建 RoomService 实例。
func NewRoomService(roomRepo repository.RoomRepository /*, stateRepo repository.StateRepository*/) *RoomService {
	return &RoomService{
		roomRepo: roomRepo,
		// stateRepo: stateRepo,
	}
}

// CreateRoom 创建一个新房间。
func (s *RoomService) CreateRoom(ctx context.Context, creatorID uint) (*domain.Room, error) {
	logCtx := logrus.WithField("creator_id", creatorID)

	// 1. 生成唯一的邀请码
	inviteCode, err := s.generateUniqueInviteCode(ctx)
	if err != nil {
		logCtx.WithError(err).Error("Failed to generate unique invite code")
		return nil, ErrInternalServer // 邀请码生成失败是内部问题
	}
	logCtx = logCtx.WithField("invite_code", inviteCode)

	// 2. 创建房间对象
	room := &domain.Room{
		CreatorID:  creatorID,
		InviteCode: inviteCode,
		// LastActive: time.Now(), // GORM 会自动处理 CreatedAt, UpdatedAt
	}

	// 3. 保存房间 (调用 Repository 接口)
	err = s.roomRepo.Save(ctx, room)
	if err != nil {
		logCtx.WithError(err).Error("Failed to save new room to database")
		return nil, ErrInternalServer
	}
	logCtx = logCtx.WithField("room_id", room.ID)

	// 4. [可选] 初始化房间在 Redis 中的状态 (如果需要)
	// if s.stateRepo != nil {
	//     // s.stateRepo.InitializeRoomState(ctx, room.ID) // 需要在 StateRepository 定义此方法
	// }

	logCtx.Info("Room created successfully")
	return room, nil
}

// JoinRoom 处理用户通过邀请码加入房间。
func (s *RoomService) JoinRoom(ctx context.Context, userID uint, inviteCode string) (*domain.Room, error) {
	logCtx := logrus.WithFields(logrus.Fields{"user_id": userID, "invite_code": inviteCode})

	// 1. 根据邀请码查找房间 (调用 Repository 接口)
	room, err := s.roomRepo.FindByInviteCode(ctx, inviteCode)
	if err != nil {
		// 假设 repo 在找不到时返回可识别错误
		// if errors.Is(err, repository.ErrRoomNotFound) { // 理想情况
		if err != nil { // 简化处理
			logCtx.WithError(err).Warn("Failed to find room by invite code")
			return nil, ErrInvalidInviteCode // 返回业务错误：无效邀请码
		}
	}
    if room == nil {
        logCtx.Warn("Failed to find room by invite code (repo returned nil room without error)")
        return nil, ErrInvalidInviteCode
    }


	logCtx = logCtx.WithField("room_id", room.ID)

	// 2. [可选] 检查用户权限或成员资格 (如果需要)

	// 3. [可选] 更新房间的 LastActive 时间戳 (可以通过 Save 实现)
	// room.LastActive = time.Now()
	// if err := s.roomRepo.Save(ctx, room); err != nil { ... }

	logCtx.Info("User joined room successfully")
	return room, nil
}

// --- 私有辅助函数 ---

// generateUniqueInviteCode 生成唯一的邀请码 (逻辑与原 handlers/room.go 中类似，但调用 Repo 接口)
func (s *RoomService) generateUniqueInviteCode(ctx context.Context) (string, error) {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const codeLength = 6
	const maxAttempts = 10

	b := make([]byte, codeLength)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("failed to generate random bytes: %w", err)
		}
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)]
		}
		code := string(b)

		// 检查邀请码是否存在 (调用 Repository 接口)
		exists, err := s.roomRepo.IsInviteCodeExists(ctx, code)
		if err != nil {
			// 数据库查询错误
			logrus.WithError(err).WithField("invite_code", code).Error("Database error checking invite code uniqueness")
			return "", fmt.Errorf("database error checking invite code: %w", err)
		}
		if !exists {
			// 未找到，此 code 可用
			return code, nil
		}
		// code 已存在，重试
		logrus.WithField("invite_code", code).Warnf("Generated invite code already exists, retrying (attempt %d)...", attempt+1)
	}

	return "", fmt.Errorf("failed to generate a unique invite code after %d attempts", maxAttempts)
}