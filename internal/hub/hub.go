package hub

import (
	"context"
	"encoding/json" // 用于序列化快照和 Action 消息
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	// 导入正确的路径
	// 需要 domain.Snapshot 等
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/service"
	"collaborative-blackboard/internal/tasks" // 导入任务定义包

	// 导入 Asynq Client 和 Task 相关
	"github.com/hibiken/asynq"
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
	Type    string  // "register", "unregister", "action"
	RoomID  uint    // 房间 ID
	UserID  uint    // 来源用户 ID (用于 Action 和识别 Client)
	Client  *Client // 仅用于 register/unregister (和 action 关联的 client)
	RawData []byte  // 仅用于 action (原始 WebSocket 消息)
	// Context context.Context // 如果 Service 调用需要特定的 Context
}

// Hub 维护活跃客户端集合并协调消息处理
type Hub struct {
	// 内部通道，处理所有来自 Client 的事件
	messageChan chan HubMessage

	// 新增: Asynq Client 用于将任务入队
	asynqClient *asynq.Client

	// 客户端集合，按 RoomID 组织
	// map[roomID]map[*Client]bool
	rooms map[uint]map[*Client]bool
	// 保护 rooms map 的读写锁
	roomsMu sync.RWMutex

	// 注入的 Service，用于处理业务逻辑
	collabService   *service.CollaborationService
	snapshotService *service.SnapshotService

	// 新增: Redis 依赖和订阅管理
	redisClient *redis.Client // Redis 客户端实例
	keyPrefix   string        // Redis key 前缀
	// 用于管理每个房间订阅 Goroutine 的 context cancel func
	roomSubscriptions map[uint]context.CancelFunc
	subMu             sync.Mutex // 保护 roomSubscriptions map 的访问
}

// NewHub 创建并返回一个新的 Hub 实例
func NewHub(collabService *service.CollaborationService, snapshotService *service.SnapshotService, asynqClient *asynq.Client, redisClient *redis.Client, keyPrefix string) *Hub {
	// 启动时检查依赖注入是否有效
	if collabService == nil {
		panic("CollaborationService cannot be nil for Hub")
	}
	if snapshotService == nil {
		panic("SnapshotService cannot be nil for Hub")
	}
	if asynqClient == nil {
		panic("Asynq Client cannot be nil for Hub")
	}
	if redisClient == nil {
		panic("Redis client cannot be nil for Hub")
	} // 检查依赖
	if keyPrefix == "" {
		keyPrefix = "bb:"
	} // 设置默认前缀
	return &Hub{
		// 创建带缓冲区的通道，大小可根据预期负载调整
		messageChan:       make(chan HubMessage, 512),
		asynqClient:       asynqClient, // 存储注入的 client
		rooms:             make(map[uint]map[*Client]bool),
		collabService:     collabService,
		snapshotService:   snapshotService,
		redisClient:       redisClient,                       // 存储依赖
		keyPrefix:         keyPrefix,                         // 存储依赖
		roomSubscriptions: make(map[uint]context.CancelFunc), // 初始化 map
	}
}

// Run 启动 Hub 的主事件处理循环。
// 它应该在一个单独的 goroutine 中运行。
func (h *Hub) Run() {
	log := logrus.WithField("component", "hub")
	log.Info("Hub is running...")

	// --- 启动定期的客户端清理 Goroutine ---
    cleanupInterval := 1 * time.Minute // 清理间隔1 分钟 
    // 创建一个新的 context 用于控制清理 goroutine 的生命周期
    // 注意：这里的 ctx 不同于 roomSubscribeLoop 的 ctx
    //cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
    //defer cleanupCancel() // 确保在 Hub Run 退出时也停止清理 (虽然 Run 通常不退出)
    // 或者直接在 goroutine 内部处理退出信号 
    go h.runClientCleanupLoop(cleanupInterval)
    log.Infof("Client cleanup routine started (interval: %v)", cleanupInterval)
    // --- 清理启动结束 ---

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
	// Hub 关闭时，可能需要关闭 asynqClient (如果 Hub 创建了它的话，但通常 Client 是共享的)
	// h.asynqClient.Close()
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
	roomClients, roomExists := h.rooms[roomID]
	if !roomExists {
		h.rooms[roomID] = make(map[*Client]bool)
		roomClients = h.rooms[roomID]
		logCtx.Info("Client list created for new room")
	}
	roomClients[client] = true
	// 检查是否是这个房间的第一个客户端
	isFirstClientInRoom := len(roomClients) == 1
	h.roomsMu.Unlock() // 尽快释放锁
	logCtx.Info("Client registered to Hub")

	// --- 如果是这个房间的第一个客户端，启动订阅 ---
	if isFirstClientInRoom {
		logCtx.Info("First client in room, starting Redis subscription...")
		h.startRoomSubscription(roomID) // 调用启动订阅的方法
	}
	// --- 订阅逻辑结束 ---

	go h.sendInitialSnapshot(client) // 发送快照不变
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
	shouldStopSubscription := false // 标记是否需要停止订阅

	h.roomsMu.Lock()
	if roomClients, roomExists := h.rooms[roomID]; roomExists {
		if _, clientExists := roomClients[client]; clientExists {
			delete(roomClients, client)
			logCtx.Debug("Client removed from room map")
			select {
			case <-client.send: // 检查是否已关闭
			default:
				close(client.send)
				logCtx.Info("Client send channel closed")
			}

			// 检查房间是否变空
			if len(roomClients) == 0 {
				delete(h.rooms, roomID) // 从 Hub 中移除房间
				logCtx.Info("Room empty, removed from Hub")
				shouldStopSubscription = true // 标记需要停止此房间的订阅
			}
		} else {
			logCtx.Warn("Client not found in room during unregister")
		}
	} else {
		logCtx.Warn("Room not found during client unregister")
	}
	h.roomsMu.Unlock() // 尽快释放锁
	logCtx.Info("Client unregistered from Hub")

	// --- 如果房间空了，停止该房间的订阅 ---
	if shouldStopSubscription {
		logCtx.Info("Room is now empty, stopping Redis subscription...")
		h.stopRoomSubscription(roomID) // 调用停止订阅的方法
	}
	// --- 订阅逻辑结束 ---
}

// runClientCleanupLoop 启动一个定时的循环来执行客户端清理检查
func (h *Hub) runClientCleanupLoop(interval time.Duration) {
	// 创建一个定时器
	ticker := time.NewTicker(interval)
	defer ticker.Stop() // 确保定时器停止

	logCtx := logrus.WithField("component", "client_cleanup")
	logCtx.Info("Client cleanup loop running...")

	// 循环执行清理检查
	for {
		select {
		case <-ticker.C: // 等待定时器触发
			h.performClientCleanup()
		// TODO: 添加一个退出信号通道，以便在 Hub 关闭时优雅停止这个 Goroutine
		// case <-h.shutdownCleanupChan:
		//     logCtx.Info("Received shutdown signal, stopping cleanup loop.")
		//     return
		}
	}
}

// performClientCleanup 执行一次客户端清理检查
func (h *Hub) performClientCleanup() {
	logCtx := logrus.WithField("operation", "performClientCleanup")
	logCtx.Debug("Starting client cleanup check...")

	// 1. 获取当前所有客户端的列表 (并发安全)
	h.roomsMu.RLock() // 加读锁以安全读取 rooms map
	// 创建一个足够大的 slice 来存储所有 client 指针
	allClients := make([]*Client, 0, len(h.rooms)*2) // 预估容量
	for _, roomClients := range h.rooms {
		for client := range roomClients {
			allClients = append(allClients, client)
		}
	}
	h.roomsMu.RUnlock() // 尽快释放读锁

	// 如果没有客户端，直接返回
	if len(allClients) == 0 {
		logCtx.Debug("No clients connected, skipping cleanup.")
		return
	}

	logCtx.Infof("Checking %d clients for inactivity...", len(allClients))
	// 2. 遍历客户端并尝试发送 Ping
	clientsToUnregister := make([]*Client, 0) // 收集需要注销的客户端
	pingTimeout := 5 * time.Second          // 为 Ping 写入设置一个较短的超时

	for _, client := range allClients {
		clientLogCtx := logCtx.WithFields(logrus.Fields{"room_id": client.RoomID(), "user_id": client.UserID()})

		// a. 设置写入超时
		err := client.conn.SetWriteDeadline(time.Now().Add(pingTimeout))
		if err != nil {
			// 设置超时失败通常意味着连接已经有问题
			clientLogCtx.WithError(err).Warn("Cleanup: Failed to set write deadline for ping, assuming inactive.")
			clientsToUnregister = append(clientsToUnregister, client)
			continue // 处理下一个
		}

		// b. 发送 Ping 控制帧
		// WriteMessage 对于控制帧是并发安全的
		if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			// 发送 Ping 失败，连接很可能已断开
			clientLogCtx.WithError(err).Warn("Cleanup: Failed to send ping message, marking client for unregister.")
			clientsToUnregister = append(clientsToUnregister, client)
            // 注意：这里不需要关闭 conn，unregister 流程会关闭
		} else {
			// Ping 发送成功
			// clientLogCtx.Debug("Cleanup: Ping sent successfully.")
			// 清除写入超时（虽然下一个 select 循环或 WritePump 会覆盖，但保持良好习惯）
			_ = client.conn.SetWriteDeadline(time.Time{})
		}
	}

	// 3. 处理需要注销的客户端
	if len(clientsToUnregister) > 0 {
		logCtx.Infof("Found %d potentially inactive clients to unregister.", len(clientsToUnregister))
		for _, client := range clientsToUnregister {
			// 将注销请求发送到 Hub 的主处理通道，保证线程安全
			unregisterMsg := HubMessage{Type: "unregister", Client: client}
			// 使用非阻塞发送，如果 Hub 处理不过来，暂时忽略（理论上不应发生）
			if !h.QueueMessage(unregisterMsg) { // 使用 QueueMessage
				logCtx.WithFields(logrus.Fields{"room_id": client.RoomID(), "user_id": client.UserID()}).Error("Cleanup: Hub message channel full, failed to send unregister request!")
                // 如果通道满了，可能 Hub 负载很高，或者有问题。
                // 极端情况下，可以直接关闭连接，但可能不安全。
                // client.CloseConn()
			} else {
                 logCtx.WithFields(logrus.Fields{"room_id": client.RoomID(), "user_id": client.UserID()}).Info("Cleanup: Unregister request sent to Hub.")
            }
		}
	} else {
		logCtx.Debug("Client cleanup check complete, no inactive clients found by ping failure.")
	}
}

// sendInitialSnapshot 异步获取并发送快照给新连接的客户端
func (h *Hub) sendInitialSnapshot(client *Client) {
	if client == nil {
		return
	}
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
		"state":   boardState,       // 使用从 Service 获取的 BoardState
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

	// Service 已经通过 stateRepo.PublishAction 发布了消息
	// Hub 不再需要调用 broadcast 方法来广播 Action
	if shouldBroadcast && processedAction != nil {
		logCtx.WithField("action_version", processedAction.Version).Info("Action processed by service and published via repository, queuing persistence task...")

		// --- 修改：使用 Asynq 将持久化任务入队 ---
		// 1. 创建任务 Payload
		taskPayloadBytes, err := tasks.NewActionPersistenceTask(*processedAction)
		if err != nil {
			logCtx.WithError(err).Error("Failed to create action persistence task payload")
			// 错误处理：记录日志，可能需要告警
			return // 暂时不入队
		}

		// 2. 创建 Asynq 任务
		// 第一个参数是任务类型，第二个是序列化后的 payload
		// 可以添加选项，如 asynq.MaxRetry(5), asynq.Timeout(1*time.Minute) 等
		task := asynq.NewTask(tasks.TypeActionPersistence, taskPayloadBytes)

		// 3. 将任务入队 (使用注入的 asynqClient)
		// EnqueueContext 是推荐的方法，可以传递上下文用于追踪等
		taskInfo, err := h.asynqClient.EnqueueContext(ctx, task)
		if err != nil {
			logCtx.WithError(err).Error("Failed to enqueue action persistence task")
			// 错误处理：记录日志，告警，可能需要重试或死信队列策略（虽然 Asynq 会处理重试）
		} else {
			logCtx.WithField("task_id", taskInfo.ID).WithField("queue", taskInfo.Queue).Debug("Action persistence task enqueued successfully")
		}
	} else {
		logCtx.Debug("Action processed but no broadcast needed (e.g., noop or error)")
	}
}

// --- 新增: Pub/Sub 订阅相关方法 ---

// startRoomSubscription 启动指定房间的 Redis Pub/Sub 订阅 Goroutine
func (h *Hub) startRoomSubscription(roomID uint) {
	h.subMu.Lock() // 保护对 roomSubscriptions 的访问
	defer h.subMu.Unlock()

	if _, exists := h.roomSubscriptions[roomID]; exists {
		logrus.Warnf("Hub: Subscription for room %d already exists", roomID)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.roomSubscriptions[roomID] = cancel // 存储 cancel 函数

	logCtx := logrus.WithFields(logrus.Fields{"room_id": roomID, "component": "subscriber"})
	logCtx.Info("Starting Redis subscription loop...")

	go h.roomSubscribeLoop(ctx, roomID, logCtx)
}

// stopRoomSubscription 停止指定房间的 Redis Pub/Sub 订阅 Goroutine
func (h *Hub) stopRoomSubscription(roomID uint) {
	h.subMu.Lock()
	defer h.subMu.Unlock()

	logCtx := logrus.WithFields(logrus.Fields{"room_id": roomID, "component": "subscriber"})

	if cancel, exists := h.roomSubscriptions[roomID]; exists {
		logCtx.Info("Stopping Redis subscription loop...")
		cancel() // 调用 cancel 函数通知 Goroutine 退出
		delete(h.roomSubscriptions, roomID)
		logCtx.Info("Subscription cancelled and removed.")
	} else {
		logCtx.Warnf("Attempted to stop subscription for room %d, but no active subscription found.", roomID)
	}
}

// stopAllSubscriptions 在 Hub 关闭时停止所有房间的订阅
func (h *Hub) StopAllSubscriptions() {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	logrus.Info("Hub shutting down, stopping all room subscriptions...")
	count := 0
	for roomID, cancel := range h.roomSubscriptions {
		cancel() // 通知 Goroutine 退出
		delete(h.roomSubscriptions, roomID)
		count++
	}
	logrus.Infof("Stopped %d room subscriptions.", count)
}

// roomSubscribeLoop 是每个房间订阅 Goroutine 的主循环体
func (h *Hub) roomSubscribeLoop(ctx context.Context, roomID uint, logCtx *logrus.Entry) {
	// 构造频道名称
	channel := fmt.Sprintf("%sroom:%d:pubsub", h.keyPrefix, roomID)
	// 使用 Hub 持有的 Redis Client 订阅频道
	pubsub := h.redisClient.Subscribe(ctx, channel)

	// 检查订阅是否成功或被取消
	_, err := pubsub.Receive(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, redis.ErrClosed) { // 忽略 context canceled 和 redis closed 错误
			logCtx.WithError(err).Errorf("Failed to subscribe to Redis channel %s", channel)
		} else {
			logCtx.Info("Subscription cancelled or Redis closed before confirmation.")
		}
		// 确保从 Hub 的 map 中移除 (即使 stop 被调用，也做一次检查)
		h.subMu.Lock()
		delete(h.roomSubscriptions, roomID)
		h.subMu.Unlock()
		_ = pubsub.Close()
		return
	}
	logCtx.Infof("Successfully subscribed to Redis channel %s", channel)
	msgChan := pubsub.Channel() // 获取消息通道

	// 在 Goroutine 退出时确保取消订阅和关闭
	defer func() {
		// 使用后台 context 取消订阅，因为原始 ctx 可能已关闭
		bgCtx := context.Background()
		if err := pubsub.Unsubscribe(bgCtx, channel); err != nil {
			logCtx.WithError(err).Warnf("Error unsubscribing from channel %s on exit", channel)
		}
		if err := pubsub.Close(); err != nil {
			logCtx.WithError(err).Warn("Error closing pubsub connection on exit")
		}
		// 确保从 Hub map 中移除
		h.subMu.Lock()
		delete(h.roomSubscriptions, roomID)
		h.subMu.Unlock()
		logCtx.Info("Subscription loop stopped.")
	}()

	for {
		select {
		case <-ctx.Done(): // 监听 Hub 主动停止信号
			logCtx.Info("Context cancelled, exiting subscription loop.")
			return // 退出

		case msg, ok := <-msgChan: // 从 Redis 接收消息
			if !ok {
				logCtx.Warn("Redis Pub/Sub channel closed unexpectedly.")
				return // 退出
			}

			logCtx.Debugf("Received message from Redis (size: %d)", len(msg.Payload))

			// 解析 Action 以获取 UserID
			var receivedAction domain.Action
			if err := json.Unmarshal([]byte(msg.Payload), &receivedAction); err != nil {
				logCtx.WithError(err).Warn("Failed to unmarshal action from Pub/Sub")
				continue // 忽略错误消息
			}

			// 将消息广播给房间内所有客户端 (排除发送者)
			// 使用读锁安全访问 rooms map
			h.roomsMu.RLock()
			if roomClients, exists := h.rooms[roomID]; exists {
				for client := range roomClients {
					// **排除原始发送者**
					if client.UserID() != receivedAction.UserID {
						select {
						case client.send <- []byte(msg.Payload): // 发送原始 JSON
						default:
							logCtx.WithField("receiver_user_id", client.UserID()).Warn("Client send channel full when forwarding Pub/Sub message")
						}
					}
				}
			}
			h.roomsMu.RUnlock() // 释放读锁
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
