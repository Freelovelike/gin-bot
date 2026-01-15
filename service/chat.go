package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	NVIDIA_CHAT_URL   = "https://integrate.api.nvidia.com/v1/chat/completions"
	NVIDIA_CHAT_MODEL = "mistralai/ministral-14b-instruct-2512"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model            string        `json:"model"`
	Messages         []ChatMessage `json:"messages"`
	MaxTokens        int           `json:"max_tokens,omitempty"`
	Temperature      float64       `json:"temperature,omitempty"`
	TopP             float64       `json:"top_p,omitempty"`
	FrequencyPenalty float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64       `json:"presence_penalty,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

// GetAIResponse 获取 AI 回复，集成 RAG（带动态变脸与时间感）
func GetAIResponse(userPrompt string) (string, error) {
	now := time.Now()
	bjTime := now.In(time.FixedZone("CST", 8*3600))
	// 简单映射星期到中文
	weekdayMap := map[string]string{
		"Monday": "一", "Tuesday": "二", "Wednesday": "三", "Thursday": "四", "Friday": "五", "Saturday": "六", "Sunday": "日",
	}
	timeInfo := fmt.Sprintf("【北京时间：%s 星期%s】", bjTime.Format("2006-01-02 15:04"), weekdayMap[bjTime.Weekday().String()])

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
			database.DB.Preload("RefMsg").Where("vector_id = ?", m.ID).First(&res)
			if res.ContentSummary != "" {
				// 计算相对时间（沧桑感）
				relTime := formatRelativeTime(res.RefMsg.CreatedAt)
				contextTexts = append(contextTexts, fmt.Sprintf("(%s前) %s", relTime, res.ContentSummary))
			}
		}

		// 检索聊天记录 (NamespaceChat)
		cMatches, _ := pinecone.QueryWithScore(ctx, pinecone.NamespaceChat, queryVec, 3, nil)
		for _, m := range cMatches {
			if m.Score > maxScore {
				maxScore = m.Score
			}
			var res models.MemberEmbedding
			database.DB.Preload("RefMsg").Where("vector_id = ?", m.ID).First(&res)
			if res.ContentSummary != "" {
				// 计算相对时间
				relTime := formatRelativeTime(res.RefMsg.CreatedAt)
				contextTexts = append(contextTexts, fmt.Sprintf("(%s前) %s", relTime, res.ContentSummary))

				// 简单判断是否是技术场景
				lowContent := strings.ToLower(res.ContentSummary)
				if strings.Contains(lowContent, "err") || strings.Contains(lowContent, "code") || strings.Contains(lowContent, "api") || strings.Contains(lowContent, "func") {
					isTechScene = true
				}
			}
		}
	}

	// 2. 构建基础 Prompt
	var contextBlock string
	if len(contextTexts) > 0 {
		contextBlock = "【脑海中的回忆片段】:\n" + strings.Join(contextTexts, "\n")
	} else {
		contextBlock = "【回忆】: (暂时没想起什么特别的)"
	}

	// 动态微调：根据场景和分数追加“调味料”
	vibePrompt := ""
	if isTechScene {
		vibePrompt = "\n**[🔧 技术场景适配]**：现在像一个热心的技术大佬在帮群友排查 Bug 一样，直接指出重点，可以带点技术圈的吐槽，但要保证准确简练。"
	} else if isPersonalScene {
		vibePrompt = "\n**[💝 情感场景适配]**：回想起这位老朋友的私事了，用更多的同情和理解来回复。添加一些相关的例子或生活经验，让回复充满温度。"
	}

	// 根据分数调整确定性
	if maxScore > 0.85 {
		vibePrompt += "\n**[⚡ 确定性强化]**：你对这段记忆非常确定，说话更有底气一点。"
	} else if maxScore > 0.0 && maxScore < 0.6 {
		vibePrompt += "\n**[❓ 模糊处理]**：记忆有点模糊，回复时可以带一句'我好像记得...'或者'不知道记错没'之类的话。"
	}

	systemPrompt := fmt.Sprintf(`你是"小黄"，一个混迹在群聊里的资深群友。你真心把群友当朋友，说话自然。
%s

%s
%s

### 你的性格：
- 幽默但不尬，偶尔损人但很暖心
- 说话简短接地气，不爱长篇大论
- 会用"笑死""好家伙""确实""6""hhh"这种口语
- 适当用 emoji 表达情绪 😂🤔💪

### 回复原则：
1. **像朋友聊天**：用"你""我们"让对话更亲近。如果【回忆】里有相关信息，请利用它们，就像想起老朋友说过的话一样自然带出来。
2. **绝对不要机械感**：禁止说"根据信息""检索结果""数据显示"这种话！回忆就是你脑子里记住的事。
3. **富有时间感**：你是个有血有肉的人，如果回忆里显示某事是几小时前发生的，你可以自然地说出这个时间差细节。
4. **共情优先**：如果用户情绪不好，先关心再给建议。
`, timeInfo, contextBlock, vibePrompt)

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	// 3. 调用 NVIDIA Chat API
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		apiKey = "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"
	}

	reqBody := ChatRequest{
		Model:       NVIDIA_CHAT_MODEL,
		Messages:    messages,
		Temperature: 0.15,
		TopP:        1.0,
		MaxTokens:   2048,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", NVIDIA_CHAT_URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	// 配置代理
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
		return "", fmt.Errorf("chat api error (%d): %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", err
	}

	if len(chatResp.Choices) > 0 {
		return chatResp.Choices[0].Message.Content, nil
	}

	return "我不知道该怎么回答你...", nil
}
