package ws

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
	clients     = make(map[string]*websocket.Conn)
	clientsMu   sync.Mutex
	actionChan  = make(chan ActionTask, 100)
	actionBatch []models.Action
	batchMu     sync.Mutex
)

// ActionTask is now defined in hub.go

func init() {
	// These background processes are now handled by Hub in hub.go
}

func WSHandler(c *gin.Context) {
	userID, roomID, conn, err := setupConnection(c)
	if err != nil {
		return
	}
	defer conn.Close()

	sessionID := strconv.FormatUint(uint64(userID), 10) + ":" + strconv.FormatUint(uint64(roomID), 10)
	registerClient(sessionID, conn)
	defer unregisterClient(sessionID)

	pubsub := config.Redis.Subscribe(c.Request.Context(), "room:"+strconv.FormatUint(uint64(roomID), 10))
	defer pubsub.Close()

	msgChan := make(chan []byte)
	go readMessages(conn, msgChan)

	pubChan := pubsub.Channel()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case msg, ok := <-msgChan:
			if !ok {
				return
			}
			handleClientMessage(c, conn, msg, userID, roomID)
		case pubMsg, ok := <-pubChan:
			if !ok {
				return
			}
			conn.WriteMessage(websocket.TextMessage, []byte(pubMsg.Payload))
		}
	}
}

func setupConnection(c *gin.Context) (uint, uint, *websocket.Conn, error) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return 0, 0, nil, gin.Error{}
	}

	roomID, err := strconv.ParseUint(c.Param("roomId"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid room ID"})
		return 0, 0, nil, err
	}

	var room models.Room
	if err := config.DB.First(&room, roomID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Room not found"})
		return 0, 0, nil, err
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logrus.WithError(err).Error("Failed to upgrade connection") // 改为 Error
		return 0, 0, nil, err
	}
	return userID.(uint), uint(roomID), conn, nil
}

func registerClient(sessionID string, conn *websocket.Conn) {
	clientsMu.Lock()
	clients[sessionID] = conn
	clientsMu.Unlock()
}

func unregisterClient(sessionID string) {
	clientsMu.Lock()
	delete(clients, sessionID)
	clientsMu.Unlock()
}

func readMessages(conn *websocket.Conn, msgChan chan<- []byte) {
	defer close(msgChan)
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			logrus.WithError(err).Warn("WebSocket read error") // 改为 Warn
			return
		}
		msgChan <- msg
	}
}

func handleClientMessage(c *gin.Context, conn *websocket.Conn, msg []byte, userID, roomID uint) {
	var data map[string]string
	if err := json.Unmarshal(msg, &data); err == nil && data["type"] == "ready" {
		SendSnapshot(conn, roomID, c)
		return
	}

	var drawData models.DrawData
	if err := json.Unmarshal(msg, &drawData); err != nil {
		logrus.WithError(err).Warn("Invalid draw data")
		return
	}

	versionKey := "room:" + strconv.FormatUint(uint64(roomID), 10) + ":version"
	version, _ := config.Redis.Incr(c.Request.Context(), versionKey).Result()

	action := models.Action{
		RoomID:     roomID,
		UserID:     userID,
		ActionType: "draw",
		Timestamp:  time.Now(),
		Version:    uint(version),
	}
	if err := action.SetData(drawData); err != nil {
		logrus.WithError(err).Error("Failed to set action data")
		return
	}

	// Convert *gin.Context to context.Context when sending to actionChan
	actionChan <- ActionTask{Action: action, RoomID: roomID, Context: c.Request.Context()}
}

func cleanupClients() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		clientsMu.Lock()
		for sessionID, conn := range clients {
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				logrus.WithField("session_id", sessionID).Warn("Removing inactive client") // 改为 Warn
				conn.Close()
				delete(clients, sessionID)
			}
		}
		clientsMu.Unlock()
	}
}
