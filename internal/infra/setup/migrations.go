package setup

import (
	"collaborative-blackboard/models"
	"fmt"
	"github.com/sirupsen/logrus"
)

// MigrateDB handles all database migrations
func MigrateDB() {
	// First, migrate the Users table with custom SQL to ensure proper index lengths
	migrateUsersTable()

	// Also handle Room table with custom SQL for proper index lengths
	migrateRoomsTable()

	// Then use AutoMigrate for the remaining models
	err := DB.AutoMigrate(&models.Action{}, &models.Snapshot{})
	if err != nil {
		logrus.Fatal("Failed to migrate other tables: ", err)
	}

	logrus.Info("Database migration completed successfully")
}

// migrateUsersTable handles the Users table migration specifically
func migrateUsersTable() {
	// Check if the users table exists
	var count int64
	DB.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'users'").Count(&count)

	if count == 0 {
		// Table doesn't exist - create it with proper index lengths
		createUsersTable()
	} else {
		// Table exists - update its structure if needed
		updateUsersTable()
	}
}

// createUsersTable creates the users table with proper index specifications
func createUsersTable() {
	sql := `
	CREATE TABLE users (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		username VARCHAR(255) NOT NULL,
		password TEXT NOT NULL,
		email VARCHAR(255),
		created_at DATETIME(3),
		updated_at DATETIME(3),
		UNIQUE INDEX idx_username (username(191)),
		UNIQUE INDEX idx_email (email(191))
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
	`

	if err := DB.Exec(sql).Error; err != nil {
		logrus.Fatal("Failed to create users table: ", err)
	}

	logrus.Info("Users table created successfully")
}

// updateUsersTable modifies the existing users table to fix any issues
func updateUsersTable() {
	// First, check and drop existing problematic indexes
	var indexExists int64
	DB.Raw("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'users' AND index_name = 'idx_username'").Count(&indexExists)
	
	if indexExists > 0 {
		if err := DB.Exec("DROP INDEX idx_username ON users").Error; err != nil {
			logrus.Warn(fmt.Sprintf("Could not drop index idx_username: %v", err))
		}
	}

	DB.Raw("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'users' AND index_name = 'idx_email'").Count(&indexExists)
	
	if indexExists > 0 {
		if err := DB.Exec("DROP INDEX idx_email ON users").Error; err != nil {
			logrus.Warn(fmt.Sprintf("Could not drop index idx_email: %v", err))
		}
	}

	// Use specific column types with proper lengths for indexed columns
	alterTable := `
	ALTER TABLE users 
	MODIFY COLUMN username VARCHAR(255) NOT NULL,
	MODIFY COLUMN email VARCHAR(255);
	`

	if err := DB.Exec(alterTable).Error; err != nil {
		logrus.Warn(fmt.Sprintf("Error altering users table columns: %v", err))
		// Continue anyway - the indexes are our main concern
	}

	// Re-create indexes with proper length specifications
	createUsernameIndex := "CREATE UNIQUE INDEX idx_username ON users(username(191))"
	if err := DB.Exec(createUsernameIndex).Error; err != nil {
		logrus.Fatal("Failed to create username index: ", err)
	}

	createEmailIndex := "CREATE UNIQUE INDEX idx_email ON users(email(191))"
	if err := DB.Exec(createEmailIndex).Error; err != nil {
		logrus.Fatal("Failed to create email index: ", err)
	}

	logrus.Info("Users table indexes updated successfully")
}

// migrateRoomsTable handles the Rooms table migration specifically
func migrateRoomsTable() {
	// Check if the rooms table exists
	var count int64
	DB.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'rooms'").Count(&count)

	if count == 0 {
		// Table doesn't exist - create it with proper index lengths
		createRoomsTable()
	} else {
		// Table exists - update its structure if needed
		updateRoomsTable()
	}
}

// createRoomsTable creates the rooms table with proper index specifications
func createRoomsTable() {
	sql := `
	CREATE TABLE rooms (
		id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		creator_id BIGINT UNSIGNED NOT NULL,
		invite_code VARCHAR(255) NOT NULL,
		created_at DATETIME(3),
		last_active DATETIME(3),
		updated_at DATETIME(3),
		INDEX idx_creator_id (creator_id),
		INDEX idx_last_active (last_active),
		UNIQUE INDEX idx_invite_code (invite_code(191))
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
	`

	if err := DB.Exec(sql).Error; err != nil {
		logrus.Fatal("Failed to create rooms table: ", err)
	}

	logrus.Info("Rooms table created successfully")
}

// updateRoomsTable modifies the existing rooms table to fix any issues
func updateRoomsTable() {
	// Check and drop existing problematic index for invite_code
	var indexExists int64
	DB.Raw("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'rooms' AND index_name = 'idx_invite_code'").Count(&indexExists)
	
	if indexExists > 0 {
		if err := DB.Exec("DROP INDEX idx_invite_code ON rooms").Error; err != nil {
			logrus.Warn(fmt.Sprintf("Could not drop index idx_invite_code: %v", err))
		}
	}

	// Ensure the invite_code column uses VARCHAR instead of TEXT
	if err := DB.Exec("ALTER TABLE rooms MODIFY invite_code VARCHAR(255) NOT NULL").Error; err != nil {
		logrus.Warn(fmt.Sprintf("Error modifying invite_code column: %v", err))
		// Continue anyway - we need to recreate the index
	}

	// Re-create the invite_code index with proper length
	if err := DB.Exec("CREATE UNIQUE INDEX idx_invite_code ON rooms(invite_code(191))").Error; err != nil {
		logrus.Fatal("Failed to create invite_code index: ", err)
	}

	logrus.Info("Rooms table indexes updated successfully")
}
