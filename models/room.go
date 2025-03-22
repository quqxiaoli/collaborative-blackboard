package models

import "time"

// Room 表示协作房间
type Room struct {
	ID         uint      `gorm:"primaryKey"`      // 主键，自增
	CreatorID  uint      `gorm:"index;not null"`  // 创建者ID，外键，带索引
	InviteCode string    `gorm:"unique;not null"` // 邀请码，唯一且非空
	CreatedAt  time.Time // 创建时间
	LastActive time.Time // 最后活跃时间
}
