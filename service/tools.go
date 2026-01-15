package service

import (
	"errors"

	"gin-bot/database"
	"gin-bot/models"

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
}

// ExecuteTool 执行指定的工具（带权限检查）
// isSuperUser: 由调用方使用 ZeroBot 的 ctx.Event.IsSuperUser() 判断后传入
func ExecuteTool(toolName string, args map[string]interface{}, groupID int64, isSuperUser bool) ToolResult {
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
	default:
		return ToolResult{Success: false, Message: "未知的工具: " + toolName}
	}
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
