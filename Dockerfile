# post_search/Dockerfile

# ---- Builder Stage ----
# 使用官方的 Go 镜像作为构建环境
FROM golang:1.23-alpine AS builder

# 设置必要的环境变量
ENV CGO_ENABLED=0 GOOS=linux
ENV GOPROXY=https://goproxy.cn,direct

# 设置工作目录
WORKDIR /build

# 复制 go.mod 和 go.sum 文件，并下载依赖项
COPY go.mod go.sum ./
RUN go mod download

# 复制整个项目的源代码
COPY . .

# 编译 Go 应用程序
# -ldflags="-w -s" 用于减小二进制文件大小
# -o /app/post_search_server 指定输出的二进制文件路径和名称
RUN go build -ldflags="-w -s" -o /app/post_search_server ./cmd/main.go


# ---- Final Stage ----
# 使用一个极简的基础镜像
FROM alpine:latest

# 安装 curl 用于健康检查
RUN apk --no-cache add curl

# 从 builder 阶段复制编译好的二进制文件
COPY --from=builder /app/post_search_server /app/post_search_server

# 复制配置文件到镜像中
COPY ./config /app/config

# 设置最终的工作目录
WORKDIR /app

# 暴露应用程序监听的端口
EXPOSE 8083

# 健康检查：检查 /api/v1/search/_health 端点是否可访问
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD curl -f http://localhost:8083/api/v1/search/_health || exit 1

# 容器启动时执行的命令
# 通过 -config 标志明确告诉程序配置文件的位置
CMD ["./post_search_server", "-config", "./config/config.yaml"]