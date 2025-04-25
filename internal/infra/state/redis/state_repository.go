package redisstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	// 导入 Redis 客户端库
	"github.com/go-redis/redis/v8"

	// 使用正确的 Domain 模型路径和 Repository 接口路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/sirupsen/logrus" // 用于日志记录
)

// RedisStateRepository 是 StateRepository 接口的 Redis 实现
type RedisStateRepository struct {
	client *redis.Client // 依赖 Redis 客户端
	// 可选：定义 Redis key 的前缀，方便管理
	keyPrefix string
}

// NewRedisStateRepository 创建 RedisStateRepository 实例
func NewRedisStateRepository(client *redis.Client, keyPrefix string) *RedisStateRepository {
	if client == nil {
		panic("redis client cannot be nil for RedisStateRepository")
	}
	if keyPrefix == "" {
		keyPrefix = "bb:" // 默认前缀 "bb:" (blackboard)
	}
	return &RedisStateRepository{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// --- Key Generation Helpers ---
func (r *RedisStateRepository) roomStateKey(roomID uint) string {
	return fmt.Sprintf("%sroom:%d:state", r.keyPrefix, roomID)
}

func (r *RedisStateRepository) roomVersionKey(roomID uint) string {
	return fmt.Sprintf("%sroom:%d:version", r.keyPrefix, roomID)
}

func (r *RedisStateRepository) roomOpCountKey(roomID uint) string {
	return fmt.Sprintf("%sroom:%d:op_count", r.keyPrefix, roomID)
}

func (r *RedisStateRepository) roomActionHistoryKey(roomID uint) string {
	return fmt.Sprintf("%sroom:%d:actions", r.keyPrefix, roomID)
}

func (r *RedisStateRepository) roomSnapshotCacheKey(roomID uint) string {
	return fmt.Sprintf("%sroom:%d:snapshot", r.keyPrefix, roomID)
}

func (r *RedisStateRepository) roomPubSubChannel(roomID uint) string {
	return fmt.Sprintf("%sroom:%d:pubsub", r.keyPrefix, roomID)
}

// --- StateRepository Interface Implementation ---

// GetBoardState 获取指定房间的当前完整白板状态 (来自 Redis Hash)
func (r *RedisStateRepository) GetBoardState(ctx context.Context, roomID uint) (domain.BoardState, error) {
	key := r.roomStateKey(roomID)
	stateMap, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: failed to get board state for room %d from %s: %w", roomID, key, err)
	}
	boardState := domain.BoardState(stateMap)
	return boardState, nil
}

// ApplyActionToState 将单个 Action 应用到 Redis 中的实时状态。
func (r *RedisStateRepository) ApplyActionToState(ctx context.Context, roomID uint, action domain.Action) error {
	stateKey := r.roomStateKey(roomID)
	data, err := action.ParseData()
	if err != nil {
		return fmt.Errorf("redis: failed to parse action data for ApplyActionToState (action id %d): %w", action.ID, err)
	}
	fieldKey := fmt.Sprintf("%d:%d", data.X, data.Y)
	var cmdErr error
	if action.ActionType == "draw" {
		cmdErr = r.client.HSet(ctx, stateKey, fieldKey, data.Color).Err()
	} else if action.ActionType == "erase" {
		cmdErr = r.client.HDel(ctx, stateKey, fieldKey).Err()
	} else {
		logrus.Warnf("redis: unsupported action type '%s' for ApplyActionToState (action id %d)", action.ActionType, action.ID)
		return nil
	}
	if cmdErr != nil {
		return fmt.Errorf("redis: failed to apply action to state for room %d (key: %s, field: %s): %w", roomID, stateKey, fieldKey, cmdErr)
	}
	return nil
}

// GetCurrentVersion 获取房间当前的最新版本号。
func (r *RedisStateRepository) GetCurrentVersion(ctx context.Context, roomID uint) (uint, error) {
	key := r.roomVersionKey(roomID)
	versionStr, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil // Key 不存在视为版本 0
		}
		return 0, fmt.Errorf("redis: failed to get current version for room %d from %s: %w", roomID, key, err)
	}
	version, parseErr := strconv.ParseUint(versionStr, 10, 64)
	if parseErr != nil {
		return 0, fmt.Errorf("redis: failed to parse version '%s' for room %d from %s: %w", versionStr, roomID, key, parseErr)
	}
	return uint(version), nil
}

// IncrementVersion 原子地增加房间的版本号并返回新版本。
func (r *RedisStateRepository) IncrementVersion(ctx context.Context, roomID uint) (uint, error) { // 返回 uint
	key := r.roomVersionKey(roomID)
	newVersionInt, err := r.client.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("redis: failed to increment version for room %d on key %s: %w", roomID, key, err)
	}
	// 可选: r.client.Expire(ctx, key, 24*time.Hour)
	return uint(newVersionInt), nil // 类型转换
}

// IncrementOpCount 原子地增加房间的操作计数器。
func (r *RedisStateRepository) IncrementOpCount(ctx context.Context, roomID uint) error {
	key := r.roomOpCountKey(roomID)
	pipe := r.client.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 1*time.Hour) // 例如 1 小时过期
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis: failed to increment op count for room %d on key %s: %w", roomID, key, err)
	}
	return nil
}

// ResetOpCount 重置房间的操作计数器。
func (r *RedisStateRepository) ResetOpCount(ctx context.Context, roomID uint) error {
	key := r.roomOpCountKey(roomID)
	err := r.client.Set(ctx, key, "0", 1*time.Hour).Err() // 重置为 0 并保持过期
	if err != nil {
		return fmt.Errorf("redis: failed to reset op count for room %d on key %s: %w", roomID, key, err)
	}
	return nil
}

// GetRecentActions 获取存储在 Redis 中的最近操作记录。
func (r *RedisStateRepository) GetRecentActions(ctx context.Context, roomID uint, limit int) ([]domain.Action, error) {
	if limit <= 0 {
		limit = 100 // 默认获取 100 条
	}
	key := r.roomActionHistoryKey(roomID)
	actionStrs, err := r.client.LRange(ctx, key, int64(-limit), -1).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: failed to get recent actions for room %d from %s: %w", roomID, key, err)
	}
	actions := make([]domain.Action, 0, len(actionStrs))
	for _, actionStr := range actionStrs {
		var action domain.Action
		if err := json.Unmarshal([]byte(actionStr), &action); err == nil {
			actions = append(actions, action)
		} else {
			logrus.Warnf("redis: failed to unmarshal action from history for room %d: %v, data: %s", roomID, err, actionStr)
		}
	}
	return actions, nil
}

// PushActionToHistory 将一个 Action 添加到 Redis 的历史记录队列。
func (r *RedisStateRepository) PushActionToHistory(ctx context.Context, roomID uint, action domain.Action) error {
	key := r.roomActionHistoryKey(roomID)
	actionBytes, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("redis: failed to marshal action for history (action id %d): %w", action.ID, err)
	}
	actionStr := string(actionBytes)
	pipe := r.client.Pipeline()
	pipe.RPush(ctx, key, actionStr)
	pipe.LTrim(ctx, key, -100, -1) // 保留最近 100 条
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis: failed to push action to history for room %d on key %s: %w", roomID, key, err)
	}
	return nil
}

// GetSnapshotCache 尝试从 Redis 缓存中获取快照。
func (r *RedisStateRepository) GetSnapshotCache(ctx context.Context, roomID uint) (*domain.Snapshot, error) {
	key := r.roomSnapshotCacheKey(roomID)
	snapshotStr, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// 映射为仓库定义的未找到错误
			return nil, repository.ErrNotFound // 使用通用 Not Found
		}
		return nil, fmt.Errorf("redis: failed to get snapshot cache for room %d from %s: %w", roomID, key, err)
	}
	var snapshot domain.Snapshot
	if err := json.Unmarshal([]byte(snapshotStr), &snapshot); err != nil {
		return nil, fmt.Errorf("redis: failed to unmarshal snapshot cache for room %d from %s: %w", roomID, key, err)
	}
	return &snapshot, nil
}

// SetSnapshotCache 将快照存入 Redis 缓存。 (接收 TTL 参数)
func (r *RedisStateRepository) SetSnapshotCache(ctx context.Context, roomID uint, snapshot *domain.Snapshot, ttlInSeconds int) error { // *** 接收 TTL 参数 ***
	ttl := time.Duration(ttlInSeconds) * time.Second // 转换为 time.Duration
	key := r.roomSnapshotCacheKey(roomID)
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("redis: failed to marshal snapshot for cache (room %d, version %d): %w", roomID, snapshot.Version, err)
	}
	snapshotStr := string(snapshotBytes)
	// 使用传入的 ttl (如果 ttl 为 0，则表示永不过期)
	err = r.client.Set(ctx, key, snapshotStr, ttl).Err()
	if err != nil {
		return fmt.Errorf("redis: failed to set snapshot cache for room %d on key %s: %w", roomID, key, err)
	}
	return nil
}

// CheckRateLimit 检查给定 key 的请求频率是否超限，并递增计数。
func (r *RedisStateRepository) CheckRateLimit(ctx context.Context, key string, limit int, duration time.Duration) (bool, error) {
	// 使用 Pipeline 减少网络往返
	pipe := r.client.Pipeline()
	// INCR 命令原子地增加计数器并返回新值
	incrCmd := pipe.Incr(ctx, key)
	// 设置或刷新过期时间
	pipe.Expire(ctx, key, duration)
	// 执行 Pipeline
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("redis: pipeline failed for rate limit check on key %s: %w", key, err)
	}
	// 获取 INCR 命令的结果
	count, err := incrCmd.Result()
	if err != nil {
		return false, fmt.Errorf("redis: failed to get incr result for rate limit on key %s: %w", key, err)
	}
	// 如果计数大于限制，则返回 true (表示超限)
	return count > int64(limit), nil
}

// PublishAction 将处理后的 Action 发布到指定房间的频道。
func (r *RedisStateRepository) PublishAction(ctx context.Context, roomID uint, action domain.Action) error {
	channel := r.roomPubSubChannel(roomID)
	payloadBytes, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("redis: failed to marshal action for publish (action id %d): %w", action.ID, err)
	}
	payload := string(payloadBytes)
	// 执行 Publish 命令
	err = r.client.Publish(ctx, channel, payload).Err()
	if err != nil {
		// 记录详细错误，包括 channel 和 payload 大小，方便调试
		logrus.WithFields(logrus.Fields{
			"channel":      channel,
			"payload_size": len(payload),
			"action_id":    action.ID,
			"room_id":      roomID,
		}).WithError(err).Error("Redis Publish failed")
		return fmt.Errorf("redis: failed to publish action to channel %s: %w", channel, err)
	}
	return nil
}