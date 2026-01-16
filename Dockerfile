# ==================== 构建阶段 ====================
FROM golang:1.25-alpine AS builder

# 安装构建依赖
RUN apk add --no-cache git ca-certificates tzdata

# 设置工作目录
WORKDIR /app

# 复制依赖文件并下载依赖（利用 Docker 缓存）
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 编译应用（静态链接、禁用 CGO、优化体积）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /app/gin-bot \
    main.go

# ==================== 运行阶段 ====================
FROM alpine:3.19

# 安装必要的运行时依赖
RUN apk add --no-cache ca-certificates tzdata

# 设置时区为上海
ENV TZ=Asia/Shanghai

# 创建非 root 用户
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的二进制文件
COPY --from=builder /app/gin-bot .

# 切换到非 root 用户
USER appuser

# 暴露端口（如果有 HTTP 服务则取消注释）
# EXPOSE 8080

# 启动应用
CMD ["./gin-bot"]
