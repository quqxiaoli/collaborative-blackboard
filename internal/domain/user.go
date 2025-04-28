// Package models 定义了应用程序中使用的数据结构 (数据库模型)。
package domain

import "time"

// User 表示应用程序中的用户。
type User struct {
	ID        uint      `gorm:"primaryKey"`                // 用户唯一标识符 (主键)
	Username  string    `gorm:"type:varchar(191);uniqueIndex:idx_username;not null"`
	Password  string    `gorm:"type:text;not null"`                  // 存储的是哈希后的密码，不能为空
	Email     string    `gorm:"type:varchar(191);uniqueIndex:idx_email"`
	CreatedAt time.Time `gorm:"autoCreateTime"`            // 用户记录创建时间 (GORM 自动填充)
	UpdatedAt time.Time `gorm:"autoUpdateTime"`            // 用户记录最后更新时间 (GORM 自动填充, 可选添加)
}
