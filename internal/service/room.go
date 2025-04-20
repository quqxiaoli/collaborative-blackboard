package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/sirupsen/logrus"
	// "time" // 暂时不需要
)

// RoomService 负责房间管理相关的业务逻辑。
type RoomService struct {
	roomRepo repository.RoomRepository
	// stateRepo repository.StateRepository // 如果创建房间时需要初始化 Redis 状态
}

// NewRoomService 创建 RoomService 实例。
func NewRoomService(roomRepo repository.RoomRepository /*, stateRepo repository.StateRepository*/) *RoomService {
	if roomRepo == nil {
		panic("RoomRepository cannot be nil for RoomService")
	}
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
		return nil, ErrInternalServer // 邀请码生成失败视为内部错误
	}
	logCtx = logCtx.WithField("invite_code", inviteCode)

	// 2. 创建房间对象
	room := &domain.Room{
		CreatorID:  creatorID,
		InviteCode: inviteCode,
		// GORM 会自动处理 CreatedAt, UpdatedAt
		// LastActive 可以由后续活动更新
	}

	// 3. 保存房间 (调用 Repository 接口)
	err = s.roomRepo.Save(ctx, room)
	if err != nil {
		// 依赖 Repository 返回明确的错误类型
		if errors.Is(err, repository.ErrDuplicateEntry) {
			logCtx.WithError(err).Error("Failed to save new room due to duplicate entry (invite code conflict?)")
			// 理论上不应发生，返回内部错误
			return nil, ErrInternalServer
		} else if isDuplicateEntryErrorString(err) { // 临时后备检查
			logCtx.WithError(err).Error("Failed to save new room due to duplicate entry (invite code conflict?)")
			return nil, ErrInternalServer
		}
		// 其他数据库错误
		logCtx.WithError(err).Error("Failed to save new room to database")
		return nil, ErrInternalServer
	}
	logCtx = logCtx.WithField("room_id", room.ID)

	// 4. [可选] 初始化房间在 Redis 中的状态 ...

	logCtx.Info("Room created successfully")
	return room, nil
}

// JoinRoom 处理用户通过邀请码加入房间。
// 注意：原代码中 JoinRoom 似乎未使用 userID 参数，这里保留但标记为可选。
func (s *RoomService) JoinRoom(ctx context.Context, userID uint, inviteCode string) (*domain.Room, error) {
	logCtx := logrus.WithFields(logrus.Fields{"user_id": userID, "invite_code": inviteCode})

	// 1. 根据邀请码查找房间 (调用 Repository 接口)
	room, err := s.roomRepo.FindByInviteCode(ctx, inviteCode)
	if err != nil {
		// 检查是否是未找到错误
		if errors.Is(err, repository.ErrRoomNotFound) {
			logCtx.WithError(err).Warn("Failed to find room by invite code: Not found")
			return nil, ErrInvalidInviteCode // 返回业务错误：无效邀请码
		}
		// 其他仓库错误
		logCtx.WithError(err).Warn("Failed to find room by invite code: Repository error")
		return nil, ErrInternalServer // 可能是内部错误
	}
	// 防御性检查
	if room == nil {
		logCtx.Warn("Failed to find room by invite code (repo returned nil room without error)")
		return nil, ErrInvalidInviteCode
	}

	logCtx = logCtx.WithField("room_id", room.ID)

	// 2. [可选] 检查用户权限或成员资格 ...
	// 3. [可选] 更新房间的 LastActive 时间戳 ...

	logCtx.Info("User joined room successfully")
	return room, nil
}

// FindRoomByID (添加一个简单的查找方法，供 WebSocket Handler 使用)
// 返回错误以便 Handler 判断
func (s *RoomService) FindRoomByID(ctx context.Context, roomID uint) (*domain.Room, error) {
    logCtx := logrus.WithField("room_id", roomID)
    room, err := s.roomRepo.FindByID(ctx, roomID)
    if err != nil {
        if errors.Is(err, repository.ErrRoomNotFound) {
            logCtx.Warn("FindRoomByID: Room not found")
            return nil, ErrRoomNotFound // 返回业务错误
        }
        logCtx.WithError(err).Error("FindRoomByID: Repository error")
        return nil, ErrInternalServer
    }
    if room == nil { // 防御
        logCtx.Warn("FindRoomByID: Repository returned nil room without error")
        return nil, ErrRoomNotFound
    }
    return room, nil
}


// --- 私有辅助函数 ---

// generateUniqueInviteCode 生成唯一的邀请码
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

		// 检查邀请码是否存在 (调用 Repository 接口 - ExistsByInviteCode)
		exists, err := s.roomRepo.IsInviteCodeExists(ctx, code) // *** 确认使用此方法名 ***
		if err != nil {
			logrus.WithError(err).WithField("invite_code", code).Error("Database error checking invite code uniqueness")
			// 返回包装后的错误，而不是直接返回内部错误给调用者
			return "", fmt.Errorf("database error checking invite code: %w", err)
		}
		if !exists {
			// 找到唯一码
			logrus.WithField("invite_code", code).Debugf("Generated unique invite code after %d attempt(s).", attempt+1)
			return code, nil
		}
		// code 已存在，重试
		logrus.WithField("invite_code", code).Warnf("Generated invite code already exists, retrying (attempt %d)...", attempt+1)
	}
	// 达到最大尝试次数
	logrus.Errorf("Failed to generate a unique invite code after %d attempts", maxAttempts)
	return "", fmt.Errorf("failed to generate a unique invite code after %d attempts", maxAttempts)
}

// isDuplicateEntryErrorString (如果需要，从 auth.go 复制或移到共享位置)
// func isDuplicateEntryErrorString(err error) bool { ... }