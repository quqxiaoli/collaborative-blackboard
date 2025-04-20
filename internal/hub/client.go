package hub

import (
	"time"

	// 导入需要的包
	// "collaborative-blackboard/internal/domain" // 如果 Client 需要 Domain 模型
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	// 注意：确保 hub.go 中定义的常量（如 pongWait, writeWait 等）在这里也能访问
	// 或者将这些常量移到 client.go 或 hub 包级别
)

// Client 代表一个连接到 Hub 的 WebSocket 客户端。
type Client struct {
	hub    *Hub            // 指向其所属的 Hub
	conn   *websocket.Conn // WebSocket 连接
	roomID uint            // 客户端所在的房间 ID
	userID uint            // 客户端的用户 ID
	send   chan []byte     // 用于向此客户端发送消息的缓冲通道
}

// Run 启动客户端的读写 goroutine
func (c *Client) Run() {
	go c.WritePump() // 调用大写的 WritePump
	go c.ReadPump()  // 调用大写的 ReadPump
}

// NewClient 创建一个新的 Client 实例
func NewClient(hub *Hub, conn *websocket.Conn, roomID uint, userID uint) *Client {
	return &Client{
		hub:    hub,
		conn:   conn,
		roomID: roomID,
		userID: userID,
		// send 通道缓冲区大小，例如 256
		send: make(chan []byte, 256),
	}
}

// readPump 将消息从 WebSocket 连接泵送到 Hub 的 messageChan。
// 它在自己的 goroutine 中运行。
func (c *Client) ReadPump() {
	defer func() {
		// 清理操作：请求 Hub 注销此客户端
		unregisterMsg := HubMessage{Type: "unregister", Client: c}
		// 尝试发送到 Hub，忽略错误（Hub 可能已停止）
		select {
		case c.hub.messageChan <- unregisterMsg:
		// 添加一个超时或默认 case，以防 Hub 阻塞
		case <-time.After(1 * time.Second): // 可选的超时
			logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).Warn("Timeout sending unregister message to Hub channel")
		}
		c.conn.Close() // 关闭 WebSocket 连接
		logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).Info("readPump exited, unregistered client")
	}()

	c.conn.SetReadLimit(maxMessageSize) // 设置最大消息大小
	// 设置初始读取超时和 Pong 处理程序
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait)) // 收到 Pong 后重置读取超时
		return nil
	})

	for {
		// 从 WebSocket 读取消息
		messageType, message, err := c.conn.ReadMessage()
		if err != nil {
			// 处理读取错误或连接关闭
			logCtx := logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID})
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logCtx.WithError(err).Warn("WebSocket read error (unexpected close)")
			} else {
				logCtx.Debug("WebSocket connection closed normally or read error")
			}
			break // 退出循环，触发 defer 中的注销
		}

		// 只处理文本消息
		if messageType == websocket.TextMessage {
			logCtx := logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID})
			logCtx.Debugf("Received raw message (size: %d)", len(message))

			// 将原始消息封装成 HubMessage 并发送到 Hub 的处理通道
			actionMsg := HubMessage{
				Type:    "action",
				RoomID:  c.roomID,
				UserID:  c.userID,
				Client:  c,       // 传递 Client 引用，Hub 可能需要
				RawData: message, // 发送原始字节数据
			}

			// 非阻塞发送到 Hub，如果 Hub 处理不过来则丢弃
			select {
			case c.hub.messageChan <- actionMsg:
				logCtx.Debug("Raw message sent to Hub channel")
			default:
				// 这种情况通常表示系统负载过高或 Hub 处理逻辑有阻塞
				logCtx.Warn("Hub message channel full, dropping client message")
			}
		} else {
			logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).Debugf("Received non-text message type: %d", messageType)
		}
	}
}

// writePump 将消息从 Client 的 send 通道泵送到 WebSocket 连接。
// 它在自己的 goroutine 中运行。
func (c *Client) WritePump() {
	// 创建一个定时器，用于定期发送 Ping 消息
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()  // 停止定时器
		c.conn.Close() // 关闭 WebSocket 连接
		logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).Info("writePump exited")
		// 不需要在这里 unregister，readPump 退出会处理
	}()

	for {
		select {
		case message, ok := <-c.send:
			// 设置写入超时
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// send 通道被 Hub 关闭了（通常在注销时）
				logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).Info("Hub closed send channel")
				// 尝试向客户端发送 WebSocket 关闭帧
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return // 退出 writePump
			}

			// 将消息写入 WebSocket 连接
			err := c.conn.WriteMessage(websocket.TextMessage, message)
			if err != nil {
				// 写入失败，记录错误并退出
				logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).WithError(err).Warn("Failed to write message to websocket")
				return
			}
			// logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).Debugf("Message sent (size: %d)", len(message))

			// 清除写入超时
			_ = c.conn.SetWriteDeadline(time.Time{})

		case <-ticker.C:
			// 定时器触发，发送 Ping 消息以保持连接活跃并检测断开
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				// 发送 Ping 失败，通常意味着连接已断开
				logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).WithError(err).Warn("Failed to send ping message")
				return // 退出 writePump
			}
			// logrus.WithFields(logrus.Fields{"user_id": c.userID, "room_id": c.roomID}).Debug("Ping message sent")
			_ = c.conn.SetWriteDeadline(time.Time{})
		}
	}
}

func (c *Client) RoomID() uint { return c.roomID }
func (c *Client) UserID() uint { return c.userID }
func (c *Client) CloseConn() { c.conn.Close() }