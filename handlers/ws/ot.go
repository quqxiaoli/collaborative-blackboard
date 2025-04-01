package ws

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func processActions() {
	for task := range actionChan {
		finalAction := applyOperationalTransform(task)
		if finalAction.ActionType != "noop" {
			updateRedisState(task.Context, task.RoomID, finalAction)
			batchAction(finalAction)
			publishAction(task.Context, task.RoomID, finalAction)
		}
	}
}

func applyOperationalTransform(task ActionTask) models.Action {
	queueKey := "room:" + strconv.FormatUint(uint64(task.RoomID), 10) + ":actions"
	queuedActions, err := config.Redis.LRange(task.Context.Request.Context(), queueKey, 0, -1).Result()
	if err != nil {
		logrus.WithError(err).Error("Failed to get action queue")
		return task.Action
	}

	finalAction := task.Action
	for _, actionStr := range queuedActions {
		var queuedAction models.Action
		if json.Unmarshal([]byte(actionStr), &queuedAction) == nil {
			finalAction, _ = transform(finalAction, queuedAction)
		}
	}
	return finalAction
}

func transform(op1, op2 models.Action) (models.Action, models.Action) {
	if op1.ActionType == "noop" || op2.ActionType == "noop" {
		return op1, op2
	}
	data1, err1 := op1.ParseData()
	data2, err2 := op2.ParseData()
	if err1 != nil || err2 != nil {
		return op1, op2
	}

	if data1.X == data2.X && data1.Y == data2.Y {
		if op1.Version > op2.Version || (op1.Version == op2.Version && op1.Timestamp.After(op2.Timestamp)) {
			nop := op2
			nop.ActionType = "noop"
			nop.Data = ""
			return op1, nop
		}
		nop := op1
		nop.ActionType = "noop"
		nop.Data = ""
		return nop, op2
	}
	return op1, op2
}

func updateRedisState(ctx *gin.Context, roomID uint, action models.Action) {
	stateKey := "room:" + strconv.FormatUint(uint64(roomID), 10) + ":state"
	opCountKey := "room:" + strconv.FormatUint(uint64(roomID), 10) + ":op_count"
	queueKey := "room:" + strconv.FormatUint(uint64(roomID), 10) + ":actions"

	pipe := config.Redis.TxPipeline()
	pipe.Incr(ctx.Request.Context(), opCountKey)
	pipe.Expire(ctx.Request.Context(), opCountKey, 1*time.Hour)
	actionBytes, _ := json.Marshal(action)
	pipe.RPush(ctx.Request.Context(), queueKey, string(actionBytes))
	pipe.LTrim(ctx.Request.Context(), queueKey, -100, -1)

	data, err := action.ParseData()
	if err == nil {
		key := strconv.Itoa(data.X) + ":" + strconv.Itoa(data.Y)
		if action.ActionType == "draw" {
			pipe.HSet(ctx.Request.Context(), stateKey, key, data.Color)
		} else if action.ActionType == "erase" {
			pipe.HDel(ctx.Request.Context(), stateKey, key)
		}
	}
	if _, err := pipe.Exec(ctx.Request.Context()); err != nil {
		logrus.WithError(err).Error("Failed to update Redis state")
	}
}

func batchAction(action models.Action) {
	batchMu.Lock()
	defer batchMu.Unlock()
	actionBatch = append(actionBatch, action)
}

func flushActionBatch() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		batchMu.Lock()
		if len(actionBatch) > 0 {
			tx := config.DB.Begin()
			if err := tx.Create(&actionBatch).Error; err != nil {
				tx.Rollback()
				logrus.WithError(err).Error("Failed to batch save actions")
			} else {
				tx.Commit()
				actionBatch = nil
			}
		}
		batchMu.Unlock()
	}
}

func publishAction(ctx *gin.Context, roomID uint, action models.Action) {
	actionBytes, _ := json.Marshal(action)
	payload := strconv.FormatInt(int64(action.Version), 10) + "|" + string(actionBytes)
	config.Redis.Publish(ctx.Request.Context(), "room:"+strconv.FormatUint(uint64(roomID), 10), payload)
}
