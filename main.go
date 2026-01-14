package main

import (
	"os"

	"gin-bot/database"
	"gin-bot/pinecone"

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

	// 运行机器人
	zero.RunAndBlock(&zero.Config{
		NickName:      []string{"bot"},
		CommandPrefix: "/",
		SuperUsers:    []int64{},
		Driver: []zero.Driver{
			// 正向 WebSocket 连接
			driver.NewWebSocketClient(botWSURL, botToken),
		},
	}, nil)
}
