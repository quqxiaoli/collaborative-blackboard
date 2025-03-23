package main

import (
    "collaborative-blackboard/config"
    "collaborative-blackboard/models"
    "collaborative-blackboard/routes"
    "context"
    "encoding/json"
    "log"
    "strconv"
    "time"

    "github.com/gin-gonic/gin"
)

func main() {
    config.InitDB()
    config.MigrateDB()
    config.InitRedis()

    r := gin.Default()
    routes.SetupRouter(r)
    r.GET("/ping", func(c *gin.Context) {
        c.JSON(200, gin.H{"message": "pong"})
    })

    go runSnapshotTask()
    go cleanupOldSnapshots() // 添加清理任务

    r.Run(":8080")
}

func runSnapshotTask() {
    ticker := time.NewTicker(5 * time.Second) // 最小检查间隔
    defer ticker.Stop()
    roomIntervals := make(map[uint]time.Duration) // 每个房间的快照间隔
    lastSnapshot := make(map[uint]time.Time)     // 每个房间的上次快照时间

    for range ticker.C {
        var rooms []models.Room
        if err := config.DB.Find(&rooms).Error; err != nil {
            log.Println("Failed to fetch rooms:", err)
            continue
        }

        for _, room := range rooms {
            roomID := room.ID
            roomIDStr := strconv.FormatUint(uint64(roomID), 10)
            opCountKey := "room:" + roomIDStr + ":op_count"

            // 获取操作计数
            opCount, _ := config.Redis.Get(context.Background(), opCountKey).Int()
            interval := calculateSnapshotInterval(opCount)
            roomIntervals[roomID] = interval

            // 检查是否需要生成快照
            lastTime, exists := lastSnapshot[roomID]
            if !exists || time.Since(lastTime) >= interval {
                if err := generateSnapshotForRoom(roomID); err == nil {
                    lastSnapshot[roomID] = time.Now()
                }
            }
        }
    }
}

func calculateSnapshotInterval(opCount int) time.Duration {
    switch {
    case opCount > 50: // 高活跃：每 10 秒
        return 10 * time.Second
    case opCount > 20: // 中活跃：每 20 秒
        return 20 * time.Second
    default: // 低活跃：每 60 秒
        return 60 * time.Second
    }
}

func generateSnapshotForRoom(roomID uint) error {
    roomIDStr := strconv.FormatUint(uint64(roomID), 10)
    stateKey := "room:" + roomIDStr + ":state"
    snapshotKey := "room:" + roomIDStr + ":snapshot"
    lockKey := "lock:room:" + roomIDStr

    locked, err := acquireLock(lockKey, 10*time.Second)
    if err != nil || !locked {
        log.Println("Failed to acquire lock for room", roomID, ":", err)
        return err
    }
    defer releaseLock(lockKey)

    return tryGenerateSnapshot(roomID, stateKey, snapshotKey)
}

func acquireLock(lockKey string, ttl time.Duration) (bool, error) {
    ctx := context.Background()
    success, err := config.Redis.SetNX(ctx, lockKey, "locked", ttl).Result()
    if err != nil {
        return false, err
    }
    return success, nil
}

func releaseLock(lockKey string) {
    ctx := context.Background()
    config.Redis.Del(ctx, lockKey)
}

func tryGenerateSnapshot(roomID uint, stateKey, snapshotKey string) error {
    const maxRetries = 3
    const retryDelay = 1 * time.Second

    for attempt := 1; attempt <= maxRetries; attempt++ {
        stateRaw, err := config.Redis.HGetAll(context.Background(), stateKey).Result()
        if err != nil {
            log.Printf("Attempt %d: Failed to get state for room %d: %v", attempt, roomID, err)
            if attempt == maxRetries {
                return err
            }
            time.Sleep(retryDelay)
            continue
        }
        state := models.BoardState(stateRaw)

        snapshot := models.Snapshot{
            RoomID:    roomID,
            CreatedAt: time.Now(),
        }
        if err := snapshot.SetState(state); err != nil {
            log.Printf("Attempt %d: Failed to set snapshot state for room %d: %v", attempt, roomID, err)
            if attempt == maxRetries {
                return err
            }
            time.Sleep(retryDelay)
            continue
        }

        snapshotBytes, _ := json.Marshal(snapshot)
        if err := config.Redis.Set(context.Background(), snapshotKey, string(snapshotBytes), 0).Err(); err != nil {
            log.Printf("Attempt %d: Failed to save snapshot to Redis for room %d: %v", attempt, roomID, err)
            if attempt == maxRetries {
                return err
            }
            time.Sleep(retryDelay)
            continue
        }

        if err := config.DB.Create(&snapshot).Error; err != nil {
            log.Printf("Attempt %d: Failed to save snapshot to MySQL for room %d: %v", attempt, roomID, err)
            if attempt == maxRetries {
                return err
            }
            time.Sleep(retryDelay)
            continue
        }

        log.Printf("Snapshot generated for room %d on attempt %d", roomID, attempt)
        return nil
    }
    return nil
}

func cleanupOldSnapshots() {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()
    for range ticker.C {
        if err := config.DB.Exec(`
            DELETE FROM snapshots 
            WHERE id NOT IN (
                SELECT id FROM (
                    SELECT id FROM snapshots 
                    ORDER BY created_at DESC 
                    LIMIT 10
                ) AS tmp
            )
        `).Error; err != nil {
            log.Println("Failed to cleanup old snapshots:", err)
        } else {
            log.Println("Old snapshots cleaned up successfully")
        }
    }
}