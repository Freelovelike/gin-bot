package config

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Config 应用配置
type Config struct {
	NvidiaAPIKey   string
	PineconeAPIKey string
	PineconeIndex  string
	DBDSN          string
	RedisAddr      string
	RedisPassword  string
	BotWSURL       string
	BotToken       string
	ProxyURL       string
	SuperUsers     []int64
}

var (
	Cfg        *Config
	httpClient *http.Client
	once       sync.Once
)

// Init 初始化配置
func Init() {
	// 加载 .env 文件（如果存在）
	if err := godotenv.Load(); err != nil {
		log.Println("未找到 .env 文件，将从系统环境变量读取配置")
	}

	Cfg = &Config{
		NvidiaAPIKey:   MustGetEnv("NVIDIA_API_KEY"),
		PineconeAPIKey: MustGetEnv("PINECONE_API_KEY"),
		PineconeIndex:  GetEnv("PINECONE_INDEX", "gin-bot"),
		DBDSN:          MustGetEnv("DB_DSN"),
		RedisAddr:      GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:  GetEnv("REDIS_PASSWORD", ""),
		BotWSURL:       GetEnv("BOT_WS_URL", "ws://127.0.0.1:3001"),
		BotToken:       GetEnv("BOT_TOKEN", ""),
		ProxyURL:       GetEnv("HTTP_PROXY", ""),
		SuperUsers:     parseSuperUsers(GetEnv("BOT_SUPER_USERS", "")),
	}
}

// MustGetEnv 获取必填环境变量，不存在则 panic
func MustGetEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("环境变量 %s 未设置", key)
	}
	return value
}

// GetEnv 获取环境变量，不存在则返回默认值
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// parseSuperUsers 解析超级用户列表（逗号分隔）
func parseSuperUsers(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	users := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			users = append(users, id)
		}
	}
	return users
}

// GetHTTPClient 获取复用的 HTTP Client（带可选代理）
func GetHTTPClient() *http.Client {
	once.Do(func() {
		transport := &http.Transport{}
		if Cfg != nil && Cfg.ProxyURL != "" {
			if proxyURL, err := url.Parse(Cfg.ProxyURL); err == nil {
				transport.Proxy = http.ProxyURL(proxyURL)
			}
		}
		httpClient = &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		}
	})
	return httpClient
}

// GetHTTPClientWithTimeout 获取指定超时时间的 HTTP Client
func GetHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	transport := &http.Transport{}
	if Cfg != nil && Cfg.ProxyURL != "" {
		if proxyURL, err := url.Parse(Cfg.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
