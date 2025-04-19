package repository

import (
	"context"
	// 使用正确的模块路径替换 "collaborative-blackboard"
	"collaborative-blackboard/internal/domain"
)

// SnapshotRepository 定义了快照数据在持久化存储（数据库）中的操作。
type SnapshotRepository interface {
	// GetLatestSnapshot 获取指定房间的最新快照记录。
	// 如果没有快照，可以返回 nil 和 nil 错误，或者一个特定的错误类型。
	GetLatestSnapshot(ctx context.Context, roomID uint) (*domain.Snapshot, error)

	// SaveSnapshot 保存快照记录到数据库。
	SaveSnapshot(ctx context.Context, snapshot *domain.Snapshot) error
}