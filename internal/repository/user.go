package repository

import (
	"context"
	// 使用正确的模块路径替换 "collaborative-blackboard"
	"collaborative-blackboard/internal/domain"
)

// UserRepository 定义了用户数据的存储和检索操作。
type UserRepository interface {
	// FindByUsername 根据用户名查找用户。
	// 如果用户不存在，应返回明确的错误，例如 domain.ErrUserNotFound。
	FindByUsername(ctx context.Context, username string) (*domain.User, error)

	// FindByID 根据用户 ID 查找用户。
	// 如果用户不存在，应返回明确的错误，例如 domain.ErrUserNotFound。
	FindByID(ctx context.Context, id uint) (*domain.User, error)

	// Save 保存用户信息。
	// 如果用户已存在 (基于 ID)，则更新；否则创建新用户。
	// 返回保存后的 User 对象（可能包含数据库生成的 ID 或时间戳）或错误。
	Save(ctx context.Context, user *domain.User) error
}

// // 可以在 domain 包或 repository 包中定义标准的错误类型
// var ErrUserNotFound = errors.New("user not found")