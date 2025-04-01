package models

import (
	"encoding/json"
	"time"
)

// Snapshot 表示房间的画板状态快照
type Snapshot struct {
	ID        uint      `gorm:"primaryKey"`
	RoomID    uint      `gorm:"index;not null"`
	Data      string    `gorm:"type:text;not null"` // JSON 编码的画板状态
	CreatedAt time.Time `gorm:"not null"`
}

// BoardState 表示画板状态
type BoardState map[string]string // 坐标 "x:y" -> 颜色

// ParseState 解析快照数据
func (s *Snapshot) ParseState() (BoardState, error) {
	var state BoardState
	err := json.Unmarshal([]byte(s.Data), &state)
	return state, err
}

// SetState 设置快照数据
func (s *Snapshot) SetState(state BoardState) error {
	bytes, err := json.Marshal(state)
	if err != nil {
		return err
	}
	s.Data = string(bytes)
	return nil
}
