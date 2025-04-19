package repository

import (
	"context"
	"time"
	// 使用正确的模块路径替换 "collaborative-blackboard"
	"collaborative-blackboard/internal/domain"
)

// StateRepository 定义了与房间实时状态相关的操作，通常由 Redis 实现。
type StateRepository interface {
	// === Board State ===

	// GetBoardState 获取指定房间当前的完整白板状态。
	GetBoardState(ctx context.Context, roomID uint) (domain.BoardState, error)

	// ApplyActionToState 将单个 Action 应用到 Redis 中的实时状态。
	// 这会处理 HSet (draw) 或 HDel (erase)。
	ApplyActionToState(ctx context.Context, roomID uint, action domain.Action) error

	// === Versioning & Counters ===

	// GetCurrentVersion 获取房间当前的最新版本号。
	GetCurrentVersion(ctx context.Context, roomID uint) (uint, error)

	// IncrementVersion 原子地增加房间的版本号并返回新版本。
	IncrementVersion(ctx context.Context, roomID uint) (uint, error) // 改为返回 uint

	// IncrementOpCount 原子地增加房间的操作计数器。
	IncrementOpCount(ctx context.Context, roomID uint) error

	// ResetOpCount 重置房间的操作计数器（通常在生成快照后调用）。
	ResetOpCount(ctx context.Context, roomID uint) error

	// === Action History for OT ===

	// GetRecentActions 获取存储在 Redis 中的最近操作记录（用于 OT）。
	GetRecentActions(ctx context.Context, roomID uint, limit int) ([]domain.Action, error)

	// PushActionToHistory 将一个 Action 添加到 Redis 的历史记录队列，并保持队列长度。
	PushActionToHistory(ctx context.Context, roomID uint, action domain.Action) error

	// === Snapshot Caching ===

	// GetSnapshotCache 尝试从 Redis 缓存中获取快照。
	GetSnapshotCache(ctx context.Context, roomID uint) (*domain.Snapshot, error)

	// SetSnapshotCache 将快照存入 Redis 缓存。
	SetSnapshotCache(ctx context.Context, roomID uint, snapshot *domain.Snapshot) error

    // === Rate Limiting ===
    // CheckRateLimit 检查给定 key 的请求频率是否超限，并递增计数。
	// 返回 true 如果超限，false 如果未超限。
    CheckRateLimit(ctx context.Context, key string, limit int, duration time.Duration) (bool, error)

}