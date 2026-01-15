package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"gin-bot/config"

	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client

// InitRedis 初始化 Redis 客户端
func InitRedis() {
	RDB = redis.NewClient(&redis.Options{
		Addr:     config.Cfg.RedisAddr,
		Password: config.Cfg.RedisPassword,
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := RDB.Ping(ctx).Err(); err != nil {
		fmt.Printf("Warning: Failed to connect to Redis: %v\n", err)
		RDB = nil
		return
	}

	log.Println("Connected to Redis successfully")
}

// SaveTemporaryMemory 保存临时记忆到 Redis（带 TTL）
// key 格式: temp:group:{groupID}:user:{userQQ}:{msgID}
func SaveTemporaryMemory(ctx context.Context, groupID int64, userQQ string, msgID uint, content string, ttl time.Duration) error {
	if RDB == nil {
		return fmt.Errorf("redis not connected")
	}

	key := fmt.Sprintf("temp:group:%d:user:%s:%d", groupID, userQQ, msgID)
	return RDB.Set(ctx, key, content, ttl).Err()
}

// GetRecentTemporaryMemories 获取群组的最近临时记忆
func GetRecentTemporaryMemories(ctx context.Context, groupID int64, limit int) ([]string, error) {
	if RDB == nil {
		return nil, fmt.Errorf("redis not connected")
	}

	pattern := fmt.Sprintf("temp:group:%d:*", groupID)
	keys, err := RDB.Keys(ctx, pattern).Result()
	if err != nil {
		return nil, err
	}

	if len(keys) == 0 {
		return nil, nil
	}

	// 限制返回数量
	if len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}

	values, err := RDB.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	var memories []string
	for _, v := range values {
		if s, ok := v.(string); ok && s != "" {
			memories = append(memories, s)
		}
	}

	return memories, nil
}
