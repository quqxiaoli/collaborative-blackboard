package ws

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"context" // Import context
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8" // Ensure redis client is imported
	"github.com/sirupsen/logrus"
)

// processActions runs in a separate goroutine, consuming actions from actionChan,
// applying operational transformation, updating Redis state, batching for DB, and publishing.
func processActions() {
	for task := range actionChan {
		logCtx := logrus.WithFields(logrus.Fields{
			"room_id": task.RoomID,
			"user_id": task.Action.UserID,
			"version": task.Action.Version,
		})
		logCtx.Debug("Processing action task")

		// Apply Operational Transformation (OT) logic
		finalAction := applyOperationalTransform(task)

		// If the action wasn't transformed into a no-op
		if finalAction.ActionType != "noop" {
			logCtx.WithField("final_action_type", finalAction.ActionType).Debug("Action not noop, proceeding")
			// Update the real-time state in Redis
			updateRedisState(task.Context, task.RoomID, finalAction, logCtx)
			// Add the action to the batch for database persistence
			batchAction(finalAction)
			// Publish the action to other clients in the room via Redis Pub/Sub
			publishAction(task.Context, task.RoomID, finalAction, logCtx)
		} else {
			logCtx.Debug("Action transformed to noop, skipping further processing.")
		}
	}
}

// applyOperationalTransform attempts to resolve conflicts with recently processed actions.
// It fetches recent actions from a Redis list (acting as a short-term buffer).
// NOTE: This is a simplified OT implementation focusing on direct conflicts at the same coordinate.
// A robust OT system would require a more complex algorithm to handle various conflict types and transformations.
func applyOperationalTransform(task ActionTask) models.Action {
	logCtx := logrus.WithFields(logrus.Fields{
		"room_id": task.RoomID,
		"user_id": task.Action.UserID,
		"version": task.Action.Version,
	})

	// Key for the Redis list storing recent actions for this room
	queueKey := fmt.Sprintf("room:%d:actions", task.RoomID)
	// Fetch recent actions (e.g., last 100) from the Redis list
	// Using the background context from the task for Redis operations
	queuedActionsStr, err := config.Redis.LRange(task.Context, queueKey, 0, -1).Result()
	if err != nil && err != redis.Nil {
		logCtx.WithError(err).Error("OT: Failed to get recent actions queue from Redis")
		// Proceed with the original action if Redis fails, might lead to inconsistencies
		return task.Action
	}

	finalAction := task.Action
	// Iterate through recent actions (from newest to oldest as stored by RPush/LRange)
	for i := len(queuedActionsStr) - 1; i >= 0; i-- {
		actionStr := queuedActionsStr[i]
		var queuedAction models.Action
		if err := json.Unmarshal([]byte(actionStr), &queuedAction); err == nil {
			// Only transform against actions with a version less than or equal to the current action's version
			// This assumes versions are sequential and helps manage the transformation window.
			// A more robust OT would likely involve transforming against a specific history based on the incoming action's base version.
			if queuedAction.Version < finalAction.Version {
				// Apply the transformation function
				transformedAction, _ := transform(finalAction, queuedAction) // We only care about the transformation of finalAction
				if transformedAction.ActionType == "noop" {
					logCtx.WithField("conflicting_action_version", queuedAction.Version).Debug("OT: Action transformed to noop")
					finalAction = transformedAction
					break // If it becomes a noop, no further transformation needed
				}
				finalAction = transformedAction // Update the action for the next comparison
			}
		} else {
			logCtx.WithError(err).WithField("action_str", actionStr).Warn("OT: Failed to unmarshal queued action")
		}
	}
	return finalAction
}

// transform 实现了一个基本的 Operational Transformation (OT) 逻辑。
// 这是一个非常简化的版本（Last Write Wins - LWW），主要基于版本号和时间戳来解决冲突。
// 如果两个操作影响同一坐标：
// 1. 版本号高的操作优先。
// 2. 如果版本号相同，时间戳晚的操作优先。
// 3. 如果版本号和时间戳都相同（理论上不太可能，除非系统时钟有问题或并发极高），则任意选择一个（这里选择 op1）。
// 优先级较低的操作会被标记为 "noop" (无操作)。
//
// 注意：这个实现没有处理更复杂的 OT 场景，例如操作的意图保留（intention preservation）
// 或对不同类型操作（如插入/删除文本）的转换。对于简单的绘图/擦除操作，这种 LWW 策略可能足够。
// A real-world OT implementation would be significantly more complex.
func transform(op1, op2 models.Action) (models.Action, models.Action) {
	// 如果任一操作已经是 no-op，则按原样返回。
	if op1.ActionType == "noop" || op2.ActionType == "noop" {
		return op1, op2
	}

	// Only handle 'draw' and 'erase' types for this simple transformation
	if (op1.ActionType != "draw" && op1.ActionType != "erase") || (op2.ActionType != "draw" && op2.ActionType != "erase") {
		return op1, op2 // No transformation for other types in this simple model
	}

	// Parse the data for both operations. If parsing fails, return original ops.
	data1, err1 := op1.ParseData()
	data2, err2 := op2.ParseData()
	// If data is missing or unparseable for draw/erase, something is wrong, but don't modify.
	if err1 != nil || err2 != nil {
		logFields := logrus.Fields{"op1_id": op1.ID, "op2_id": op2.ID}
		if err1 != nil {
			logFields["op1_error"] = err1
		}
		if err2 != nil {
			logFields["op2_error"] = err2
		}
		logrus.WithFields(logFields).Warn("OT: Failed to parse data for one or both operations in transform")
		return op1, op2
	}

	// Check if both operations target the same coordinate.
	if data1.X == data2.X && data1.Y == data2.Y {
		// Conflict resolution: prioritize based on version, then timestamp.
		// The operation with the lower priority becomes a no-op.
		if op1.Version > op2.Version || (op1.Version == op2.Version && op1.Timestamp.After(op2.Timestamp)) {
			// op1 takes precedence
			nop := op2
			nop.ActionType = "noop" // Mark op2 as no-op
			nop.Data = ""           // Clear data for no-op
			logrus.WithFields(logrus.Fields{
				"op1_id": op1.ID, "op1_version": op1.Version, "op1_ts": op1.Timestamp,
				"op2_id": op2.ID, "op2_version": op2.Version, "op2_ts": op2.Timestamp,
			}).Debug("OT Conflict: op1 wins, op2 becomes noop")
			return op1, nop
		} else if op2.Version > op1.Version || (op1.Version == op2.Version && op2.Timestamp.After(op1.Timestamp)) {
			// op2 takes precedence
			nop := op1
			nop.ActionType = "noop" // Mark op1 as no-op
			nop.Data = ""           // Clear data for no-op
			logrus.WithFields(logrus.Fields{
				"op1_id": op1.ID, "op1_version": op1.Version, "op1_ts": op1.Timestamp,
				"op2_id": op2.ID, "op2_version": op2.Version, "op2_ts": op2.Timestamp,
			}).Debug("OT Conflict: op2 wins, op1 becomes noop")
			return nop, op2
		}
		// If versions and timestamps are identical (highly unlikely but possible), arbitrarily make op2 a no-op.
		// This case might indicate a potential issue or need for finer-grained tie-breaking (e.g., using UserID).
		logrus.WithFields(logrus.Fields{
			"op1_id": op1.ID, "op1_version": op1.Version, "op1_ts": op1.Timestamp,
			"op2_id": op2.ID, "op2_version": op2.Version, "op2_ts": op2.Timestamp,
		}).Warn("OT Conflict: Identical version and timestamp, arbitrarily making op2 noop")
		nop := op2
		nop.ActionType = "noop"
		nop.Data = ""
		return op1, nop
	}

	// If operations do not conflict (target different coordinates), return them unchanged.
	return op1, op2
}

// updateRedisState 根据操作更新 Redis 中的当前画板状态。
// 它还会增加操作计数并将操作添加到最近操作列表中。
func updateRedisState(ctx context.Context, roomID uint, action models.Action, logCtx *logrus.Entry) {
	roomIDStr := strconv.FormatUint(uint64(roomID), 10)
	stateKey := fmt.Sprintf("room:%s:state", roomIDStr)     // 存储画板状态的哈希 (坐标 -> 颜色)
	opCountKey := fmt.Sprintf("room:%s:op_count", roomIDStr) // 用于快照频率逻辑的计数器
	queueKey := fmt.Sprintf("room:%s:actions", roomIDStr)   // 存储用于 OT 的最近操作的列表

	// 使用 Redis pipeline 以保证原子性和效率
	// 注意：虽然 TxPipeline 提供了原子性，但这里的操作序列（Incr, Expire, RPush, LTrim, HSet/HDel）
	// 并非严格意义上的事务，因为它们依赖于多个键。Redis 的 WATCH/MULTI/EXEC 可能更适合严格事务，但这会增加复杂性。
	// 对于此场景，Pipeline 提供了性能优势。
	pipe := config.Redis.TxPipeline()

	// Increment operation count and set expiry (e.g., 1 hour)
	pipe.Incr(ctx, opCountKey)
	pipe.Expire(ctx, opCountKey, 1*time.Hour)

	// Add the action (as JSON) to the right end of the list (queue)
	actionBytes, err := json.Marshal(action)
	if err != nil {
		logCtx.WithError(err).Error("Failed to marshal action for Redis queue")
		// Don't proceed with pipeline if marshalling fails
		return
	}
	pipe.RPush(ctx, queueKey, string(actionBytes))
	// Trim the list to keep only the last N actions (e.g., 100) for OT purposes
	pipe.LTrim(ctx, queueKey, -100, -1) // Keep elements from index -100 to -1 (last 100)

	// Update the board state hash based on the action type
	data, err := action.ParseData()
	if err == nil { // Only update state if data is valid
		key := fmt.Sprintf("%d:%d", data.X, data.Y) // Use fmt.Sprintf for clarity
		if action.ActionType == "draw" {
			pipe.HSet(ctx, stateKey, key, data.Color)
		} else if action.ActionType == "erase" {
			pipe.HDel(ctx, stateKey, key)
		}
		// Add handling for other action types like "clear" if needed
		// else if action.ActionType == "clear" {
		//  pipe.Del(ctx, stateKey) // Example: Clear the entire board state
		// }
	} else {
		logCtx.WithError(err).Warn("Failed to parse action data for Redis state update")
	}

	// Execute the pipeline
	_, err = pipe.Exec(ctx)
	if err != nil {
		logCtx.WithError(err).Error("Failed to execute Redis pipeline for state update")
	} else {
		logCtx.Debug("Redis state updated successfully")
	}
}

// batchAction adds an action to the batch for later database insertion.
func batchAction(action models.Action) {
	batchMu.Lock()
	defer batchMu.Unlock()
	actionBatch = append(actionBatch, action)
	logrus.WithField("action_id", action.ID).Debugf("Action added to batch. Batch size: %d", len(actionBatch))
}

// flushActionBatch runs in a goroutine and periodically saves the accumulated action batch to the database.
func flushActionBatch() {
	// Ticker for periodic flushing, e.g., every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		batchMu.Lock()
		// Check if there are actions to flush
		if len(actionBatch) == 0 {
			batchMu.Unlock()
			continue // Nothing to do, wait for the next tick
		}

		// Create a copy of the batch to save, so we can clear the original batch
		// This prevents holding the lock during the potentially long DB operation
		batchToSave := make([]models.Action, len(actionBatch))
		copy(batchToSave, actionBatch)
		actionBatch = []models.Action{} // Clear the original batch immediately after copying
		batchMu.Unlock()  // Unlock as soon as possible

		logCtx := logrus.WithField("batch_size", len(batchToSave))
		logCtx.Info("Attempting to flush action batch to database.")

		// Use a background context for the database operation as it's not tied to a specific request
		ctx := context.Background()
		tx := config.DB.WithContext(ctx).Begin()
		if tx.Error != nil {
			logCtx.WithError(tx.Error).Error("Failed to begin transaction for batch save.")
			// If beginning transaction fails, re-add the batch for the next attempt
			batchMu.Lock()
			actionBatch = append(batchToSave, actionBatch...) // Prepend to avoid reordering if new actions arrived
			batchMu.Unlock()
			continue // Skip to the next ticker cycle
		}

		// Perform the batch insert within the transaction
		if err := tx.Create(&batchToSave).Error; err != nil {
			tx.Rollback() // Rollback on error
			logCtx.WithError(err).Error("Failed to batch save actions, rolling back transaction.")
			// Re-add the batch for the next attempt
			batchMu.Lock()
			actionBatch = append(batchToSave, actionBatch...)
			batchMu.Unlock()
		} else {
			// Commit the transaction if the insert was successful
			if err := tx.Commit().Error; err != nil {
				logCtx.WithError(err).Error("Failed to commit transaction for batch save.")
				// Re-add the batch for the next attempt, as commit failed
				batchMu.Lock()
				actionBatch = append(batchToSave, actionBatch...)
				batchMu.Unlock()
			} else {
				logCtx.Info("Successfully flushed action batch to database.")
			}
		}
	}
}

// publishAction publishes the processed action to the Redis Pub/Sub channel for the room.
func publishAction(ctx context.Context, roomID uint, action models.Action, logCtx *logrus.Entry) {
	// Marshal the action to JSON
	actionBytes, err := json.Marshal(action)
	if err != nil {
		logCtx.WithError(err).WithField("action_id", action.ID).Error("Failed to marshal action for publishing")
		return
	}

	// Payload format: "version|json_data" (Consider a more robust format like JSON object if needed)
	payload := fmt.Sprintf("%d|%s", action.Version, string(actionBytes))
	redisChannel := fmt.Sprintf("room:%d", roomID)

	// Publish the message using the provided context
	cmd := config.Redis.Publish(ctx, redisChannel, payload)
	if err := cmd.Err(); err != nil {
		logCtx.WithError(err).WithFields(logrus.Fields{
			"channel": redisChannel,
			"payload_size": len(payload),
		}).Error("Failed to publish action to Redis")
	} else {
		logCtx.WithFields(logrus.Fields{
			"channel": redisChannel,
			"subscribers": cmd.Val(), // Log number of subscribers that received the message
		}).Debug("Action published to Redis")
	}
}
