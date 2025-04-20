package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/sirupsen/logrus"
	// "gorm.io/gorm" // Service 不直接依赖 gorm
)

// SnapshotService 负责处理白板快照相关的业务逻辑。
type SnapshotService struct {
	snapshotRepo repository.SnapshotRepository // DB 操作
	stateRepo    repository.StateRepository    // Redis 缓存和实时状态操作
	actionRepo   repository.ActionRepository   // 用于获取操作计数
}

// NewSnapshotService 创建 SnapshotService 实例。
func NewSnapshotService(
	snapshotRepo repository.SnapshotRepository,
	stateRepo repository.StateRepository,
	actionRepo repository.ActionRepository,
) *SnapshotService {
	// 添加 nil 检查
	if snapshotRepo == nil || stateRepo == nil || actionRepo == nil {
		panic("All repositories must be non-nil for SnapshotService")
	}
	return &SnapshotService{
		snapshotRepo: snapshotRepo,
		stateRepo:    stateRepo,
		actionRepo:   actionRepo,
	}
}

// GetSnapshotForClient 获取发送给客户端的快照。
// 实现 "缓存优先，数据库备用，回填缓存" 策略。
func (s *SnapshotService) GetSnapshotForClient(ctx context.Context, roomID uint) (*domain.Snapshot, domain.BoardState, error) {
	logCtx := logrus.WithFields(logrus.Fields{"room_id": roomID, "operation": "GetSnapshotForClient"})

	// 1. 尝试从 Redis 缓存获取快照
	cachedSnapshot, err := s.stateRepo.GetSnapshotCache(ctx, roomID)
	if err == nil && cachedSnapshot != nil {
		logCtx.Info("Snapshot cache hit")
		state, parseErr := cachedSnapshot.ParseState()
		if parseErr != nil {
			logCtx.WithError(parseErr).Error("Failed to parse snapshot state from cache")
			// 缓存数据损坏，继续尝试从 DB 获取
		} else {
			return cachedSnapshot, state, nil // 缓存命中且解析成功
		}
	}
	// 检查是否是明确的未找到错误
	if err != nil && !errors.Is(err, repository.ErrNotFound) { // 假设 ErrNotFound 定义在 repository
		// 记录非 "not found" 的 Redis 错误
		logCtx.WithError(err).Warn("Failed to get snapshot from cache repository")
	} else if errors.Is(err, repository.ErrNotFound) {
		logCtx.Info("Snapshot cache miss")
	} else {
        // err == nil && cachedSnapshot == nil (理论上 Redis 不会这样返回)
        logCtx.Info("Snapshot cache miss (nil snapshot without error)")
    }


	// 2. 缓存未命中或解析失败，尝试从数据库获取最新快照
	dbSnapshot, err := s.snapshotRepo.GetLatestSnapshot(ctx, roomID)
	if err != nil {
		// 检查是否是未找到错误
		if errors.Is(err, repository.ErrSnapshotNotFound) || errors.Is(err, repository.ErrNotFound){ // 检查特定或通用错误
			logCtx.Info("No snapshot found in database, returning empty state")
			// 没有快照，返回空状态和版本 0
			emptySnapshot := &domain.Snapshot{
				RoomID: roomID,
				Version: 0,
                // Data 和 State 需要初始化为空，确保 ParseState 不会 panic
                Data: "{}",
			}
            emptyState, _ := emptySnapshot.ParseState() // 解析空 JSON
			return emptySnapshot, emptyState, nil
		}
		// 其他数据库错误
		logCtx.WithError(err).Error("Failed to get latest snapshot from database repository")
		return nil, nil, ErrInternalServer
	}
    // 防御性检查
    if dbSnapshot == nil {
         logCtx.Error("Snapshot repository returned nil snapshot without error")
         return nil, nil, ErrInternalServer
    }

	// 3. 数据库命中，解析状态并回填缓存
	state, parseErr := dbSnapshot.ParseState()
	if parseErr != nil {
		logCtx.WithError(parseErr).Error("Failed to parse snapshot state from database")
		return nil, nil, ErrInternalServer // 解析错误视为内部错误
	}

	logCtx.Info("Snapshot loaded from database")
	// 异步回填缓存 (使用带 TTL 的 SetSnapshotCache)
	go func(snapshotToCache *domain.Snapshot) {
		// 使用后台 context 进行缓存操作，避免阻塞主流程
		// 如果原始请求的 context 取消，后台操作仍会继续
		cacheCtx := context.Background()
		// 定义缓存 TTL，例如 1 小时 (3600 秒)
		ttlSeconds := 3600
		// 调用包含 TTL 的接口方法
		if err := s.stateRepo.SetSnapshotCache(cacheCtx, snapshotToCache.RoomID, snapshotToCache, ttlSeconds); err != nil { // *** 传递 TTL ***
			// 记录缓存失败，但不影响主流程
			logrus.WithFields(logrus.Fields{
				"room_id": snapshotToCache.RoomID,
				"version": snapshotToCache.Version,
			}).WithError(err).Warn("Failed to warm snapshot cache after DB load")
		} else {
			logrus.WithFields(logrus.Fields{
				"room_id": snapshotToCache.RoomID,
				"version": snapshotToCache.Version,
			}).Info("Snapshot cache warmed successfully")
		}
		// 注意：直接在 goroutine 中使用 snapshotToCache 可能有并发问题，如果 snapshotToCache 后续被修改。
		// 更安全的方式是传递一个深拷贝。对于当前结构，直接传递指针问题不大，但需注意。
	}(dbSnapshot)

	return dbSnapshot, state, nil
}

// CheckAndGenerateSnapshot 检查是否需要为房间生成快照，如果需要则生成。
// lastSnapshotTime 是上次成功生成快照的时间。
// 返回更新后的 lastSnapshotTime 和错误。
func (s *SnapshotService) CheckAndGenerateSnapshot(ctx context.Context, roomID uint, lastSnapshotTime time.Time) (time.Time, error) {
	logCtx := logrus.WithField("room_id", roomID)

	// 1. 获取自上次快照以来的操作计数
	opCount, err := s.actionRepo.GetCountSince(ctx, roomID, lastSnapshotTime)
	if err != nil {
		logCtx.WithError(err).Error("Failed to get action count since last snapshot")
		return lastSnapshotTime, ErrInternalServer // 返回原始时间戳和错误
	}

	// 2. 计算快照间隔并判断是否需要生成
	interval := calculateSnapshotInterval(int(opCount))
	if !shouldGenerateSnapshot(lastSnapshotTime, interval) {
		logCtx.Debugf("Snapshot condition not met (Last: %s, Interval: %s, OpsSince: %d)",
			lastSnapshotTime.Format(time.RFC3339), interval, opCount)
		return lastSnapshotTime, nil // 不需要生成，返回原始时间戳
	}

	logCtx.Info("Snapshot condition met, attempting to generate snapshot.")

	// 3. 生成快照
	newSnapshotTime := time.Now() // 记录尝试生成快照的时间
	if err := s.generateSnapshot(ctx, roomID); err != nil {
		logCtx.WithError(err).Error("Snapshot generation failed.")
		// 生成失败，不更新时间戳，以便下次更快重试
		return lastSnapshotTime, err // 返回原始时间和错误
	}

	logCtx.Info("Snapshot generated successfully.")
	// 生成成功，返回新的时间戳
	return newSnapshotTime, nil
}


// generateSnapshot 实际执行快照生成的逻辑。
func (s *SnapshotService) generateSnapshot(ctx context.Context, roomID uint) error {
	logCtx := logrus.WithField("room_id", roomID)

	// 1. 从 Redis 获取当前的实时状态和版本号 (调用 Repository)
	currentState, errState := s.stateRepo.GetBoardState(ctx, roomID)
	currentVersion, errVersion := s.stateRepo.GetCurrentVersion(ctx, roomID)

	// 处理错误 (版本获取错误是关键问题)
	if errVersion != nil {
		logCtx.WithError(errVersion).Error("Snapshot: Failed to get current version from state repository")
		return errVersion // 返回具体错误
	}
	// 状态获取错误可以容忍（视为空状态）
	if errState != nil {
		logCtx.WithError(errState).Warn("Snapshot: Failed to get current board state from state repository, proceeding with empty state")
		currentState = make(domain.BoardState) // 确保是空 map 而不是 nil
	}

	// 2. 创建 Snapshot 领域对象
	snapshot := &domain.Snapshot{
		RoomID:    roomID,
		CreatedAt: time.Now().UTC(), // 使用 UTC 时间
		Version:   currentVersion,
	}
	if err := snapshot.SetState(currentState); err != nil {
		logCtx.WithError(err).Error("Snapshot: Failed to set snapshot state")
		return fmt.Errorf("failed to set snapshot state: %w", err)
	}

	// 3. 保存快照到数据库 (调用 Repository)
	if err := s.snapshotRepo.SaveSnapshot(ctx, snapshot); err != nil {
		logCtx.WithError(err).Error("Snapshot: Failed to save snapshot to database repository")
		return err // 数据库保存失败是关键错误
	}

	// 4. 将新快照更新到 Redis 缓存 (异步)
	go func(snapshotToCache *domain.Snapshot) {
		cacheCtx := context.Background()
		ttlSeconds := 3600 // 快照生成后缓存 1 小时
		if err := s.stateRepo.SetSnapshotCache(cacheCtx, snapshotToCache.RoomID, snapshotToCache, ttlSeconds); err != nil {
			logrus.WithFields(logrus.Fields{"room_id": roomID, "version": snapshotToCache.Version}).WithError(err).Warn("Snapshot: Failed to update snapshot cache after generation")
		} else {
			logrus.WithFields(logrus.Fields{"room_id": roomID, "version": snapshotToCache.Version}).Info("Snapshot: Cache updated after generation")
		}
	}(snapshot) // 传递指针（假设 snapshot 在此之后不会被修改）

	// 5. 重置 Redis 中的操作计数器 (调用 Repository)
	if err := s.stateRepo.ResetOpCount(ctx, roomID); err != nil {
		// 记录错误，但不认为是生成快照失败
		logCtx.WithError(err).Warn("Snapshot: Failed to reset op_count in state repository")
	}

	logCtx.WithField("version", snapshot.Version).Info("Snapshot generated and saved.")
	return nil
}


// --- 快照任务辅助函数 (保持私有) ---

func calculateSnapshotInterval(opCountSinceLast int) time.Duration {
	// 可以根据实际情况调整这些阈值和间隔
	if opCountSinceLast > 100 { // 高活跃度
		return 1 * time.Minute   // 缩短间隔
	} else if opCountSinceLast > 20 { // 中等活跃度
		return 5 * time.Minute
	} else { // 低活跃度或首次
		return 15 * time.Minute // 较长间隔
	}
}

func shouldGenerateSnapshot(lastSnapshotTime time.Time, interval time.Duration) bool {
	// 如果从未生成过快照，或者距离上次生成时间已超过计算出的间隔
	return lastSnapshotTime.IsZero() || time.Since(lastSnapshotTime) >= interval
}

// isDuplicateEntryErrorString (如果需要，从 auth.go 复制或移到共享位置)
// func isDuplicateEntryErrorString(err error) bool { ... }