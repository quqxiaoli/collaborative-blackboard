package hub

import (
	"context"
	"encoding/json" // 用于序列化快照和 Action 消息
	"sync"
	"time"

	// 导入正确的路径
	//"collaborative-blackboard/internal/domain" // 需要 domain.Snapshot 等
	"collaborative-blackboard/internal/service"

	//"github.com/gorilla/websocket" // 需要导入以在 client.go 中使用（虽然这里没直接用）
	"github.com/sirupsen/logrus"
)

// 包级别的 WebSocket 常量，供 hub 和 client 包内使用
const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 1024 // 增加到 1024 字节，根据需要调整
)

// HubMessage 定义了在 Hub 内部通道传递的消息类型
type HubMessage struct {
	Type     string      // "register", "unregister", "action"
	RoomID   uint        // 房间 ID
	UserID   uint        // 来源用户 ID (用于 Action 和识别 Client)
	Client   *Client     // 仅用于 register/unregister (和 action 关联的 client)
	RawData  []byte      // 仅用于 action (原始 WebSocket 消息)
	// Context context.Context // 如果 Service 调用需要特定的 Context
}

// Hub 维护活跃客户端集合并协调消息处理
type Hub struct {
	// 内部通道，处理所有来自 Client 的事件
	messageChan chan HubMessage

	// 客户端集合，按 RoomID 组织
	// map[roomID]map[*Client]bool
	rooms map[uint]map[*Client]bool
	// 保护 rooms map 的读写锁
	roomsMu sync.RWMutex

	// 注入的 Service，用于处理业务逻辑
	collabService *service.CollaborationService
	snapshotService *service.SnapshotService
}

// NewHub 创建并返回一个新的 Hub 实例
func NewHub(collabService *service.CollaborationService, snapshotService *service.SnapshotService) *Hub {
	// 启动时检查依赖注入是否有效
	if collabService == nil {
		panic("CollaborationService cannot be nil for Hub")
	}
	if snapshotService == nil {
		panic("SnapshotService cannot be nil for Hub")
	}
	return &Hub{
		// 创建带缓冲区的通道，大小可根据预期负载调整
		messageChan:   make(chan HubMessage, 512),
		rooms:         make(map[uint]map[*Client]bool),
		collabService: collabService,
		snapshotService: snapshotService,
	}
}

// Run 启动 Hub 的主事件处理循环。
// 它应该在一个单独的 goroutine 中运行。
func (h *Hub) Run() {
	log := logrus.WithField("component", "hub")
	log.Info("Hub is running...")

	// 持续从 messageChan 读取并处理消息
	for msg := range h.messageChan {
		switch msg.Type {
		case "register":
			// 处理客户端注册请求
			h.registerClient(msg.Client)
		case "unregister":
			// 处理客户端注销请求
			h.unregisterClient(msg.Client)
		case "action":
			// 异步处理客户端发送的操作，避免阻塞 Hub 主循环
			// 如果需要严格的顺序处理，则不能使用 go h.handleClientAction(msg)
			// 但对于白板操作，允许一定程度的并发处理通常是可以接受的
			go h.handleClientAction(msg)
		default:
			// 记录未知的消息类型
			log.Warnf("Hub: Received unknown message type: %s from user %d in room %d", msg.Type, msg.UserID, msg.RoomID)
		}
	}
	// 当 messageChan 关闭时，循环结束
	log.Info("Hub is shutting down...")
}

// registerClient 处理客户端注册逻辑
func (h *Hub) registerClient(client *Client) {
	// 防御性编程：检查 client 是否为 nil
	if client == nil {
		logrus.Error("Hub: Attempted to register a nil client")
		return
	}
	roomID := client.RoomID()
	userID := client.UserID()
	logCtx := logrus.WithFields(logrus.Fields{
		"room_id": roomID,
		"user_id": userID,
		"action":  "registerClient",
	})

	h.roomsMu.Lock()
	// 检查房间是否存在，不存在则创建
	if _, ok := h.rooms[roomID]; !ok {
		h.rooms[roomID] = make(map[*Client]bool)
		logCtx.Info("Client list created for new room")
	}
	// 将客户端添加到房间
	h.rooms[roomID][client] = true
	h.roomsMu.Unlock() // 操作完成后立即解锁
	logCtx.Info("Client registered to Hub")

	// 异步获取并发送初始快照给新客户端
	go h.sendInitialSnapshot(client)
}

// unregisterClient 处理客户端注销逻辑
func (h *Hub) unregisterClient(client *Client) {
	if client == nil {
		logrus.Error("Hub: Attempted to unregister a nil client")
		return
	}
	roomID := client.RoomID()
	userID := client.UserID()
	logCtx := logrus.WithFields(logrus.Fields{
		"room_id": roomID,
		"user_id": userID,
		"action":  "unregisterClient",
	})

	h.roomsMu.Lock()
	// 检查房间和客户端是否存在
	if roomClients, roomExists := h.rooms[roomID]; roomExists {
		if _, clientExists := roomClients[client]; clientExists {
			// 从房间中删除客户端
			delete(roomClients, client)
			logCtx.Debug("Client removed from room map")

			// 关闭此客户端的 send 通道，这将导致其 WritePump 退出
			// 需要检查通道是否已关闭，防止重复关闭 panic
			select {
			case <-client.send:
				// 通道已关闭或有残留数据（理论上不应有残留）
				logCtx.Warn("Client send channel already closed or has data during unregister")
			default:
				// 通道未关闭，安全地关闭它
				close(client.send)
				logCtx.Info("Client send channel closed")
			}

			// 如果房间变空，则从 Hub 中删除该房间记录
			if len(roomClients) == 0 {
				delete(h.rooms, roomID)
				logCtx.Info("Room empty, removed from Hub")
				// TODO: 在这里或专门的清理任务中触发房间相关资源的清理 (如 Redis keys)
			}
		} else {
			logCtx.Warn("Client not found in room during unregister")
		}
	} else {
		logCtx.Warn("Room not found during client unregister")
	}
	h.roomsMu.Unlock()
	logCtx.Info("Client unregistered from Hub")
}

// sendInitialSnapshot 异步获取并发送快照给新连接的客户端
func (h *Hub) sendInitialSnapshot(client *Client) {
	if client == nil { return }
	logCtx := logrus.WithFields(logrus.Fields{
		"room_id":   client.RoomID(),
		"user_id":   client.UserID(),
		"operation": "sendInitialSnapshot",
	})
	logCtx.Info("Attempting to send initial snapshot")

	// 使用后台 context，因为 Service 调用可能涉及 IO 且不应被原始请求取消
	ctx := context.Background()
	// 调用 SnapshotService 获取快照数据
	snapshot, boardState, err := h.snapshotService.GetSnapshotForClient(ctx, client.RoomID())
	if err != nil {
		logCtx.WithError(err).Error("Failed to get snapshot data from service")
		// 考虑向客户端发送错误信息
		errorMsg := `{"type": "error", "message": "Failed to load initial board state"}`
		// 尝试发送错误消息，忽略发送通道满的情况
		select {
		case client.send <- []byte(errorMsg):
		default:
		}
		return
	}

	// 构造快照消息
	snapshotMsg := map[string]interface{}{
		"type":    "snapshot",
		"version": snapshot.Version, // 使用从 Service 获取的快照版本
		"state":   boardState,     // 使用从 Service 获取的 BoardState
	}
	stateBytes, err := json.Marshal(snapshotMsg)
	if err != nil {
		logCtx.WithError(err).Error("Failed to marshal snapshot message")
		return
	}

	// 将消息发送到客户端的 send 通道
	select {
	case client.send <- stateBytes:
		logCtx.WithField("version", snapshot.Version).Info("Snapshot message sent to client channel")
	default:
		// 如果发送通道已满，记录警告。客户端可能已断开。
		logCtx.Warn("Client send channel full when trying to send snapshot, message dropped")
	}
}

// handleClientAction 异步处理客户端发送的操作消息
func (h *Hub) handleClientAction(msg HubMessage) {
	// 使用后台 context 进行处理
	ctx := context.Background()
	logCtx := logrus.WithFields(logrus.Fields{
		"room_id":   msg.RoomID,
		"user_id":   msg.UserID,
		"operation": "handleClientAction",
	})
	logCtx.Debugf("Processing client action (data size: %d)", len(msg.RawData))

	// 调用 CollaborationService 处理操作
	processedAction, shouldBroadcast, err := h.collabService.ProcessIncomingAction(ctx, msg.RoomID, msg.UserID, msg.RawData)

	if err != nil {
		logCtx.WithError(err).Error("Error processing action in service")
		// TODO: 向发送者客户端 (msg.Client) 发送错误消息
		// errorPayload := map[string]string{"type": "error", "message": fmt.Sprintf("Action failed: %s", err.Error())}
		// errorBytes, _ := json.Marshal(errorPayload)
		// select { case msg.Client.send <- errorBytes: default: }
		return
	}

	// 如果 Service 处理成功并且需要广播 (Action 不是 noop)
	if shouldBroadcast && processedAction != nil {
		logCtx.WithField("action_version", processedAction.Version).Info("Action processed, broadcasting...")
		// 序列化处理后的 Action 用于广播
		broadcastMsgBytes, err := json.Marshal(processedAction)
		if err != nil {
			logCtx.WithError(err).Error("Failed to marshal processed action for broadcast")
			return
		}

		// 调用广播方法，排除发送者 (msg.Client)
		h.broadcast(msg.RoomID, broadcastMsgBytes, msg.Client)

		// TODO: 触发 Action 的数据库持久化 (移至后台任务)
		// 例如: taskqueue.EnqueueSaveAction(ctx, *processedAction)
		// logCtx.Debug("Triggering background action persistence")

	} else {
		logCtx.Debug("Action processed but no broadcast needed (e.g., noop or error)")
	}
}


// broadcast 将消息发送给指定房间的所有客户端，排除发送者
func (h *Hub) broadcast(roomID uint, message []byte, sender *Client) {
	h.roomsMu.RLock()
	roomClients, ok := h.rooms[roomID]
	// 创建一个接收者列表的副本，以避免长时间持有锁
	clientsToSend := make([]*Client, 0, len(roomClients))
	if ok {
		for client := range roomClients {
			// 排除发送者
			if client != sender {
				clientsToSend = append(clientsToSend, client)
			}
		}
	}
	h.roomsMu.RUnlock() // 尽快释放读锁

	// 如果没有接收者，则直接返回
	if !ok || len(clientsToSend) == 0 {
		return
	}

	logCtx := logrus.WithFields(logrus.Fields{
		"room_id":         roomID,
		"message_size":    len(message),
		"recipient_count": len(clientsToSend),
	})
	if sender != nil {
		logCtx = logCtx.WithField("sender_user_id", sender.UserID())
	}
	logCtx.Debug("Broadcasting message to clients")

	// 遍历副本列表进行发送
	for _, client := range clientsToSend {
		// 使用非阻塞发送，避免单个慢客户端阻塞广播
		select {
		case client.send <- message:
			// 消息成功放入该客户端的发送队列
		default:
			// 如果客户端的发送通道已满，记录警告。
			// 让该客户端的 WritePump 或清理任务负责处理后续问题（如断开连接）。
			logCtx.WithField("receiver_user_id", client.UserID()).Warn("Client send channel full during broadcast, skipping this client")
		}
	}
}

// --- 公共方法 ---

// QueueMessage 将消息放入 Hub 的处理队列 (非阻塞)。
// 这是 Client 向 Hub 发送消息的安全方式。
// 返回 true 如果消息成功入队，false 如果队列已满。
func (h *Hub) QueueMessage(msg HubMessage) bool {
	select {
	case h.messageChan <- msg:
		return true // 发送成功
	default:
		// 队列满，记录警告并返回失败
		logrus.WithFields(logrus.Fields{
			"message_type": msg.Type,
			"room_id":      msg.RoomID,
			"user_id":      msg.UserID,
		}).Warn("Hub message channel full, dropping message")
		return false // 发送失败
	}
}


// *** 添加 MessageChan 方法 ***
// MessageChan 返回一个只写的 channel，用于外部向 Hub 发送消息。
// 这种方式比直接暴露 messageChan 更好，但仍然允许外部阻塞 Hub（如果队列满）。
// QueueMessage 是非阻塞的，通常更推荐。
// 但如果 WebSocketHandler 确实需要阻塞等待 Hub 接受注册消息，可以使用这个。
// 考虑到我们之前在 Handler 中使用了 select default，QueueMessage 可能是更一致的选择。
// 我们暂时添加它以解决编译错误，但可以考虑是否真的需要它。
func (h *Hub) MessageChan() chan<- HubMessage {
    return h.messageChan
}

// --- 不再包含 Client, NewClient, readPump, writePump ---
// --- 也不再包含后台任务逻辑 ---