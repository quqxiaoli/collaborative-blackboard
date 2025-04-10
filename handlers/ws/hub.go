package ws

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// Client 代表一个连接到 Hub 的 WebSocket 客户端。
type Client struct {
	hub    *Hub            // 指向 Hub 的指针
	conn   *websocket.Conn // WebSocket 连接
	roomID uint            // 客户端所在的房间 ID
	userID uint            // 客户端的用户 ID
	send   chan []byte     // 用于向此客户端发送消息的缓冲通道
}

// Hub 维护活跃客户端集合并广播消息。
type Hub struct {
	// 注册请求通道。
	register chan *Client

	// 注销请求通道。
	unregister chan *Client

	// 按 RoomID 组织的客户端集合。
	// map[roomID]map[*Client]bool
	rooms map[uint]map[*Client]bool

	// 保护 rooms map 的互斥锁。
	roomsMu sync.RWMutex

	// 从客户端接收到的需要处理的操作。
	actionChan chan ActionTask

	// 批量存储待写入数据库的操作。
	actionBatch []models.Action

	// 保护 actionBatch 的互斥锁。
	batchMu sync.Mutex
}

// NewHub 创建并返回一个新的 Hub 实例。
func NewHub() *Hub {
	return &Hub{
		register:   make(chan *Client),
		unregister: make(chan *Client),
		rooms:      make(map[uint]map[*Client]bool),
		actionChan: make(chan ActionTask, 256), // 增加缓冲区大小
		// actionBatch 初始化为空 slice
		// batchMu 自动初始化
	}
}

// Run 启动 Hub 的主循环，监听并处理来自通道的消息。
func (h *Hub) Run() {
	// 启动后台任务
	go h.processActions()   // 处理 actionChan 中的任务
	go h.flushActionBatch() // 定期将 actionBatch 写入数据库
	go h.cleanupClients()   // 定期清理不活跃的 WebSocket 连接

	log := logrus.WithField("component", "hub")
	log.Info("Hub 正在运行...")

	for {
		select {
		case client := <-h.register:
			h.roomsMu.Lock()
			if _, ok := h.rooms[client.roomID]; !ok {
				h.rooms[client.roomID] = make(map[*Client]bool)
			}
			h.rooms[client.roomID][client] = true
			h.roomsMu.Unlock()
			log.WithFields(logrus.Fields{
				"room_id": client.roomID,
				"user_id": client.userID,
			}).Info("客户端已注册到 Hub")

		case client := <-h.unregister:
			h.roomsMu.Lock()
			if roomClients, ok := h.rooms[client.roomID]; ok {
				if _, ok := roomClients[client]; ok {
					delete(roomClients, client)
					close(client.send) // 关闭客户端的发送通道
					if len(roomClients) == 0 {
						delete(h.rooms, client.roomID) // 如果房间为空，则删除房间条目
						log.WithField("room_id", client.roomID).Info("房间已空，从 Hub 中移除")
					}
				}
			}
			h.roomsMu.Unlock()
			log.WithFields(logrus.Fields{
				"room_id": client.roomID,
				"user_id": client.userID,
			}).Info("客户端已从 Hub 注销")

			// 可选：如果房间空了，可以在这里触发一些清理逻辑，比如取消 Redis PubSub 订阅（如果订阅在 Hub 中管理）
		}
	}
}

// broadcast 将消息发送给指定房间的所有客户端，除了发送者自己。
func (h *Hub) broadcast(roomID uint, message []byte, sender *Client) {
	h.roomsMu.RLock()
	defer h.roomsMu.RUnlock()

	logCtx := logrus.WithFields(logrus.Fields{
		"room_id": roomID,
		"sender_user_id": sender.userID,
		"message_size": len(message),
	})

	if roomClients, ok := h.rooms[roomID]; ok {
		logCtx.Debugf("向 %d 个客户端广播消息", len(roomClients)-1) // 减去发送者
		for client := range roomClients {
			if client != sender { // 不发送给发送者自己
				select {
				case client.send <- message:
					// 消息成功放入通道
				default:
					// 如果客户端的发送通道已满，可能表示客户端处理缓慢或已断开
					// 关闭并注销此客户端
					logCtx.WithField("receiver_user_id", client.userID).Warn("客户端发送通道已满或阻塞，关闭连接")
					close(client.send)
					delete(roomClients, client)
					// 注意：这里在 RLock 内部修改了 map，严格来说不安全。
					// 更安全的做法是标记要删除的客户端，然后在 RUnlock 后再获取写锁进行删除。
					// 或者，将删除操作发送到 unregister 通道。
					// go func(c *Client) { h.unregister <- c }(client) // 异步注销
				}
			}
		}
	} else {
		logCtx.Debug("广播时未找到房间或房间内无其他客户端")
	}
}

// --- 从 ot.go 移动过来的方法 ---

// processActions 处理来自 actionChan 的操作任务。
func (h *Hub) processActions() {
	for task := range h.actionChan { // 使用 h.actionChan
		logCtx := logrus.WithFields(logrus.Fields{
			"room_id": task.RoomID,
			"user_id": task.Action.UserID,
			"version": task.Action.Version,
		})
		logCtx.Debug("处理操作任务") // Processing action task

		// 应用操作转换 (OT) 逻辑
		finalAction := h.applyOperationalTransform(task) // 调用 Hub 的方法

		// 如果操作未被转换为 noop (无操作)
		if finalAction.ActionType != "noop" {
			logCtx.WithField("final_action_type", finalAction.ActionType).Debug("操作非 noop，继续处理") // Action not noop, proceeding
			// 更新 Redis 中的实时状态
			h.updateRedisState(task.Context, task.RoomID, finalAction, logCtx) // 调用 Hub 的方法
			// 将操作添加到批处理队列以供数据库持久化
			h.batchAction(finalAction) // 调用 Hub 的方法
			// 通过 Redis Pub/Sub 将操作发布给房间内的其他客户端
			h.publishAction(task.Context, task.RoomID, finalAction, logCtx) // 调用 Hub 的方法
		} else {
			logCtx.Debug("操作被转换为 noop，跳过后续处理。") // Action transformed to noop, skipping further processing.
		}
	}
}

// applyOperationalTransform 尝试解决与最近处理的操作之间的冲突。
func (h *Hub) applyOperationalTransform(task ActionTask) models.Action {
	logCtx := logrus.WithFields(logrus.Fields{
		"room_id": task.RoomID,
		"user_id": task.Action.UserID,
		"version": task.Action.Version,
	})

	queueKey := fmt.Sprintf("room:%d:actions", task.RoomID)
	queuedActionsStr, err := config.Redis.LRange(task.Context, queueKey, 0, -1).Result()
	if err != nil && err != redis.Nil {
		logCtx.WithError(err).Error("OT: 从 Redis 获取最近操作队列失败")
		return task.Action
	}

	finalAction := task.Action
	for i := len(queuedActionsStr) - 1; i >= 0; i-- {
		actionStr := queuedActionsStr[i]
		var queuedAction models.Action
		if err := json.Unmarshal([]byte(actionStr), &queuedAction); err == nil {
			if queuedAction.Version < finalAction.Version {
				transformedAction, _ := transform(finalAction, queuedAction)
				if transformedAction.ActionType == "noop" {
					logCtx.WithField("conflicting_action_version", queuedAction.Version).Debug("OT: 操作被转换为 noop")
					finalAction = transformedAction
					break
				}
				finalAction = transformedAction
			}
		} else {
			logCtx.WithError(err).WithField("action_str", actionStr).Warn("OT: 反序列化队列中的操作失败")
		}
	}
	return finalAction
}

// transform 实现了一个基本的 Operational Transformation (OT) 逻辑。
// (保持不变，因为它不直接依赖 Hub 状态)
func transform(op1, op2 models.Action) (models.Action, models.Action) {
	// 如果任一操作已经是 no-op，则按原样返回。
	if op1.ActionType == "noop" || op2.ActionType == "noop" {
		return op1, op2
	}

	// 此简单转换仅处理 'draw' 和 'erase' 类型
	if (op1.ActionType != "draw" && op1.ActionType != "erase") || (op2.ActionType != "draw" && op2.ActionType != "erase") {
		return op1, op2 // 对于此简单模型中的其他类型，不进行转换
	}

	// 解析两个操作的数据。如果解析失败，则返回原始操作。
	data1, err1 := op1.ParseData()
	data2, err2 := op2.ParseData()
	// 如果 draw/erase 操作的数据丢失或无法解析，说明有问题，但不修改。
	if err1 != nil || err2 != nil {
		logFields := logrus.Fields{"op1_id": op1.ID, "op2_id": op2.ID}
		if err1 != nil {
			logFields["op1_error"] = err1
		}
		if err2 != nil {
			logFields["op2_error"] = err2
		}
		logrus.WithFields(logFields).Warn("OT: 转换中解析一个或两个操作的数据失败") // OT: Failed to parse data for one or both operations in transform
		return op1, op2
	}

	// 检查两个操作是否针对同一坐标。
	if data1.X == data2.X && data1.Y == data2.Y {
		// 冲突解决：基于版本号优先，然后是时间戳。
		// 优先级较低的操作变为 no-op。
		if op1.Version > op2.Version || (op1.Version == op2.Version && op1.Timestamp.After(op2.Timestamp)) {
			// op1 优先
			nop := op2
			nop.ActionType = "noop" // 将 op2 标记为 no-op
			nop.Data = ""           // 清除 no-op 的数据
			logrus.WithFields(logrus.Fields{
				"op1_id": op1.ID, "op1_version": op1.Version, "op1_ts": op1.Timestamp,
				"op2_id": op2.ID, "op2_version": op2.Version, "op2_ts": op2.Timestamp,
			}).Debug("OT 冲突：op1 获胜，op2 变为 noop") // OT Conflict: op1 wins, op2 becomes noop
			return op1, nop
		} else if op2.Version > op1.Version || (op1.Version == op2.Version && op2.Timestamp.After(op1.Timestamp)) {
			// op2 优先
			nop := op1
			nop.ActionType = "noop" // 将 op1 标记为 no-op
			nop.Data = ""           // 清除 no-op 的数据
			logrus.WithFields(logrus.Fields{
				"op1_id": op1.ID, "op1_version": op1.Version, "op1_ts": op1.Timestamp,
				"op2_id": op2.ID, "op2_version": op2.Version, "op2_ts": op2.Timestamp,
			}).Debug("OT 冲突：op2 获胜，op1 变为 noop") // OT Conflict: op2 wins, op1 becomes noop
			return nop, op2
		}
		// 如果版本号和时间戳都相同（不太可能但可能发生），则任意将 op2 设为 no-op。
		// 这种情况可能表明存在潜在问题或需要更细粒度的决胜规则（例如，使用 UserID）。
		logrus.WithFields(logrus.Fields{
			"op1_id": op1.ID, "op1_version": op1.Version, "op1_ts": op1.Timestamp,
			"op2_id": op2.ID, "op2_version": op2.Version, "op2_ts": op2.Timestamp,
		}).Warn("OT 冲突：版本号和时间戳相同，任意将 op2 设为 noop") // OT Conflict: Identical version and timestamp, arbitrarily making op2 noop
		nop := op2
		nop.ActionType = "noop"
		nop.Data = ""
		return op1, nop
	}

	// 如果操作不冲突（目标不同坐标），则保持不变返回。
	return op1, op2
}


// updateRedisState 根据操作更新 Redis 中的当前画板状态。
func (h *Hub) updateRedisState(ctx context.Context, roomID uint, action models.Action, logCtx *logrus.Entry) {
	roomIDStr := strconv.FormatUint(uint64(roomID), 10)
	stateKey := fmt.Sprintf("room:%s:state", roomIDStr)
	opCountKey := fmt.Sprintf("room:%s:op_count", roomIDStr)
	queueKey := fmt.Sprintf("room:%s:actions", roomIDStr)

	pipe := config.Redis.TxPipeline()
	pipe.Incr(ctx, opCountKey)
	pipe.Expire(ctx, opCountKey, 1*time.Hour)

	actionBytes, err := json.Marshal(action)
	if err != nil {
		logCtx.WithError(err).Error("为 Redis 队列序列化操作失败")
		return
	}
	pipe.RPush(ctx, queueKey, string(actionBytes))
	pipe.LTrim(ctx, queueKey, -100, -1)

	data, err := action.ParseData()
	if err == nil {
		key := fmt.Sprintf("%d:%d", data.X, data.Y)
		if action.ActionType == "draw" {
			pipe.HSet(ctx, stateKey, key, data.Color)
		} else if action.ActionType == "erase" {
			pipe.HDel(ctx, stateKey, key)
		}
	} else {
		logCtx.WithError(err).Warn("为 Redis 状态更新解析操作数据失败")
	}

	_, execErr := pipe.Exec(ctx)
	if execErr != nil {
		logCtx.WithError(execErr).Error("执行 Redis pipeline 进行状态更新失败")
	} else {
		logCtx.Debug("Redis 状态更新成功")
	}
}

// batchAction 将操作添加到批处理队列。
func (h *Hub) batchAction(action models.Action) {
	h.batchMu.Lock()
	defer h.batchMu.Unlock()
	h.actionBatch = append(h.actionBatch, action) // 使用 h.actionBatch
	logrus.WithField("action_id", action.ID).Debugf("操作已添加到批处理。批处理大小: %d", len(h.actionBatch))
}

// flushActionBatch 定期将批处理的操作保存到数据库。
func (h *Hub) flushActionBatch() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		h.batchMu.Lock()
		if len(h.actionBatch) == 0 {
			h.batchMu.Unlock()
			continue
		}

		batchToSave := make([]models.Action, len(h.actionBatch))
		copy(batchToSave, h.actionBatch)
		h.actionBatch = []models.Action{} // 清空 Hub 中的批处理
		h.batchMu.Unlock()

		logCtx := logrus.WithField("batch_size", len(batchToSave))
		logCtx.Info("尝试将操作批处理刷新到数据库。")

		ctx := context.Background()
		tx := config.DB.WithContext(ctx).Begin()
		if tx.Error != nil {
			logCtx.WithError(tx.Error).Error("为批处理保存启动事务失败。")
			h.batchMu.Lock()
			h.actionBatch = append(batchToSave, h.actionBatch...) // 失败时放回批处理
			h.batchMu.Unlock()
			continue
		}

		if err := tx.Create(&batchToSave).Error; err != nil {
			tx.Rollback()
			logCtx.WithError(err).Error("批量保存操作失败，正在回滚事务。")
			h.batchMu.Lock()
			h.actionBatch = append(batchToSave, h.actionBatch...) // 失败时放回批处理
			h.batchMu.Unlock()
		} else {
			if err := tx.Commit().Error; err != nil {
				logCtx.WithError(err).Error("提交批处理保存事务失败。")
				h.batchMu.Lock()
				h.actionBatch = append(batchToSave, h.actionBatch...) // 提交失败时放回批处理
				h.batchMu.Unlock()
			} else {
				logCtx.Info("成功将操作批处理刷新到数据库。")
			}
		}
	}
}

// publishAction 将处理后的操作发布到 Redis Pub/Sub。
func (h *Hub) publishAction(ctx context.Context, roomID uint, action models.Action, logCtx *logrus.Entry) {
	actionBytes, err := json.Marshal(action)
	if err != nil {
		logCtx.WithError(err).WithField("action_id", action.ID).Error("为发布序列化操作失败")
		return
	}

	payload := string(actionBytes)
	redisChannel := fmt.Sprintf("room:%d", roomID)

	// 使用后台 context 发布消息
	pubCtx := context.Background()
	cmd := config.Redis.Publish(pubCtx, redisChannel, payload)
	if err := cmd.Err(); err != nil {
		logCtx.WithError(err).WithFields(logrus.Fields{
			"channel":      redisChannel,
			"payload_size": len(payload),
		}).Error("向 Redis 发布操作失败")
	} else {
		logCtx.WithFields(logrus.Fields{
			"channel":     redisChannel,
			"subscribers": cmd.Val(),
		}).Debug("操作已发布到 Redis")
	}
}

// --- 从 ws.go 移动过来的方法 ---

// cleanupClients 定期检查并清理不活跃的 WebSocket 连接
func (h *Hub) cleanupClients() {
	pingInterval := 1 * time.Minute
	writeWait := 10 * time.Second
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for range ticker.C {
		h.roomsMu.Lock() // 使用 Hub 的锁
		if len(h.rooms) == 0 {
			h.roomsMu.Unlock()
			continue
		}
		logrus.Debugf("运行清理任务，检查 %d 个房间。", len(h.rooms))

		clientsToUnregister := []*Client{} // 收集需要注销的客户端

		for roomID, roomClients := range h.rooms {
			logCtxRoom := logrus.WithField("room_id", roomID)
			if len(roomClients) == 0 {
				logCtxRoom.Debug("清理：房间为空，跳过。")
				continue
			}
			logCtxRoom.Debugf("清理：检查房间中的 %d 个客户端。", len(roomClients))

			for client := range roomClients {
				logCtxClient := logCtxRoom.WithField("user_id", client.userID)
				// 设置写入超时
				if err := client.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
					logCtxClient.WithError(err).Warn("清理：设置 ping 写入超时失败。")
					clientsToUnregister = append(clientsToUnregister, client) // 标记以便稍后注销
					continue
				}

				// 发送 Ping 消息
				if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					logCtxClient.WithError(err).Warn("清理：发送 ping 消息失败，标记为注销。")
					clientsToUnregister = append(clientsToUnregister, client) // 标记以便稍后注销
				} else {
					logCtxClient.Debug("清理：已发送 ping 消息。")
					// Ping 发送成功，移除写入超时设置
					if err := client.conn.SetWriteDeadline(time.Time{}); err != nil {
						logCtxClient.WithError(err).Warn("清理：重置 ping 后写入超时失败。")
					}
				}
			}
		}
		h.roomsMu.Unlock() // 解锁 rooms map

		// 在锁外执行注销操作
		if len(clientsToUnregister) > 0 {
			logrus.Infof("清理：准备注销 %d 个客户端。", len(clientsToUnregister))
			for _, client := range clientsToUnregister {
				// 关闭连接并从 Hub 注销
				client.conn.Close()       // 关闭底层连接
				h.unregister <- client // 通过通道请求注销
			}
		}
	}
}
