package handlers

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"log"
	"net/http"
	"strconv"
	"time"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// 定义全局缓冲区和锁
var (
    actionBuffer []models.Action
    bufferMutex  sync.Mutex
)

// 全局连接池，key 为 roomID
var clients = make(map[uint][]*websocket.Conn)

// WebSocket 升级器
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // 开发时允许跨域
}

// WSHandler 处理 WebSocket 连接
func WSHandler(c *gin.Context) {
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
	defer conn.Close()

	pubsub := config.Redis.Subscribe(c.Request.Context(), "room:"+roomIDStr)
	defer pubsub.Close()

	// 启动写入 goroutine
    go func() {
        ch := pubsub.Channel()
        for msg := range ch {
            if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
                log.Println("Write error:", err)
                return
            }
        }
    }()

    // 启动读取 goroutine
    go func() {
        defer conn.Close() // 确保读取出错时关闭连接
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                log.Println("Read error:", err)
                return
            }

            // 生成操作序列号
            seqKey := "room:" + roomIDStr + ":seq"
            seq, err := config.Redis.Incr(c.Request.Context(), seqKey).Result()
            if err != nil {
                log.Println("Failed to generate sequence:", err)
                continue
            }
			// 创建操作
            action := models.Action{
                RoomID:     uint(roomID),
                UserID:     1, // 后续替换为 JWT
                ActionType: "draw",
                Data:       string(msg),
                Timestamp:  time.Now(),
            }

            // 添加到缓冲区
            bufferMutex.Lock()
            actionBuffer = append(actionBuffer, action)
            if len(actionBuffer) >= 100 { // 达到 100 条时写入
                if err := config.DB.Create(&actionBuffer).Error; err != nil {
                    log.Println("Failed to batch save actions:", err)
                }
                actionBuffer = nil // 清空缓冲区
            }
            bufferMutex.Unlock()

            // 缓存和发布（保持不变）
            cacheKey := "room:" + roomIDStr + ":recent"
            config.Redis.RPush(c.Request.Context(), cacheKey, string(msg))
            config.Redis.LTrim(c.Request.Context(), cacheKey, -100, -1)
            payload := strconv.FormatInt(seq, 10) + "|" + string(msg)
            config.Redis.Publish(c.Request.Context(), "room:"+roomIDStr, payload)
        }
    }()

	// 启动定时批量写入（全局唯一）
    go func() {
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        for range ticker.C {
            bufferMutex.Lock()
            if len(actionBuffer) > 0 { // 有数据时写入
                if err := config.DB.Create(&actionBuffer).Error; err != nil {
                    log.Println("Failed to batch save actions:", err)
                }
                actionBuffer = nil
            }
            bufferMutex.Unlock()
        }
    }()

    // 主 goroutine 保持连接存活
    select {}
}