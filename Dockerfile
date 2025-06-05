# --- 构建阶段 (Builder Stage) ---
# 使用特定版本的 Go Alpine 镜像作为构建环境，Alpine 镜像体积小
FROM golang:1.23-alpine AS builder
LABEL stage=builder

# 设置工作目录，后续的命令都会在这个目录下执行
WORKDIR /app

# (可选) 如果在国内环境构建，设置 GOPROXY 可以加速 Go 模块下载
# ENV GOPROXY=https://goproxy.cn,direct

# 1. 复制 go.mod 和 go.sum 文件
# 目的是先下载依赖，利用 Docker 的层缓存机制。
# 只有当 go.mod 或 go.sum 发生变化时，才会重新执行下面的 RUN go mod download。
COPY go.mod go.sum ./
# 2. 下载依赖项
RUN go mod download && go mod verify

# 3. 复制项目的其余所有源代码到工作目录
COPY . .

# 4. 构建 Go 应用
#    CGO_ENABLED=0: 禁用 CGO，创建静态链接的二进制文件，有助于减小镜像体积并避免对系统 C 库的依赖。
#    GOOS=linux: 指定目标操作系统为 Linux (因为我们的运行阶段镜像是 Alpine Linux)。
#    -a: 强制重新构建所有依赖的包。
#    -installsuffix cgo: 通常与 CGO_ENABLED=1 配合使用，但在 CGO_ENABLED=0 时影响不大，可以保留或移除。
#    -ldflags="-s -w": 优化编译选项。
#        -s: 移除符号表 (symbol table)。
#        -w: 移除 DWARF 调试信息。
#        这两个选项可以显著减小最终二进制文件的大小。
#    -o /app/post-search-service: 指定编译输出的文件名和路径。
#    .: 表示编译当前目录 (WORKDIR /app) 下的 Go 主包。
#      (假设您的 main.go 文件位于项目根目录，即 Dockerfile 所在的目录，或者 cmd/main.go 并且您在 WORKDIR /app/cmd 下执行构建)
#      根据您之前的文件结构，main.go 位于项目根目录 (post_search/main.go)，所以这里的 "." 是正确的，
#      前提是 Docker 构建上下文是项目根目录 (post_search)。
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-s -w" -o /app/post-search-service .

# --- 运行阶段 (Runtime Stage) ---
# 使用轻量级的 Alpine Linux 作为最终运行环境的基础镜像
FROM alpine:latest

# (推荐) 安装 ca-certificates 以支持 HTTPS 通信，以及 tzdata 以支持正确的时区处理
RUN apk update && apk add --no-cache ca-certificates tzdata && update-ca-certificates

# 设置工作目录
WORKDIR /app

# (推荐) 创建一个非 root 用户来运行应用，增强安全性
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
# 后续 CMD 将以这个用户身份运行

# 从构建阶段复制编译好的二进制可执行文件到当前阶段的工作目录
COPY --from=builder /app/post-search-service /app/post-search-service

# 复制配置文件目录到镜像中
# 假设您的应用会从 /config/config.development.yaml (或您在 CMD 中指定的路径) 读取配置
COPY config /config

# (可选) 如果您的应用需要其他静态资源 (例如 Swagger UI 文件，如果不是通过代码嵌入的)，也在这里复制
# COPY static /app/static
# COPY templates /app/templates

# 声明应用程序在容器内监听的端口
# 这个端口应该与您在 config.development.yaml 中 server.port 配置的端口一致。
# 根据之前提供的 config.development.yaml，端口是 "8083"。
EXPOSE 8083

# 设置容器启动时默认执行的命令
# 使用 -config 参数指定配置文件的路径 (相对于容器内的文件系统)
# 使用非 root 用户运行
USER appuser
CMD ["/app/post-search-service", "-config", "/config/config.development.yaml"]
