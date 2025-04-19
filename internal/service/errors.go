package service

import "errors"

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrRoomNotFound        = errors.New("room not found")
	ErrAuthenticationFailed = errors.New("authentication failed")
	ErrRegistrationFailed   = errors.New("registration failed: username or email already exists")
	ErrInvalidInviteCode    = errors.New("invalid or expired invite code")
	ErrInternalServer       = errors.New("internal server error")
	ErrInvalidAction        = errors.New("invalid action data")
	ErrVersionConflict      = errors.New("version conflict detected") // 如果需要显式处理版本冲突
)

// 该函数用于将仓库层的错误（如 GORM 或 Redis 错误）映射到服务层定义的错误。
// 目前仅实现了基础的映射逻辑，可根据实际需求扩展。
func mapRepoError(err error) error {
    if err == nil {
        return nil
    }

    // 示例：假设已经导入了 gorm 包
    // if errors.Is(err, gorm.ErrRecordNotFound) {
    //     // 这里可以根据上下文决定返回哪个服务层错误
    //     return ErrUserNotFound
    // }

    // 其他可能的错误映射可以在这里添加
    // 例如，处理 Redis 连接错误
    // if isRedisConnectionError(err) {
    //     return ErrInternalServer
    // }

    // 默认返回内部服务器错误
    return ErrInternalServer
}