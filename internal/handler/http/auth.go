package http

import (
	"errors"
	"net/http"

	// 导入 Service 和定义的业务错误
	"collaborative-blackboard/internal/service" // 导入 Service 包

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// AuthHandler 封装了与用户认证相关的 HTTP 处理逻辑
type AuthHandler struct {
	authService *service.AuthService // 依赖 AuthService 接口（或具体类型）
}

// NewAuthHandler 创建 AuthHandler 实例
func NewAuthHandler(authService *service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

// RegisterRequest 定义注册请求的结构体
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"` // 添加更多验证规则
	Password string `json:"password" binding:"required,min=6"`
	Email    string `json:"email" binding:"omitempty,email"` // 邮箱可选但必须是有效格式
}

// Register 处理用户注册请求
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	// 1. 绑定并验证输入 JSON
	if err := c.ShouldBindJSON(&req); err != nil {
		logrus.WithError(err).Warn("Handler.Register: Invalid input format")
		// 可以返回更详细的验证错误信息给客户端
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input", "details": err.Error()})
		return
	}

	// 2. 调用 Service 层处理注册逻辑
	// 注意：现在传递的是解析后的字段，而不是整个模型
	newUser, err := h.authService.Register(c.Request.Context(), req.Username, req.Password, req.Email)

	// 3. 处理 Service 返回的错误
	if err != nil {
		logCtx := logrus.WithFields(logrus.Fields{"username": req.Username, "email": req.Email})
		// 根据 Service 返回的错误类型决定 HTTP 响应状态码
		if errors.Is(err, service.ErrRegistrationFailed) { // 检查业务错误
			logCtx.WithError(err).Warn("Handler.Register: Registration failed (likely duplicate)")
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()}) // 返回业务错误信息
		} else {
			// 其他错误（可能是 ErrInternalServer 或更具体的）
			logCtx.WithError(err).Error("Handler.Register: Internal error during registration")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Registration failed due to server error"})
		}
		return
	}

	// 4. 注册成功响应
	logrus.WithField("user_id", newUser.ID).Info("Handler.Register: User registered successfully")
	// 响应中不应包含密码等敏感信息
	c.JSON(http.StatusOK, gin.H{
		"message": "User registered successfully",
		"user_id": newUser.ID, // 返回明确的 user_id
	})
}

// LoginRequest 定义登录请求的结构体
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse 定义登录成功的响应结构体
type LoginResponse struct {
	Message string `json:"message"`
	Token   string `json:"token"`
}

// Login 处理用户登录请求
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	// 1. 绑定并验证输入 JSON
	if err := c.ShouldBindJSON(&req); err != nil {
		logrus.WithError(err).Warn("Handler.Login: Invalid input format")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input: username and password required"})
		return
	}

	// 2. 调用 Service 层处理登录逻辑
	token, err := h.authService.Login(c.Request.Context(), req.Username, req.Password)

	// 3. 处理 Service 返回的错误
	if err != nil {
		logCtx := logrus.WithField("username", req.Username)
		if errors.Is(err, service.ErrAuthenticationFailed) { // 检查业务错误
			logCtx.WithError(err).Warn("Handler.Login: Authentication failed")
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()}) // 401 Unauthorized
		} else {
			// 其他错误 (ErrInternalServer)
			logCtx.WithError(err).Error("Handler.Login: Internal error during login")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Login failed due to server error"})
		}
		return
	}

	// 4. 登录成功响应
	logrus.WithField("username", req.Username).Info("Handler.Login: User logged in successfully")
	c.JSON(http.StatusOK, LoginResponse{
		Message: "Login successful",
		Token:   token,
	})
}

