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

// Lua 脚本：原子地应用 DrawData (HSet/HDel) 并增加版本号 (INCR)
// KEYS[1]: room state key (e.g., "bb:room:1:state")
// KEYS[2]: room version key (e.g., "bb:room:1:version")
// ARGV[1]: field key for HSet/HDel (e.g., "10:20")
// ARGV[2]: color value for HSet (or empty string/special value for HDel)
// ARGV[3]: action type ("draw" or "erase")
var applyAndIncrScript = redis.NewScript(`
    local fieldKey = ARGV[1]
    local colorValue = ARGV[2]
    local actionType = ARGV[3]
    local stateKey = KEYS[1]
    local versionKey = KEYS[2]

    -- Apply action to state
    if actionType == 'draw' then
        redis.call('HSET', stateKey, fieldKey, colorValue)
    elseif actionType == 'erase' then
        redis.call('HDEL', stateKey, fieldKey)
    end

    -- Increment version
    local newVersion = redis.call('INCR', versionKey)

    return newVersion
`)

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

func (r *RedisStateRepository) roomLastSnapshotTimeKey(roomID uint) string {
	return fmt.Sprintf("%sroom:%d:last_snapshot_time", r.keyPrefix, roomID) // String key
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

// ---  实现 Snapshot Worker State 方法 ---

// GetLastSnapshotTime 获取指定房间上次快照的时间戳 (来自 Redis)
func (r *RedisStateRepository) GetLastSnapshotTime(ctx context.Context, roomID uint) (time.Time, error) {
	key := r.roomLastSnapshotTimeKey(roomID)
	// 时间戳通常存储为 Unix 时间戳 (秒或纳秒) 的字符串形式
	timestampStr, err := r.client.Get(ctx, key).Result()

	if err != nil {
		if errors.Is(err, redis.Nil) {
			// Key 不存在，表示从未记录过，返回 time.Time 的零值
			return time.Time{}, nil
		}
		// 其他 Redis 错误
		return time.Time{}, fmt.Errorf("redis: failed to get last snapshot time for room %d from %s: %w", roomID, key, err)
	}

	// 将字符串解析为 int64 (假设存储的是 Unix 秒)
	timestampSec, parseErr := strconv.ParseInt(timestampStr, 10, 64)
	if parseErr != nil {
		// Redis 中存储的值格式不正确
		logrus.Warnf("redis: failed to parse last snapshot time '%s' for room %d from %s: %v", timestampStr, roomID, key, parseErr)
		// 可以选择返回零值或错误
		return time.Time{}, nil // 容错处理，下次会重新生成
	}

	// 将 Unix 秒转换为 time.Time
	return time.Unix(timestampSec, 0), nil
}

// SetLastSnapshotTime 设置指定房间上次快照的时间戳 (存储到 Redis)
func (r *RedisStateRepository) SetLastSnapshotTime(ctx context.Context, roomID uint, timestamp time.Time, ttl time.Duration) error {
	key := r.roomLastSnapshotTimeKey(roomID)
	// 将 time.Time 转换为 Unix 时间戳 (秒) 的字符串
	timestampStr := strconv.FormatInt(timestamp.Unix(), 10)

	// 使用 Set 命令写入，并设置过期时间 TTL
	// 如果 ttl 为 0，表示不过期
	err := r.client.Set(ctx, key, timestampStr, ttl).Err()
	if err != nil {
		return fmt.Errorf("redis: failed to set last snapshot time for room %d on key %s: %w", roomID, key, err)
	}
	return nil
}

func (r *RedisStateRepository) CleanupRoomState(ctx context.Context, roomID uint) error {
	keysToDelete := []string{
		r.roomStateKey(roomID),
		r.roomVersionKey(roomID),
		r.roomOpCountKey(roomID),
		r.roomActionHistoryKey(roomID),
		r.roomSnapshotCacheKey(roomID),
		r.roomLastSnapshotTimeKey(roomID), // 也清理上次快照时间
	}

	// 使用 Del 命令批量删除 key
	// Del 返回成功删除的 key 的数量，如果 key 不存在也不会报错
	deletedCount, err := r.client.Del(ctx, keysToDelete...).Result()
	if err != nil {
		// 记录错误，但可能不是致命的
		logrus.WithError(err).Warnf("Redis: Failed to delete some keys during room cleanup for room %d", roomID)
		return fmt.Errorf("redis: failed to cleanup room state for room %d: %w", roomID, err)
	}
	logrus.Infof("Redis: Cleaned up %d keys for room %d", deletedCount, roomID)
	return nil
}

func (r *RedisStateRepository) ApplyActionDataAndIncrementVersionAtomically(ctx context.Context, roomID uint, actionType string, actionData domain.DrawData) (uint, error) {
	stateKey := r.roomStateKey(roomID)
	versionKey := r.roomVersionKey(roomID)
	fieldKey := fmt.Sprintf("%d:%d", actionData.X, actionData.Y)

	// 根据 DrawData 判断 actionType 和 colorValue
	var colorValue string
	if actionData.Color != "" { // 假设 color 非空代表 draw
		actionType = "draw"
		colorValue = actionData.Color
	} else { // color 为空代表 erase
		actionType = "erase"
		colorValue = "" // 对于 HDEL，这个值其实不重要
	}

	// 执行 Lua 脚本
	result, err := applyAndIncrScript.Run(ctx, r.client, []string{stateKey, versionKey}, fieldKey, colorValue, actionType).Result()

	if err != nil {
		// 检查是否是脚本未加载错误，如果是可以尝试加载并重试 (更健壮的实现)
		// if redis.HasErrorPrefix(err, "NOSCRIPT") { ... }
		return 0, fmt.Errorf("redis: failed to run applyAndIncr Lua script for room %d: %w", roomID, err)
	}

	// 解析 Lua 脚本返回的新版本号 (它返回的是 int64)
	newVersionInt, ok := result.(int64)
	if !ok {
		return 0, fmt.Errorf("redis: unexpected result type from applyAndIncr Lua script: %T", result)
	}

	return uint(newVersionInt), nil // 转换为 uint 返回
}
