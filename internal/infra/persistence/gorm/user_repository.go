package gormpersistence

import (
	"context"
	"errors"
	"fmt"
	"strings" // 用于检查错误字符串 (临时方案)

	"gorm.io/gorm"
	// "gorm.io/gorm/clause" // Save 方法暂时不需要这个

	// 使用正确的 Domain 模型路径 (internal/domain) 和 Repository 接口路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"
)

// GormUserRepository 是 UserRepository 接口的 GORM 实现
type GormUserRepository struct {
	db *gorm.DB // 依赖 GORM DB 连接
}

// NewGormUserRepository 创建 GormUserRepository 实例
// db *gorm.DB 通过依赖注入传入
func NewGormUserRepository(db *gorm.DB) *GormUserRepository {
	if db == nil {
		// 早期失败比运行时 panic 更好
		panic("database connection cannot be nil for GormUserRepository")
	}
	return &GormUserRepository{db: db}
}

// FindByUsername 实现根据用户名查找用户
func (r *GormUserRepository) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	var user domain.User // 使用 domain.User
	// 使用 GORM 的 First 方法查找匹配的第一个记录
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error

	if err != nil {
		// 检查是否是记录未找到错误
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 映射为定义的仓库层错误
			return nil, repository.ErrUserNotFound
		}
		// 对于其他数据库错误，包装原始错误并返回
		return nil, fmt.Errorf("gorm: find user by username '%s': %w", username, err)
	}
	// 找到用户，返回用户对象和 nil 错误
	return &user, nil
}

// FindByID 实现根据用户 ID 查找用户
func (r *GormUserRepository) FindByID(ctx context.Context, id uint) (*domain.User, error) {
	var user domain.User // 使用 domain.User
	// GORM 会自动根据主键查找
	err := r.db.WithContext(ctx).First(&user, id).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, repository.ErrUserNotFound
		}
		return nil, fmt.Errorf("gorm: find user by id %d: %w", id, err)
	}
	return &user, nil
}

// Save 实现保存用户信息（创建或更新）
// GORM 的 Save 方法会根据主键是否为零值决定是 INSERT 还是 UPDATE。
func (r *GormUserRepository) Save(ctx context.Context, user *domain.User) error { // 参数类型是 *domain.User
	// 调用 GORM 的 Save 方法
	result := r.db.WithContext(ctx).Save(user) // GORM 会自动处理 user.ID
	err := result.Error

	if err != nil {
		// 尝试检查是否是唯一约束错误
		// TODO: 替换为更健壮的唯一约束错误检查方式
		if isDuplicateEntryError(err) {
			return repository.ErrDuplicateEntry // 映射为定义的仓库错误
		}
		// 返回包装后的其他错误
		return fmt.Errorf("gorm: save user (id: %d, username: %s): %w", user.ID, user.Username, err)
	}

	// 可选：检查是否有行受到影响
	// if result.RowsAffected == 0 && user.ID != 0 {
	//     // 如果是更新操作但没有行受影响，可能表示记录不存在？
	//     // 但 Save 通常不用于这种情况，Find + Update 更合适
	// }

	return nil
}

// isDuplicateEntryError 是一个临时的辅助函数，用于检查常见的唯一约束错误字符串。
// 强烈建议替换为特定数据库驱动的错误检查。
func isDuplicateEntryError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// 常见的错误信息片段
	return strings.Contains(msg, "UNIQUE constraint failed") || // SQLite
		strings.Contains(msg, "Duplicate entry") || // MySQL
		strings.Contains(msg, "duplicate key value violates unique constraint") // PostgreSQL
}