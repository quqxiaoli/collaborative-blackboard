package ws

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"context"
	"encoding/json"
	"errors" // Import errors
	"fmt"    // Import fmt
	"strconv"
	"sync" // Import sync
	"time"

	"github.com/go-redis/redis/v8" // Ensure redis client is imported
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm" // Import gorm
)

// SendSnapshot retrieves the latest snapshot for a room and sends it to a newly connected client.
// It now accepts context.Context instead of *gin.Context.
func SendSnapshot(conn *websocket.Conn, roomID uint, ctx context.Context) {
	logCtx := logrus.WithFields(logrus.Fields{"room_id": roomID})
	snapshotKey := fmt.Sprintf("room:%d:snapshot", roomID)

	// Retrieve the latest snapshot JSON string from Redis
	snapshotStr, err := config.Redis.Get(ctx, snapshotKey).Result()
	if err == redis.Nil {
		// No snapshot found in Redis, try fetching the latest from DB
		logCtx.Info("Snapshot not found in Redis, attempting to load from DB.")
		var latestSnapshot models.Snapshot
		// Use a background context for DB query if the original context might be short-lived
		dbCtx := context.Background()
		dbErr := config.DB.WithContext(dbCtx).Where("room_id = ?", roomID).Order("created_at desc").First(&latestSnapshot).Error
		if dbErr == nil {
			// Found in DB, parse and send
			state, parseErr := latestSnapshot.ParseState()
			if parseErr != nil {
				logCtx.WithError(parseErr).Error("Failed to parse snapshot data from DB")
				// Optionally send an error message to the client
				return
			}
			sendSnapshotData(conn, state, latestSnapshot.Version, logCtx)
			// Cache the loaded snapshot back to Redis
			snapshotBytes, marshalErr := json.Marshal(latestSnapshot)
			if marshalErr == nil {
				// Use background context for caching as well
				cacheCtx := context.Background()
				setErr := config.Redis.Set(cacheCtx, snapshotKey, string(snapshotBytes), 0).Err() // Cache indefinitely or with TTL
				if setErr != nil {
					logCtx.WithError(setErr).Warn("Failed to cache snapshot loaded from DB")
				}
			} else {
				logCtx.WithError(marshalErr).Warn("Failed to marshal snapshot for caching")
			}
		} else if !errors.Is(dbErr, gorm.ErrRecordNotFound) {
			// DB error other than not found
			logCtx.WithError(dbErr).Error("Failed to load snapshot from DB")
			// Optionally send an error message to the client
		} else {
			// No snapshot found in Redis or DB, send empty state
			logCtx.Info("No snapshot found in Redis or DB, sending empty initial state.")
			sendSnapshotData(conn, make(models.BoardState), 0, logCtx) // Send empty state with version 0
		}
		return
	} else if err != nil {
		// Other Redis error
		logCtx.WithError(err).Error("Failed to get snapshot from Redis")
		// Optionally send an error message to the client
		return
	}

	// Snapshot found in Redis, proceed to parse and send
	var snapshot models.Snapshot
	if err := json.Unmarshal([]byte(snapshotStr), &snapshot); err != nil {
		logCtx.WithError(err).Error("Failed to unmarshal snapshot data from Redis")
		// Optionally send an error message to the client
		return
	}
	state, err := snapshot.ParseState()
	if err != nil {
		logCtx.WithError(err).Error("Failed to parse snapshot state from Redis data")
		// Optionally send an error message to the client
		return
	}

	sendSnapshotData(conn, state, snapshot.Version, logCtx)
}

// sendSnapshotData formats and sends the snapshot data over the WebSocket connection.
func sendSnapshotData(conn *websocket.Conn, state models.BoardState, version uint, logCtx *logrus.Entry) {
	// Prepare the message payload
	snapshotMsg := map[string]interface{}{
		"type":    "snapshot",
		"version": version, // Include the version number in the snapshot message
		"state":   state,
	}
	stateBytes, err := json.Marshal(snapshotMsg)
	if err != nil {
		logCtx.WithError(err).Error("Failed to marshal snapshot message")
		return
	}

	// Set write deadline
	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logCtx.WithError(err).Error("Failed to set write deadline for sending snapshot")
		return
	}

	// Send the snapshot message
	if err := conn.WriteMessage(websocket.TextMessage, stateBytes); err != nil {
		logCtx.WithError(err).Error("Failed to write snapshot message to WebSocket")
	} else {
		logCtx.WithField("version", version).Info("Snapshot sent to client")
	}

	// Clear write deadline
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		logCtx.WithError(err).Warn("Failed to clear write deadline after sending snapshot")
	}
}


// RunSnapshotTask runs periodically to check rooms and generate snapshots if needed.
func RunSnapshotTask() {
	// Use a longer initial delay before the first run, e.g., 1 minute
	time.Sleep(1 * time.Minute)

	// Ticker for periodic checks, e.g., every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Store the last snapshot time and calculated interval per room
	roomSnapshotInfo := make(map[uint]struct {
		lastSnapshotTime time.Time
		interval         time.Duration
	})
	var infoMutex sync.Mutex // Mutex to protect access to roomSnapshotInfo

	log := logrus.WithField("task", "snapshotter")
	log.Info("Snapshot task started")

	for range ticker.C {
		log.Debug("Snapshot task tick")
		var rooms []models.Room
		// Use a background context for this periodic task
		ctx := context.Background()
		// Fetch only active rooms or rooms with recent activity if possible
		// For simplicity, fetching all rooms for now. Consider optimizing if many rooms exist.
		if err := config.DB.WithContext(ctx).Find(&rooms).Error; err != nil {
			log.WithError(err).Error("SnapshotTask: Failed to fetch rooms from DB")
			continue // Skip this cycle if DB fetch fails
		}

		if len(rooms) == 0 {
			log.Debug("SnapshotTask: No rooms found to check.")
			continue
		}

		log.Debugf("SnapshotTask: Checking %d rooms", len(rooms))

		for _, room := range rooms {
			roomID := room.ID
			logCtx := log.WithField("room_id", roomID)

			infoMutex.Lock()
			currentInfo, exists := roomSnapshotInfo[roomID]
			infoMutex.Unlock()

			// Get operation count since last snapshot (or from start if no snapshot yet)
			opCount := getOpCountSince(ctx, roomID, currentInfo.lastSnapshotTime)
			newInterval := calculateSnapshotInterval(opCount)

			// Update interval if it changed
			if !exists || newInterval != currentInfo.interval {
				infoMutex.Lock()
				// Re-fetch in case it was updated concurrently (unlikely but safer)
				currentInfo = roomSnapshotInfo[roomID]
				currentInfo.interval = newInterval
				roomSnapshotInfo[roomID] = currentInfo
				infoMutex.Unlock()
				logCtx.WithField("new_interval", newInterval).Debug("Snapshot interval updated")
			}

			// Check if it's time to generate a snapshot
			if shouldGenerateSnapshot(currentInfo.lastSnapshotTime, currentInfo.interval) {
				logCtx.Info("Snapshot condition met, attempting to generate snapshot.")
				if err := generateSnapshot(ctx, roomID); err == nil {
					// Update last snapshot time on success
					infoMutex.Lock()
					// Re-fetch in case it was updated concurrently
					currentInfo = roomSnapshotInfo[roomID]
					currentInfo.lastSnapshotTime = time.Now()
					roomSnapshotInfo[roomID] = currentInfo
					infoMutex.Unlock()
					logCtx.Info("Snapshot generated successfully.")
				} else {
					logCtx.WithError(err).Error("Snapshot generation failed.")
					// Keep the old lastSnapshotTime so it retries sooner
				}
			} else {
				logCtx.Debugf("Snapshot condition not met (Last: %s, Interval: %s, OpsSince: %d)",
					currentInfo.lastSnapshotTime.Format(time.RFC3339), currentInfo.interval, opCount)
			}
		}
	}
}

// getOpCountSince retrieves the number of operations since a given time.
// This implementation queries the database for actions newer than lastSnapshotTime.
func getOpCountSince(ctx context.Context, roomID uint, lastSnapshotTime time.Time) int {
	var count int64
	query := config.DB.WithContext(ctx).Model(&models.Action{}).Where("room_id = ?", roomID)

	// If a snapshot exists, count actions since that snapshot's creation time.
	if !lastSnapshotTime.IsZero() {
		query = query.Where("created_at > ?", lastSnapshotTime)
	}

	err := query.Count(&count).Error
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"room_id":          roomID,
			"lastSnapshotTime": lastSnapshotTime,
		}).Error("Failed to count actions from DB since last snapshot")
		return 0 // Return 0 on error
	}
	return int(count)
}


// calculateSnapshotInterval determines how often a snapshot should be generated based on activity.
// More frequent snapshots if activity is high.
func calculateSnapshotInterval(opCountSinceLast int) time.Duration {
	// Example logic:
	if opCountSinceLast > 100 { // High activity
		return 30 * time.Second
	} else if opCountSinceLast > 20 { // Medium activity
		return 2 * time.Minute
	} else { // Low activity or first snapshot
		return 10 * time.Minute // Default interval
	}
	// Consider adding a maximum interval (e.g., 1 hour) regardless of activity.
	// if interval > 1*time.Hour { interval = 1*time.Hour }
}

// shouldGenerateSnapshot checks if enough time has passed since the last snapshot.
func shouldGenerateSnapshot(lastSnapshotTime time.Time, interval time.Duration) bool {
	// If never snapshotted or interval has passed
	return lastSnapshotTime.IsZero() || time.Since(lastSnapshotTime) >= interval
}

// generateSnapshot creates a snapshot of the current room state and saves it to Redis and the database.
func generateSnapshot(ctx context.Context, roomID uint) error {
	roomIDStr := strconv.FormatUint(uint64(roomID), 10)
	stateKey := "room:" + roomIDStr + ":state"
	snapshotKey := "room:" + roomIDStr + ":snapshot"
	versionKey := "room:" + roomIDStr + ":version" // Key for the current version
	opCountKey := "room:" + roomIDStr + ":op_count" // Key for the operation counter

	logCtx := logrus.WithField("room_id", roomID)

	// Get the current state and version from Redis
	// Use a pipeline to get state and version somewhat atomically
	pipe := config.Redis.Pipeline()
	stateCmd := pipe.HGetAll(ctx, stateKey)
	versionCmd := pipe.Get(ctx, versionKey)
	_, err := pipe.Exec(ctx)

	// Handle potential errors from pipeline execution
	// We need both state and version to proceed correctly.
	if err != nil && err != redis.Nil {
		logCtx.WithError(err).Error("Snapshot: Failed to execute Redis pipeline to get state and version")
		return err
	}

	stateRaw, err := stateCmd.Result()
	if err != nil && err != redis.Nil { // Allow empty state (redis.Nil is not an error here)
		logCtx.WithError(err).Error("Snapshot: Failed to get state from Redis")
		return err
	}

	versionStr, err := versionCmd.Result()
	var currentVersion int64
	if err == redis.Nil {
		// Key doesn't exist, assume version 0. This might happen if the room is new or Redis restarted.
		currentVersion = 0
		logCtx.Warn("Snapshot: Version key not found in Redis, assuming version 0.")
	} else if err != nil {
		logCtx.WithError(err).Error("Snapshot: Failed to get version from Redis")
		return err
	} else {
		currentVersion, err = strconv.ParseInt(versionStr, 10, 64)
		if err != nil {
			logCtx.WithError(err).Error("Snapshot: Failed to parse version from Redis")
			return err
		}
	}


	// Create the snapshot model
	snapshot := models.Snapshot{
		RoomID:    roomID,
		CreatedAt: time.Now().UTC(), // Use UTC time for consistency
		Version:   uint(currentVersion), // Store the version associated with this snapshot
	}
	if err := snapshot.SetState(models.BoardState(stateRaw)); err != nil {
		logCtx.WithError(err).Error("Snapshot: Failed to set snapshot state")
		return err
	}

	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		logCtx.WithError(err).Error("Snapshot: Failed to marshal snapshot")
		return err
	}

	// Save snapshot to Redis (overwrite existing)
	// Consider adding an expiry if snapshots are only needed for recent recovery (e.g., 24h)
	if err := config.Redis.Set(ctx, snapshotKey, string(snapshotBytes), 0).Err(); err != nil { // 0 = no expiration
		logCtx.WithError(err).Error("Snapshot: Failed to save snapshot to Redis")
		// Decide if this is critical. Maybe just log and continue to save to DB?
		// return err // For now, let's try saving to DB anyway
	}

	// Save snapshot to Database
	// Use a separate context for DB operation if needed, but background context is likely fine here.
	if err := config.DB.WithContext(ctx).Create(&snapshot).Error; err != nil {
		logCtx.WithError(err).Error("Snapshot: Failed to save snapshot to MySQL")
		// This is more critical, return the error
		return err
	}

	// Reset the operation count *after* successfully creating a snapshot in both Redis and DB
	// Set expiry to keep the key around even if inactive for a while
	if err := config.Redis.Set(ctx, opCountKey, "0", 1*time.Hour).Err(); err != nil {
		// Log the error but don't necessarily fail the snapshot process
		logCtx.WithError(err).Warn("Snapshot: Failed to reset op_count in Redis")
	}


	logCtx.WithField("version", snapshot.Version).Info("Snapshot generated and saved.")
	return nil
}
