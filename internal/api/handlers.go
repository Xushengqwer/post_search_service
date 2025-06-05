package api

import (
	"context" // 导入 context 包
	"net/http"
	"strconv" // 导入 strconv 包用于转换 limit 参数
	"strings" // 导入 strings 包用于 TrimSpace
	"time"    // 导入 time 包用于异步记录的超时

	"github.com/Xushengqwer/gateway/pkg/response" // 确保这个包路径正确
	"github.com/Xushengqwer/go-common/core"
	"github.com/Xushengqwer/post_search/internal/models"
	"github.com/Xushengqwer/post_search/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SearchHandler 封装搜索相关的 API 请求处理逻辑.
type SearchHandler struct {
	searchService *service.SearchService
	logger        *core.ZapLogger
}

// NewSearchHandler 创建 SearchHandler 实例.
// ... (您现有的 NewSearchHandler 函数保持不变) ...
func NewSearchHandler(searchSvc *service.SearchService, logger *core.ZapLogger) *SearchHandler { // [cite: post_search/internal/api/handlers.go]
	if logger == nil {
		panic("NewSearchHandler: logger cannot be nil")
	}
	if searchSvc == nil {
		logger.Fatal("NewSearchHandler: SearchService 不能为 nil")
	}

	return &SearchHandler{
		searchService: searchSvc,
		logger:        logger,
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
// @Router       /api/v1/search/search [get]
func (h *SearchHandler) SearchPosts(c *gin.Context) {
	var req models.SearchRequest

	if err := c.ShouldBindQuery(&req); err != nil {
		h.logger.Warn("请求参数绑定或验证失败", zap.Error(err)) // [cite: post_search/internal/api/handlers.go]
		response.RespondError(c, http.StatusBadRequest, response.ErrCodeClientInvalidInput, "请求参数无效")
		return
	}
	h.logger.Debug("绑定后的搜索请求", zap.Any("request", req)) // [cite: post_search/internal/api/handlers.go]

	// --- 新增：异步记录搜索关键词 ---
	if strings.TrimSpace(req.Query) != "" {
		// 使用 goroutine 异步执行，避免阻塞主搜索流程
		// 复制 req.Query 到一个新变量，以避免在 goroutine 中捕获循环变量或请求对象的问题
		queryToLog := req.Query
		go func(query string) {
			// 为这个异步操作创建一个独立的上下文，可以设置一个较短的超时
			// 注意：c.Request.Context() 是针对整个HTTP请求的，如果请求结束，这个上下文会被取消。
			// 对于后台任务，最好创建一个新的上下文。
			logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 例如5秒超时
			defer cancel()

			if err := h.searchService.LogSearchQuery(logCtx, query); err != nil {
				// 记录热门词失败通常不应影响主搜索请求的成功状态，所以只记录错误。
				h.logger.Error("异步记录搜索关键词失败",
					zap.String("query", query),
					zap.Error(err),
				)
			} else {
				h.logger.Debug("搜索关键词已异步提交记录", zap.String("query", query))
			}
		}(queryToLog)
	}
	// --- 结束新增部分 ---

	results, err := h.searchService.Search(c.Request.Context(), req) // [cite: post_search/internal/api/handlers.go]
	if err != nil {
		h.logger.Error("服务层搜索失败", zap.Error(err)) // [cite: post_search/internal/api/handlers.go]
		response.RespondError(c, http.StatusInternalServerError, response.ErrCodeServerInternal, "搜索服务内部错误")
		return
	}

	h.logger.Info("搜索成功", zap.Int("结果数量", len(results.Hits))) // [cite: post_search/internal/api/handlers.go]
	response.RespondSuccess(c, results, "搜索成功")
}

// GetHotSearchTerms 处理获取热门搜索词的请求
// @Summary      获取热门搜索词
// @Description  返回最流行或最近搜索词的列表。
// @Tags         Search
// @Produce      json
// @Param        limit    query     int     false  "返回的热门搜索词数量" default(10) minimum(1) maximum(50)
// @Success      200      {object}  models.SwaggerHotSearchTermsResponse "成功，返回热门搜索词列表。"
// @Failure      500      {object}  models.SwaggerErrorResponse "服务器内部错误，无法获取热门搜索词。"
// @Router       /api/v1/search/hot-terms [get]
func (h *SearchHandler) GetHotSearchTerms(c *gin.Context) {
	// 从查询参数中获取 limit，并提供默认值和范围验证
	limitStr := c.DefaultQuery("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 10 // 如果转换失败或值无效，则使用默认值
	} else if limit > 50 {
		limit = 50 // 设置一个最大上限，防止请求过多数据
	}

	h.logger.Info("收到获取热门搜索词请求", zap.Int("limit", limit))

	// 调用服务层获取热门搜索词
	// 使用 c.Request.Context() 将请求上下文传递给服务层
	terms, err := h.searchService.GetHotSearchTerms(c.Request.Context(), limit)
	if err != nil {
		h.logger.Error("服务层获取热门搜索词失败", zap.Int("limit", limit), zap.Error(err))
		// 使用您项目中定义的标准错误响应格式
		response.RespondError(c, http.StatusInternalServerError, response.ErrCodeServerInternal, "获取热门搜索词失败")
		return
	}

	// 如果热门搜索词列表为nil（例如，数据库中还没有任何统计数据），
	// 返回一个空数组而不是null，这通常是前端更期望的格式。
	if terms == nil {
		terms = make([]models.HotSearchTerm, 0)
	}

	h.logger.Info("成功获取热门搜索词列表", zap.Int("count", len(terms)), zap.Int("requested_limit", limit))
	// 使用您项目中定义的标准成功响应格式
	response.RespondSuccess(c, terms, "热门搜索词获取成功")
}

// HealthCheck 健康检查处理函数
// ... (您现有的 HealthCheck 函数保持不变) ...
func (h *SearchHandler) HealthCheck(c *gin.Context) { // [cite: post_search/internal/api/handlers.go]
	h.logger.Debug("执行存活度健康检查")
	response.RespondSuccess(c, gin.H{"status": "ok"}, "服务存活")
}

// RegisterRoutes 将搜索相关的路由注册到提供的 Gin 路由组 (RouterGroup) 上。
func (h *SearchHandler) RegisterRoutes(rg *gin.RouterGroup) {
	h.logger.Info("开始注册 SearchHandler 的路由...") // [cite: post_search/internal/api/handlers.go]

	// 注册帖子搜索接口
	rg.GET("/search", h.SearchPosts)                               // [cite: post_search/internal/api/handlers.go]
	h.logger.Info("路由 GET /search 已注册到 SearchHandler.SearchPosts") // [cite: post_search/internal/api/handlers.go]

	// 新增：注册获取热门搜索词接口
	rg.GET("/hot-terms", h.GetHotSearchTerms)
	h.logger.Info("路由 GET /hot-terms 已注册到 SearchHandler.GetHotSearchTerms")

	// 注册健康检查接口
	rg.GET("/_health", h.HealthCheck)                               // [cite: post_search/internal/api/handlers.go]
	h.logger.Info("路由 GET /_health 已注册到 SearchHandler.HealthCheck") // [cite: post_search/internal/api/handlers.go]

	h.logger.Info("SearchHandler 的所有路由已注册完成。") // [cite: post_search/internal/api/handlers.go]
}
