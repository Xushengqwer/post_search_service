package service

import (
	"context"
	"fmt"
	"strings" // 导入 strings 包用于规范化查询

	"github.com/Xushengqwer/go-common/core" // 确保这是你项目中 core 包的正确路径

	"github.com/Xushengqwer/post_search/internal/models"       // 确保 models 包路径正确
	"github.com/Xushengqwer/post_search/internal/repositories" // 确保 repositories 包路径正确

	"go.uber.org/zap"
)

// SearchService 封装了与帖子搜索相关的业务逻辑。
// 它作为 API 处理层（例如 HTTP Handler）和数据仓库层 (Repository) 之间的中介，
// 负责协调搜索请求的处理、调用数据访问操作，并可能执行一些业务规则或数据转换。
type SearchService struct {
	postRepo          repositories.PostRepository          // PostRepository 接口的实例，用于与 Elasticsearch 交互帖子数据。
	hotSearchTermRepo repositories.HotSearchTermRepository // 新增：HotSearchTermRepository 接口的实例，用于热门搜索词统计。
	logger            *core.ZapLogger                      // ZapLogger 实例，用于结构化日志记录。
}

// NewSearchService 创建 SearchService 的一个新实例。
// 参数:
//   - postRepo: 一个已经初始化并准备好的 PostRepository 实例。
//   - hotSearchTermRepo: 一个已经初始化并准备好的 HotSearchTermRepository 实例。
//   - logger: 一个注入的 Logger 实例，用于服务内部的日志记录。
//
// 返回值:
//   - *SearchService: 成功创建的 SearchService 实例。
func NewSearchService(
	postRepo repositories.PostRepository,
	hotSearchTermRepo repositories.HotSearchTermRepository, // 新增参数
	logger *core.ZapLogger,
) *SearchService {
	if logger == nil {
		panic("创建 SearchService 失败：Logger 实例不能为 nil。")
	}
	if postRepo == nil {
		logger.Fatal("创建 SearchService 失败：PostRepository 实例不能为 nil。服务将无法执行帖子搜索操作。")
	}
	if hotSearchTermRepo == nil { // 新增依赖检查
		logger.Fatal("创建 SearchService 失败：HotSearchTermRepository 实例不能为 nil。服务将无法处理热门搜索词功能。")
	}

	logger.Info("SearchService 初始化成功 (包含热门搜索词支持)。")
	return &SearchService{
		postRepo:          postRepo,
		hotSearchTermRepo: hotSearchTermRepo, // 初始化新字段
		logger:            logger,
	}
}

// Search 根据提供的请求条件执行帖子搜索操作。
// ... (您现有的 Search 方法保持不变，它只负责帖子搜索的核心逻辑) ...
func (s *SearchService) Search(ctx context.Context, req models.SearchRequest) (*models.SearchResult, error) { // [cite: post_search/internal/service/search_service.go]
	logFields := []zap.Field{
		zap.String("搜索关键词", req.Query),
		zap.Int("请求页码", req.Page),
		zap.Int("每页数量", req.Size),
		zap.String("排序字段", req.SortBy),
		zap.String("排序顺序", req.SortOrder),
	}
	if req.AuthorID != "" {
		logFields = append(logFields, zap.String("筛选_作者ID", req.AuthorID))
	}
	if req.Status != nil {
		logFields = append(logFields, zap.Any("筛选_状态", *req.Status))
	}
	s.logger.Info("正在处理帖子搜索请求", logFields...)

	searchResult, err := s.postRepo.SearchPosts(ctx, req)
	if err != nil {
		s.logger.Error("调用 PostRepository 执行搜索操作时发生错误",
			zap.Error(err),
			zap.String("搜索关键词_OnError", req.Query),
			zap.Int("请求页码_OnError", req.Page),
		)
		return nil, fmt.Errorf("执行搜索操作失败: %w", err)
	}

	s.logger.Info("帖子搜索成功完成",
		zap.Int64("总命中数", searchResult.Total),
		zap.Int("返回结果数", len(searchResult.Hits)),
		zap.Int("当前页码", searchResult.Page),
		zap.Int("每页数量", searchResult.Size),
		zap.Int64("查询耗时_ms", searchResult.Took),
	)

	return searchResult, nil
}

// --- 新增服务方法 ---

// LogSearchQuery 记录一个搜索查询，用于热门搜索词分析。
// 它会规范化查询字符串，然后调用 HotSearchTermRepository 来递增该词的计数。
func (s *SearchService) LogSearchQuery(ctx context.Context, query string) error {
	// 1. 规范化查询字符串
	//    - 转换为小写，以确保 "Go" 和 "go" 被视为同一个词。
	//    -去除首尾多余的空格。
	normalizedQuery := strings.TrimSpace(strings.ToLower(query))

	// 2. 验证规范化后的查询 (例如，不记录空字符串)
	if normalizedQuery == "" {
		s.logger.Debug("接收到空查询字符串，跳过热门搜索词记录。")
		return nil // 对于空查询，不执行任何操作，也不报错
	}

	// 3. 记录将要递增计数的词
	s.logger.Debug("准备记录并递增搜索词计数",
		zap.String("original_query", query),
		zap.String("normalized_query_to_log", normalizedQuery),
	)

	// 4. 调用 HotSearchTermRepository 的方法
	err := s.hotSearchTermRepo.IncrementSearchTermCount(ctx, normalizedQuery)
	if err != nil {
		s.logger.Error("调用 HotSearchTermRepository 递增搜索词计数失败",
			zap.String("normalized_query", normalizedQuery),
			zap.Error(err),
		)
		// 包装错误并返回。上层（例如API Handler）可以决定如何处理这个错误
		// (例如，是否因为记录失败而影响主搜索请求的成功状态)。
		// 通常，记录热门词失败不应阻塞主搜索流程。
		return fmt.Errorf("记录搜索词 '%s' 失败: %w", normalizedQuery, err)
	}

	s.logger.Debug("搜索词计数已成功请求递增", zap.String("normalized_query", normalizedQuery))
	return nil
}

// GetHotSearchTerms 从 HotSearchTermRepository 检索热门搜索词列表。
func (s *SearchService) GetHotSearchTerms(ctx context.Context, limit int) ([]models.HotSearchTerm, error) {
	s.logger.Info("服务层：正在请求获取热门搜索词列表", zap.Int("limit", limit))

	terms, err := s.hotSearchTermRepo.GetHotSearchTerms(ctx, limit)
	if err != nil {
		s.logger.Error("调用 HotSearchTermRepository 获取热门搜索词列表失败",
			zap.Int("limit", limit),
			zap.Error(err),
		)
		return nil, fmt.Errorf("获取热门搜索词列表失败 (limit: %d): %w", limit, err)
	}

	s.logger.Info("服务层：成功获取热门搜索词列表",
		zap.Int("retrieved_count", len(terms)),
		zap.Int("requested_limit", limit),
	)
	return terms, nil
}
