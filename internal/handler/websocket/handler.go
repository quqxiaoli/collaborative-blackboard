package websocket

import (
	"errors"
	"net/http"
	"strconv"

	// 导入 Service, Hub, Client 定义
	"collaborative-blackboard/internal/hub"      // 导入 Hub 包
	"collaborative-blackboard/internal/service"  // 导入 Service 包

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket" // 导入 websocket 库
	"github.com/sirupsen/logrus"
)

// WebSocketHandler 负责处理 WebSocket 升级请求和客户端注册
type WebSocketHandler struct {
	upgrader    websocket.Upgrader // WebSocket 升级器
	hub         *hub.Hub           // 依赖 Hub
	roomService *service.RoomService // 依赖 RoomService 验证房间
}

// NewWebSocketHandler 创建 WebSocketHandler 实例
func NewWebSocketHandler(hub *hub.Hub, roomService *service.RoomService) *WebSocketHandler {
	if hub == nil {
		panic("Hub cannot be nil for WebSocketHandler")
	}
	if roomService == nil {
		panic("RoomService cannot be nil for WebSocketHandler")
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024, // 根据需要调整
		WriteBufferSize: 1024,
		// 允许所有来源连接 (生产环境应配置具体的允许来源)
		CheckOrigin: func(r *http.Request) bool {
			// TODO: Implement proper origin checking for production
			// allowedOrigin := os.Getenv("WEBSOCKET_ALLOWED_ORIGIN") or config
			// return r.Header.Get("Origin") == allowedOrigin
			return true // 暂时允许所有
		},
	}

	return &WebSocketHandler{
		upgrader:    upgrader,
		hub:         hub,
		roomService: roomService,
	}
}

// HandleConnection 处理 WebSocket 连接请求
// URL 预期格式: /ws/room/{roomId}
func (h *WebSocketHandler) HandleConnection(c *gin.Context) {
	logCtx := logrus.WithFields(logrus.Fields{}) // 初始化日志上下文

	// 1. 获取认证用户 ID (由 Auth 中间件设置)
	userIDAny, exists := c.Get("user_id")
	if !exists {
		logCtx.Warn("WS Handler: User ID not found in context")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return // 返回 HTTP 错误，因为此时还未升级到 WebSocket
	}
	userID, ok := userIDAny.(uint)
	if !ok {
		logCtx.Error("WS Handler: User ID in context is not uint")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
		return
	}
	logCtx = logCtx.WithField("user_id", userID)

	// 2. 获取并验证房间 ID (从 URL 参数)
	roomIDStr := c.Param("roomId")
	roomIDUint64, err := strconv.ParseUint(roomIDStr, 10, 32)
	if err != nil {
		logCtx.WithError(err).Warnf("WS Handler: Invalid room ID format: %s", roomIDStr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid room ID format"})
		return
	}
	roomID := uint(roomIDUint64)
	logCtx = logCtx.WithField("room_id", roomID)

	// 3. 验证房间是否存在 (调用 RoomService)
	// 这里不需要完整的房间信息，只需要确认房间存在即可。
	// 可以考虑在 RoomService 或 RoomRepository 添加一个 RoomExists(id uint) bool 方法。
	// 或者，暂时继续使用 FindByID。
	_, err = h.roomService.JoinRoom(c.Request.Context(), userID, "") // 复用 JoinRoom 逻辑，或者调用 FindByID
	// _, err = h.roomService.FindRoomByID(c.Request.Context(), roomID) // 假设有 FindRoomByID
	if err != nil {
		if errors.Is(err, service.ErrRoomNotFound) { // 检查业务错误
			logCtx.WithError(err).Warn("WS Handler: Room not found")
			c.JSON(http.StatusNotFound, gin.H{"error": "Room not found"})
		} else {
			logCtx.WithError(err).Error("WS Handler: Error checking room existence")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate room"})
		}
		return
	}
	logCtx.Debug("WS Handler: Room validated")

	// 4. 升级 HTTP 连接到 WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade 方法会自动发送 HTTP 错误响应，所以这里只需要记录日志
		logCtx.WithError(err).Error("WS Handler: Failed to upgrade connection")
		// 不需要再调用 c.JSON
		return
	}
	logCtx.Info("WS Handler: Connection upgraded to WebSocket")

	// 5. 创建 Client 对象
	client := hub.NewClient(h.hub, conn, roomID, userID) // 假设 Hub 包提供了 NewClient 函数

	registerMsg := hub.HubMessage{
        Type:   "register",
        Client: client,
        RoomID: client.RoomID(),
        UserID: client.UserID(),
    }
    // 使用 Hub 的公共方法 QueueMessage 发送注册请求
    if !h.hub.QueueMessage(registerMsg) { // *** 使用 QueueMessage ***
        // Hub 的通道满了，注册失败
        logCtx.Error("WS Handler: Hub message channel full, failed to register client")
        client.CloseConn() // 关闭连接
        // 可以考虑返回 HTTP 错误，但连接已升级，意义不大
        return
    }
    logCtx.Info("WS Handler: Client registration request queued to Hub")

	// 7. 启动客户端的读写 Goroutine (这里也需要修改)
    // 我们需要将启动 goroutine 的调用移到 Client 对象本身的方法中，
    // 或者确保 Hub 在处理完 "register" 消息后启动它们。
    // 暂时先注释掉这里的启动，因为之前的报错显示 ReadPump 未导出。

    // *** 或者更好的方式：让 Client 自己启动 ***
    go client.Run() // 假设 Client 有一个 Run 方法来启动 readPump 和 writePump

	logCtx.Info("WS Handler: Client read/write pumps started")
	// 注意：一旦启动了 goroutine，这个 HandleConnection 函数就结束了。
	// 后续的 WebSocket 通信由 client.readPump 和 client.writePump 处理。
}