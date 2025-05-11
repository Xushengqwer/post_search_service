package service

import (
	"context"
	"fmt"
	"github.com/Xushengqwer/go-common/core" // 确保这是你项目中 core 包的正确路径

	"github.com/Xushengqwer/post_search/internal/models"       // 确保 models 包路径正确
	"github.com/Xushengqwer/post_search/internal/repositories" // 确保 repositories 包路径正确

	"go.uber.org/zap"
)

// SearchService 封装了与帖子搜索相关的业务逻辑。
// 它作为 API 处理层（例如 HTTP Handler）和数据仓库层 (Repository) 之间的中介，
// 负责协调搜索请求的处理、调用数据访问操作，并可能执行一些业务规则或数据转换。
type SearchService struct {
	postRepo repositories.PostRepository // PostRepository 接口的实例，用于与 Elasticsearch 交互。
	logger   *core.ZapLogger             // ZapLogger 实例，用于结构化日志记录。
}

// NewSearchService 创建 SearchService 的一个新实例。
// 参数:
//   - postRepo: 一个已经初始化并准备好的 PostRepository 实例。这是 SearchService 的核心依赖。
//   - logger: 一个注入的 Logger 实例，用于服务内部的日志记录。
//
// 返回值:
//   - *SearchService: 成功创建的 SearchService 实例。
//
// 注意：此构造函数在关键依赖项 (logger, postRepo) 为 nil 时会引发 panic 或调用 logger.Fatal。
// 这是因为服务在缺少这些核心组件的情况下无法正常运行，采用快速失败策略可以防止服务以不一致或损坏的状态启动。
func NewSearchService(postRepo repositories.PostRepository, logger *core.ZapLogger) *SearchService {
	// 为什么检查 logger 是否为 nil?
	// Logger 是服务运行的基础，如果缺失，所有日志（包括错误日志）都无法记录，这将使得问题排查极为困难。
	// 因此，这是一个启动时的致命条件。
	if logger == nil {
		panic("创建 SearchService 失败：Logger 实例不能为 nil。")
	}

	// 为什么检查 postRepo 是否为 nil?
	// PostRepository 是执行实际数据搜索的核心依赖。如果它为 nil，SearchService 将无法执行其主要功能。
	// 使用 logger.Fatal 会记录错误并终止程序，这在启动阶段是合理的。
	if postRepo == nil {
		logger.Fatal("创建 SearchService 失败：PostRepository 实例不能为 nil。服务将无法执行搜索操作。")
		// logger.Fatal 通常会调用 os.Exit(1)，如果希望更细致地控制退出流程或返回错误，
		// 可以考虑改为 logger.Error(...) 后 panic，或让 NewSearchService 返回 (PostRepository, error)。
		// 但对于应用主流程中的服务初始化，Fatal/panic 是可接受的。
	}

	logger.Info("SearchService 初始化成功。")
	return &SearchService{
		postRepo: postRepo,
		logger:   logger,
	}
}

// Search 根据提供的请求条件执行帖子搜索操作。
// 它会记录请求参数，调用底层的 PostRepository 执行搜索，然后记录并返回结果。
// 参数:
//   - ctx: Context 对象，用于控制搜索操作的生命周期，例如设置超时或允许外部取消。
//   - req: models.SearchRequest 结构体，包含了用户输入的搜索关键词、分页参数（页码、每页数量）以及排序条件。
//
// 返回值:
//   - *models.SearchResult: 指向 models.SearchResult 结构体的指针，包含了搜索命中的帖子列表、总命中数以及分页信息。
//   - error: 如果在搜索过程中发生任何错误（例如，与 Elasticsearch 通信失败、查询构建错误等），则返回具体的错误信息。
func (s *SearchService) Search(ctx context.Context, req models.SearchRequest) (*models.SearchResult, error) {
	// 记录接收到搜索请求的详细信息，这对于追踪用户行为和调试非常有价值。
	// 使用结构化日志字段，方便后续的日志分析和查询。
	logFields := []zap.Field{
		zap.String("搜索关键词", req.Query),
		zap.Int("请求页码", req.Page),
		zap.Int("每页数量", req.Size),
		zap.String("排序字段", req.SortBy),
		zap.String("排序顺序", req.SortOrder),
	}
	// 如果 SearchRequest 中有过滤器字段，也应该在这里记录它们的值。
	if req.AuthorID != "" {
		logFields = append(logFields, zap.String("筛选_作者ID", req.AuthorID))
	}
	if req.Status != nil {
		logFields = append(logFields, zap.Any("筛选_状态", *req.Status)) // 解引用指针
	}
	s.logger.Info("正在处理帖子搜索请求", logFields...)

	// --- 调用 Repository 层执行搜索 ---
	// 将实际的搜索操作委托给 PostRepository。
	// Service 层不直接关心数据如何存储或检索，只关心业务流程和规则。
	searchResult, err := s.postRepo.SearchPosts(ctx, req)
	if err != nil {
		// 如果 Repository 层返回错误，记录详细的错误信息。
		s.logger.Error("调用 PostRepository 执行搜索操作时发生错误",
			zap.Error(err), // 记录原始错误以获取完整的错误上下文
			// 再次记录关键请求参数，便于关联错误日志和请求
			zap.String("搜索关键词_OnError", req.Query),
			zap.Int("请求页码_OnError", req.Page),
		)
		// 向上层（例如 API Handler）返回一个包装后的错误。
		// 使用 %w 包装错误，可以保留原始错误的类型和信息，允许上层进行更细致的错误处理。
		return nil, fmt.Errorf("执行搜索操作失败: %w", err)
	}

	// --- （可选）后期处理或业务逻辑 ---
	// 在这里可以对从 Repository 获取到的 searchResult 进行进一步的业务处理，例如：
	// 1. 数据转换：如果 API 响应的格式与 Repository 返回的格式不同。
	// 2. 数据丰富：例如，根据结果中的用户ID去调用用户服务获取更详细的用户信息。
	// 3. 权限过滤：根据当前用户的权限，过滤掉其不应看到的结果。
	// 4. 业务规则应用：例如，如果某些帖子是“推荐”的，可能需要在这里调整它们的排序或添加标记。
	// 如果当前没有这类需求，直接返回 searchResult 是完全正确的。

	// 记录搜索成功的日志，并包含一些关键的统计信息。
	s.logger.Info("帖子搜索成功完成",
		zap.Int64("总命中数", searchResult.Total),
		zap.Int("返回结果数", len(searchResult.Hits)),
		zap.Int("当前页码", searchResult.Page),
		zap.Int("每页数量", searchResult.Size),
		zap.Int64("查询耗时_ms", searchResult.Took), // 记录查询耗时
	)

	// 返回从 Repository 获取到的（可能经过处理的）搜索结果。
	return searchResult, nil
}
