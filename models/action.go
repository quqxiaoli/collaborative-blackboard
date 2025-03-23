package models

import (
	"encoding/json"
	"time"
)

// Action 表示涂鸦操作
type Action struct {
    ID         uint      `gorm:"primaryKey"`         // 主键，自增
    RoomID     uint      `gorm:"index;not null"`     // 房间ID，外键，带索引
    UserID     uint      `gorm:"index;not null"`     // 用户ID，外键，带索引
    ActionType string    `gorm:"not null"`           // 操作类型，如 "draw"、"erase"
    Data       string    `gorm:"type:text;not null"` // 操作数据，JSON 格式
    Timestamp  time.Time `gorm:"index;not null"`     // 操作时间，带索引
    Version    uint      `gorm:"not null"` // 操作基于的状态版本
}

// DrawData 定义画点操作的具体数据
type DrawData struct {
    X     int    `json:"x"`     // X 坐标
    Y     int    `json:"y"`     // Y 坐标
    Color string `json:"color"` // 颜色（如 "red", "blue"）
}

// ParseData 解析 Action 的 Data 字段为 DrawData
func (a *Action) ParseData() (DrawData, error) {
    var data DrawData
    err := json.Unmarshal([]byte(a.Data), &data)
    return data, err
}

// SetData 设置 Action 的 Data 字段
func (a *Action) SetData(data DrawData) error {
    bytes, err := json.Marshal(data)
    if err != nil {
        return err
    }
    a.Data = string(bytes)
    return nil
}