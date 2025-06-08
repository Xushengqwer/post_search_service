# post_search/Dockerfile (已修正配置文件复制路径)

# ---- Builder Stage ----
FROM golang:1.23-alpine AS builder

ENV CGO_ENABLED=0 GOOS=linux
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-w -s" -o /app/post_search_server ./main.go


# ---- Final Stage ----
FROM alpine:latest

RUN apk --no-cache add curl

# 【关键修正】确保目标目录存在
RUN mkdir -p /app/config

# 从 builder 阶段复制编译好的二进制文件
COPY --from=builder /app/post_search_server /app/post_search_server

# 复制本地的 config.development.yaml 并重命名为 config.yaml
COPY ./config/config.development.yaml /app/config/config.yaml

WORKDIR /app
EXPOSE 8083

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD curl -f http://localhost:8083/api/v1/search/_health || exit 1

CMD ["./post_search_server", "-config", "./config/config.yaml"]