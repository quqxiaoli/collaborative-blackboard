package handlers

import (
    "collaborative-blackboard/config"
    "collaborative-blackboard/models"
    "encoding/json"
    "log"
    "net/http"
    "strconv"
    "time"
    "sync"

    "github.com/gin-gonic/gin"
    "github.com/gorilla/websocket"
)

var (
    actionBuffer []models.Action
    bufferMutex  sync.Mutex
    clients      = make(map[string]*websocket.Conn)
    clientMutex  sync.Mutex
    otChan       = make(chan otTask, 100)
)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin:     func(r *http.Request) bool { return true },
}

type otTask struct {
    Action  models.Action
    RoomID  uint
    Context *gin.Context
}

func init() {
    go func() {
        for task := range otChan {
            processOT(task)
        }
    }()
}

func processOT(task otTask) {
    queueKey := "room:" + strconv.FormatUint(uint64(task.RoomID), 10) + ":actions"
    stateKey := "room:" + strconv.FormatUint(uint64(task.RoomID), 10) + ":state"
    opCountKey := "room:" + strconv.FormatUint(uint64(task.RoomID), 10) + ":op_count"
    queuedActions, err := config.Redis.LRange(task.Context.Request.Context(), queueKey, 0, -1).Result()
    if err != nil {
        log.Println("Failed to get action queue:", err)
        return
    }

    finalAction := task.Action
    for _, queuedActionStr := range queuedActions {
        var queuedAction models.Action
        if err := json.Unmarshal([]byte(queuedActionStr), &queuedAction); err == nil {
            finalAction, _ = Transform(finalAction, queuedAction)
        }
    }

    if finalAction.ActionType != "noop" {
        // 增加操作计数
        config.Redis.Incr(task.Context.Request.Context(), opCountKey)
        config.Redis.Expire(task.Context.Request.Context(), opCountKey, 1*time.Hour) // 计数每小时重置
        actionBytes, _ := json.Marshal(finalAction)
        config.Redis.RPush(task.Context.Request.Context(), queueKey, string(actionBytes))
        config.Redis.LTrim(task.Context.Request.Context(), queueKey, -100, -1)

        data, err := finalAction.ParseData()
        if err == nil {
            key := strconv.Itoa(data.X) + ":" + strconv.Itoa(data.Y)
            if finalAction.ActionType == "draw" {
                config.Redis.HSet(task.Context.Request.Context(), stateKey, key, data.Color)
            } else if finalAction.ActionType == "erase" {
                config.Redis.HDel(task.Context.Request.Context(), stateKey, key)
            }
        }

        bufferMutex.Lock()
        actionBuffer = append(actionBuffer, finalAction)
        if len(actionBuffer) >= 100 {
            if err := config.DB.Create(&actionBuffer).Error; err != nil {
                log.Println("Failed to batch save actions:", err)
            }
            actionBuffer = nil
        }
        bufferMutex.Unlock()

        payload := strconv.FormatInt(int64(finalAction.Version), 10) + "|" + string(actionBytes)
        config.Redis.Publish(task.Context.Request.Context(), "room:"+strconv.FormatUint(uint64(task.RoomID), 10), payload)
    }
}

func WSHandler(c *gin.Context) {
    userID, exists := c.Get("user_id")
    if !exists {
        c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
        return
    }

    roomIDStr := c.Param("roomId")
    roomID, err := strconv.ParseUint(roomIDStr, 10, 32)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid room ID"})
        return
    }

    var room models.Room
    if err := config.DB.First(&room, uint(roomID)).Error; err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Room not found"})
        return
    }

    conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        log.Println("Failed to upgrade:", err)
        return
    }

    sessionID := strconv.FormatUint(uint64(userID.(uint)), 10) + ":" + roomIDStr
    clientMutex.Lock()
    clients[sessionID] = conn
    clientMutex.Unlock()

    sessionKey := "session:" + sessionID
    lastVersionKey := sessionKey + ":last_version"
    config.Redis.Set(c.Request.Context(), sessionKey, "active", 30*time.Minute)

    pubsub := config.Redis.Subscribe(c.Request.Context(), "room:"+roomIDStr)
    defer pubsub.Close()

    readyChan := make(chan struct{}) // 用于等待客户端 ready 信号

    go func() {
        ch := pubsub.Channel()
        for msg := range ch {
            if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
                log.Println("Write error:", err)
                return
            }
        }
    }()

    go func() {
        defer func() {
            conn.Close()
            clientMutex.Lock()
            delete(clients, sessionID)
            clientMutex.Unlock()
            config.Redis.Set(c.Request.Context(), sessionKey, "inactive", 30*time.Minute)
            close(readyChan)
        }()

        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                log.Println("Read error:", err)
                return
            }

            var msgData map[string]string
            if err := json.Unmarshal(msg, &msgData); err == nil && msgData["type"] == "ready" {
                // 客户端发送 ready，推送快照
                select {
                case readyChan <- struct{}{}:
                default:
                }
                continue
            }

            versionKey := "room:" + roomIDStr + ":version"
            version, err := config.Redis.Incr(c.Request.Context(), versionKey).Result()
            if err != nil {
                log.Println("Failed to generate version:", err)
                continue
            }

            var drawData models.DrawData
            if err := json.Unmarshal(msg, &drawData); err != nil {
                log.Println("Invalid draw data:", err)
                continue
            }

            action := models.Action{
                RoomID:     uint(roomID),
                UserID:     userID.(uint),
                ActionType: "draw",
                Timestamp:  time.Now(),
                Version:    uint(version),
            }
            if err := action.SetData(drawData); err != nil {
                log.Println("Failed to set data:", err)
                continue
            }

            config.Redis.Set(c.Request.Context(), lastVersionKey, version, 30*time.Minute)
            otChan <- otTask{
                Action:  action,
                RoomID:  uint(roomID),
                Context: c,
            }
        }
    }()

    // 等待客户端 ready 后推送快照
    go func() {
        select {
        case <-readyChan:
            snapshotKey := "room:" + roomIDStr + ":snapshot"
            snapshotStr, err := config.Redis.Get(c.Request.Context(), snapshotKey).Result()
            var snapshotVersion uint
            if err == nil {
                var snapshot models.Snapshot
                if err := json.Unmarshal([]byte(snapshotStr), &snapshot); err == nil {
                    state, err := snapshot.ParseState()
                    if err == nil {
                        stateBytes, _ := json.Marshal(map[string]interface{}{
                            "type":  "snapshot",
                            "state": state,
                        })
                        conn.WriteMessage(websocket.TextMessage, stateBytes)
                        queueKey := "room:" + roomIDStr + ":actions"
                        queuedActions, _ := config.Redis.LRange(c.Request.Context(), queueKey, -1, -1).Result()
                        if len(queuedActions) > 0 {
                            var lastAction models.Action
                            json.Unmarshal([]byte(queuedActions[0]), &lastAction)
                            snapshotVersion = lastAction.Version
                        }
                    }
                }
            }

            lastVersion, _ := config.Redis.Get(c.Request.Context(), lastVersionKey).Int64()
            queueKey := "room:" + roomIDStr + ":actions"
            queuedActions, _ := config.Redis.LRange(c.Request.Context(), queueKey, 0, -1).Result()
            for _, actionStr := range queuedActions {
                var action models.Action
                if err := json.Unmarshal([]byte(actionStr), &action); err == nil {
                    if int64(action.Version) > lastVersion && action.Version > snapshotVersion {
                        actionBytes, _ := json.Marshal(action)
                        conn.WriteMessage(websocket.TextMessage, actionBytes)
                    }
                }
            }
        case <-time.After(10 * time.Second):
            log.Println("Client did not send ready within 10 seconds for session", sessionID)
        }
    }()

    go func() {
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        for range ticker.C {
            bufferMutex.Lock()
            if len(actionBuffer) > 0 {
                if err := config.DB.Create(&actionBuffer).Error; err != nil {
                    log.Println("Failed to batch save actions:", err)
                }
                actionBuffer = nil
            }
            bufferMutex.Unlock()
        }
    }()

    select {}
}