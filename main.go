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
	repoES "github.com/Xushengqwer/post_search/internal/repositories"
	"github.com/Xushengqwer/post_search/internal/service"
	"github.com/Xushengqwer/post_search/router" // 导入我们定义的 router 包
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
)

func main() {
	// --- 0. 配置和基础设置 ---
	var configFile string
	// 从命令行参数读取配置文件路径，默认为 "config/config.development.yaml"
	flag.StringVar(&configFile, "config", "config/config.development.yaml", "指定配置文件的路径")
	flag.Parse()

	// 1. 加载配置
	var cfg config.PostSearchConfig
	// 使用公共模块加载配置，cfg 需要是指针
	if err := core.LoadConfig(configFile, &cfg); err != nil {
		log.Fatalf("致命错误: 加载配置文件 '%s' 失败: %v", configFile, err)
	}

	// 2. 初始化 Logger
	logger, loggerErr := core.NewZapLogger(cfg.ZapConfig) // 使用 core.NewZapLogger
	if loggerErr != nil {
		log.Fatalf("致命错误: 初始化 ZapLogger 失败: %v", loggerErr)
	}
	// 使用 defer 确保在 main 函数退出时同步日志缓冲区
	defer func() {
		logger.Info("正在同步所有日志条目...")
		if err := logger.Logger().Sync(); err != nil {
			// Sync 失败通常不影响程序运行，记录标准库 log 警告即可
			log.Printf("警告: ZapLogger Sync 操作失败: %v\n", err)
		}
	}()
	logger.Info("Logger 初始化成功。")

	// --- HTTP Transport 和 Tracer 初始化 ---
	// 为 Elasticsearch 客户端准备一个基础的 HTTP Transport
	// 这是标准库 http.DefaultTransport 的一个典型配置副本
	baseHttpTransport := &http.Transport{
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
	var esHttpClientTransport http.RoundTripper = baseHttpTransport // 默认使用基础 transport

	// 3. 初始化 TracerProvider (如果启用)
	var tracerShutdown func(context.Context) error = func(ctx context.Context) error { return nil } // 默认为空操作的关闭函数
	if cfg.TracerConfig.Enabled {
		var err error
		// 初始化 TracerProvider，传入服务名、版本和配置
		tracerShutdown, err = sharedTracing.InitTracerProvider(
			constants.ServiceName,    // 服务名常量
			constants.ServiceVersion, // 服务版本常量 (假设存在)
			cfg.TracerConfig,         // 追踪配置
		)
		if err != nil {
			logger.Fatal("初始化分布式追踪 TracerProvider 失败", zap.Error(err))
		}
		// 使用 defer 确保追踪系统在程序退出时优雅关闭
		defer func() {
			// 创建一个带超时的上下文用于关闭操作，例如5秒
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
		// 初始化 OTel HTTP Transport 以便自动追踪出站 HTTP 请求 (如果服务会发起 HTTP 调用)
		// 即使服务本身不主动发起 HTTP 请求，初始化它通常也无害，并为未来扩展做好准备。
		http.DefaultTransport = otelhttp.NewTransport(http.DefaultTransport)
		logger.Debug("OpenTelemetry HTTP Transport 已初始化并设置为默认值 (用于出站请求追踪)。")
	} else {
		logger.Info("分布式追踪功能已禁用 (根据配置)。")
	}

	// --- 核心组件初始化 ---

	// 4. 初始化 Elasticsearch 客户端
	esClientCore, err := coreES.NewESClient(cfg.ElasticsearchConfig, logger, esHttpClientTransport)
	if err != nil {
		logger.Fatal("创建 Elasticsearch 客户端失败", zap.Error(err))
		logger.Info("EventService 初始化成功。")

	}
	logger.Info("Elasticsearch 客户端初始化成功。")

	// 5. 初始化 Elasticsearch Repository
	esRepo := repoES.NewESPostRepository(esClientCore.Client, cfg.ElasticsearchConfig.IndexName, logger)
	logger.Info("Elasticsearch Repository 初始化成功。")

	// 6. 初始化业务服务层 - SearchService
	searchSvc := service.NewSearchService(esRepo, logger)
	logger.Info("SearchService 初始化成功。")

	// 7. 初始化业务服务层 - EventService (用于处理 Kafka 事件)
	eventSvc := coreKafka.NewEventService(esRepo, logger) // EventService 依赖 esRepo 和 logger
	// 8. 初始化 Kafka Sarama 配置
	saramaCfg, err := coreKafka.ConfigureSarama(cfg.KafkaConfig, logger)
	if err != nil {
		logger.Fatal("配置 Sarama (Kafka 客户端库) 失败", zap.Error(err))
	}
	logger.Info("Sarama (Kafka 客户端库) 配置初始化成功。")

	// 9. 初始化 Kafka DLQ (死信队列) 生产者
	dlqProducer, err := coreKafka.NewSyncProducer(cfg.KafkaConfig, saramaCfg, logger)
	if err != nil {
		logger.Fatal("创建 Kafka DLQ 同步生产者失败", zap.Error(err))
	}
	defer func() {
		logger.Info("正在关闭 Kafka DLQ 生产者...")
		if err := dlqProducer.Close(); err != nil {
			logger.Error("关闭 Kafka DLQ 生产者时发生错误", zap.Error(err))
		} else {
			logger.Info("Kafka DLQ 生产者已成功关闭。")
		}
	}()
	logger.Info("Kafka DLQ 同步生产者初始化成功。")

	// 10. 初始化 Kafka 消息处理器 (Handler)
	// 从 SubscribedTopics 获取 auditTopic 和 deleteTopic
	// **重要假设**: auditTopic 是 SubscribedTopics[0], deleteTopic 是 SubscribedTopics[1]
	// 你需要根据你的实际配置和需求来确定这两个主题的来源。
	var auditTopic, deleteTopic string
	if len(cfg.KafkaConfig.SubscribedTopics) >= 1 {
		auditTopic = cfg.KafkaConfig.SubscribedTopics[0]
	} else {
		logger.Fatal("Kafka 配置错误：未找到用于审计事件的主题 (SubscribedTopics[0])")
	}
	if len(cfg.KafkaConfig.SubscribedTopics) >= 2 {
		deleteTopic = cfg.KafkaConfig.SubscribedTopics[1]
	} else {
		// 如果只有一个订阅主题，或者删除主题不是第二个，这里需要调整逻辑
		// 例如，如果删除事件有自己独立的配置字段，或者你需要更复杂的逻辑来确定它
		logger.Warn("Kafka 配置警告：未明确找到用于删除事件的主题 (期望在 SubscribedTopics[1])。如果服务不处理删除事件，此警告可忽略。")
		// deleteTopic 保持为空字符串，或者根据你的逻辑设置一个默认值或引发错误
	}
	if auditTopic == "" || (deleteTopic == "" && len(cfg.KafkaConfig.SubscribedTopics) > 1) /* 只有在期望有两个主题时才检查deleteTopic */ {
		logger.Fatal("Kafka 主题配置不完整：auditTopic 或 deleteTopic 未能正确从 SubscribedTopics 中提取。")
	}

	kafkaHandler := coreKafka.NewHandler(
		eventSvc,
		dlqProducer,
		cfg.KafkaConfig.DLQTopic,
		auditTopic,  // 使用从 SubscribedTopics 提取的 auditTopic
		deleteTopic, // 使用从 SubscribedTopics 提取的 deleteTopic
		logger,
		cfg.KafkaConfig.MaxRetryAttempts, // 传递最大重试次数
	)
	logger.Info("Kafka 消息处理器 (Handler) 初始化成功。")

	// 11. 初始化 Kafka 消费者组
	consumerGroup, err := coreKafka.NewConsumerGroup(
		cfg.KafkaConfig,
		saramaCfg,
		kafkaHandler,
		logger,
	)
	if err != nil {
		logger.Fatal("创建 Kafka 消费者组失败", zap.Error(err))
	}
	defer func() {
		logger.Info("正在关闭 Kafka 消费者组...")
		if err := consumerGroup.Close(); err != nil {
			logger.Error("关闭 Kafka 消费者组时发生错误", zap.Error(err))
		} else {
			logger.Info("Kafka 消费者组已成功关闭。")
		}
	}()
	logger.Info("Kafka 消费者组初始化成功。")

	// 12. 初始化 API Handler (控制器)
	searchApiHandler := api.NewSearchHandler(searchSvc, logger) // 使用 searchApiHandler 避免与 kafkaHandler 名称混淆
	logger.Info("API Handler (SearchHandler) 初始化成功。")

	// 13. 初始化并配置 Gin Web 引擎及路由
	// 调用我们定义的 router 包中的 SetupRouter 函数
	ginRouter := router.SetupRouter(logger, &cfg, searchApiHandler) // 传递 searchApiHandler
	logger.Info("Gin Web 引擎及 API 路由初始化和注册成功。")

	// --- 服务启动与优雅关闭 ---

	// 14. 创建一个带取消功能的上下文，用于协调服务的优雅关闭
	// 当接收到关闭信号时，调用 cancel() 可以通知所有监听此上下文的 goroutine 开始关闭。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // 确保在 main 函数退出时，即使发生 panic，也会调用 cancel。

	// 14.a 启动 Kafka 消费者组 (在后台 Goroutine 中运行)
	// Start 方法内部会启动一个或多个 goroutine 来消费消息。
	consumerGroup.Start(ctx) // 将可取消的上下文传递给消费者组
	logger.Info("Kafka 消费者组已启动，开始在后台消费消息。")

	// 14.b 启动 HTTP 服务器 (在后台 Goroutine 中运行)
	// serverAddr := cfg.Server.ListenAddr // 如果 ListenAddr 包含 IP 和端口，例如 "0.0.0.0:8080"
	// if serverAddr == "" { // 如果 ListenAddr 为空，则只使用 Port
	//  serverAddr = ":" + cfg.Server.Port
	// }
	// 修正：确保 serverAddr 的格式正确
	serverAddr := cfg.Server.ListenAddr
	if serverAddr == "" { // 如果 ListenAddr 为空字符串，则监听所有接口的指定端口
		serverAddr = ":" + cfg.Server.Port
	} else if !strings.Contains(serverAddr, ":") { // 如果 ListenAddr 只有 IP，则附加端口
		serverAddr = serverAddr + ":" + cfg.Server.Port
	}

	httpServer := &http.Server{
		Addr:    serverAddr,
		Handler: ginRouter, // 使用配置好的 Gin 引擎作为处理器
		// ReadTimeout:  cfg.Server.ReadTimeout,  // 建议在 ServerConfig 中添加 ReadTimeout, WriteTimeout 等
		// WriteTimeout: cfg.Server.WriteTimeout,
		// IdleTimeout:  cfg.Server.IdleTimeout,
	}

	go func() {
		logger.Info("HTTP API 服务器正在启动...", zap.String("listen_address", serverAddr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// http.ErrServerClosed 是 Shutdown 或 Close 方法正常调用时返回的错误，不应视为致命错误。
			logger.Error("HTTP API 服务器启动失败或意外停止", zap.Error(err))
			cancel() // 如果服务器启动失败，通知其他部分（如 Kafka 消费者）也开始关闭。
		}
	}()

	// 15. 实现优雅关闭机制
	// 创建一个通道来监听操作系统信号。
	quitSignal := make(chan os.Signal, 1)
	// signal.Notify 使 quitSignal 通道能够接收指定的信号。
	// syscall.SIGINT: 通常由 Ctrl+C 触发。
	// syscall.SIGTERM: 通常由 `kill` 命令（不带 -9）或其他进程管理工具发送的终止信号。
	signal.Notify(quitSignal, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("服务已成功启动。正在监听中断或终止信号以进行优雅关闭...")

	// 阻塞主 goroutine，直到从 quitSignal 通道接收到一个信号。
	receivedSignal := <-quitSignal
	logger.Info("接收到关闭信号，开始进行服务的优雅关闭...", zap.String("signal", receivedSignal.String()))

	// 调用 cancel() 函数，这将使得传递给 Kafka 消费者组 (consumerGroup.Start(ctx))
	// 以及其他可能监听此上下文的 goroutine 的上下文被取消。
	// 这些 goroutine 在检测到上下文取消后，应开始执行它们自己的清理和退出逻辑。
	cancel()
	logger.Info("已发出全局上下文取消信号，通知所有组件开始关闭。")

	// 为 HTTP 服务器的关闭操作创建一个带超时的上下文。
	// 这确保了 Shutdown 操作不会无限期阻塞，即使有活动的连接未能及时关闭。
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second) // 例如，15秒超时
	defer shutdownCancel()

	// 关闭 HTTP 服务器。
	// httpServer.Shutdown 会尝试优雅地关闭服务器：它首先停止接受新的连接，
	// 然后等待现有活动连接完成处理（在 shutdownCtx 的超时限制内）。
	logger.Info("正在优雅地关闭 HTTP API 服务器...")
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("关闭 HTTP API 服务器时发生错误", zap.Error(err))
	} else {
		logger.Info("HTTP API 服务器已成功关闭。")
	}

	// Kafka ConsumerGroup 和 DLQ Producer 的关闭已经通过各自的 defer 语句安排。
	// defer 的执行顺序是后进先出 (LIFO)。
	// consumerGroup.Close() 和 dlqProducer.Close() 内部应该有逻辑等待其相关的 goroutine 完成。

	logger.Info("服务所有组件已完成关闭流程，程序即将退出。")
}
