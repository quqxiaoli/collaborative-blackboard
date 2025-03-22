package models

import "time"

// Action 表示涂鸦操作
type Action struct {
    ID         uint      `gorm:"primaryKey"`         // 主键，自增
    RoomID     uint      `gorm:"index;not null"`     // 房间ID，外键，带索引
    UserID     uint      `gorm:"index;not null"`     // 用户ID，外键，带索引
    ActionType string    `gorm:"not null"`           // 操作类型，如 "draw"、"erase"
    Data       string    `gorm:"type:text;not null"` // 操作数据，JSON 格式
    Timestamp  time.Time `gorm:"index;not null"`     // 操作时间，带索引
}