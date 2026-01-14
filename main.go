package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"gin-bot/database"
	"gin-bot/pinecone"
	"gin-bot/service"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
)

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

	// 初始化 Pinecone
	pinecone.InitPinecone()

	// 从环境变量获取配置
	botWSURL := getEnv("BOT_WS_URL", "ws://127.0.0.1:3001")
	botToken := getEnv("BOT_TOKEN", "hwc20010616")

	// 注册一个简单的 hello 命令作为示例
	zero.OnCommand("hello").Handle(func(ctx *zero.Ctx) {
		ctx.Send("Hello World!")
	})

	// RAG 核心：统一消息处理器
	zero.OnMessage().Handle(func(ctx *zero.Ctx) {
		content := ctx.Event.RawMessage
		selfIDStr := strconv.FormatInt(ctx.Event.SelfID, 10)
		atMe := strings.Contains(content, "[CQ:at,qq="+selfIDStr+"]") || ctx.Event.MessageType == "private"

		// 1. 如果是艾特机器人或私聊，则进入 AI 回复流程
		if atMe {
			prompt := strings.TrimSpace(content)
			// 移除艾特 CQ 码
			prompt = strings.ReplaceAll(prompt, "[CQ:at,qq="+selfIDStr+"]", "")
			prompt = strings.TrimSpace(prompt)

			if prompt == "" {
				ctx.Send("在呢，找我有什么事吗？")
				return
			}

			// 异步请求 AI
			go func() {
				reply, err := service.GetAIResponse(prompt)
				if err != nil {
					log.Printf("[Chat] AI Response Error: %v", err)
					ctx.Send("抱歉，我的大脑暂时断网了...")
					return
				}
				ctx.Send(reply)
			}()
			
			// 艾特的消息通常很有价值，建议也进入归档，所以这里不 return
		}

		// 2. 基础过滤：日常聊天归档到 RAG
		if len(content) < 5 {
			return 
		}
		if len(content) > 0 && content[0] == '/' {
			return // 忽略指令
		}

		// 3. 身份识别
		nickname := ctx.Event.Sender.NickName
		if nickname == "" {
			nickname = "未知用户"
		}

		// 4. 归档到 RAG
		go service.SaveMessageToRAG(
			strconv.FormatInt(ctx.Event.UserID, 10),
			nickname,
			ctx.Event.GroupID,
			content,
		)
	})

	// 运行机器人
	zero.RunAndBlock(&zero.Config{
		NickName:      []string{"bot"},
		CommandPrefix: "/",
		SuperUsers:    []int64{},
		Driver: []zero.Driver{
			driver.NewWebSocketClient(botWSURL, botToken),
		},
	}, nil)
}
