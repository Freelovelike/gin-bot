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
	"strconv"
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

// classifyWithAI 使用轻量 AI 判断消息类型并探测主动性触发点
// 返回格式: 类型|是否主动频率(true/false)|原因
func classifyWithAI(content string) string {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		apiKey = "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"
	}

	prompt := fmt.Sprintf(`你是一个深度社交观察员。分析以下群聊消息并给出分类。

### 分类规则：
- personal: 持久性个人信息（职业、爱好、身份、性格特征）。
- temporary: 临时状态（饿了、去洗澡、在忙、困了、即时情绪）。
- chat: 普通闲聊或讨论话题。

### 主动性探测 (Proactive)：
如果消息包含以下特征，请标记为触发主动关怀：
1. 强烈的负面情绪（极度焦虑、悲伤、受挫）。
2. 明确的短期重大计划（明天面试、下午相亲、要去赶飞机）。
3. 寻求帮助但未明确@机器人。

回复格式必须为："类型|是否触发(true/false)|原因描述"
示例："personal|false|普通爱好描述" 或 "temporary|true|用户表达了极度焦虑"

消息：%s`, content)

	reqBody := map[string]interface{}{
		"model": CLASSIFIER_MODEL,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  64,
		"temperature": 0.1,
	}

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", NVIDIA_CHAT_URL, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	proxyUrl, _ := url.Parse("http://127.0.0.1:7890")
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyUrl),
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Classifier] AI request failed: %v", err)
		return classifyWithRegex(content) + "|false|fallback"
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 {
		return classifyWithRegex(content) + "|false|error"
	}

	return strings.TrimSpace(result.Choices[0].Message.Content)
}

// SaveMessageToRAG 将消息存入 RAG 系统（三层存储 + 主动性探测）
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

	// 2. 使用 AI 分类并探测主动性
	fullRes := classifyWithAI(content)
	parts := strings.Split(fullRes, "|")
	msgType := "chat"
	isProactive := false
	proactiveReason := ""

	if len(parts) >= 1 {
		msgType = strings.ToLower(strings.TrimSpace(parts[0]))
		// 校验合法性
		if !strings.Contains("personal|temporary|chat", msgType) {
			msgType = "chat"
		}
	}
	if len(parts) >= 2 {
		isProactive = strings.TrimSpace(parts[1]) == "true"
	}
	if len(parts) >= 3 {
		proactiveReason = strings.TrimSpace(parts[2])
	}

	// 3. 主动性处理 (Proactive Action)
	if isProactive && IsBotActive(groupID) {
		log.Printf("[Proactive] Trigger detected! Reason: %s", proactiveReason)
		// 自动安排一个 4 小时后的随访任务
		go func() {
			userIDInt, _ := strconv.ParseInt(qq, 10, 64)
			task := ScheduledTask{
				ID:      fmt.Sprintf("proactive_%d", time.Now().Unix()),
				Type:    "once",
				Content: proactiveReason + "|" + content, // 传入原因和原始消息
				GroupID: groupID,
				UserID:  userIDInt,
				TargetAt: time.Now().Add(4 * time.Hour).Unix(),
			}
			err := AddTask(task)
			if err != nil {
				log.Printf("[Proactive] Failed to add follow-up task: %v", err)
			}
		}()
	}

	// 4. 根据类型存入不同存储
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
				"group_id":   groupID,
				"user_qq":    qq,
				"created_at": time.Now().Unix(), // 恢复时间戳
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
