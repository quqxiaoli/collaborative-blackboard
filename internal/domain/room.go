package domain

import "time"

// Room 表示一个协作画板房间。
type Room struct {
	ID         uint      `gorm:"primaryKey"`           // 房间唯一标识符 (主键)
	CreatorID  uint      `gorm:"index;not null"`       // 创建该房间的用户 ID (外键关联到 User.ID, 添加索引)
	InviteCode string    `gorm:"uniqueIndex;size:191;not null"` // 用于加入房间的邀请码，必须唯一且不能为空 (添加 uniqueIndex)
	CreatedAt  time.Time `gorm:"autoCreateTime"`       // 房间创建时间 (GORM 自动填充)
	LastActive time.Time `gorm:"index"`                // 房间最后活跃时间 (可选，用于清理不活跃房间等，添加索引)
	UpdatedAt  time.Time `gorm:"autoUpdateTime"`       // 记录最后更新时间 (GORM 自动填充, 可选添加)

	// 可以考虑添加与 User 的关联关系，如果需要方便地查询创建者信息
	// Creator User `gorm:"foreignKey:CreatorID"`
}
