package models

import "time"

// User 表示用户信息
type User struct {
    ID        uint      `gorm:"primaryKey"`         // 主键，自增
    Username  string    `gorm:"unique;not null"`    // 用户名，唯一且非空
    Password  string    `gorm:"not null"`           // 密码，非空
    Email     string    `gorm:"unique"`             // 邮箱，可选但唯一
    CreatedAt time.Time // 创建时间，自动填充
}