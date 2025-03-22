package handlers

import (
    "bytes"
    "collaborative-blackboard/config"
    "collaborative-blackboard/models"
    "github.com/gin-gonic/gin"
    "github.com/stretchr/testify/assert"
    "net/http"
    "net/http/httptest"
    "strconv"
    "testing"
)

func setupTestDB() {
    config.InitDB()
    config.MigrateDB()
    config.InitRedis()
    // 清空表
    config.DB.Exec("DELETE FROM actions")
    config.DB.Exec("DELETE FROM rooms")
    config.DB.Exec("DELETE FROM users")
}

func TestCreateRoom(t *testing.T) {
    // 初始化测试环境
    gin.SetMode(gin.TestMode)
    setupTestDB()

    // 创建测试用户
    user := models.User{Username: "test", Password: "123", Email: "test@example.com"}
    if err := config.DB.Create(&user).Error; err != nil {
        t.Fatalf("Failed to create test user: %v", err)
    }
    t.Logf("Created user with ID: %d", user.ID) // 调试输出用户 ID

    // 模拟 Gin 上下文
    w := httptest.NewRecorder()
    c, _ := gin.CreateTestContext(w)
    body := bytes.NewBufferString(`{"user_id": ` + strconv.Itoa(int(user.ID)) + `}`)
    c.Request, _ = http.NewRequest("POST", "/room", body)
    c.Request.Header.Set("Content-Type", "application/json")

    // 调用 handler
    CreateRoom(c)

    // 验证
    assert.Equal(t, http.StatusOK, w.Code, "Expected status 200 OK")
    assert.Contains(t, w.Body.String(), "Room created", "Expected response to contain 'Room created'")
}