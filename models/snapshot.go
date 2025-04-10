package models

import (
	"encoding/json"
	"fmt"
	"time"
)

// Snapshot 存储特定时间点某个房间画板的完整状态。
type Snapshot struct {
	ID        uint      `gorm:"primaryKey"`         // 快照唯一标识符 (主键)
	RoomID    uint      `gorm:"index;not null"`     // 快照对应的房间 ID (外键关联 Room.ID, 添加索引)
	Data      string    `gorm:"type:longtext;not null"` // 存储画板状态的 JSON 字符串 (使用 longtext 以支持更大的画板)
	CreatedAt time.Time `gorm:"index;not null"`     // 快照创建时间 (添加索引)
	Version   uint      `gorm:"index"`              // 快照对应的最后一个 Action 的版本号 (可选，用于恢复)

	// 可以考虑添加与 Room 的关联关系
	// Room Room `gorm:"foreignKey:RoomID"`
}

// BoardState 定义了画板状态的数据结构。
// 使用 map 将坐标（格式化为 "x:y" 字符串）映射到颜色字符串。
type BoardState map[string]string // 例如: {"10:20": "#FF0000", "11:21": "#0000FF"}

// ParseState 将 Snapshot 的 Data 字段 (JSON 字符串) 解析为 BoardState。
func (s *Snapshot) ParseState() (BoardState, error) {
	var state BoardState
	if s.Data == "" {
		// 如果快照数据为空，返回一个空的 map 而不是错误
		return make(BoardState), nil
	}
	err := json.Unmarshal([]byte(s.Data), &state)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot data: %w", err)
	}
	// 如果 JSON 解析结果是 nil (例如，原始数据是 "null")，也返回一个空的 map
	if state == nil {
		return make(BoardState), nil
	}
	return state, nil
}

// SetState 将 BoardState 序列化为 JSON 字符串，并设置到 Snapshot 的 Data 字段。
func (s *Snapshot) SetState(state BoardState) error {
	// 如果 state 为 nil 或空，将其序列化为空 JSON 对象 "{}" 或空字符串
	if state == nil || len(state) == 0 {
		s.Data = "{}" // 或者 s.Data = ""
		return nil
	}
	bytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal board state: %w", err)
	}
	s.Data = string(bytes)
	return nil
}
