package service

import (
	"context"
	"fmt"
	"time"

	// 使用正确的模块路径
	"collaborative-blackboard/internal/domain"
	"collaborative-blackboard/internal/repository"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm" // 引入 gorm 以便检查 ErrRecordNotFound
    "errors"       // 引入 errors
)

// SnapshotService 负责处理白板快照相关的业务逻辑。
type SnapshotService struct {
	snapshotRepo repository.SnapshotRepository // DB 操作
	stateRepo    repository.StateRepository  // Redis 缓存和实时状态操作
	actionRepo   repository.ActionRepository // 用于获取操作计数
}

// NewSnapshotService 创建 SnapshotService 实例。
func NewSnapshotService(
	snapshotRepo repository.SnapshotRepository,
	stateRepo repository.StateRepository,
	actionRepo repository.ActionRepository,
) *SnapshotService {
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
			// 缓存数据损坏，尝试从 DB 获取
		} else {
			return cachedSnapshot, state, nil // 缓存命中且解析成功
		}
	}
	if err != nil {
		// 记录非 "not found" 的 Redis 错误
        // (需要 StateRepository 实现返回可识别的 "not found" 错误)
        // if !errors.Is(err, repository.ErrCacheMiss) { // 理想情况
		    logCtx.WithError(err).Warn("Failed to get snapshot from cache")
        // } else {
             logCtx.Info("Snapshot cache miss")
        // }
	}

	// 2. 缓存未命中或解析失败，尝试从数据库获取最新快照
	dbSnapshot, err := s.snapshotRepo.GetLatestSnapshot(ctx, roomID)
	if err != nil {
        // 检查是否是 GORM 的 RecordNotFound 错误
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logCtx.Info("No snapshot found in database, returning empty state")
			// 没有快照，返回空状态和版本 0
			emptySnapshot := &domain.Snapshot{RoomID: roomID, Version: 0}
			emptyState := make(domain.BoardState)
			return emptySnapshot, emptyState, nil
		}
		// 其他数据库错误
		logCtx.WithError(err).Error("Failed to get latest snapshot from database")
		return nil, nil, ErrInternalServer
	}

	// 3. 数据库命中，解析状态并回填缓存
	state, parseErr := dbSnapshot.ParseState()
	if parseErr != nil {
		logCtx.WithError(parseErr).Error("Failed to parse snapshot state from database")
		// 即使解析失败，也可能需要返回快照元数据？或者返回错误？
		// 暂时返回错误
		return nil, nil, ErrInternalServer
	}

	logCtx.Info("Snapshot loaded from database")
	// 异步回填缓存
	go func(snapshotToCache *domain.Snapshot) {
		cacheCtx := context.Background() // 使用后台 context 进行缓存
		if err := s.stateRepo.SetSnapshotCache(cacheCtx, snapshotToCache.RoomID, snapshotToCache); err != nil {
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
	}(dbSnapshot)

	return dbSnapshot, state, nil
}

// CheckAndGenerateSnapshot 检查是否需要为房间生成快照，如果需要则生成。
// lastSnapshotTime 是上次成功生成快照的时间。
func (s *SnapshotService) CheckAndGenerateSnapshot(ctx context.Context, roomID uint, lastSnapshotTime time.Time) (time.Time, error) {
	logCtx := logrus.WithField("room_id", roomID)

	// 1. 获取自上次快照以来的操作计数
	opCount, err := s.actionRepo.GetCountSince(ctx, roomID, lastSnapshotTime)
	if err != nil {
		logCtx.WithError(err).Error("Failed to get action count since last snapshot")
		return lastSnapshotTime, ErrInternalServer // 返回原始时间戳和错误
	}

	// 2. 计算快照间隔并判断是否需要生成
	interval := calculateSnapshotInterval(int(opCount)) // 使用之前的辅助函数
	if !shouldGenerateSnapshot(lastSnapshotTime, interval) { // 使用之前的辅助函数
		logCtx.Debugf("Snapshot condition not met (Last: %s, Interval: %s, OpsSince: %d)",
			lastSnapshotTime.Format(time.RFC3339), interval, opCount)
		return lastSnapshotTime, nil // 不需要生成，返回原始时间戳
	}

	logCtx.Info("Snapshot condition met, attempting to generate snapshot.")

	// 3. 生成快照
	newSnapshotTime := time.Now()
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

	// 处理错误 (允许状态为空，但版本获取错误是问题)
	if errVersion != nil {
        // 假设 repo 在 key 不存在时返回 0 和 nil 错误
        logCtx.WithError(errVersion).Error("Snapshot: Failed to get current version from state repository")
        return errVersion
	}
	if errState != nil {
         // 允许状态获取错误 (可能为空)，但记录警告
         logCtx.WithError(errState).Warn("Snapshot: Failed to get current board state from state repository, proceeding with potentially empty state")
         if currentState == nil {
             currentState = make(domain.BoardState) // 确保不是 nil
         }
	}


	// 2. 创建 Snapshot 领域对象
	snapshot := &domain.Snapshot{
		RoomID:    roomID,
		CreatedAt: time.Now().UTC(),
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

	// 4. [可选] 将新快照更新到 Redis 缓存 (可以异步进行)
	go func(snapshotToCache *domain.Snapshot) {
        cacheCtx := context.Background()
		if err := s.stateRepo.SetSnapshotCache(cacheCtx, snapshotToCache.RoomID, snapshotToCache); err != nil {
			logrus.WithFields(logrus.Fields{"room_id": roomID, "version": snapshotToCache.Version}).WithError(err).Warn("Snapshot: Failed to update snapshot cache after generation")
		} else {
            logrus.WithFields(logrus.Fields{"room_id": roomID, "version": snapshotToCache.Version}).Info("Snapshot: Cache updated after generation")
        }
	}(snapshot)


	// 5. 重置 Redis 中的操作计数器 (调用 Repository)
	if err := s.stateRepo.ResetOpCount(ctx, roomID); err != nil {
		// 记录错误，但不一定是致命的
		logCtx.WithError(err).Warn("Snapshot: Failed to reset op_count in state repository")
	}

	logCtx.WithField("version", snapshot.Version).Info("Snapshot generated and saved.")
	return nil
}


// --- 快照任务辅助函数 (从 snapshot.go 移入，保持私有) ---

func calculateSnapshotInterval(opCountSinceLast int) time.Duration {
	if opCountSinceLast > 100 {
		return 30 * time.Second
	} else if opCountSinceLast > 20 {
		return 2 * time.Minute
	} else {
		return 10 * time.Minute
	}
}

func shouldGenerateSnapshot(lastSnapshotTime time.Time, interval time.Duration) bool {
	return lastSnapshotTime.IsZero() || time.Since(lastSnapshotTime) >= interval
}