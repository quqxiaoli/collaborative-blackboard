package handlers

import (
	"collaborative-blackboard/config"
	"collaborative-blackboard/models"
	"crypto/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func CreateRoom(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	inviteCode, err := generateUniqueInviteCode()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate invite code"})
		return
	}

	room := models.Room{
		CreatorID:  userID.(uint),
		InviteCode: inviteCode,
		CreatedAt:  time.Now(),
		LastActive: time.Now(),
	}
	if err := config.DB.Create(&room).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create room"})
		return
	}

	if err := config.Redis.Set(c.Request.Context(), "room:"+strconv.FormatUint(uint64(room.ID), 10), "active", 0).Err(); err != nil {
		logrus.WithError(err).Error("Failed to cache room status")
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Room created",
		"room_id":     room.ID,
		"invite_code": inviteCode,
	})
}

func JoinRoom(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	var input struct {
		InviteCode string `json:"invite_code"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
		return
	}

	var room models.Room
	if err := config.DB.Where("invite_code = ?", input.InviteCode).First(&room).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Room not found"})
		return
	}

	logrus.WithFields(logrus.Fields{"user_id": userID, "room_id": room.ID}).Info("User joined room")
	c.JSON(http.StatusOK, gin.H{"message": "Joined room", "room_id": room.ID})
}

func generateUniqueInviteCode() (string, error) {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 6)
	for {
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		for i := range b {
			b[i] = letters[b[i]%byte(len(letters))]
		}
		code := string(b)
		var existing models.Room
		if config.DB.Where("invite_code = ?", code).First(&existing).Error != nil {
			return code, nil
		}
	}
}
