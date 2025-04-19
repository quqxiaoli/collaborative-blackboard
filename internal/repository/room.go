package repository

import (
	"context"
	// 使用正确的模块路径替换 "collaborative-blackboard"
	"collaborative-blackboard/internal/domain"
)

// RoomRepository 定义了房间数据的存储和检索操作。
type RoomRepository interface {
	// FindByID 根据房间 ID 查找房间。
	// 如果房间不存在，应返回明确的错误，例如 domain.ErrRoomNotFound。
	FindByID(ctx context.Context, id uint) (*domain.Room, error)

	// FindByInviteCode 根据邀请码查找房间。
	// 如果房间不存在，应返回明确的错误，例如 domain.ErrRoomNotFound。
	FindByInviteCode(ctx context.Context, code string) (*domain.Room, error)

	// Save 保存房间信息。
	// 如果房间已存在 (基于 ID)，则更新；否则创建新房间。
	// 返回保存后的 Room 对象或错误。
	Save(ctx context.Context, room *domain.Room) error

	// FindAllActive 根据一组房间 ID 查询房间列表。
	// 主要用于快照任务，查找当前活跃的房间信息。
	FindAllActive(ctx context.Context, roomIDs []uint) ([]domain.Room, error)

	// IsInviteCodeExists 检查邀请码是否已存在。
	IsInviteCodeExists(ctx context.Context, code string) (bool, error)
}

// // 可以在 domain 包或 repository 包中定义标准的错误类型
// var ErrRoomNotFound = errors.New("room not found")