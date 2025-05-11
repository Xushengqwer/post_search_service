package api

import (
	"github.com/Xushengqwer/go-common/core"
	"github.com/Xushengqwer/post_search/internal/models"
	"github.com/Xushengqwer/post_search/internal/service"
	"go.uber.org/zap"
	"net/http"

	// 导入共享响应包和 Gin
	"github.com/Xushengqwer/gateway/pkg/response"
	"github.com/gin-gonic/gin"
	// 如果有特定的服务层错误类型需要判断，也需要导入
)

// SearchHandler 封装搜索相关的 API 请求处理逻辑.
type SearchHandler struct {
	searchService *service.SearchService
	logger        *core.ZapLogger
}

// NewSearchHandler 创建 SearchHandler 实例.
func NewSearchHandler(searchSvc *service.SearchService, logger *core.ZapLogger) *SearchHandler {
	// 在这里进行依赖检查。Logger 本身也不能为 nil。
	if logger == nil {
		// 如果 logger 都为 nil，只能 panic
		panic("NewSearchHandler: logger cannot be nil")
	}
	if searchSvc == nil {
		// 使用注入的 logger 记录 Fatal 错误
		logger.Fatal("NewSearchHandler: SearchService 不能为 nil") // 日志消息已改为中文
	}

	return &SearchHandler{
		searchService: searchSvc,
		logger:        logger, // 注入 Logger
	}
}

// SearchPosts 处理帖子搜索请求
// @Summary      搜索帖子
// @Description  根据关键词、分页、排序等条件搜索帖子列表
// @Tags         Search
// @Accept       json
// @Produce      json
// @Param        q         query     string  false  "搜索关键词"
// @Param        page      query     int     false  "页码 (从1开始)" default(1) minimum(1)
// @Param        size      query     int     false  "每页数量" default(10) minimum(1) maximum(100)
// @Param        sort_by   query     string  false  "排序字段 (例如: updated_at, view_count, _score)" default(updated_at)
// @Param        sort_order query    string  false  "排序顺序 (asc 或 desc)" default(desc) Enums(asc, desc)
// @Success      200       {object}  models.SwaggerSearchResultResponse "搜索成功，返回匹配的帖子列表及分页信息。"
// @Failure      400       {object}  models.SwaggerErrorResponse "请求参数无效，例如页码超出范围或排序字段不支持。"
// @Failure      500       {object}  models.SwaggerErrorResponse "服务器内部错误，搜索服务遇到未预期的问题。"
// @Router       /api/v1/search [get]
func (h *SearchHandler) SearchPosts(c *gin.Context) {
	var req models.SearchRequest

	// 1. 绑定查询参数并进行验证
	// ShouldBindQuery 会根据 'form' 和 'binding' 标签进行绑定和验证
	if err := c.ShouldBindQuery(&req); err != nil {
		// 使用 logger 记录 Warn 级别日志，使用 zap.Error 字段记录错误对象
		h.logger.Warn("请求参数绑定或验证失败", zap.Error(err))

		// 使用共享响应包返回客户端错误
		response.RespondError(c, http.StatusBadRequest, response.ErrCodeClientInvalidInput, "请求参数无效")
		return
	}

	// 记录绑定后的请求参数 (调试用)
	// 使用 logger 记录 Debug 级别日志，使用 zap.Any 或其他字段记录请求结构体
	h.logger.Debug("绑定后的搜索请求", zap.Any("request", req))

	// 2. 调用服务层执行搜索逻辑
	// 使用 c.Request.Context() 将请求上下文传递给服务层，用于超时控制和追踪
	results, err := h.searchService.Search(c.Request.Context(), req)
	if err != nil {
		// 使用 logger 记录 Error 级别日志，使用 zap.Error 记录错误对象
		h.logger.Error("服务层搜索失败", zap.Error(err))

		// --- 错误处理判断 (示例) ---
		// 这里可以根据服务层返回的具体错误类型来决定返回给客户端的状态码和错误码
		// 例如，如果服务层返回一个表示资源未找到的特定错误：
		// var notFoundErr *service.NotFoundError // 假设有这样的错误类型
		// if errors.As(err, &notFoundErr) {
		//  response.RespondError(c, http.StatusNotFound, response.ErrCodeClientResourceNotFound, "未找到相关资源")
		//  return
		// }

		// 默认情况下，将服务层错误视为服务器内部错误
		response.RespondError(c, http.StatusInternalServerError, response.ErrCodeServerInternal, "搜索服务内部错误")
		return
	}

	// 3. 使用共享响应包返回成功响应
	// 使用 logger 记录 Info 级别日志，使用 zap.Int 字段记录结果数量
	h.logger.Info("搜索成功", zap.Int("结果数量", len(results.Hits)))
	response.RespondSuccess(c, results, "搜索成功")
}

// HealthCheck 健康检查处理函数（存活度检查）。
// 只检查服务进程是否在运行并能响应 HTTP 请求。
// @Summary      健康检查
// @Description  检查服务存活状态
// @Tags         Health
// @Produce      json
// @Success      200 {object} models.SwaggerHealthCheckResponse "服务存活"
// @Failure      503 {object} models.SwaggerErrorResponse "服务不存活（通常不会发生，除非服务进程已挂）"
// @Router       /api/v1/_health [get] // 修正：包含 /api/v1 前缀
func (h *SearchHandler) HealthCheck(c *gin.Context) {
	// 对于简单的存活度检查，只要请求能到达这里，就认为服务是活着的。
	// 直接返回成功响应即可。
	h.logger.Debug("执行存活度健康检查")
	response.RespondSuccess(c, gin.H{"status": "ok"}, "服务存活")
}

// RegisterRoutes 将搜索相关的路由注册到提供的 Gin 路由组 (RouterGroup) 上。
// 这个方法使得路由定义与 SearchHandler 的具体实现内聚，方便管理。
// 参数:
//   - rg: 一个 *gin.RouterGroup 实例，SearchHandler 的路由将被注册到这个组下。
//     例如，如果这个 rg 是通过 router.Group("/api/v1") 创建的，
//     那么这里注册的 "/search" 最终路径将是 "/api/v1/search"。
func (h *SearchHandler) RegisterRoutes(rg *gin.RouterGroup) {
	h.logger.Info("开始注册 SearchHandler 的路由...")
	// 注册帖子搜索接口
	// GET /search (实际完整路径取决于传入的 rg)
	// 对应 Swagger 注释中的 @Router /search [get]
	rg.GET("/search", h.SearchPosts)
	h.logger.Info("路由 GET /search 已注册到 SearchHandler.SearchPosts")

	// 注册健康检查接口
	// GET /_health (实际完整路径取决于传入的 rg)
	// 对应 Swagger 注释中的 @Router /_health [get]
	rg.GET("/_health", h.HealthCheck)
	h.logger.Info("路由 GET /_health 已注册到 SearchHandler.HealthCheck")
	h.logger.Info("SearchHandler 的所有路由已注册完成。")
}
