package setup // 确认包名是 setup

import (
	// 使用正确的 Domain 模型路径
	"collaborative-blackboard/internal/domain"
	"fmt"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm" // 导入 gorm
)

// MigrateDB handles all database migrations using the provided GORM DB instance.
// 返回错误以便调用者知道迁移是否成功。
func MigrateDB(db *gorm.DB) error {
	// 检查 db 是否为 nil
	if db == nil {
		return fmt.Errorf("cannot migrate database with nil DB connection")
	}

	// 迁移 Users 表 (使用传入的 db)
	if err := migrateUsersTable(db); err != nil {
		return fmt.Errorf("failed to migrate users table: %w", err)
	}

	// 迁移 Rooms 表 (使用传入的 db)
	if err := migrateRoomsTable(db); err != nil {
		return fmt.Errorf("failed to migrate rooms table: %w", err)
	}

	// 使用 AutoMigrate 迁移剩余的模型 (使用传入的 db)
	// 将 domain 包下的模型传入
	err := db.AutoMigrate(
		&domain.Action{},
		&domain.Snapshot{},
		// 确保 domain.User 和 domain.Room 结构体也包含必要的 GORM tags
		// 如果 migrateUsersTable/migrateRoomsTable 已创建表，AutoMigrate 会尝试添加新列或索引
		// &domain.User{}, // 通常自定义 SQL 创建后不需要再 AutoMigrate
		// &domain.Room{}, // 通常自定义 SQL 创建后不需要再 AutoMigrate
	)
	if err != nil {
		logrus.Errorf("Failed to auto-migrate other tables: %v", err)
		return fmt.Errorf("failed to auto-migrate tables: %w", err)
	}

	logrus.Info("Database migration completed successfully")
	return nil // 迁移成功
}

// migrateUsersTable 使用传入的 db 处理 Users 表迁移，并返回错误
func migrateUsersTable(db *gorm.DB) error {
	var count int64
	// 使用传入的 db
	db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'users'").Count(&count)

	if count == 0 {
		if err := createUsersTable(db); err != nil { // 传入 db
			return err
		}
	} else {
		if err := updateUsersTable(db); err != nil { // 传入 db
			return err
		}
	}
	return nil
}

// createUsersTable 使用传入的 db 创建 users 表，并返回错误
func createUsersTable(db *gorm.DB) error {
	sql := `
	CREATE TABLE users (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		username VARCHAR(191) NOT NULL, -- 限制长度以匹配索引
		password TEXT NOT NULL,
		email VARCHAR(191), -- 限制长度以匹配索引
		created_at DATETIME(3),
		updated_at DATETIME(3),
		UNIQUE INDEX idx_username (username), -- GORM 会自动处理长度，或者保持 (191)
		UNIQUE INDEX idx_email (email)       -- GORM 会自动处理长度，或者保持 (191)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
	`
	// 使用传入的 db
	if err := db.Exec(sql).Error; err != nil {
		logrus.Errorf("Failed to create users table: %v", err)
		return fmt.Errorf("failed to create users table: %w", err)
	}
	logrus.Info("Users table created successfully")
	return nil
}

// updateUsersTable 使用传入的 db 修改 users 表，并返回错误
func updateUsersTable(db *gorm.DB) error {
	// 检查并可能修改列类型或索引
	// 使用 db.Migrator() 获取迁移器进行更安全的模式修改可能更好
	// 例如：db.Migrator().AlterColumn(&domain.User{}, "Username")
	// 但由于之前使用了原生 SQL，这里暂时保留，但标记为可改进
	// TODO: Consider using db.Migrator() for safer schema updates

	// 示例：确保 username 和 email 列类型正确 (如果 AutoMigrate 未处理)
	if err := db.Exec("ALTER TABLE users MODIFY COLUMN username VARCHAR(191) NOT NULL").Error; err != nil {
		logrus.Warnf("Could not modify username column: %v", err)
		// 可能不是严重错误，继续
	}
	if err := db.Exec("ALTER TABLE users MODIFY COLUMN email VARCHAR(191)").Error; err != nil {
		logrus.Warnf("Could not modify email column: %v", err)
	}

	// GORM 的 AutoMigrate 通常能处理索引的创建和更新
	// 如果自定义 SQL 创建的索引有问题，AutoMigrate 可能失败
	// 确保 domain.User 结构体有正确的 GORM 索引标签：
	// type User struct {
	//     ...
	//     Username string `gorm:"uniqueIndex:idx_username,size:191"`
	//     Email    string `gorm:"uniqueIndex:idx_email,size:191"`
	// }
	// 然后调用 AutoMigrate 处理 User 模型
	if err := db.AutoMigrate(&domain.User{}); err != nil {
		logrus.Errorf("Failed to auto-migrate User table for index updates: %v", err)
		return fmt.Errorf("failed to migrate user indexes: %w", err)
	}


	logrus.Info("Users table schema checked/updated successfully")
	return nil
}

// migrateRoomsTable 使用传入的 db 处理 Rooms 表迁移，并返回错误
func migrateRoomsTable(db *gorm.DB) error {
	var count int64
	db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'rooms'").Count(&count)

	if count == 0 {
		if err := createRoomsTable(db); err != nil { // 传入 db
			return err
		}
	} else {
		if err := updateRoomsTable(db); err != nil { // 传入 db
			return err
		}
	}
	return nil
}

// createRoomsTable 使用传入的 db 创建 rooms 表，并返回错误
func createRoomsTable(db *gorm.DB) error {
	sql := `
	CREATE TABLE rooms (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		creator_id BIGINT UNSIGNED NOT NULL,
		invite_code VARCHAR(191) NOT NULL, -- 限制长度以匹配索引
		created_at DATETIME(3),
		last_active DATETIME(3),
		updated_at DATETIME(3),
		INDEX idx_creator_id (creator_id),
		INDEX idx_last_active (last_active),
		UNIQUE INDEX idx_invite_code (invite_code) -- GORM 会自动处理长度，或保持 (191)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
	`
	if err := db.Exec(sql).Error; err != nil {
		logrus.Errorf("Failed to create rooms table: %v", err)
		return fmt.Errorf("failed to create rooms table: %w", err)
	}
	logrus.Info("Rooms table created successfully")
	return nil
}

// updateRoomsTable 使用传入的 db 修改 rooms 表，并返回错误
func updateRoomsTable(db *gorm.DB) error {
	// 同样，优先使用 db.Migrator() 或 AutoMigrate
	// 确保 domain.Room 结构体有正确的 GORM 索引标签：
	// type Room struct {
	//     ...
	//     InviteCode string `gorm:"uniqueIndex:idx_invite_code,size:191"`
	// }
	if err := db.AutoMigrate(&domain.Room{}); err != nil {
		logrus.Errorf("Failed to auto-migrate Room table for index updates: %v", err)
		return fmt.Errorf("failed to migrate room indexes: %w", err)
	}

	logrus.Info("Rooms table schema checked/updated successfully")
	return nil
}