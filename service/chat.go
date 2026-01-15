package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gin-bot/config"
	"gin-bot/database"
	"gin-bot/embedding"
	"gin-bot/models"
	"gin-bot/pinecone"
)

const NVIDIA_CHAT_URL = "https://integrate.api.nvidia.com/v1/chat/completions"

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GetAIResponse 获取 AI 回复，集成 RAG（带动态变脸与时间感）
func GetAIResponse(userPrompt string) (string, error) {
	now := time.Now()
	bjTime := now.In(time.FixedZone("CST", 8*3600))
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
				relTime := formatRelativeTime(res.RefMsg.CreatedAt)
				contextTexts = append(contextTexts, fmt.Sprintf("(%s前) %s", relTime, res.ContentSummary))

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

	vibePrompt := ""
	if isTechScene {
		vibePrompt = "\n**[🔧 技术场景适配]**：现在像一个热心的技术大佬在帮群友排查 Bug 一样，直接指出重点，可以带点技术圈的吐槽，但要保证准确简练。"
	} else if isPersonalScene {
		vibePrompt = "\n**[💝 情感场景适配]**：回想起这位老朋友的私事了，用更多的同情和理解来回复。添加一些相关的例子或生活经验，让回复充满温度。"
	}

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

	return callNvidiaAPI(messages, "mistralai/mixtral-8x7b-instruct-v0.1")
}

// GetProactiveResponse 主动插嘴判断逻辑
func GetProactiveResponse(userPrompt string, groupID int64, userID int64) (string, bool) {
	queryVec, err := embedding.GetEmbedding(userPrompt, "query", 1024)
	if err != nil {
		return "", false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	maxScore := float32(0.0)
	var bestMatch models.MemberEmbedding

	// 检索个人信息 (NamespacePersonal) - 强制 ID 隔离
	pFilter := map[string]interface{}{
		"user_qq": strconv.FormatInt(userID, 10),
	}
	pMatches, _ := pinecone.QueryWithScore(ctx, pinecone.NamespacePersonal, queryVec, 1, pFilter)
	if len(pMatches) > 0 && pMatches[0].Score > maxScore {
		maxScore = pMatches[0].Score
		database.DB.Preload("RefMsg").Where("vector_id = ?", pMatches[0].ID).First(&bestMatch)
	}

	chatFilter := map[string]interface{}{"group_id": groupID}
	cMatches, _ := pinecone.QueryWithScore(ctx, pinecone.NamespaceChat, queryVec, 1, chatFilter)
	if len(cMatches) > 0 && cMatches[0].Score > maxScore {
		maxScore = cMatches[0].Score
		database.DB.Preload("RefMsg").Where("vector_id = ?", cMatches[0].ID).First(&bestMatch)
	}

	// 阈值判定：分数 > 0.88 才主动插嘴
	if maxScore < 0.88 {
		return "", false
	}

	relTime := formatRelativeTime(bestMatch.RefMsg.CreatedAt)
	contextBlock := fmt.Sprintf("【突然想起的事】: (%s前) %s", relTime, bestMatch.ContentSummary)

	now := time.Now()
	timeInfo := fmt.Sprintf("【北京时间：%s】", now.Format("2006-01-02 15:04"))

	systemPrompt := fmt.Sprintf(`你是"小黄"，一个资深群友。你刚才在偷听大家聊天，突然想起了一件非常相关的事，忍不住想插句嘴。

%s
%s

### 你的插嘴原则：
1. **自然接入**：不要表现得像机器检索，要像突然拍大腿想起件事："哎呀我突然想起..."、"说起这个，我记得..."。
2. **相关性极强**：既然你开口了，说明这件事非常有价值。
3. **简短有力**：插嘴不要太长，点到为止。
4. **带有时间感**：提到的记忆如果有点久了，可以带上一句"好久之前了"或者"就在刚才"。
`, timeInfo, contextBlock)

	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	reply, err := callNvidiaAPI(messages, "mistralai/mixtral-8x7b-instruct-v0.1")
	if err != nil {
		return "", false
	}

	return reply, true
}

func callNvidiaAPI(messages []ChatMessage, model string) (string, error) {
	reqBody := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"temperature": 0.3,
		"max_tokens":  1024,
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
	req.Header.Set("Authorization", "Bearer "+config.Cfg.NvidiaAPIKey)

	client := config.GetHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &res); err != nil || len(res.Choices) == 0 {
		return "", fmt.Errorf("api error: %s", string(body))
	}

	return res.Choices[0].Message.Content, nil
}

// GetProactiveCareReply 生成主动关怀回复
func GetProactiveCareReply(taskContent string, groupID int64) (string, error) {
	parts := strings.Split(taskContent, "|")
	reason := "随访"
	origMsg := ""
	if len(parts) >= 2 {
		reason = parts[0]
		origMsg = parts[1]
	}

	systemPrompt := `你是"小黄"，一个像老朋友一样贴心的群友。你刚才在自己的记事本里看到几个小时前某个群友提到了一些事，现在你想主动打个招呼关心一下。

### 你的关怀原则：
1. **极其自然**：不要说"我检测到你提到了..."，要说"诶，刚才看你说..."、"对了，下午那会儿你说...，现在好点没？"。
2. **真诚且随性**：像好哥们/好闺蜜一样的语气，可以带点损但核心是关怀。
3. **不要压力**：不要让用户觉得你在监控他，要表现得是你刚才闲着没事突然想起来了。
4. **简洁**：1-2 句话即可。

### 当前背景：
- 提醒缘由：%s
- 之前的话：%s

请生成一段主动关怀的消息，不需要带任何前缀。`

	prompt := fmt.Sprintf(systemPrompt, reason, origMsg)
	messages := []ChatMessage{
		{Role: "system", Content: prompt},
	}

	return callNvidiaAPI(messages, "mistralai/mixtral-8x7b-instruct-v0.1")
}
