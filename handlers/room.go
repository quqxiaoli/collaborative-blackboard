package handlers

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"crypto/rand"
	"encoding/json"
	"image"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/disintegration/imaging"
	"github.com/gin-gonic/gin"
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
		UserID uint `json:"user_id"` // 暂时保留兼容，后续移除
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	userID, exists := c.Get("user_id")
	log.Printf("Get user_id: %v, exists: %v", userID, exists) // 添加日志
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
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
		"message":     "Room created",
		"room_id":     room.ID,
		"invite_code": room.InviteCode,
	})
}

// JoinRoom 通过邀请码加入房间
func JoinRoom(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var input struct {
		InviteCode string `json:"invite_code"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var room models.Room
	if err := config.DB.Where("invite_code = ?", input.InviteCode).First(&room).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Room not found"})
		return
	}

	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User not found"})
		return
	}

	// 这里可以添加用户-房间关联逻辑（如 members 表），当前仅验证
	log.Printf("User %d joined room %d", user.ID, room.ID)
	c.JSON(http.StatusOK, gin.H{"message": "Joined room", "room_id": room.ID})
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

// UploadImage 上传图片到房间
func UploadImage(c *gin.Context) {
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

	// 获取上传文件
	file, header, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to get image"})
		return
	}
	defer file.Close()

	// 生成文件名
	filename := strconv.FormatUint(uint64(roomID), 10) + "_" + strconv.FormatInt(time.Now().UnixNano(), 10) + filepath.Ext(header.Filename)
	filepath := filepath.Join("uploads", filename)

	// 保存文件
	out, err := os.Create(filepath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save image"})
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write image"})
		return
	}

	// 记录到数据库
	action := models.Action{
		RoomID:     uint(roomID),
		UserID:     1, // 硬编码，后续优化
		ActionType: "image",
		Data:       `{"filename": "` + filename + `"}`,
		Timestamp:  time.Now(),
	}
	if err := config.DB.Create(&action).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save action"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Image uploaded",
		"room_id":  roomID,
		"filename": filename,
	})
}

// ExportBoard 导出黑板状态为图片
func ExportBoard(c *gin.Context) {
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

	// 获取所有操作
	var actions []models.Action
	if err := config.DB.Where("room_id = ?", roomID).Order("timestamp ASC").Find(&actions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch actions"})
		return
	}

	// 创建空白画布（假设 800x600）
	canvas := imaging.New(800, 600, image.White)

	// 应用所有操作（简化，只处理图片）
	for _, action := range actions {
		if action.ActionType == "image" {
			var data struct {
				Filename string `json:"filename"`
			}
			if err := json.Unmarshal([]byte(action.Data), &data); err != nil {
				continue
			}
			imgPath := filepath.Join("uploads", data.Filename)
			img, err := imaging.Open(imgPath)
			if err != nil {
				continue
			}
			// 简单叠加（假设图片位置固定）
			canvas = imaging.Paste(canvas, img, image.Pt(0, 0))
		}
	}

	// 保存导出图片
	exportFilename := "board_" + roomIDStr + ".png"
	exportPath := filepath.Join("uploads", exportFilename)
	if err := imaging.Save(canvas, exportPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to export board"})
		return
	}

	// 返回文件
	c.File(exportPath)
}
