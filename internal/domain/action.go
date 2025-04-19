package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

// Action 表示用户在画板上执行的一个操作记录。
type Action struct {
	ID         uint      `gorm:"primaryKey"`             // 操作记录的唯一标识符 (主键)
	RoomID     uint      `gorm:"index;not null"`         // 操作发生的房间 ID (外键关联 Room.ID, 添加索引)
	UserID     uint      `gorm:"index;not null"`         // 执行操作的用户 ID (外键关联 User.ID, 添加索引)
	ActionType string    `gorm:"size:50;not null"`       // 操作类型，例如 "draw", "erase", "clear" 等 (限制长度)
	Data       string    `gorm:"type:text;not null"`     // 操作的具体数据，通常是 JSON 格式的字符串
	Timestamp  time.Time `gorm:"index;not null"`         // 操作发生的时间戳 (添加索引)
	Version    uint      `gorm:"not null"`               // 该操作基于的画板状态版本号，用于 OT 或冲突检测
	CreatedAt  time.Time `gorm:"autoCreateTime;index"`   // 记录创建时间 (GORM 自动填充, 可选添加索引)

	// 可以考虑添加与 User 和 Room 的关联关系
	// User User `gorm:"foreignKey:UserID"`
	// Room Room `gorm:"foreignKey:RoomID"`
}

// DrawData 定义了 "draw" 或 "erase" 类型操作的具体数据结构。
// 对于 "erase"，Color 字段可能为空或表示背景色。
type DrawData struct {
	X     int    `json:"x"`               // 操作目标的 X 坐标
	Y     int    `json:"y"`               // 操作目标的 Y 坐标
	Color string `json:"color,omitempty"` // 绘制的颜色 (例如 "#FF0000")，对于 erase 操作可能为空
	// 可以添加其他绘图属性，如画笔粗细等
	// BrushSize int `json:"brushSize,omitempty"`
}

// ParseData 将 Action 的 Data 字段 (JSON 字符串) 解析为 DrawData 结构体。
func (a *Action) ParseData() (DrawData, error) {
	var data DrawData
	// 检查 Data 字段是否为空，避免不必要的 Unmarshal 操作
	if a.Data == "" || a.Data == "null" { // "noop" action might have empty data
		// 根据 ActionType 决定是否返回错误
		if a.ActionType == "draw" || a.ActionType == "erase" { // Or other types requiring data
			return data, fmt.Errorf("action data is empty for action type %s", a.ActionType)
		}
		// For action types that don't require data, return empty struct and no error
		return data, nil
	}
	err := json.Unmarshal([]byte(a.Data), &data)
	if err != nil {
		return data, fmt.Errorf("failed to unmarshal action data: %w", err)
	}
	return data, nil
}

// SetData 将 DrawData 结构体序列化为 JSON 字符串，并设置到 Action 的 Data 字段。
func (a *Action) SetData(data DrawData) error {
	// 检查是否需要序列化 (例如，对于某些不需要数据的 ActionType)
	// if a.ActionType == "clear" { // Example
	//  a.Data = ""
	//  return nil
	// }
	bytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal action data: %w", err)
	}
	a.Data = string(bytes)
	return nil
}
