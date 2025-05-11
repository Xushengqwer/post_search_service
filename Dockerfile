    # --- 构建阶段 ---
    FROM golang:1.21-alpine AS builder
    LABEL stage=builder

    # 设置工作目录
    WORKDIR /app

    # 解决国内可能的 Go 模块下载问题 (可选, 根据你的网络环境)
    # ENV GOPROXY=https://goproxy.cn,direct

    # 复制 go.mod 和 go.sum 文件并下载依赖项
    # 这样可以利用 Docker 的层缓存机制，只有当这些文件改变时才重新下载依赖
    COPY go.mod go.sum ./
    RUN go mod download && go mod verify

    # 复制项目的其余源代码
    COPY . .

    # 构建 Go 应用
    # CGO_ENABLED=0 禁用 CGO，以创建静态链接的二进制文件，减小镜像大小，避免对 C 库的依赖
    # GOOS=linux 指定目标操作系统为 Linux
    # -ldflags="-s -w" 减小二进制文件大小 (可选)
    # 假设你的 main.go 在项目根目录 (根据你之前的 main.go 结构)
    RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-s -w" -o /app/post-search-service .

    # --- 运行阶段 ---
    FROM alpine:latest

    # 设置工作目录
    WORKDIR /app

    # 从构建阶段复制编译好的二进制文件
    COPY --from=builder /app/post-search-service /app/post-search-service

    # 复制配置文件目录到镜像中
    # 你的 main.go 启动时会从 /config/config.development.yaml 读取配置
    COPY config /config

    # (可选) 如果你的应用需要其他静态资源或模板，也在这里复制

    # 暴露应用程序监听的端口 (与 docker-compose.yaml 中的 ports 配置对应)
    EXPOSE 8080

    # 设置容器启动时执行的命令
    # 使用 -config 参数指定配置文件的路径
    CMD ["/app/post-search-service", "-config", "/config/config.development.yaml"]
