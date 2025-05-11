package router

import (
	"time"

	"github.com/Xushengqwer/go-common/core"                        // 核心包，包含 ZapLogger
	commonMiddleware "github.com/Xushengqwer/go-common/middleware" // 通用中间件
	"github.com/Xushengqwer/post_search/config"                    // 项目特定的配置包
	"github.com/Xushengqwer/post_search/constants"                 // 假设常量包定义了 ServiceName
	_ "github.com/Xushengqwer/post_search/docs"                    // 确保路径正确
	"github.com/Xushengqwer/post_search/internal/api"              // 项目的 API Handler 包

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	otelgin "go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.uber.org/zap"
)

// SetupRouter 初始化并配置 Gin 引擎，为 PostSearch 服务注册所有中间件和路由。
// 设计目的:
//   - 作为应用路由配置的统一入口点。
//   - 应用全局中间件，处理通用逻辑如日志、错误恢复、超时等。
//   - 创建 API 版本分组（例如 /api/v1）。
//   - 接收已实例化的 API Handler (例如 SearchHandler)，并将它们的路由注册到相应的分组下。
//
// 参数:
//   - logger: *core.ZapLogger 实例，用于中间件和应用日志。
//   - cfg: *config.PostSearchConfig 实例，包含应用的全局配置，如服务器设置、超时等。
//   - searchHandler: *api.SearchHandler 实例，搜索 API 的处理器。
//
// 返回:
//   - *gin.Engine: 配置完成的 Gin 引擎实例，可以直接运行。
func SetupRouter(
	logger *core.ZapLogger,
	cfg *config.PostSearchConfig,
	searchHandler *api.SearchHandler, // 直接注入 SearchHandler
) *gin.Engine {
	logger.Info("开始为 PostSearch 服务设置 Gin 路由...")

	// 1. 创建 Gin 引擎实例
	// gin.Default() 会默认使用 Logger (Gin自带的日志中间件) 和 Recovery (Panic恢复) 中间件。
	router := gin.Default()

	// 2. 应用通用中间件 (中间件的注册顺序通常很重要)

	// 2.1 OpenTelemetry 中间件 (建议放在最前面)
	// 使用在 constants 包中定义的服务名称。
	router.Use(otelgin.Middleware(constants.ServiceName)) // 使用 constants.ServiceName
	logger.Info("OpenTelemetry (OTel) 中间件已注册。", zap.String("service_name", constants.ServiceName))

	// 2.2 全局错误处理中间件 (Panic Recovery)
	router.Use(commonMiddleware.ErrorHandlingMiddleware(logger))
	logger.Info("全局错误处理 (Panic Recovery) 中间件已注册。")

	// 2.3 请求日志中间件
	if baseLogger := logger.Logger(); baseLogger != nil {
		router.Use(commonMiddleware.RequestLoggerMiddleware(baseLogger))
		logger.Info("请求日志中间件已注册。")
	} else {
		logger.Warn("无法获取底层的 *zap.Logger 实例，跳过请求日志中间件的注册。")
	}

	// 2.4 请求超时中间件
	var requestTimeout time.Duration
	if cfg.Server.RequestTimeout > 0 {
		requestTimeout = cfg.Server.RequestTimeout
		logger.Info("从配置文件中加载请求超时设置。", zap.Duration("configured_timeout", requestTimeout))
	} else {
		// 如果 cfg.Server.RequestTimeout 是 0 或负数，
		// 这可能意味着 YAML 中的 requestTimeout 字段缺失、为空、为 "0s" 或为无效的持续时间字符串，
		// 导致 mapstructure 将其解析为 time.Duration 的零值 (0) 或一个无效值。
		logger.Warn("配置文件中的请求超时 (server.requestTimeout) 无效或未设置，将使用默认超时10秒。",
			zap.Duration("parsed_duration_from_config", cfg.Server.RequestTimeout), // 记录从配置中解析到的（可能是零或无效的）值
		)
		requestTimeout = 10 * time.Second // 设置一个合理的默认超时
	}
	router.Use(commonMiddleware.RequestTimeoutMiddleware(logger, requestTimeout))
	logger.Info("请求超时中间件已注册。", zap.Duration("timeout_duration", requestTimeout))

	// 3. 创建 API 版本路由组
	// API 前缀可以考虑从配置中读取，以增加灵活性。
	apiV1Group := router.Group("/api/v1")
	logger.Info("API 路由将统一注册到基础路径 /api/v1 分组下。")

	// 4. 注册 SearchHandler 的路由
	// 检查 searchHandler 是否已正确初始化。
	if searchHandler != nil {
		// 调用 SearchHandler 内部定义的 RegisterRoutes 方法，
		// 将其负责的路由（例如 /search, /_health）注册到我们创建的 apiV1Group下。
		searchHandler.RegisterRoutes(apiV1Group)
		logger.Info("SearchHandler 的相关路由已成功注册到 /api/v1 分组。")
	} else {
		// 如果 SearchHandler 未初始化，这是一个严重的配置问题。
		logger.Error("SearchHandler 实例为 nil，其 API 路由无法注册！")
		// 对于核心功能，panic 可能更合适，以尽早暴露问题。
		panic("致命错误：SearchHandler 未初始化，无法注册 API 路由。")
	}

	logger.Info("所有业务相关的 API 路由已注册完成。")

	// 5. 配置 Swagger UI 路由
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	logger.Info("Swagger UI 路由已注册。可以通过 /swagger/index.html 访问 API 文档。")

	logger.Info("PostSearch 服务的 Gin 路由设置已全部完成。")
	// 6. 返回配置好的 Gin 引擎实例
	return router
}
