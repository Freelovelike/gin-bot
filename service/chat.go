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

// GetAIResponse 获取 AI 回复，集成 RAG
func GetAIResponse(userPrompt string) (string, error) {
	// 1. RAG 检索
	contextTexts := []string{}

	// 生成检索向量 (使用 query 模式)
	queryVec, err := embedding.GetEmbedding(userPrompt, "query", 1024)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		vectorIDs, err := pinecone.Query(ctx, queryVec, 5)
		if err == nil && len(vectorIDs) > 0 {
			// 在本地数据库查找具体的文本内容
			var results []models.MemberEmbedding
			database.DB.Where("vector_id IN ?", vectorIDs).Find(&results)

			for _, res := range results {
				contextTexts = append(contextTexts, res.ContentSummary)
			}
		}
	} else {
		log.Printf("[Chat] Failed to get query embedding: %v", err)
	}

	// 2. 构建 Prompt
	systemPrompt := "你是一个贴心、幽默的群聊助手。你会参考以下历史对话背景来回答用户的问题。如果背景信息无关，请直接根据你的知识回答。"
	if len(contextTexts) > 0 {
		systemPrompt += "\n\n已知背景信息：\n" + strings.Join(contextTexts, "\n---\n")
	}

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
