package handlers

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"context" // 导入 context 包

	"crypto/rand"
	"errors" // 导入 errors 包
	"fmt"    // 导入 fmt 包
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm" // 导入 gorm 以便使用 gorm.ErrRecordNotFound
)

// CreateRoom 处理创建新房间的请求
func CreateRoom(c *gin.Context) {
	// 1. 从 Gin 上下文中获取认证用户 ID
	userIDAny, exists := c.Get("user_id")
	if !exists {
		// 如果上下文中没有 user_id，说明认证中间件未成功或未运行
		logrus.Warn("CreateRoom: User ID not found in context, likely unauthorized access attempt.")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}
	// 类型断言，确保 userID 是 uint 类型
	userID, ok := userIDAny.(uint)
	if !ok {
		logrus.Error("CreateRoom: User ID in context is not of type uint.")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error processing user ID"})
		return
	}
	logCtx := logrus.WithField("user_id", userID) // 创建带 user_id 的日志上下文

	// 2. 生成唯一的邀请码
	inviteCode, err := generateUniqueInviteCode(c.Request.Context()) // 传递 context
	if err != nil {
		logCtx.WithError(err).Error("CreateRoom: 生成唯一邀请码失败。") // Failed to generate unique invite code.
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate invite code"})
		return
	}
	logCtx = logCtx.WithField("invite_code", inviteCode) // 添加 invite_code 到日志上下文

	// 3. 创建房间记录
	room := models.Room{
		CreatorID:  userID,
		InviteCode: inviteCode,
		// CreatedAt 和 UpdatedAt 由 GORM 自动填充
		// LastActive 在这里设置初始值，后续可通过 WS 活动更新
		LastActive: time.Now(),
	}
	// 使用请求上下文执行数据库操作
	if err := config.DB.WithContext(c.Request.Context()).Create(&room).Error; err != nil {
		logCtx.WithError(err).Error("CreateRoom: 在数据库中创建房间失败。") // Failed to create room in database.
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create room"})
		return
	}
	logCtx = logCtx.WithField("room_id", room.ID) // 添加 room_id 到日志上下文

	// 4. (可选) 在 Redis 中缓存房间状态或元数据
	// 这里简单设置一个 key 表示房间存在，可以根据需要存储更复杂的信息
	// 使用 SetEx 设置过期时间，避免 Redis 中残留过多无效 key
	redisRoomKey := "room_meta:" + strconv.FormatUint(uint64(room.ID), 10)
	// 使用 Set 而不是 SetEx，因为 Set 的最后一个参数 0 表示永不过期，如果需要过期请用 SetEx
	if err := config.Redis.Set(c.Request.Context(), redisRoomKey, "active", 0).Err(); err != nil { // 0 表示永不过期
		// 缓存失败通常不应阻塞主流程，记录警告即可
		logCtx.WithError(err).Warn("CreateRoom: 在 Redis 中缓存房间状态失败。") // Failed to cache room status in Redis.
	}

	// 5. 成功响应
	logCtx.Info("房间创建成功。") // Room created successfully.
	c.JSON(http.StatusOK, gin.H{
		"message":     "Room created successfully",
		"room_id":     room.ID,
		"invite_code": inviteCode,
	})
}

// JoinRoom 处理用户加入房间的请求
func JoinRoom(c *gin.Context) {
	// 1. 获取认证用户 ID
	userIDAny, exists := c.Get("user_id")
	if !exists {
		logrus.Warn("JoinRoom: User ID not found in context.")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}
	userID, ok := userIDAny.(uint)
	if !ok {
		logrus.Error("JoinRoom: User ID in context is not of type uint.")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error processing user ID"})
		return
	}
	logCtx := logrus.WithField("user_id", userID)

	// 2. 绑定请求体中的邀请码
	var input struct {
		InviteCode string `json:"invite_code" binding:"required"` // 添加 binding 验证
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		logCtx.WithError(err).Warn("JoinRoom: 无效的输入格式或缺少邀请码。") // Invalid input format or missing invite code.
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: invite_code is required"})
		return
	}
	logCtx = logCtx.WithField("invite_code", input.InviteCode)

	// 3. 根据邀请码查找房间
	var room models.Room
	// 使用请求上下文执行数据库查询
	err := config.DB.WithContext(c.Request.Context()).Where("invite_code = ?", input.InviteCode).First(&room).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logCtx.Warn("JoinRoom: 未找到对应邀请码的房间。") // Room not found for the given invite code.
			c.JSON(http.StatusNotFound, gin.H{"error": "Room not found or invalid invite code"})
		} else {
			logCtx.WithError(err).Error("JoinRoom: 通过邀请码查找房间时数据库出错。") // Database error finding room by invite code.
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find room"})
		}
		return
	}
	logCtx = logCtx.WithField("room_id", room.ID)

	// 4. (可选) 可以在这里添加逻辑，检查用户是否已经是房间成员 (如果需要实现成员列表)
	// 5. (可选) 更新房间的 LastActive 时间戳
	// config.DB.Model(&room).Update("last_active", time.Now())

	// 6. 成功响应
	logCtx.Info("用户成功加入房间。") // User joined room successfully.
	c.JSON(http.StatusOK, gin.H{"message": "Joined room successfully", "room_id": room.ID})
}

// generateUniqueInviteCode 生成一个在数据库中唯一的 6 位字母数字邀请码。
// 它会尝试生成，直到找到一个未被使用的码。
// 注意：在高并发下，生成和检查之间可能存在微小的竞争条件，但对于此应用场景通常可接受。
// 更严格的方法是使用数据库事务或唯一约束重试。
func generateUniqueInviteCode(ctx context.Context) (string, error) {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" // 使用大写字母和数字
	const codeLength = 6                                  // 邀请码长度
	const maxAttempts = 10                                // 设置最大尝试次数，防止无限循环

	b := make([]byte, codeLength)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 使用 crypto/rand 生成高质量随机字节
		if _, err := rand.Read(b); err != nil {
			logrus.WithError(err).Error("generateUniqueInviteCode: 读取随机字节失败。") // Failed to read random bytes.
			return "", fmt.Errorf("failed to generate random bytes: %w", err)
		}

		// 将随机字节映射到允许的字符集
		for i := range b {
			b[i] = letters[int(b[i])%len(letters)] // 使用取模确保在字符集范围内
		}
		code := string(b)

		// 检查生成的 code 是否已存在于数据库中
		var existing models.Room
		// 使用传入的 context 执行数据库查询
		// Select("id") 优化查询，只获取 ID 字段
		err := config.DB.WithContext(ctx).Select("id").Where("invite_code = ?", code).First(&existing).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// 如果未找到记录 (gorm.ErrRecordNotFound)，说明 code 可用
				logrus.WithField("invite_code", code).Infof("在 %d 次尝试后生成唯一邀请码。", attempt+1) // Generated unique invite code after %d attempt(s).
				return code, nil // 成功找到唯一 code
			}
			// 其他数据库查询错误
			logrus.WithError(err).WithField("invite_code", code).Error("generateUniqueInviteCode: 检查邀请码唯一性时数据库出错。") // Database error checking invite code uniqueness.
			return "", fmt.Errorf("database error checking invite code uniqueness: %w", err)
		}
		// 如果找到了记录 (err == nil)，说明 code 已存在，继续循环尝试下一个
		logrus.WithField("invite_code", code).Warnf("generateUniqueInviteCode: 生成的邀请码已存在，重试 (尝试 %d)...", attempt+1) // Generated code already exists, retrying (attempt %d)...
	}

	// 如果达到最大尝试次数仍未找到唯一 code
	logrus.Error("generateUniqueInviteCode: 达到最大尝试次数后未能生成唯一邀请码。") // Failed to generate a unique invite code after maximum attempts.
	return "", fmt.Errorf("failed to generate a unique invite code after %d attempts", maxAttempts)
}
