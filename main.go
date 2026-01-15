package main

import (
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
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
				ctx.SendGroupMessage(groupID, content)
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

	// RAG 核心：统一消息处理器
	zero.OnMessage().Handle(func(ctx *zero.Ctx) {
		content := ctx.Event.RawMessage
		selfIDStr := strconv.FormatInt(ctx.Event.SelfID, 10)
		atMe := strings.Contains(content, "[CQ:at,qq="+selfIDStr+"]") || ctx.Event.MessageType == "private"
		groupID := ctx.Event.GroupID

		// 1. 如果是艾特机器人或私聊，则进入 AI 回复流程
		if atMe {
			isSuperUser := zero.SuperUserPermission(ctx)

			// 检查机器人是否在本群开启（私聊始终开启）
			if ctx.Event.MessageType != "private" && !service.IsBotActive(groupID) {
				// 机器人已关闭，只有 SuperUser 才能操作（用于开启机器人）
				if !isSuperUser {
					return // 普通用户直接忽略
				}
			}

			prompt := strings.TrimSpace(content)
			// 移除艾特 CQ 码
			prompt = strings.ReplaceAll(prompt, "[CQ:at,qq="+selfIDStr+"]", "")
			prompt = strings.TrimSpace(prompt)

			if prompt == "" {
				if service.IsBotActive(groupID) {
					ctx.Send("在呢，找我有什么事吗？")
				}
				return
			}

			// 异步请求 AI (带 Function Calling)
			userID := ctx.Event.UserID
			isPrivate := ctx.Event.MessageType == "private"
			go func() {
				reply, err := service.GetAIResponseWithFC(prompt, groupID, isSuperUser)
				if err != nil {
					log.Printf("[Chat] AI Response Error: %v", err)
					// 只有开启状态才回复错误
					if service.IsBotActive(groupID) {
						ctx.Send("抱歉，我的大脑暂时断网了...")
					}
					return
				}

				// 清理 AI 回复中可能存在的 CQ 码（防止艾特错人）
				reply = cleanCQCodes(reply)

				// 群聊时艾特原始提问者
				if !isPrivate {
					reply = "[CQ:at,qq=" + strconv.FormatInt(userID, 10) + "] " + reply
				}

				ctx.Send(reply)
			}()

			// 艾特的消息通常很有价值，建议也进入归档，所以这里不 return
		}

		// 2. 基础过滤：日常聊天归档到 RAG
		// 使用字符数（非字节数）判断，并清理 CQ 码后再判断
		if !hasMeaningfulContent(content) {
			return
		}
		if content[0] == '/' {
			return // 忽略指令
		}

		// 3. 身份识别
		nickname := ctx.Event.Sender.NickName
		if nickname == "" {
			nickname = "未知用户"
		}

		// 4. 检查 RAG 是否开启
		if !service.IsRAGEnabled(groupID) {
			return
		}

		// 5. 归档到 RAG
		go service.SaveMessageToRAG(
			strconv.FormatInt(ctx.Event.UserID, 10),
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
