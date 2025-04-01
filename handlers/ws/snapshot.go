package ws

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

func SendSnapshot(conn *websocket.Conn, roomID uint, c *gin.Context) {
	snapshotKey := "room:" + strconv.FormatUint(uint64(roomID), 10) + ":snapshot"
	snapshotStr, err := config.Redis.Get(c.Request.Context(), snapshotKey).Result()
	if err != nil {
		return
	}

	var snapshot models.Snapshot
	if json.Unmarshal([]byte(snapshotStr), &snapshot) != nil {
		return
	}
	state, err := snapshot.ParseState()
	if err != nil {
		return
	}

	stateBytes, _ := json.Marshal(map[string]interface{}{
		"type":  "snapshot",
		"state": state,
	})
	conn.WriteMessage(websocket.TextMessage, stateBytes)
}

func RunSnapshotTask() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	roomIntervals := make(map[uint]time.Duration)
	lastSnapshot := make(map[uint]time.Time)

	for range ticker.C {
		var rooms []models.Room
		if err := config.DB.Find(&rooms).Error; err != nil {
			logrus.WithError(err).Error("Failed to fetch rooms")
			continue
		}

		for _, room := range rooms {
			roomID := room.ID
			opCount := getOpCount(roomID)
			interval := calculateSnapshotInterval(opCount)
			roomIntervals[roomID] = interval

			if shouldGenerateSnapshot(roomID, interval, lastSnapshot) {
				if err := generateSnapshot(roomID); err == nil {
					lastSnapshot[roomID] = time.Now()
				}
			}
		}
	}
}

func getOpCount(roomID uint) int {
	opCountKey := "room:" + strconv.FormatUint(uint64(roomID), 10) + ":op_count"
	count, _ := config.Redis.Get(context.Background(), opCountKey).Int()
	return count
}

func calculateSnapshotInterval(opCount int) time.Duration {
	switch {
	case opCount > 50:
		return 10 * time.Second
	case opCount > 20:
		return 20 * time.Second
	default:
		return 60 * time.Second
	}
}

func shouldGenerateSnapshot(roomID uint, interval time.Duration, lastSnapshot map[uint]time.Time) bool {
	lastTime, exists := lastSnapshot[roomID]
	return !exists || time.Since(lastTime) >= interval
}

func generateSnapshot(roomID uint) error {
	roomIDStr := strconv.FormatUint(uint64(roomID), 10)
	stateKey := "room:" + roomIDStr + ":state"
	snapshotKey := "room:" + roomIDStr + ":snapshot"

	stateRaw, err := config.Redis.HGetAll(context.Background(), stateKey).Result()
	if err != nil {
		logrus.WithError(err).Error("Failed to get state for room ", roomID)
		return err
	}

	snapshot := models.Snapshot{
		RoomID:    roomID,
		CreatedAt: time.Now(),
	}
	if err := snapshot.SetState(models.BoardState(stateRaw)); err != nil {
		logrus.WithError(err).Error("Failed to set snapshot state for room ", roomID)
		return err
	}

	snapshotBytes, _ := json.Marshal(snapshot)
	if err := config.Redis.Set(context.Background(), snapshotKey, string(snapshotBytes), 0).Err(); err != nil {
		logrus.WithError(err).Error("Failed to save snapshot to Redis for room ", roomID)
		return err
	}

	if err := config.DB.Create(&snapshot).Error; err != nil {
		logrus.WithError(err).Error("Failed to save snapshot to MySQL for room ", roomID)
		return err
	}
	logrus.Info("Snapshot generated for room ", roomID)
	return nil
}
