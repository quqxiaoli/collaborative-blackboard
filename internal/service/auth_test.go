package service_test // 测试包

import (
	"context"
	"errors"
	"testing"
	"time"

	// 导入必要的包
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"
	"collaborative-blackboard/internal/repository/mocks" // 导入 Mock 实现
	"collaborative-blackboard/internal/service"         // 导入被测试的包
	"github.com/stretchr/testify/assert"                // 导入断言库
	"github.com/stretchr/testify/mock"                  // 导入 Mock 库
	"github.com/stretchr/testify/require"               // 导入 Require 断言库
	"golang.org/x/crypto/bcrypt"                        // 需要 bcrypt 用于密码哈希比较 (如果需要)
)

// --- 测试 Register 方法 ---

func TestAuthService_Register_Success(t *testing.T) {
	// Arrange: 准备 Mock 对象, Service 实例, 和测试数据
	mockUserRepo := new(mocks.UserRepository) // 创建 Mock UserRepository
	jwtSecret := "very-secret-key"
	jwtExpiry := 1 // 1 小时过期 (用于 NewAuthService)
	authService, err := service.NewAuthService(mockUserRepo, jwtSecret, jwtExpiry)
	require.NoError(t, err, "创建 AuthService 不应失败") // 使用 require 确保前置条件满足

	ctx := context.Background()
	username := "newbie"
	password := "StrongPass123"
	email := "newbie@example.com"

	// 设置 Mock 预期:
	// 1. 当 FindByUsername 被调用时，模拟用户不存在
	mockUserRepo.On("FindByUsername", ctx, username).
		Return(nil, repository.ErrUserNotFound). // 返回预设的 "未找到" 错误
		Once() // 预期调用一次

	// 2. 当 Save 被调用时，模拟保存成功，并填充 ID/时间戳
	mockUserRepo.On("Save", ctx, mock.MatchedBy(func(user *domain.User) bool {
		// 可以在这里添加对传入 user 对象的断言检查
		assert.Equal(t, username, user.Username)
		assert.Equal(t, email, user.Email)
		// 验证密码是否已哈希 (简单检查长度或使用 bcrypt.Compare)
		assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)), "密码应被正确哈希")
		return true // 返回 true 表示参数匹配预期
	})).
		Run(func(args mock.Arguments) { // 模拟数据库填充字段
			userArg := args.Get(1).(*domain.User)
			userArg.ID = 5 // 假设分配的 ID 是 5
			userArg.CreatedAt = time.Now().Add(-time.Second) // 模拟创建时间
			userArg.UpdatedAt = time.Now().Add(-time.Second) // 模拟更新时间
		}).
		Return(nil). // 返回 nil 表示 Save 成功
		Once()       // 预期调用一次

	// Act: 执行被测试的 Register 方法
	registeredUser, err := authService.Register(ctx, username, password, email)

	// Assert: 验证 Register 的结果
	assert.NoError(t, err, "成功注册时不应有错误")
	assert.NotNil(t, registeredUser, "成功注册时应返回用户对象")
	if registeredUser != nil { // 添加检查避免 nil panic
		assert.Equal(t, uint(5), registeredUser.ID, "返回的用户 ID 应为 5")
		assert.Equal(t, username, registeredUser.Username)
		assert.Equal(t, email, registeredUser.Email)
		assert.Empty(t, registeredUser.Password, "返回的用户密码应为空") // Service 应清除密码
		assert.False(t, registeredUser.CreatedAt.IsZero(), "创建时间应被设置")
		assert.False(t, registeredUser.UpdatedAt.IsZero(), "更新时间应被设置")
	}

	// Verify: 确保 Mock 的所有预期都被满足
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_Register_UsernameTaken(t *testing.T) {
	// Arrange
	mockUserRepo := new(mocks.UserRepository)
	authService, _ := service.NewAuthService(mockUserRepo, "secret", 1)
	ctx := context.Background()
	username := "existingUser"

	// 设置 Mock 预期: FindByUsername 找到一个已存在的用户
	existingUser := &domain.User{ID: 10, Username: username}
	mockUserRepo.On("FindByUsername", ctx, username).Return(existingUser, nil).Once()
	// 预期 Save 不会被调用

	// Act
	_, err := authService.Register(ctx, username, "password", "email@test.com")

	// Assert
	require.Error(t, err, "用户名已存在时应返回错误") // 使用 require 强调错误必须发生
	assert.True(t, errors.Is(err, service.ErrRegistrationFailed), "错误类型应为 ErrRegistrationFailed")

	// Verify
	mockUserRepo.AssertExpectations(t)
	// 明确断言 Save 没有被调用 (更严格的验证)
	mockUserRepo.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
}

func TestAuthService_Register_SaveFails_DuplicateEntry(t *testing.T) {
	// Arrange
	mockUserRepo := new(mocks.UserRepository)
	authService, _ := service.NewAuthService(mockUserRepo, "secret", 1)
	ctx := context.Background()
	username := "anotherNewUser"

	// 设置 Mock 预期:
	// 1. FindByUsername 找不到用户
	mockUserRepo.On("FindByUsername", ctx, username).Return(nil, repository.ErrUserNotFound).Once()
	// 2. Save 调用时模拟数据库返回唯一约束错误
	mockUserRepo.On("Save", ctx, mock.AnythingOfType("*domain.User")).Return(repository.ErrDuplicateEntry).Once()

	// Act
	_, err := authService.Register(ctx, username, "password", "email2@test.com")

	// Assert
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrRegistrationFailed), "保存冲突时应返回 ErrRegistrationFailed")

	// Verify
	mockUserRepo.AssertExpectations(t)
}

// --- 测试 Login 方法 ---

func TestAuthService_Login_Success(t *testing.T) {
	// Arrange
	mockUserRepo := new(mocks.UserRepository)
	authService, _ := service.NewAuthService(mockUserRepo, "test-secret", 24)
	ctx := context.Background()
	username := "testuser"
	password := "password123"
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	userInDb := &domain.User{ID: 1, Username: username, Password: string(hashedPassword)}

	// 设置 Mock 预期: FindByUsername 成功找到用户
	mockUserRepo.On("FindByUsername", ctx, username).Return(userInDb, nil).Once()

	// Act
	token, err := authService.Login(ctx, username, password)

	// Assert
	assert.NoError(t, err)
	assert.NotEmpty(t, token)
	// 可以进一步验证 Token 内容 (可选)

	// Verify
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_Login_UserNotFound(t *testing.T) {
	// Arrange
	mockUserRepo := new(mocks.UserRepository)
	authService, _ := service.NewAuthService(mockUserRepo, "test-secret", 24)
	ctx := context.Background()
	username := "nonexistent"

	// 设置 Mock 预期: FindByUsername 找不到用户
	mockUserRepo.On("FindByUsername", ctx, username).Return(nil, repository.ErrUserNotFound).Once()

	// Act
	token, err := authService.Login(ctx, username, "password")

	// Assert
	require.Error(t, err)
	assert.Empty(t, token)
	assert.True(t, errors.Is(err, service.ErrAuthenticationFailed))

	// Verify
	mockUserRepo.AssertExpectations(t)
}

func TestAuthService_Login_IncorrectPassword(t *testing.T) {
	// Arrange
	mockUserRepo := new(mocks.UserRepository)
	authService, _ := service.NewAuthService(mockUserRepo, "test-secret", 24)
	ctx := context.Background()
	username := "testuser"
	correctPassword := "password123"
	incorrectPassword := "wrongpassword"
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(correctPassword), bcrypt.DefaultCost)
	userInDb := &domain.User{ID: 1, Username: username, Password: string(hashedPassword)}

	// 设置 Mock 预期: FindByUsername 找到用户
	mockUserRepo.On("FindByUsername", ctx, username).Return(userInDb, nil).Once()

	// Act
	token, err := authService.Login(ctx, username, incorrectPassword)

	// Assert
	require.Error(t, err)
	assert.Empty(t, token)
	assert.True(t, errors.Is(err, service.ErrAuthenticationFailed))

	// Verify
	mockUserRepo.AssertExpectations(t)
}