package main

import (
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gin-bot/database"
	"gin-bot/pinecone"
	"gin-bot/service"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
)

// cqCodeRegex 匹配 CQ 码的正则表达式
var cqCodeRegex = regexp.MustCompile(`\[CQ:[^\]]+\]`)

// cleanCQCodes 清理字符串中的 CQ 码
func cleanCQCodes(s string) string {
	return cqCodeRegex.ReplaceAllString(s, "")
}

// hasMeaningfulContent 检查消息是否有意义（清理 CQ 码后至少 5 个字符）
func hasMeaningfulContent(content string) bool {
	cleaned := strings.TrimSpace(cleanCQCodes(content))
	// 使用 RuneCountInString 计算字符数（而非字节数）
	return utf8.RuneCountInString(cleaned) >= 5
}

// getEnv 获取环境变量，如果不存在则返回默认值
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func main() {
	// 初始化数据库
	database.InitDB()

	// 初始化 Redis（短期记忆）
	database.InitRedis()

	// 初始化 Pinecone
	pinecone.InitPinecone()

	// 初始化调度器（时间感 - 未来感）
	service.InitScheduler(func(groupID int64, userID int64, content string) {
		zero.RangeBot(func(id int64, ctx *zero.Ctx) bool {
			if groupID != 0 {
				msg := content
				if userID != 0 {
					msg = "[CQ:at,qq=" + strconv.FormatInt(userID, 10) + "] " + msg
				}
				ctx.SendGroupMessage(groupID, msg)
			} else {
				ctx.SendPrivateMessage(userID, content)
			}
			return false
		})
	})

	// 从环境变量获取配置
	botWSURL := getEnv("BOT_WS_URL", "ws://127.0.0.1:3001")
	botToken := getEnv("BOT_TOKEN", "hwc20010616")

	// 超级用户列表（用于 ZeroBot 配置）
	superUsers := []int64{3144622944}

	// 注册一个简单的 hello 命令作为示例
	zero.OnCommand("hello").Handle(func(ctx *zero.Ctx) {
		ctx.Send("Hello World!")
	})

	// 冷却时间记录：groupID -> 上次主动发言时间
	proactiveCooldown := make(map[int64]time.Time)

	// RAG 核心：统一消息处理器
	zero.OnMessage().Handle(func(ctx *zero.Ctx) {
		content := ctx.Event.RawMessage
		selfIDStr := strconv.FormatInt(ctx.Event.SelfID, 10)
		atMe := strings.Contains(content, "[CQ:at,qq="+selfIDStr+"]") || ctx.Event.MessageType == "private"
		groupID := ctx.Event.GroupID
		userID := ctx.Event.UserID
		nickname := ctx.Event.Sender.NickName
		if nickname == "" {
			nickname = "未知用户"
		}

		// 1. 如果是艾特机器人或私聊，则进入常规 AI 回复流程
		if atMe {
			isSuperUser := zero.SuperUserPermission(ctx)
			if ctx.Event.MessageType != "private" && !service.IsBotActive(groupID) {
				if !isSuperUser {
					return
				}
			}

			prompt := strings.TrimSpace(content)
			prompt = strings.ReplaceAll(prompt, "[CQ:at,qq="+selfIDStr+"]", "")
			prompt = strings.TrimSpace(prompt)

			if prompt == "" {
				if service.IsBotActive(groupID) {
					ctx.Send("在呢，找我有什么事吗？")
				}
				return
			}

			isPrivate := ctx.Event.MessageType == "private"
			go func() {
				reply, err := service.GetAIResponseWithFC(prompt, groupID, isSuperUser)
				if err != nil {
					log.Printf("[Chat] AI Response Error: %v", err)
					if service.IsBotActive(groupID) {
						ctx.Send("抱歉，我的大脑暂时断网了...")
					}
					return
				}
				reply = cleanCQCodes(reply)
				if !isPrivate {
					reply = "[CQ:at,qq=" + strconv.FormatInt(userID, 10) + "] " + reply
				}
				ctx.Send(reply)
			}()
		} else if ctx.Event.MessageType == "group" && service.IsBotActive(groupID) {
			// 2. 主动插嘴逻辑 (Proactive Interjection)
			// 只有清理完内容后长度足够的才考虑
			if !hasMeaningfulContent(content) {
				return
			}

			// 冷却检查：同一群聊 5 分钟内最多主动插嘴一次
			if lastTime, ok := proactiveCooldown[groupID]; ok && time.Since(lastTime) < 5*time.Minute {
				// 虽然不插嘴，但还是要把消息存入 RAG（在后面统一处理）
			} else {
				// 尝试获取主动回复
				go func() {
					// 这个函数会内部判断 RAG 匹配分和语义触发
					reply, shouldReply := service.GetProactiveResponse(content, groupID)
					if shouldReply && reply != "" {
						proactiveCooldown[groupID] = time.Now()
						ctx.Send(cleanCQCodes(reply))
					}
				}()
			}
		}

		// 3. 归档到 RAG（包含消息过滤）
		if !hasMeaningfulContent(content) || content[0] == '/' {
			return
		}
		if !service.IsRAGEnabled(groupID) {
			return
		}

		go service.SaveMessageToRAG(
			strconv.FormatInt(userID, 10),
			nickname,
			groupID,
			content,
		)
	})

	// 运行机器人
	zero.RunAndBlock(&zero.Config{
		NickName:      []string{"bot"},
		CommandPrefix: "/",
		SuperUsers:    superUsers,
		Driver: []zero.Driver{
			driver.NewWebSocketClient(botWSURL, botToken),
		},
	}, nil)
}
