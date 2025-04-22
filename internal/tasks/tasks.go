package tasks

import (
	"encoding/json"
	"collaborative-blackboard/internal/domain" // 导入 Action 模型
)

// 定义任务类型常量
const (
	TypeActionPersistence = "action:persist" // Action 持久化任务类型
	// 可以定义其他任务类型，例如:
	// TypeSnapshotGeneration = "snapshot:generate"
	// TypeClientCleanup      = "client:cleanup"
)

// ActionPersistencePayload 定义了 Action 持久化任务的数据结构
type ActionPersistencePayload struct {
	// 直接嵌入 Action 对象，或者只包含必要信息
	// 使用 Action 对象更方便，但序列化后会稍大
	Action domain.Action
	// 或者只传递 Action 的 JSON 字符串，在 Worker 端反序列化
	// ActionJSON string
}

// NewActionPersistenceTask 创建一个新的 Action 持久化任务
func NewActionPersistenceTask(action domain.Action) ([]byte, error) {
	payload := ActionPersistencePayload{
		Action: action,
	}
	// 将 Payload 序列化为 JSON 字节
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	// 注意：Asynq 的 NewTask 现在推荐直接传递 []byte payload
	// task := asynq.NewTask(TypeActionPersistence, payloadBytes)
	return payloadBytes, nil // 返回序列化后的 payload
}