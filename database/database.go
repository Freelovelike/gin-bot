package database

import (
	"fmt"
	"log"
	"os"
	"time"

	"gin-bot/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

	// 配置 GORM Logger，忽略 RecordNotFound 错误
	gormLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true, // 忽略 record not found 日志
			Colorful:                  true,
		},
	)

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		// SkipDefaultTransaction: true, // 解决跨海延迟的灵丹妙药
		PrepareStmt: true,
		Logger:      gormLogger,
	})
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
