package http

import (
	"errors"
	"net/http"

	// 导入 Service 和定义的业务错误
	"collaborative-blackboard/internal/service" // 导入 Service 包
	// middleware "collaborative-blackboard/internal/middleware" // 如果需要从 Context 获取 UserID

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// RoomHandler 封装了与房间管理相关的 HTTP 处理逻辑
type RoomHandler struct {
	roomService *service.RoomService // 依赖 RoomService
}

// NewRoomHandler 创建 RoomHandler 实例
func NewRoomHandler(roomService *service.RoomService) *RoomHandler {
	return &RoomHandler{roomService: roomService}
}

// CreateRoomResponse 定义创建房间成功的响应结构体
type CreateRoomResponse struct {
	Message    string `json:"message"`
	RoomID     uint   `json:"room_id"`
	InviteCode string `json:"invite_code"`
}

// CreateRoom 处理创建新房间的请求
func (h *RoomHandler) CreateRoom(c *gin.Context) {
	// 1. 从 Gin 上下文中获取认证用户 ID
	//    这需要 Auth 中间件已经运行并设置了 "user_id"
	userIDAny, exists := c.Get("user_id")
	if !exists {
		logrus.Warn("Handler.CreateRoom: User ID not found in context, middleware missing or failed?")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}
	userID, ok := userIDAny.(uint)
	if !ok {
		logrus.Error("Handler.CreateRoom: User ID in context is not uint")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error processing user ID"})
		return
	}
	logCtx := logrus.WithField("user_id", userID)

	// 2. 调用 Service 层创建房间
	newRoom, err := h.roomService.CreateRoom(c.Request.Context(), userID)

	// 3. 处理 Service 返回的错误
	if err != nil {
		logCtx.WithError(err).Error("Handler.CreateRoom: Failed to create room via service")
		// 假设 CreateRoom 只会返回内部错误
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create room"})
		return
	}

	// 4. 成功响应
	logCtx.WithFields(logrus.Fields{"room_id": newRoom.ID, "invite_code": newRoom.InviteCode}).Info("Handler.CreateRoom: Room created successfully")
	c.JSON(http.StatusOK, CreateRoomResponse{
		Message:    "Room created successfully",
		RoomID:     newRoom.ID,
		InviteCode: newRoom.InviteCode,
	})
}

// JoinRoomRequest 定义加入房间请求的结构体
type JoinRoomRequest struct {
	InviteCode string `json:"invite_code" binding:"required,len=6"` // 假设邀请码为 6 位
}

// JoinRoomResponse 定义加入房间成功的响应结构体
type JoinRoomResponse struct {
	Message string `json:"message"`
	RoomID  uint   `json:"room_id"`
}

// JoinRoom 处理用户加入房间的请求
func (h *RoomHandler) JoinRoom(c *gin.Context) {
	// 1. 获取认证用户 ID
	userIDAny, exists := c.Get("user_id")
	if !exists {
		logrus.Warn("Handler.JoinRoom: User ID not found in context.")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}
	userID, ok := userIDAny.(uint)
	if !ok {
		logrus.Error("Handler.JoinRoom: User ID in context is not uint.")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error processing user ID"})
		return
	}
	logCtx := logrus.WithField("user_id", userID)

	// 2. 绑定请求体中的邀请码
	var req JoinRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logCtx.WithError(err).Warn("Handler.JoinRoom: Invalid input format")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: invite_code is required"})
		return
	}
	logCtx = logCtx.WithField("invite_code", req.InviteCode)

	// 3. 调用 Service 层处理加入房间逻辑
	joinedRoom, err := h.roomService.JoinRoom(c.Request.Context(), userID, req.InviteCode)

	// 4. 处理 Service 返回的错误
	if err != nil {
		logCtx.WithError(err).Warn("Handler.JoinRoom: Failed to join room via service")
		if errors.Is(err, service.ErrInvalidInviteCode) || errors.Is(err, service.ErrRoomNotFound) { // 检查业务错误
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()}) // 404 Not Found
		} else {
			// 其他内部错误
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to join room due to server error"})
		}
		return
	}

	// 5. 成功响应
	logCtx.WithField("room_id", joinedRoom.ID).Info("Handler.JoinRoom: User joined room successfully")
	c.JSON(http.StatusOK, JoinRoomResponse{
		Message: "Joined room successfully",
		RoomID:  joinedRoom.ID,
	})
}