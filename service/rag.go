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
	"regexp"
	"strings"
	"time"

	"gin-bot/database"
	"gin-bot/embedding"
	"gin-bot/models"
	"gin-bot/pinecone"
)

// 轻量 AI 分类器模型
const CLASSIFIER_MODEL = "mistralai/ministral-14b-instruct-2512"

// 个人信息关键词模式（降级使用）
var personalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`我(喜欢|爱|讨厌|不喜欢|偏好)`),
	regexp.MustCompile(`我(是|叫|名字)`),
	regexp.MustCompile(`我的(爱好|兴趣|习惯|工作|职业|年龄|生日)`),
	regexp.MustCompile(`(我今年|我属|我住在|我来自)`),
}

// classifyWithRegex 使用正则判断消息类型（降级方案）
func classifyWithRegex(content string) string {
	for _, pattern := range personalPatterns {
		if pattern.MatchString(content) {
			return "personal"
		}
	}
	return "chat"
}

// classifyWithAI 使用轻量 AI 判断消息类型
// 返回: "personal"(持久个人信息) / "temporary"(临时状态) / "chat"(普通聊天)
func classifyWithAI(content string) string {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		apiKey = "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"
	}

	prompt := fmt.Sprintf(`你是一个信息分类专家。将以下消息分类为三种类型之一。

【personal - 持久性个人信息】需要长期记住：
- 身份特征：职业、年龄、性别、星座、生日、籍贯、居住地
- 兴趣爱好：喜欢的事物、讨厌的事物、习惯、特长
- 隐性信息：能推断出的职业/身份（如"加班写代码腰疼"暗示程序员）

【temporary - 临时状态】短期记住即可：
- 临时动作：我饿了、我去洗澡、我在吃饭、我困了、我出门了
- 即时情绪：我好开心、我生气了、无聊、累了
- 短期计划：我等会要去、我明天要

【chat - 普通聊天】不需要特别记忆：
- 日常对话：你好、谢谢、哈哈哈、好的
- 讨论话题：今天天气、这个新闻
- 闲聊内容

只回答 "personal"、"temporary" 或 "chat"，不要解释。

消息：%s`, content)

	reqBody := map[string]interface{}{
		"model": CLASSIFIER_MODEL,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  10,
		"temperature": 0,
	}

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", NVIDIA_CHAT_URL, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	proxyUrl, _ := url.Parse("http://127.0.0.1:7890")
	client := &http.Client{
		Timeout: 5 * time.Second, // 快速超时
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyUrl),
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Classifier] AI request failed, fallback to regex: %v", err)
		return classifyWithRegex(content)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Printf("[Classifier] AI error (%d), fallback to regex", resp.StatusCode)
		return classifyWithRegex(content)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 {
		return classifyWithRegex(content)
	}

	answer := strings.ToLower(strings.TrimSpace(result.Choices[0].Message.Content))
	if strings.Contains(answer, "personal") {
		return "personal"
	}
	if strings.Contains(answer, "temporary") {
		return "temporary"
	}
	return "chat"
}

// SaveMessageToRAG 将消息存入 RAG 系统（三层存储）
func SaveMessageToRAG(qq string, nickname string, groupID int64, content string) {
	// 1. 记录原始消息到数据库
	var user models.User
	database.DB.FirstOrCreate(&user, models.User{QQ: qq})
	if user.Nickname != nickname {
		user.Nickname = nickname
		database.DB.Save(&user)
	}

	history := models.ChatHistory{
		UserID:  user.ID,
		GroupID: groupID,
		Content: content,
	}
	if err := database.DB.Create(&history).Error; err != nil {
		log.Printf("[RAG] Failed to save chat history: %v", err)
		return
	}

	// 2. 使用 AI 分类消息
	msgType := classifyWithAI(content)

	// 3. 根据类型存入不同存储
	switch msgType {
	case "temporary":
		// 临时状态 → Redis（TTL 2小时）
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := database.SaveTemporaryMemory(ctx, groupID, qq, history.ID, content, 2*time.Hour)
			if err != nil {
				log.Printf("[RAG] Failed to save to Redis: %v", err)
				return
			}
			log.Printf("[RAG] Archived msg %d → Redis (temporary) from %s", history.ID, nickname)
		}()

	case "personal", "chat":
		// personal/chat → Pinecone
		go func() {
			vec, err := embedding.GetEmbedding(content, "passage", 1024)
			if err != nil {
				log.Printf("[RAG] Failed to get embedding for msg %d: %v", history.ID, err)
				return
			}

			metadata := map[string]interface{}{
				"group_id": groupID,
				"user_qq":  qq,
			}

			vectorID := fmt.Sprintf("msg_%d", history.ID)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			var namespace string
			if msgType == "personal" {
				namespace = pinecone.NamespacePersonal
			} else {
				namespace = pinecone.NamespaceChat
			}

			err = pinecone.UpsertToNamespace(ctx, namespace, vectorID, vec, metadata)
			if err != nil {
				log.Printf("[RAG] Failed to upsert to Pinecone: %v", err)
				return
			}

			go func() {
				embRecord := models.MemberEmbedding{
					VectorID:       vectorID,
					ContentSummary: content,
					RefMsgID:       history.ID,
				}
				database.DB.Create(&embRecord)
			}()

			log.Printf("[RAG] Archived msg %d → %s namespace from %s", history.ID, namespace, nickname)
		}()
	}
}
