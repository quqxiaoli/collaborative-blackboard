package handlers

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

	go func() {
		ch := pubsub.Channel()
		for msg := range ch {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
				log.Println("Write error:", err)
				return
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			break
		}

		// 生成操作序列号
        seqKey := "room:" + roomIDStr + ":seq"
        seq, err := config.Redis.Incr(c.Request.Context(), seqKey).Result()
        if err != nil {
            log.Println("Failed to generate sequence:", err)
            continue
        }

        action := models.Action{
            RoomID:     uint(roomID),
            UserID:     1,
            ActionType: "draw",
            Data:       string(msg),
            Timestamp:  time.Now(),
        }
        if err := config.DB.Create(&action).Error; err != nil {
            log.Println("Failed to save action:", err)
        }

        cacheKey := "room:" + roomIDStr + ":recent"
        config.Redis.RPush(c.Request.Context(), cacheKey, string(msg))
        config.Redis.LTrim(c.Request.Context(), cacheKey, -100, -1)

        // 发布消息，附加序列号
        payload := strconv.FormatInt(seq, 10) + "|" + string(msg)
        config.Redis.Publish(c.Request.Context(), "room:"+roomIDStr, payload)
    }
}