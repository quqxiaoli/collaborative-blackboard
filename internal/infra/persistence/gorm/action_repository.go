package gormpersistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	// 使用正确的 Domain 模型路径和 Repository 接口路径
	"collaborative-blackboard/internal/domain"
)

// GormActionRepository 是 ActionRepository 接口的 GORM 实现
type GormActionRepository struct {
	db *gorm.DB
}

// NewGormActionRepository 创建 GormActionRepository 实例
func NewGormActionRepository(db *gorm.DB) *GormActionRepository {
	if db == nil {
		panic("database connection cannot be nil for GormActionRepository")
	}
	return &GormActionRepository{db: db}
}

// SaveBatch 实现批量保存用户操作记录
// GORM 的 Create 方法支持传入切片进行批量插入
func (r *GormActionRepository) SaveBatch(ctx context.Context, actions []domain.Action) error {
	if len(actions) == 0 {
		return nil // 如果没有操作需要保存，直接返回
	}

	// 使用 Create 方法进行批量插入
	// 注意：如果 actions 切片很大，可能需要分批插入以避免数据库限制或性能问题
	// GORM 默认会处理一些批量插入的优化，但具体行为可能依赖数据库驱动
	err := r.db.WithContext(ctx).Create(&actions).Error
	if err != nil {
		// 批量插入的错误处理可能比较复杂，错误可能只涉及部分记录
		// 这里简单地包装并返回整个错误
		return fmt.Errorf("gorm: failed to save action batch (size %d): %w", len(actions), err)
	}
	// 批量保存成功
	return nil
}

// GetCountSince 实现获取指定房间在某个时间点之后的操作记录数量
func (r *GormActionRepository) GetCountSince(ctx context.Context, roomID uint, timestamp time.Time) (int64, error) {
	var count int64

	// 构建查询条件
	query := r.db.WithContext(ctx).Model(&domain.Action{}).Where("room_id = ?", roomID)

	// 如果时间戳不是零值，则添加时间条件
	if !timestamp.IsZero() {
		query = query.Where("created_at > ?", timestamp) // 或者 timestamp 字段，取决于你的模型定义
	}

	// 执行 Count 查询
	err := query.Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("gorm: failed to count actions for room %d since %v: %w", roomID, timestamp, err)
	}

	return count, nil
}

// GetActionsForSnapshot (如果需要实现)
// func (r *GormActionRepository) GetActionsForSnapshot(ctx context.Context, roomID uint, fromVersion uint, toVersion uint) ([]domain.Action, error) {
//  var actions []domain.Action
//  err := r.db.WithContext(ctx).
//      Where("room_id = ? AND version > ? AND version <= ?", roomID, fromVersion, toVersion).
//      Order("version asc"). // 按版本排序很重要
//      Find(&actions).Error
//  if err != nil {
//      return nil, fmt.Errorf("gorm: failed to get actions for snapshot (room %d, v%d-%d): %w", roomID, fromVersion, toVersion, err)
//  }
//  return actions, nil
// }