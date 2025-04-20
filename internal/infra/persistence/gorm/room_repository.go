package gormpersistence

import (
	"context"
	"errors"
	"fmt"
	//"strings" // 用于 isDuplicateEntryError

	"gorm.io/gorm"

	// 使用正确的 Domain 模型路径 (internal/domain) 和 Repository 接口路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"
)

// GormRoomRepository 是 RoomRepository 接口的 GORM 实现
type GormRoomRepository struct {
	db *gorm.DB
}

// NewGormRoomRepository 创建 GormRoomRepository 实例
func NewGormRoomRepository(db *gorm.DB) *GormRoomRepository {
	if db == nil {
		panic("database connection cannot be nil for GormRoomRepository")
	}
	return &GormRoomRepository{db: db}
}

// FindByID 实现根据房间 ID 查找房间
func (r *GormRoomRepository) FindByID(ctx context.Context, id uint) (*domain.Room, error) { // 返回 *domain.Room
	var roomData domain.Room // 使用 domain.Room
	err := r.db.WithContext(ctx).First(&roomData, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrRoomNotFound // 使用定义的错误
		}
		return nil, fmt.Errorf("gorm: find room by id %d: %w", id, err)
	}
	return &roomData, nil
}

// FindByInviteCode 实现根据邀请码查找房间
func (r *GormRoomRepository) FindByInviteCode(ctx context.Context, code string) (*domain.Room, error) { // 返回 *domain.Room
	var roomData domain.Room // 使用 domain.Room
	err := r.db.WithContext(ctx).Where("invite_code = ?", code).First(&roomData).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrRoomNotFound
		}
		return nil, fmt.Errorf("gorm: find room by invite code '%s': %w", code, err)
	}
	return &roomData, nil
}

// Save 实现保存房间信息（创建或更新）
func (r *GormRoomRepository) Save(ctx context.Context, roomData *domain.Room) error { // 参数 *domain.Room
	result := r.db.WithContext(ctx).Save(roomData)
	err := result.Error
	if err != nil {
		// 检查 invite_code 唯一约束错误
		// TODO: 替换为更健壮的唯一约束错误检查
		if isDuplicateEntryError(err) { // 使用与 user_repository 相同的辅助函数
			return repository.ErrDuplicateEntry
		}
		return fmt.Errorf("gorm: save room (id: %d, invite_code: %s): %w", roomData.ID, roomData.InviteCode, err)
	}
	return nil
}

// FindAllActive 实现根据 ID 列表批量获取房间信息
func (r *GormRoomRepository) FindAllActive(ctx context.Context, roomIDs []uint) ([]domain.Room, error) { // 返回 []domain.Room
	var rooms []domain.Room // 使用 domain.Room
	if len(roomIDs) == 0 {
		return rooms, nil // 避免空的 IN 查询，直接返回空 slice
	}
	// GORM 会自动处理 "id IN (...)"
	err := r.db.WithContext(ctx).Where("id IN ?", roomIDs).Find(&rooms).Error
	if err != nil {
		// 批量查询通常不返回 ErrRecordNotFound，即使部分 ID 没找到
		return nil, fmt.Errorf("gorm: find active rooms by ids: %w", err)
	}
	return rooms, nil
}

// IsInviteCodeExists 实现检查邀请码是否存在
func (r *GormRoomRepository) IsInviteCodeExists(ctx context.Context, code string) (bool, error) {
	var count int64
	// 使用 Count() 优化查询，只查询数量
	// 指定 Model(&domain.Room{}) 明确查询的表
	err := r.db.WithContext(ctx).Model(&domain.Room{}).Where("invite_code = ?", code).Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("gorm: count rooms by invite code '%s': %w", code, err)
	}
	// 如果 count > 0，则表示存在
	return count > 0, nil
}

// isDuplicateEntryError (如果 user_repository.go 中未定义，则在此定义或移至共享位置)
// func isDuplicateEntryError(err error) bool { ... } // 同上