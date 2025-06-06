package main

import (
	"context"
	"errors"
	"flag"
	_ "github.com/Xushengqwer/post_search/docs" // 确保路径正确
	"log"                                       // 标准库 log 用于早期启动错误
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Xushengqwer/go-common/core"
	sharedTracing "github.com/Xushengqwer/go-common/core/tracing"
	"github.com/Xushengqwer/post_search/config"
	"github.com/Xushengqwer/post_search/constants"
	"github.com/Xushengqwer/post_search/internal/api"
	coreES "github.com/Xushengqwer/post_search/internal/core/es"
	coreKafka "github.com/Xushengqwer/post_search/internal/core/kafka"
	repoES "github.com/Xushengqwer/post_search/internal/repositories" // 确保导入了 repositories 包
	"github.com/Xushengqwer/post_search/internal/service"
	"github.com/Xushengqwer/post_search/router"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
)

// @title 帖子搜索服务 API
// @version 1.0.0
// @description 这是帖子搜索服务的 API 文档。它允许搜索从 Kafka 事件中索引的帖子。
// @termsOfService http://swagger.io/terms/

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8083 // 请根据您的实际配置修改
// @schemes http https  // 根据您的服务支持情况调整
func main() {
	// --- 0. 配置和基础设置 ---
	var configFile string
	flag.StringVar(&configFile, "config", "config/config.development.yaml", "指定配置文件的路径")
	flag.Parse()

	var cfg config.PostSearchConfig
	if err := core.LoadConfig(configFile, &cfg); err != nil {
		log.Fatalf("致命错误: 加载配置文件 '%s' 失败: %v", configFile, err)
	}

	logger, loggerErr := core.NewZapLogger(cfg.ZapConfig)
	if loggerErr != nil {
		log.Fatalf("致命错误: 初始化 ZapLogger 失败: %v", loggerErr)
	}
	defer func() {
		logger.Info("正在同步所有日志条目...")
		if err := logger.Logger().Sync(); err != nil {
			log.Printf("警告: ZapLogger Sync 操作失败: %v\n", err)
		}
	}()
	logger.Info("Logger 初始化成功。")

	// --- HTTP Transport 和 Tracer 初始化 ---
	baseHttpTransport := &http.Transport{ // [cite: post_search/main.go]
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	var esHttpClientTransport http.RoundTripper = baseHttpTransport

	var tracerShutdown func(context.Context) error = func(ctx context.Context) error { return nil }
	if cfg.TracerConfig.Enabled {
		var err error
		tracerShutdown, err = sharedTracing.InitTracerProvider(
			constants.ServiceName,
			constants.ServiceVersion,
			cfg.TracerConfig,
		)
		if err != nil {
			logger.Fatal("初始化分布式追踪 TracerProvider 失败", zap.Error(err))
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			logger.Info("正在关闭分布式追踪 TracerProvider...")
			if err := tracerShutdown(ctx); err != nil {
				logger.Error("关闭分布式追踪 TracerProvider 时发生错误", zap.Error(err))
			} else {
				logger.Info("分布式追踪 TracerProvider 已成功关闭。")
			}
		}()
		logger.Info("分布式追踪功能已初始化。")
		http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
		logger.Debug("OpenTelemetry HTTP Transport 已初始化并设置为默认值 (用于出站请求追踪)。")
	} else {
		logger.Info("分布式追踪功能已禁用 (根据配置)。")
	}

	// --- 核心组件初始化 ---

	// 4. 初始化 Elasticsearch 客户端
	// NewESClient 现在会处理两个索引的创建（如果它们不存在）
	esClientCore, err := coreES.NewESClient(cfg.ElasticsearchConfig, logger, esHttpClientTransport) // [cite: post_search/main.go]
	if err != nil {
		logger.Fatal("创建 Elasticsearch 客户端失败", zap.Error(err))
	}
	logger.Info("Elasticsearch 客户端初始化成功。")

	// 5. 初始化 Elasticsearch Repositories
	// 5.a 初始化主帖子仓库 (PostRepository)
	// 从配置中获取主帖子索引的名称
	primaryIndexName := cfg.ElasticsearchConfig.PrimaryIndex.Name
	if primaryIndexName == "" {
		logger.Fatal("主帖子索引名称 (elasticsearchConfig.primaryIndex.name) 未在配置中指定。")
	}
	postRepo := repoES.NewESPostRepository(esClientCore.Client, primaryIndexName, logger)
	logger.Info("主帖子 Elasticsearch Repository (PostRepository) 初始化成功。", zap.String("index_name", primaryIndexName))

	// 5.b 初始化热门搜索词仓库 (HotSearchTermRepository)
	// 从配置中获取热门搜索词索引的名称
	hotTermsIndexName := cfg.ElasticsearchConfig.HotTermsIndex.Name
	if hotTermsIndexName == "" {
		logger.Fatal("热门搜索词索引名称 (elasticsearchConfig.hotTermsIndex.name) 未在配置中指定。")
	}
	hotSearchTermRepo := repoES.NewESHotSearchTermRepository(esClientCore.Client, logger, hotTermsIndexName)
	logger.Info("热门搜索词 Elasticsearch Repository (HotSearchTermRepository) 初始化成功。", zap.String("index_name", hotTermsIndexName))

	// 6. 初始化业务服务层 - SearchService
	// 将两个仓库都注入到 SearchService
	searchSvc := service.NewSearchService(postRepo, hotSearchTermRepo, logger) // [cite: post_search/main.go]
	logger.Info("SearchService 初始化成功。")

	// 7. 初始化业务服务层 - EventService (用于处理 Kafka 事件)
	// EventService 依赖 postRepo (用于帖子索引) 和 logger
	eventSvc := coreKafka.NewEventService(postRepo, logger) // [cite: post_search/main.go]
	logger.Info("EventService 初始化成功。")                      // [cite: post_search/main.go]

	// 8. 初始化 Kafka Sarama 配置
	saramaCfg, err := coreKafka.ConfigureSarama(cfg.KafkaConfig, logger) // [cite: post_search/main.go]
	if err != nil {
		logger.Fatal("配置 Sarama (Kafka 客户端库) 失败", zap.Error(err))
	}
	logger.Info("Sarama (Kafka 客户端库) 配置初始化成功。")

	// 9. 初始化 Kafka DLQ (死信队列) 生产者
	dlqProducer, err := coreKafka.NewSyncProducer(cfg.KafkaConfig, saramaCfg, logger) // [cite: post_search/main.go]
	if err != nil {
		logger.Fatal("创建 Kafka DLQ 同步生产者失败", zap.Error(err))
	}
	defer func() { // [cite: post_search/main.go]
		logger.Info("正在关闭 Kafka DLQ 生产者...")
		if err := dlqProducer.Close(); err != nil {
			logger.Error("关闭 Kafka DLQ 生产者时发生错误", zap.Error(err))
		} else {
			logger.Info("Kafka DLQ 生产者已成功关闭。")
		}
	}()
	logger.Info("Kafka DLQ 同步生产者初始化成功。")

	// 10. 初始化 Kafka 消息处理器 (Handler)
	var auditTopic, deleteTopic string
	if len(cfg.KafkaConfig.SubscribedTopics) >= 1 { // [cite: post_search/main.go]
		auditTopic = cfg.KafkaConfig.SubscribedTopics[0]
	} else {
		logger.Fatal("Kafka 配置错误：未找到用于审计事件的主题 (SubscribedTopics[0])")
	}
	if len(cfg.KafkaConfig.SubscribedTopics) >= 2 { // [cite: post_search/main.go]
		deleteTopic = cfg.KafkaConfig.SubscribedTopics[1]
	} else {
		logger.Warn("Kafka 配置警告：未明确找到用于删除事件的主题 (期望在 SubscribedTopics[1])。如果服务不处理删除事件，此警告可忽略。")
	}
	// auditTopic 和 deleteTopic 的检查保持不变
	if auditTopic == "" || (deleteTopic == "" && len(cfg.KafkaConfig.SubscribedTopics) > 1) { // [cite: post_search/main.go]
		logger.Fatal("Kafka 主题配置不完整：auditTopic 或 deleteTopic 未能正确从 SubscribedTopics 中提取。")
	}

	kafkaHandler := coreKafka.NewHandler( // [cite: post_search/main.go]
		eventSvc,
		dlqProducer,
		cfg.KafkaConfig.DLQTopic,
		auditTopic,
		deleteTopic,
		logger,
		cfg.KafkaConfig.MaxRetryAttempts,
	)
	logger.Info("Kafka 消息处理器 (Handler) 初始化成功。")

	// 11. 初始化 Kafka 消费者组
	consumerGroup, err := coreKafka.NewConsumerGroup( // [cite: post_search/main.go]
		cfg.KafkaConfig,
		saramaCfg,
		kafkaHandler,
		logger,
	)
	if err != nil {
		logger.Fatal("创建 Kafka 消费者组失败", zap.Error(err))
	}
	defer func() { // [cite: post_search/main.go]
		logger.Info("正在关闭 Kafka 消费者组...")
		if err := consumerGroup.Close(); err != nil {
			logger.Error("关闭 Kafka 消费者组时发生错误", zap.Error(err))
		} else {
			logger.Info("Kafka 消费者组已成功关闭。")
		}
	}()
	logger.Info("Kafka 消费者组初始化成功。")

	// 12. 初始化 API Handler (控制器)
	searchApiHandler := api.NewSearchHandler(searchSvc, logger) // [cite: post_search/main.go]
	logger.Info("API Handler (SearchHandler) 初始化成功。")

	// 13. 初始化并配置 Gin Web 引擎及路由
	ginRouter := router.SetupRouter(logger, &cfg, searchApiHandler) // [cite: post_search/main.go]
	logger.Info("Gin Web 引擎及 API 路由初始化和注册成功。")

	// --- 服务启动与优雅关闭 ---
	ctx, cancel := context.WithCancel(context.Background()) // [cite: post_search/main.go]
	defer cancel()                                          // [cite: post_search/main.go]

	consumerGroup.Start(ctx) // [cite: post_search/main.go]
	logger.Info("Kafka 消费者组已启动，开始在后台消费消息。")

	serverAddr := cfg.Server.ListenAddr // [cite: post_search/main.go]
	if serverAddr == "" {
		serverAddr = ":" + cfg.Server.Port
	} else if !strings.Contains(serverAddr, ":") {
		serverAddr = serverAddr + ":" + cfg.Server.Port
	}

	httpServer := &http.Server{ // [cite: post_search/main.go]
		Addr:    serverAddr,
		Handler: ginRouter,
	}

	go func() { // [cite: post_search/main.go]
		logger.Info("HTTP API 服务器正在启动...", zap.String("listen_address", serverAddr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP API 服务器启动失败或意外停止", zap.Error(err))
			cancel()
		}
	}()

	quitSignal := make(chan os.Signal, 1)                      // [cite: post_search/main.go]
	signal.Notify(quitSignal, syscall.SIGINT, syscall.SIGTERM) // [cite: post_search/main.go]
	logger.Info("服务已成功启动。正在监听中断或终止信号以进行优雅关闭...")

	receivedSignal := <-quitSignal // [cite: post_search/main.go]
	logger.Info("接收到关闭信号，开始进行服务的优雅关闭...", zap.String("signal", receivedSignal.String()))

	cancel() // [cite: post_search/main.go]
	logger.Info("已发出全局上下文取消信号，通知所有组件开始关闭。")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second) // [cite: post_search/main.go]
	defer shutdownCancel()                                                                   // [cite: post_search/main.go]

	logger.Info("正在优雅地关闭 HTTP API 服务器...")                   // [cite: post_search/main.go]
	if err := httpServer.Shutdown(shutdownCtx); err != nil { // [cite: post_search/main.go]
		logger.Error("关闭 HTTP API 服务器时发生错误", zap.Error(err))
	} else {
		logger.Info("HTTP API 服务器已成功关闭。")
	}

	logger.Info("服务所有组件已完成关闭流程，程序即将退出。")
}
