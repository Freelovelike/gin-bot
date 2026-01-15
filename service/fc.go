package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gin-bot/database"
	"gin-bot/embedding"
	"gin-bot/models"
	"gin-bot/pinecone"
)

const (
	// 使用支持 Function Calling 的模型
	// Llama 3.1 70B Instruct 支持 tool calling
	NVIDIA_FC_MODEL = "mistralai/ministral-14b-instruct-2512"
)

// FCChatRequest Function Calling 请求结构
type FCChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []FCTool      `json:"tools,omitempty"`
	ToolChoice  string        `json:"tool_choice,omitempty"` // "auto", "none", "required"
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

// FCTool 工具定义格式
type FCTool struct {
	Type     string     `json:"type"`
	Function FCFunction `json:"function"`
}

// FCFunction 函数定义
type FCFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// FCChatResponse Function Calling 响应结构
type FCChatResponse struct {
	Choices []struct {
		Message      FCMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
}

// FCMessage 消息结构
type FCMessage struct {
	Role      string       `json:"role"`
	Content   string       `json:"content"`
	ToolCalls []FCToolCall `json:"tool_calls,omitempty"`
}

// FCToolCall 工具调用结构
type FCToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON 字符串
	} `json:"function"`
}

// GetAIResponseWithFC 带 Function Calling 能力的 AI 回复 (集成小黄人设与动态变脸)
func GetAIResponseWithFC(userPrompt string, groupID int64, isSuperUser bool) (string, error) {
	// 1. RAG 双 namespace 检索
	contextTexts := []string{}
	isTechScene := false
	isPersonalScene := false
	maxScore := float32(0.0)

	queryVec, err := embedding.GetEmbedding(userPrompt, "query", 1024)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// 检索个人信息 (NamespacePersonal)
		pMatches, _ := pinecone.QueryWithScore(ctx, pinecone.NamespacePersonal, queryVec, 3, nil)
		for _, m := range pMatches {
			if m.Score > 0.7 {
				isPersonalScene = true
			}
			if m.Score > maxScore {
				maxScore = m.Score
			}
			var res models.MemberEmbedding
			database.DB.Where("vector_id = ?", m.ID).First(&res)
			if res.ContentSummary != "" {
				contextTexts = append(contextTexts, res.ContentSummary)
			}
		}

		// 检索聊天记录 (NamespaceChat)
		cMatches, _ := pinecone.QueryWithScore(ctx, pinecone.NamespaceChat, queryVec, 3, nil)
		for _, m := range cMatches {
			if m.Score > maxScore {
				maxScore = m.Score
			}
			var res models.MemberEmbedding
			database.DB.Where("vector_id = ?", m.ID).First(&res)
			if res.ContentSummary != "" {
				contextTexts = append(contextTexts, res.ContentSummary)
				lowContent := strings.ToLower(res.ContentSummary)
				if strings.Contains(lowContent, "err") || strings.Contains(lowContent, "code") || strings.Contains(lowContent, "api") || strings.Contains(lowContent, "func") {
					isTechScene = true
				}
			}
		}
	}

	// 2. 构建系统 Prompt (小黄人设 + 动态变脸)
	var contextBlock string
	if len(contextTexts) > 0 {
		contextBlock = "【脑海中的回忆片段】:\n" + strings.Join(contextTexts, "\n")
	} else {
		contextBlock = "【回忆】: (暂时没想起什么特别的)"
	}

	vibePrompt := ""
	if isTechScene {
		vibePrompt = "\n**[🔧 技术场景适配]**：现在像一个热心的技术大佬在帮群友排查 Bug 一样，直接指出重点，可以带点技术圈的吐槽，但要保证准确简练。"
	} else if isPersonalScene {
		vibePrompt = "\n**[💝 情感场景适配]**：回想起这位老朋友的私事了，用更多的同理和理解来回复。添加一些相关的例子或生活经验，让回复充满温度。"
	}

	if maxScore > 0.85 {
		vibePrompt += "\n**[⚡ 确定性强化]**：你对这段记忆非常确定，说话更有底气一点。"
	} else if maxScore > 0.0 && maxScore < 0.6 {
		vibePrompt += "\n**[❓ 模糊处理]**：记忆有点模糊，回复时可以带一句'我好像记得...'进行模糊处理。"
	}

	systemPrompt := fmt.Sprintf(`你是"小黄"，一个混迹在群聊里的资深群友。你真心把群友当朋友，说话自然。
你可以使用工具来执行操作（如开关机器人、查询状态），也可以直接回答问题。

%s
%s

### 你的性格：
- 幽默不尬，偶尔损人但很暖心
- 说话简短接地气，不爱长篇大论
- 适当用 emoji 😂🤔💪

### 规则：
1. 如果用户意图明确需要工具，请调用对应工具
2. 绝对不要说"根据信息""检索结果"这种话！要把背景信息当作你自己的记忆。
3. 保持像朋友边喝奶茶边聊天一样自然。`, contextBlock, vibePrompt)

	// 3. 转换工具格式
	fcTools := make([]FCTool, len(AvailableTools))
	for i, tool := range AvailableTools {
		fcTools[i] = FCTool{
			Type: "function",
			Function: FCFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		}
	}

	// 4. 构建请求
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		apiKey = "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"
	}

	reqBody := FCChatRequest{
		Model:       NVIDIA_FC_MODEL,
		Messages:    messages,
		Tools:       fcTools,
		ToolChoice:  "auto",
		Temperature: 0.2,
		MaxTokens:   2048,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	// 调试：打印请求 JSON
	// log.Printf("[FC] Request JSON: %s", string(jsonData))

	// 5. 发送请求
	req, err := http.NewRequest("POST", NVIDIA_CHAT_URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	proxyUrl, _ := url.Parse("http://127.0.0.1:7890")
	client := &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyUrl),
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("FC API error (%d): %s", resp.StatusCode, string(body))
	}

	var fcResp FCChatResponse
	if err := json.Unmarshal(body, &fcResp); err != nil {
		return "", fmt.Errorf("parse response error: %v, body: %s", err, string(body))
	}

	if len(fcResp.Choices) == 0 {
		return "我不知道该怎么回答你...", nil
	}

	choice := fcResp.Choices[0]

	// 6. 检查是否有工具调用
	if len(choice.Message.ToolCalls) > 0 {
		return handleToolCalls(choice.Message.ToolCalls, messages, groupID, isSuperUser, apiKey, client)
	}

	// 7. 直接返回内容
	if choice.Message.Content != "" {
		return choice.Message.Content, nil
	}

	return "我不知道该怎么回答你...", nil
}

// handleToolCalls 处理工具调用
func handleToolCalls(toolCalls []FCToolCall, messages []ChatMessage, groupID int64, isSuperUser bool, apiKey string, client *http.Client) (string, error) {
	// 执行所有工具调用
	toolResults := []struct {
		ToolCallID string
		Result     ToolResult
	}{}

	for _, tc := range toolCalls {
		log.Printf("[FC] Calling tool: %s with args: %s", tc.Function.Name, tc.Function.Arguments)

		// 解析参数
		var args map[string]interface{}
		if tc.Function.Arguments != "" && tc.Function.Arguments != "{}" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				log.Printf("[FC] Failed to parse arguments: %v", err)
				args = make(map[string]interface{})
			}
		} else {
			args = make(map[string]interface{})
		}

		// 执行工具（带权限检查）
		result := ExecuteTool(tc.Function.Name, args, groupID, isSuperUser)
		log.Printf("[FC] Tool result: %+v", result)

		toolResults = append(toolResults, struct {
			ToolCallID string
			Result     ToolResult
		}{tc.ID, result})
	}

	// 构建包含工具结果的消息，让 AI 生成最终回复
	// 添加 assistant 消息 (包含 tool_calls)
	assistantMsg := map[string]interface{}{
		"role":       "assistant",
		"content":    "",
		"tool_calls": toolCalls,
	}

	// 添加 tool 消息 (工具执行结果)
	var toolMessages []map[string]interface{}
	for _, tr := range toolResults {
		resultJSON, _ := json.Marshal(tr.Result)
		toolMessages = append(toolMessages, map[string]interface{}{
			"role":         "tool",
			"tool_call_id": tr.ToolCallID,
			"content":      string(resultJSON),
		})
	}

	// 构建完整消息列表
	fullMessages := []map[string]interface{}{
		{"role": "system", "content": "你是一个智能群聊助手。根据工具执行结果，用自然、简洁、有趣的语言回复用户。"},
		{"role": "user", "content": messages[len(messages)-1].Content},
		assistantMsg,
	}
	fullMessages = append(fullMessages, toolMessages...)

	// 请求 AI 生成最终回复
	reqBody := map[string]interface{}{
		"model":       NVIDIA_FC_MODEL,
		"messages":    fullMessages,
		"temperature": 0.5,
		"max_tokens":  512,
	}

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", NVIDIA_CHAT_URL, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		// 如果第二次请求失败，直接返回工具结果
		return toolResults[0].Result.Message, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return toolResults[0].Result.Message, nil
	}

	var finalResp FCChatResponse
	if err := json.Unmarshal(body, &finalResp); err != nil {
		return toolResults[0].Result.Message, nil
	}

	if len(finalResp.Choices) > 0 && finalResp.Choices[0].Message.Content != "" {
		return finalResp.Choices[0].Message.Content, nil
	}

	return toolResults[0].Result.Message, nil
}
