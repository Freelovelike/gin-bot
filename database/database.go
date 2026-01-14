package database

import (
	"fmt"
	"log"
	"os"

	"gin-bot/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

// getEnv 获取环境变量，如果不存在则返回默认值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// InitDB 初始化数据库连接
func InitDB() {
	// 获取数据库配置
	_ = getEnv("DB_DRIVER", "postgres") // 预留驱动选择逻辑
	dsn := getEnv("DB_DSN", "postgres://freelove:hwc20010616@localhost:5432/go_demo?sslmode=disable")

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// 自动迁移
	fmt.Println("Running database migrations...")
	err = DB.AutoMigrate(
		&models.User{},
		&models.ChatHistory{},
		&models.MemberEmbedding{},
		&models.Group{},
	)
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
	fmt.Println("Database initialization completed.")
}
