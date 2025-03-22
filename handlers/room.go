package handlers

import (
    "collaborative-blackboard/config"
    "collaborative-blackboard/models"
    "crypto/rand"
    "github.com/gin-gonic/gin"
    "net/http"
    "time"
	"strconv"
)

// generateInviteCode 生成 6 位随机邀请码
func generateInviteCode() string {
    const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
    b := make([]byte, 6)
    rand.Read(b)
    for i := range b {
        b[i] = letters[b[i]%byte(len(letters))]
    }
    return string(b)
}

// CreateRoom 创建新房间
func CreateRoom(c *gin.Context) {
    var input struct {
        UserID uint `json:"user_id"`
    }
    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
        return
    }

    // 检查用户是否存在
    var user models.User
    if err := config.DB.First(&user, input.UserID).Error; err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "User not found"})
        return
    }

    // 生成唯一邀请码
    var inviteCode string
    for {
        inviteCode = generateInviteCode()
        var existingRoom models.Room
        if config.DB.Where("invite_code = ?", inviteCode).First(&existingRoom).Error != nil {
            break // 没有重复，退出循环
        }
    }

    // 创建房间
    room := models.Room{
        CreatorID:  input.UserID,
        InviteCode: inviteCode,
        CreatedAt:  time.Now(),
        LastActive: time.Now(),
    }
    if err := config.DB.Create(&room).Error; err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create room"})
        return
    }

    // 在 Redis 中缓存房间状态
    err := config.Redis.Set(c.Request.Context(), "room:"+strconv.FormatUint(uint64(room.ID), 10), "active", 0).Err()
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cache room status"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "message":    "Room created",
        "room_id":    room.ID,
        "invite_code": room.InviteCode,
    })
}

// JoinRoom 通过邀请码加入房间
func JoinRoom(c *gin.Context) {
    var input struct {
        InviteCode string `json:"invite_code"`
    }
    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
        return
    }

    // 查询房间
    var room models.Room
    if err := config.DB.Where("invite_code = ?", input.InviteCode).First(&room).Error; err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Room not found"})
        return
    }

    // 检查房间状态（Redis）
    status, err := config.Redis.Get(c.Request.Context(), "room:"+strconv.FormatUint(uint64(room.ID), 10)).Result()
    if err != nil || status != "active" {
        c.JSON(http.StatusForbidden, gin.H{"error": "Room is not active"})
        return
    }

    // 更新最后活跃时间
    room.LastActive = time.Now()
    config.DB.Save(&room)

    c.JSON(http.StatusOK, gin.H{
        "message": "Joined room",
        "room_id": room.ID,
    })
}

// GetReplay 获取房间的涂鸦历史
func GetReplay(c *gin.Context) {
    roomIDStr := c.Param("id")
    roomID, err := strconv.ParseUint(roomIDStr, 10, 32)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid room ID"})
        return
    }

    var room models.Room
    if err := config.DB.First(&room, uint(roomID)).Error; err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Room not found"})
        return
    }

    // 先尝试从 Redis 获取最近操作
    cacheKey := "room:" + roomIDStr + ":recent"
    cachedActions, err := config.Redis.LRange(c.Request.Context(), cacheKey, 0, -1).Result()
    if err == nil && len(cachedActions) > 0 {
        c.JSON(http.StatusOK, gin.H{
            "message": "Replay data from cache",
            "room_id": roomID,
            "actions": cachedActions,
        })
        return
    }

    // 如果缓存为空，从 MySQL 查询
    var actions []models.Action
    if err := config.DB.Where("room_id = ?", roomID).Order("timestamp ASC").Find(&actions).Error; err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch actions"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "message": "Replay data retrieved",
        "room_id": roomID,
        "actions": actions,
    })
}