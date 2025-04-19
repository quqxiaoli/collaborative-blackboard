package repository

import (
	"context"
	"time"
	// 使用正确的模块路径替换 "collaborative-blackboard"
	"collaborative-blackboard/internal/domain"
)

// ActionRepository 定义了用户操作记录的存储和查询。
type ActionRepository interface {
	// SaveBatch 批量保存 Action 记录到持久化存储（如数据库）。
	SaveBatch(ctx context.Context, actions []domain.Action) error

	// GetCountSince 获取指定房间在某个时间点之后的操作记录数量。
	// 用于判断是否需要生成快照。
	GetCountSince(ctx context.Context, roomID uint, timestamp time.Time) (int64, error)

	// GetActionsForSnapshot 获取生成快照所需的操作记录。
	// (可选) 根据快照生成的具体策略决定是否需要。
	// 例如，如果快照是基于应用所有 Action 生成的，可能需要此方法。
	// GetActionsForSnapshot(ctx context.Context, roomID uint, sinceVersion uint) ([]domain.Action, error)
}