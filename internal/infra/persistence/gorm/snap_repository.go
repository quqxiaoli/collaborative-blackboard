package gormpersistence

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	// 使用正确的 Domain 模型路径和 Repository 接口路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"
)

// GormSnapshotRepository 是 SnapshotRepository 接口的 GORM 实现
type GormSnapshotRepository struct {
	db *gorm.DB
}

// NewGormSnapshotRepository 创建 GormSnapshotRepository 实例
func NewGormSnapshotRepository(db *gorm.DB) *GormSnapshotRepository {
	if db == nil {
		panic("database connection cannot be nil for GormSnapshotRepository")
	}
	return &GormSnapshotRepository{db: db}
}

// GetLatestSnapshot 实现获取指定房间的最新快照记录
// 通过按创建时间降序排序并取第一个实现
func (r *GormSnapshotRepository) GetLatestSnapshot(ctx context.Context, roomID uint) (*domain.Snapshot, error) {
	var snapshot domain.Snapshot
	// 查询条件: room_id = ?
	// 排序条件: created_at DESC
	// 使用 First() 获取第一条记录
	err := r.db.WithContext(ctx).
		Where("room_id = ?", roomID).
		Order("created_at DESC").
		First(&snapshot).Error

	if err != nil {
		// 检查是否是记录未找到错误
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 映射为定义的仓库层错误
			return nil, repository.ErrSnapshotNotFound // 使用 repository.ErrNotFound 或专门的 ErrSnapshotNotFound
		}
		// 其他数据库错误
		return nil, fmt.Errorf("gorm: failed to get latest snapshot for room %d: %w", roomID, err)
	}
	// 找到快照
	return &snapshot, nil
}

// SaveSnapshot 实现将快照记录保存到数据库
// 由于快照通常是只写的（或者说每次生成都是新的），使用 Create 更合适
func (r *GormSnapshotRepository) SaveSnapshot(ctx context.Context, snapshot *domain.Snapshot) error {
	// 使用 Create 方法插入新的快照记录
	// 假设 Snapshot 的 ID 是自增的，或者在创建前是零值
	err := r.db.WithContext(ctx).Create(snapshot).Error
	if err != nil {
		// 包装错误并返回
		return fmt.Errorf("gorm: failed to save snapshot (room %d, version %d): %w", snapshot.RoomID, snapshot.Version, err)
	}
	// 保存成功
	return nil
}