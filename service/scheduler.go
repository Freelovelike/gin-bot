package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gin-bot/database"

	redis "github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

// ScheduledTask 任务结构体
type ScheduledTask struct {
	ID       string `json:"id"`
	Type     string `json:"type"`      // "once" 或 "periodic"
	Content  string `json:"content"`   // 提醒内容
	GroupID  int64  `json:"group_id"`  // 目标群组
	UserID   int64  `json:"user_id"`   // 提醒对象
	TimeExpr string `json:"time_expr"` // 10分钟后，或者 cron 表达式
	TargetAt int64  `json:"target_at"` // 目标执行时间戳 (仅针对 once 类型)
}

// MsgSender 统一消息发送函数类型
type MsgSender func(groupID int64, userID int64, content string)

var (
	GlobalSender    MsgSender
	CronManager     *cron.Cron
	ZSetKey         = "tasks:oneshot:schedule"      // 仅存储任务 ID -> 时间戳
	HashKeyOneshot  = "tasks:oneshot:data"          // 存储任务详情 (Once)
	HashKeyPeriodic = "tasks:periodic:data"         // 存储任务详情 (Periodic)
	PeriodicEntries = make(map[string]cron.EntryID) // ID -> Cron EntryID
	schedulerMu     sync.RWMutex
)

// InitScheduler 初始化调度器
func InitScheduler(sender MsgSender) {
	GlobalSender = sender
	CronManager = cron.New(cron.WithSeconds(), cron.WithLocation(time.Local))
	CronManager.Start()

	// 启动 Redis ZSet 轮询协程
	go startZSetPoll()

	// 重载周期任务
	go ReloadPeriodicTasks()

	log.Println("Scheduler initialized successfully")
}

// ReloadPeriodicTasks 从 Redis 加载并恢复周期任务
func ReloadPeriodicTasks() {
	if database.RDB == nil {
		return
	}
	ctx := context.Background()
	all, err := database.RDB.HGetAll(ctx, HashKeyPeriodic).Result()
	if err != nil {
		log.Printf("[Scheduler] Failed to reload periodic tasks: %v", err)
		return
	}

	schedulerMu.Lock()
	defer schedulerMu.Unlock()

	for id, data := range all {
		var t ScheduledTask
		if err := json.Unmarshal([]byte(data), &t); err != nil {
			log.Printf("[Scheduler] Failed to unmarshal task %s: %v", id, err)
			continue
		}
		// 创建局部副本，避免闭包捕获循环变量
		taskCopy := t
		entryID, err := CronManager.AddFunc(taskCopy.TimeExpr, func() {
			if GlobalSender != nil {
				GlobalSender(taskCopy.GroupID, taskCopy.UserID, "【周期提醒】"+taskCopy.Content)
			}
		})
		if err == nil {
			PeriodicEntries[id] = entryID
			log.Printf("[Scheduler] Reloaded periodic task: %s", id)
		} else {
			log.Printf("[Scheduler] Failed to add cron for task %s: %v", id, err)
		}
	}
}

// AddTask 添加任务
func AddTask(t ScheduledTask) error {
	if t.ID == "" {
		t.ID = fmt.Sprintf("task_%d_%d", time.Now().UnixNano(), t.UserID)
	}

	if database.RDB == nil {
		return fmt.Errorf("redis not connected")
	}

	ctx := context.Background()

	if t.Type == "periodic" {
		// 添加周期任务到 Cron
		schedulerMu.Lock()
		defer schedulerMu.Unlock()

		// 创建局部副本，避免闭包捕获循环变量
		taskCopy := t
		entryID, err := CronManager.AddFunc(taskCopy.TimeExpr, func() {
			if GlobalSender != nil {
				GlobalSender(taskCopy.GroupID, taskCopy.UserID, "【周期提醒】"+taskCopy.Content)
			}
		})
		if err != nil {
			return err
		}

		// 记录映射和持久化详情
		PeriodicEntries[t.ID] = entryID
		data, _ := json.Marshal(t)
		return database.RDB.HSet(ctx, HashKeyPeriodic, t.ID, string(data)).Err()
	}

	// 添加一次性任务 (Hash 存详情 + ZSet 调度)
	data, _ := json.Marshal(t)
	// 1. 存入 Hash 详情
	if err := database.RDB.HSet(ctx, HashKeyOneshot, t.ID, string(data)).Err(); err != nil {
		return err
	}
	// 2. 存入 ZSet 调度
	err := database.RDB.ZAdd(ctx, ZSetKey, redis.Z{
		Score:  float64(t.TargetAt),
		Member: t.ID, // 仅存 ID
	}).Err()
	return err
}

// ListTasks 列出指定范围的任务
func ListTasks(groupID int64, userID int64) []ScheduledTask {
	ctx := context.Background()
	tasks := []ScheduledTask{}

	if database.RDB == nil {
		return tasks
	}

	// 1. 获取一次性任务
	onceData, err := database.RDB.HVals(ctx, HashKeyOneshot).Result()
	if err != nil {
		log.Printf("[Scheduler] Failed to get oneshot tasks: %v", err)
	}
	for _, d := range onceData {
		var t ScheduledTask
		if err := json.Unmarshal([]byte(d), &t); err != nil {
			log.Printf("[Scheduler] Failed to unmarshal oneshot task: %v", err)
			continue
		}
		if (groupID == 0 || t.GroupID == groupID) && (userID == 0 || t.UserID == userID) {
			tasks = append(tasks, t)
		}
	}

	// 2. 获取周期任务
	periodicData, err := database.RDB.HVals(ctx, HashKeyPeriodic).Result()
	if err != nil {
		log.Printf("[Scheduler] Failed to get periodic tasks: %v", err)
	}
	for _, d := range periodicData {
		var t ScheduledTask
		if err := json.Unmarshal([]byte(d), &t); err != nil {
			log.Printf("[Scheduler] Failed to unmarshal periodic task: %v", err)
			continue
		}
		if (groupID == 0 || t.GroupID == groupID) && (userID == 0 || t.UserID == userID) {
			tasks = append(tasks, t)
		}
	}

	return tasks
}

// RemoveTask 移除任务
func RemoveTask(id string) error {
	ctx := context.Background()
	if database.RDB == nil {
		return fmt.Errorf("redis not connected")
	}

	// 检查是否在周期任务中
	schedulerMu.Lock()
	if entryID, ok := PeriodicEntries[id]; ok {
		CronManager.Remove(entryID)
		delete(PeriodicEntries, id)
		schedulerMu.Unlock()
		database.RDB.HDel(ctx, HashKeyPeriodic, id)
		return nil
	}
	schedulerMu.Unlock()

	// 尝试从一次性任务中移除
	database.RDB.ZRem(ctx, ZSetKey, id)
	return database.RDB.HDel(ctx, HashKeyOneshot, id).Err()
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

		// 获取已到期的任务 ID
		ids, err := database.RDB.ZRangeByScore(ctx, ZSetKey, &redis.ZRangeBy{
			Min: "0",
			Max: fmt.Sprintf("%d", now),
		}).Result()
		if err != nil || len(ids) == 0 {
			continue
		}

		for _, id := range ids {
			// 从 Hash 获取详情
			data, err := database.RDB.HGet(ctx, HashKeyOneshot, id).Result()
			if err != nil {
				database.RDB.ZRem(ctx, ZSetKey, id)
				continue
			}

			var t ScheduledTask
			if err := json.Unmarshal([]byte(data), &t); err != nil {
				database.RDB.ZRem(ctx, ZSetKey, id)
				database.RDB.HDel(ctx, HashKeyOneshot, id)
				continue
			}

			// 执行并移除
			if GlobalSender != nil {
				content := t.Content
				if strings.HasPrefix(t.ID, "proactive_") {
					reply, err := GetProactiveCareReply(t.Content, t.GroupID)
					if err == nil && reply != "" {
						content = reply
					} else {
						content = "记得你说今天有事，一切还顺利吗？"
					}
				}
				GlobalSender(t.GroupID, t.UserID, content)
			}

			// 清理
			database.RDB.ZRem(ctx, ZSetKey, id)
			database.RDB.HDel(ctx, HashKeyOneshot, id)
		}
	}
}
