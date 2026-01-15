package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gin-bot/database"

	redis "github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

// ScheduledTask 任务结构体
type ScheduledTask struct {
	ID        string `json:"id"`
	Type      string `json:"type"`       // "once" 或 "periodic"
	Content   string `json:"content"`    // 提醒内容
	GroupID   int64  `json:"group_id"`   // 目标群组
	UserID    int64  `json:"user_id"`    // 提醒对象
	TimeExpr  string `json:"time_expr"`  // 10分钟后，或者 cron 表达式
	TargetAt  int64  `json:"target_at"`  // 目标执行时间戳 (仅针对 once 类型)
}

// MsgSender 统一消息发送函数类型
type MsgSender func(groupID int64, userID int64, content string)

var (
	GlobalSender MsgSender
	CronManager  *cron.Cron
	ZSetKey      = "tasks:oneshot"
)

// InitScheduler 初始化调度器
func InitScheduler(sender MsgSender) {
	GlobalSender = sender
	CronManager = cron.New(cron.WithSeconds())
	CronManager.Start()

	// 启动 Redis ZSet 轮询协程
	go startZSetPoll()

	log.Println("Scheduler initialized successfully")
}

// AddTask 添加任务
func AddTask(t ScheduledTask) error {
	if t.Type == "periodic" {
		// 添加周期任务
		_, err := CronManager.AddFunc(t.TimeExpr, func() {
			if GlobalSender != nil {
				GlobalSender(t.GroupID, t.UserID, "【周期提醒】"+t.Content)
			}
		})
		return err
	}

	// 添加一次性任务到 Redis ZSet
	if database.RDB == nil {
		return fmt.Errorf("redis not connected")
	}

	data, _ := json.Marshal(t)
	err := database.RDB.ZAdd(context.Background(), ZSetKey, redis.Z{
		Score:  float64(t.TargetAt),
		Member: string(data),
	}).Err()
	return err
}

// startZSetPoll 轮询 Redis ZSet 执行一次性任务
func startZSetPoll() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if database.RDB == nil {
			continue
		}

		ctx := context.Background()
		now := time.Now().Unix()

		// 获取已到期的任务
		vals, err := database.RDB.ZRangeByScore(ctx, ZSetKey, &redis.ZRangeBy{
			Min: "0",
			Max: fmt.Sprintf("%d", now),
		}).Result()
		if err != nil {
			log.Printf("[Scheduler] Poll error: %v", err)
			continue
		}

		for _, val := range vals {
			var t ScheduledTask
			if err := json.Unmarshal([]byte(val), &t); err != nil {
				continue
			}

			// 执行并移除
			if GlobalSender != nil {
				GlobalSender(t.GroupID, t.UserID, "【闹钟提醒】"+t.Content)
			}
			database.RDB.ZRem(ctx, ZSetKey, val)
		}
	}
}
