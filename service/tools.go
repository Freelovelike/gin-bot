package service

import (
	"errors"
	"fmt"
	"gin-bot/database"
	"gin-bot/models"
	"time"

	"gorm.io/gorm"
)

// Tool 定义 Function Calling 工具
type Tool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Parameters   map[string]interface{} `json:"parameters"`
	RequireAdmin bool                   `json:"-"` // 是否需要管理员权限
}

// ToolCall AI 返回的工具调用请求
type ToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolResult 工具执行结果
type ToolResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// AvailableTools 定义所有可用的工具
var AvailableTools = []Tool{
	{
		Name:         "toggle_bot",
		Description:  "开启或关闭机器人在当前群的回复功能。当用户说想要关闭机器人、让机器人别说话、让机器人闭嘴、或者想要开启机器人时调用此工具。",
		RequireAdmin: true, // 需要管理员权限
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"active": map[string]interface{}{
					"type":        "boolean",
					"description": "true 表示开启机器人，false 表示关闭机器人",
				},
			},
			"required": []string{"active"},
		},
	},
	{
		Name:        "get_bot_status",
		Description: "查询机器人当前在本群的状态（是否开启）。当用户询问机器人是否开着、什么状态时调用。",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:         "toggle_rag",
		Description:  "开启或关闭机器人的记忆/RAG功能。当用户说不想被记录、关闭记忆、或者开启记忆功能时调用。",
		RequireAdmin: true, // 需要管理员权限
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"enabled": map[string]interface{}{
					"type":        "boolean",
					"description": "true 表示开启记忆功能，false 表示关闭",
				},
			},
			"required": []string{"enabled"},
		},
	},
	{
		Name:        "get_rag_status",
		Description: "查询RAG记忆功能当前状态。当用户问机器人是否在记录消息时调用。",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "add_timer_task",
		Description: "设置定时提醒任务。可以是单次提醒（如10分钟后提醒我喝水）或周期性闹钟（如每天早上9点提醒我打卡）。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"once", "periodic"},
					"description": "任务类型：once表示单次提醒，periodic表示周期闹钟。",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "提醒的具体内容，如'喝水'、'开会'。",
				},
				"delay_seconds": map[string]interface{}{
					"type":        "integer",
					"description": "针对 once 类型，设置多少秒后执行提醒。请根据用户描述转换，如'一小时后'转为 3600。",
				},
				"cron_expr": map[string]interface{}{
					"type":        "string",
					"description": "针对 periodic 类型，提供标准 Cron 表达式（带秒级，6位）。如每天早九点：'0 0 9 * * *'。",
				},
			},
			"required": []string{"type", "content"},
		},
	},
	{
		Name:        "list_timer_tasks",
		Description: "列出当前用户在本群设置的所有活跃定时提醒和周期闹钟。当用户想看自己设了哪些闹钟、想管理提醒时调用。",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "remove_timer_task",
		Description: "取消或删除指定的定时任务。需要提供任务 ID。建议先调用 list_timer_tasks 获取 ID。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "要删除的任务 ID（如 task_123456...）。",
				},
			},
			"required": []string{"id"},
		},
	},
}

// ExecuteTool 执行指定的工具（带权限检查）
// isSuperUser: 由调用方使用 ZeroBot 的 ctx.Event.IsSuperUser() 判断后传入
func ExecuteTool(toolName string, args map[string]interface{}, groupID int64, userID int64, isSuperUser bool) ToolResult {
	// 检查工具是否需要管理员权限
	for _, tool := range AvailableTools {
		if tool.Name == toolName && tool.RequireAdmin {
			if !isSuperUser {
				return ToolResult{Success: false, Message: "抱歉，这个操作只有管理员才能执行哦~"}
			}
			break
		}
	}

	switch toolName {
	case "toggle_bot":
		return executeToggleBot(args, groupID)
	case "get_bot_status":
		return executeGetBotStatus(groupID)
	case "toggle_rag":
		return executeToggleRAG(args, groupID)
	case "get_rag_status":
		return executeGetRAGStatus(groupID)
	case "add_timer_task":
		return executeAddTimerTask(args, groupID, userID)
	case "list_timer_tasks":
		return executeListTimerTasks(groupID, userID, isSuperUser)
	case "remove_timer_task":
		return executeRemoveTimerTask(args)
	default:
		return ToolResult{Success: false, Message: "未知的工具: " + toolName}
	}
}

// executeAddTimerTask 添加定时任务
func executeAddTimerTask(args map[string]interface{}, groupID int64, userID int64) ToolResult {
	taskType, _ := args["type"].(string)
	content, _ := args["content"].(string)

	task := ScheduledTask{
		Type:    taskType,
		Content: content,
		GroupID: groupID,
		UserID:  userID, // 记录下任务的用户 ID
	}

	if taskType == "once" {
		delaySec, ok := args["delay_seconds"].(float64)
		if !ok {
			return ToolResult{Success: false, Message: "单次任务需要提供有效的 delay_seconds"}
		}
		task.TargetAt = time.Now().Unix() + int64(delaySec)
	} else if taskType == "periodic" {
		cronExpr, ok := args["cron_expr"].(string)
		if !ok {
			return ToolResult{Success: false, Message: "周期任务需要提供有效的 cron_expr"}
		}
		task.TimeExpr = cronExpr
	}

	if err := AddTask(task); err != nil {
		return ToolResult{Success: false, Message: "设置提醒失败: " + err.Error()}
	}

	return ToolResult{Success: true, Message: "设置成功！到时间我会提醒你的~ ID: " + task.ID}
}

// executeListTimerTasks 列出任务
func executeListTimerTasks(groupID int64, userID int64, isSuperUser bool) ToolResult {
	queryUserID := userID
	if isSuperUser {
		queryUserID = 0 // 超级用户查询全量
	}

	tasks := ListTasks(groupID, queryUserID)
	if len(tasks) == 0 {
		return ToolResult{Success: true, Message: "目前没有设置任何活跃的任务哦。"}
	}

	msg := "你当前的定时任务如下：\n"
	if isSuperUser {
		msg = "本群当前活跃的定时任务如下（超级用户视图）：\n"
	}

	for _, t := range tasks {
		timeStr := ""
		if t.Type == "once" {
			timeStr = time.Unix(t.TargetAt, 0).Format("2006-01-02 15:04:05")
		} else {
			timeStr = "周期性: " + t.TimeExpr
		}

		userLabel := ""
		if isSuperUser {
			userLabel = fmt.Sprintf(" [用户:%d]", t.UserID)
		}

		msg += fmt.Sprintf("- [%s] %s (%s)%s\n", t.ID, t.Content, timeStr, userLabel)
	}
	return ToolResult{Success: true, Message: msg, Data: tasks}
}

// executeRemoveTimerTask 移除任务
func executeRemoveTimerTask(args map[string]interface{}) ToolResult {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return ToolResult{Success: false, Message: "移除失败：请提供有效的任务 ID"}
	}

	if err := RemoveTask(id); err != nil {
		return ToolResult{Success: false, Message: "取消任务失败: " + err.Error()}
	}

	return ToolResult{Success: true, Message: "成功取消了该任务！"}
}

// executeToggleBot 开关机器人
func executeToggleBot(args map[string]interface{}, groupID int64) ToolResult {
	active, ok := args["active"].(bool)
	if !ok {
		return ToolResult{Success: false, Message: "参数 active 无效"}
	}

	// 查找或创建群组配置
	var group models.Group
	result := database.DB.FirstOrCreate(&group, models.Group{GroupID: groupID})
	if result.Error != nil {
		return ToolResult{Success: false, Message: "数据库错误: " + result.Error.Error()}
	}

	// 更新状态
	group.IsActive = active
	if err := database.DB.Save(&group).Error; err != nil {
		return ToolResult{Success: false, Message: "保存失败: " + err.Error()}
	}

	if active {
		return ToolResult{Success: true, Message: "机器人已开启", Data: map[string]bool{"active": true}}
	}
	return ToolResult{Success: true, Message: "机器人已关闭", Data: map[string]bool{"active": false}}
}

// executeGetBotStatus 获取机器人状态
func executeGetBotStatus(groupID int64) ToolResult {
	var group models.Group
	result := database.DB.Where("group_id = ?", groupID).First(&group)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ToolResult{Success: true, Message: "机器人当前是开启状态", Data: map[string]bool{"active": true}}
		}
		return ToolResult{Success: false, Message: "数据库错误: " + result.Error.Error()}
	}

	if group.IsActive {
		return ToolResult{Success: true, Message: "机器人当前是开启状态", Data: map[string]bool{"active": true}}
	}
	return ToolResult{Success: true, Message: "机器人当前是关闭状态", Data: map[string]bool{"active": false}}
}

// executeToggleRAG 开关 RAG 功能
func executeToggleRAG(args map[string]interface{}, groupID int64) ToolResult {
	enabled, ok := args["enabled"].(bool)
	if !ok {
		return ToolResult{Success: false, Message: "参数 enabled 无效"}
	}

	var group models.Group
	result := database.DB.FirstOrCreate(&group, models.Group{GroupID: groupID})
	if result.Error != nil {
		return ToolResult{Success: false, Message: "数据库错误: " + result.Error.Error()}
	}

	group.RAGEnabled = enabled
	if err := database.DB.Save(&group).Error; err != nil {
		return ToolResult{Success: false, Message: "保存失败: " + err.Error()}
	}

	if enabled {
		return ToolResult{Success: true, Message: "记忆功能已开启", Data: map[string]bool{"rag_enabled": true}}
	}
	return ToolResult{Success: true, Message: "记忆功能已关闭", Data: map[string]bool{"rag_enabled": false}}
}

// executeGetRAGStatus 获取 RAG 状态
func executeGetRAGStatus(groupID int64) ToolResult {
	var group models.Group
	result := database.DB.Where("group_id = ?", groupID).First(&group)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return ToolResult{Success: true, Message: "记忆功能当前是开启状态", Data: map[string]bool{"rag_enabled": true}}
		}
		return ToolResult{Success: false, Message: "数据库错误: " + result.Error.Error()}
	}

	if group.RAGEnabled {
		return ToolResult{Success: true, Message: "记忆功能当前是开启状态", Data: map[string]bool{"rag_enabled": true}}
	}
	return ToolResult{Success: true, Message: "记忆功能当前是关闭状态", Data: map[string]bool{"rag_enabled": false}}
}

// IsBotActive 检查机器人在指定群是否开启
func IsBotActive(groupID int64) bool {
	var group models.Group
	result := database.DB.Where("group_id = ?", groupID).First(&group)
	if result.Error != nil {
		return true // 默认开启
	}
	return group.IsActive
}

// IsRAGEnabled 检查 RAG 是否在指定群开启
func IsRAGEnabled(groupID int64) bool {
	var group models.Group
	result := database.DB.Where("group_id = ?", groupID).First(&group)
	if result.Error != nil {
		return true // 默认开启
	}
	return group.RAGEnabled
}
