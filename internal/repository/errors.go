package repository

import "errors"

// 通用的存储库错误
var (
	// ErrNotFound 表示请求的记录未找到
	ErrNotFound = errors.New("repository: record not found")
	// ErrDuplicateEntry 表示尝试插入或更新的数据违反了唯一约束
	ErrDuplicateEntry = errors.New("repository: duplicate entry")
	// ErrOptimisticLock 表示并发更新时发生版本冲突 (如果使用乐观锁)
	// ErrOptimisticLock = errors.New("repository: optimistic lock conflict")
)

// 特定资源的错误 (可以基于通用错误创建)
var (
	ErrUserNotFound = ErrNotFound
	ErrRoomNotFound = ErrNotFound
	ErrSnapshotNotFound = ErrNotFound
	// 如果需要区分，可以定义新的
	// ErrUsernameTaken = ErrDuplicateEntry
	// ErrEmailTaken = ErrDuplicateEntry
	// ErrInviteCodeTaken = ErrDuplicateEntry
)