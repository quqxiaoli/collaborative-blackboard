// Package models 定义了应用程序中使用的数据结构 (数据库模型)。
package models

import "time"

// User 表示应用程序中的用户。
type User struct {
	ID        uint      `gorm:"primaryKey"`                // 用户唯一标识符 (主键)
	Username  string    `gorm:"uniqueIndex;not null"`      // 用户名，必须唯一且不能为空 (添加 uniqueIndex 以提高查询性能)
	Password  string    `gorm:"not null"`                  // 存储的是哈希后的密码，不能为空
	Email     string    `gorm:"uniqueIndex"`               // 用户邮箱，唯一 (可选)
	CreatedAt time.Time `gorm:"autoCreateTime"`            // 用户记录创建时间 (GORM 自动填充)
	UpdatedAt time.Time `gorm:"autoUpdateTime"`            // 用户记录最后更新时间 (GORM 自动填充, 可选添加)
}
