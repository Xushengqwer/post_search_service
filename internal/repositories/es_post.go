// FileName: repositories/post_repository.go
package repositories

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Xushengqwer/go-common/core"
	"github.com/Xushengqwer/post_search/internal/models" // 确保 EsPostDocument, SearchResult 等模型定义在此

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"go.uber.org/zap"
)

// PostRepository 定义了与帖子数据在 Elasticsearch 中持久化和检索相关的操作接口。
// 这种接口化设计使得业务逻辑层可以解耦具体的存储实现。
type PostRepository interface {
	// IndexPost 索引（创建或更新）一个帖子文档到 Elasticsearch。
	// 如果具有相同 ID 的文档已存在，则会更新它；否则，创建新文档。
	IndexPost(ctx context.Context, doc models.EsPostDocument) error

	// DeletePost 根据帖子 ID 从 Elasticsearch 中删除一个帖子文档。
	// 如果文档不存在，此操作应被视为幂等成功。
	DeletePost(ctx context.Context, postID uint64) error

	// SearchPosts 根据提供的搜索请求在 Elasticsearch 中执行搜索查询。
	SearchPosts(ctx context.Context, req models.SearchRequest) (*models.SearchResult, error)
}

// esPostRepository 是 PostRepository 接口针对 Elasticsearch 的具体实现。
type esPostRepository struct {
	client    *elasticsearch.Client // 注入的 Elasticsearch Go 客户端实例。
	indexName string                // 此仓库操作的目标 Elasticsearch 索引名称。
	logger    *core.ZapLogger       // 注入的 Logger 实例，用于结构化日志记录。
}

// NewESPostRepository 创建一个新的 esPostRepository 实例。
// 参数:
//   - client: 一个初始化完成且可用的 *elasticsearch.Client 实例。
//   - indexName: 将要操作的 Elasticsearch 索引的名称。不能为空。
//   - logger: 一个 *core.ZapLogger 实例，用于日志记录。
//
// 返回值:
//   - PostRepository: 返回一个符合 PostRepository 接口的 esPostRepository 实例。
//
// 注意：此构造函数在关键依赖缺失时会 panic，因为仓库无法在缺少这些依赖的情况下正常工作。
// 这是一种快速失败的策略，确保服务不会以不完整状态启动。
func NewESPostRepository(client *elasticsearch.Client, indexName string, logger *core.ZapLogger) PostRepository {
	if logger == nil {
		// Logger 是最基础的依赖，如果它缺失，后续的任何操作和错误都无法被有效记录。
		panic("创建 esPostRepository 失败：Logger 实例不能为 nil")
	}
	if client == nil {
		// 如果 Elasticsearch 客户端未提供，仓库将无法与 Elasticsearch 通信。
		logger.Fatal("创建 esPostRepository 失败：Elasticsearch 客户端实例 (client) 不能为 nil。服务将无法执行任何数据库操作。")
		// logger.Fatal 通常会导致程序退出。如果希望更灵活地处理，可以考虑返回 error。
	}
	if indexName == "" {
		// 索引名称是必需的，因为它告诉仓库应该在哪个索引上执行操作。
		logger.Fatal("创建 esPostRepository 失败：Elasticsearch 索引名称 (indexName) 不能为空。无法确定操作的目标索引。")
	}

	logger.Info("Elasticsearch PostRepository 初始化成功",
		zap.String("index_name", indexName),
	)
	return &esPostRepository{
		client:    client,
		indexName: indexName,
		logger:    logger,
	}
}

// logAndWrapESError 是一个辅助函数，用于处理和记录 Elasticsearch API 响应中的错误。
// 它会尝试读取响应体，记录详细的错误信息（包括状态码和响应体），并返回一个包装后的、统一格式的错误。
// 参数:
//   - res: *esapi.Response 对象，通常在其 IsError() 方法返回 true 时调用此函数。
//   - operationDesc: 描述当前正在执行的操作的字符串，例如 "索引文档"、"删除文档"、"搜索文档"。
//   - contextIdentifier: 用于日志记录的上下文标识符，例如文档ID、查询关键词等。
//
// 返回值:
//   - error: 一个格式化的错误，包含了原始状态码和可能的响应体信息。
func (repo *esPostRepository) logAndWrapESError(res *esapi.Response, operationDesc string, contextIdentifier interface{}) error {
	var errBody strings.Builder
	var readErr error
	// 即使 res.Body 为 nil (不太可能在 IsError() 为 true 时发生)，或者 io.Copy 出错，
	// 仍然尝试记录状态码等基本信息。
	if res.Body != nil {
		_, readErr = io.Copy(&errBody, res.Body)
	}

	logFields := []zap.Field{
		zap.Any("context_identifier", contextIdentifier), // 使用更通用的字段名，可以是文档ID或查询词
		zap.String("es_status", res.Status()),
	}

	responseBodyStr := errBody.String()
	if readErr != nil {
		// 如果读取响应体本身就出错了，记录这个读取错误。
		logFields = append(logFields, zap.Error(fmt.Errorf("读取 Elasticsearch 错误响应体失败: %w", readErr)))
	} else if responseBodyStr != "" {
		// 如果成功读取到响应体内容，将其加入日志。
		logFields = append(logFields, zap.String("es_error_response_body", responseBodyStr))
	}

	// 记录统一格式的错误日志。
	repo.logger.Error(fmt.Sprintf("Elasticsearch 操作 '%s' 失败", operationDesc), logFields...)

	// 返回给调用者的错误信息。
	if responseBodyStr != "" {
		return fmt.Errorf("Elasticsearch 操作 '%s' 失败，状态码: %s，响应: %s", operationDesc, res.Status(), responseBodyStr)
	}
	return fmt.Errorf("Elasticsearch 操作 '%s' 失败，状态码: %s", operationDesc, res.Status())
}

// IndexPost 在 Elasticsearch 中索引（创建或更新）一个帖子文档。
// 它使用文档的 ID 作为 Elasticsearch 文档的 _id，从而实现幂等性：
// 如果具有相同 ID 的文档已存在，则会更新它；否则，会创建新文档。
func (repo *esPostRepository) IndexPost(ctx context.Context, doc models.EsPostDocument) error {
	// 为什么在这里设置 UpdatedAt?
	// 确保每次索引操作（无论是创建还是更新）都会刷新文档的最后更新时间戳。
	// 这有助于追踪文档的最新状态，并可用于排序或过滤。使用 UTC 时间是最佳实践，以避免时区问题。
	doc.UpdatedAt = time.Now().UTC()
	docID := strconv.FormatUint(doc.ID, 10) // Elasticsearch 的 DocumentID 通常是字符串类型。

	// 将 Go 结构体（文档）序列化为 JSON 字节流，以便作为请求体发送给 Elasticsearch。
	payload, err := json.Marshal(doc)
	if err != nil {
		repo.logger.Error("序列化 EsPostDocument 为 JSON 失败，无法发送给 Elasticsearch",
			zap.Uint64("post_id", doc.ID),
			zap.Error(err), // 记录具体的序列化错误
		)
		// 这是一个应用程序内部的错误，通常表明模型定义或数据有问题。
		return fmt.Errorf("序列化帖子文档 (ID: %d) 失败: %w", doc.ID, err)
	}
	repo.logger.Debug("准备索引的文档JSON体", zap.String("document_id", docID), zap.ByteString("payload", payload))

	// 构建 Elasticsearch 的 IndexRequest。
	req := esapi.IndexRequest{
		Index:      repo.indexName,           // 指定目标索引。
		DocumentID: docID,                    // 指定文档 ID，实现创建或更新 (upsert) 行为。
		Body:       bytes.NewReader(payload), // 请求体包含序列化后的文档数据。
		Refresh:    "false",                  // "false" (默认): 异步刷新。写入操作会先写入内存缓冲区和事务日志，然后才刷新到磁盘段，使其可搜索。
		// 这种方式写入性能较高，但新写入或更新的数据在短时间内（通常1秒，可配置）可能对搜索不可见。
		// "true": 立即刷新相关的分片，使更改立即可见。这会显著影响写入性能，通常仅用于测试或特定低吞吐量场景。
		// "wait_for": 请求会等待刷新发生后再返回，是 "true" 的一种折衷，确保数据可见但仍有性能开销。
		// 对于高吞吐量的索引场景（如 Kafka 消费），"false" 通常是首选。
	}

	// 执行 Elasticsearch 索引请求。
	res, err := req.Do(ctx, repo.client)
	if err != nil {
		// 此处的错误通常表示网络问题、Elasticsearch 服务不可达或客户端配置错误。
		repo.logger.Error("执行 Elasticsearch 索引请求时发生连接或客户端错误",
			zap.Uint64("post_id", doc.ID),
			zap.Error(err),
		)
		return fmt.Errorf("Elasticsearch 索引请求 (ID: %d) 失败: %w", doc.ID, err)
	}
	defer res.Body.Close() // 关键：确保在函数结束时关闭响应体，以释放网络连接和资源。

	// 检查 Elasticsearch 是否返回了错误状态码（例如 4xx, 5xx 系列）。
	if res.IsError() {
		return repo.logAndWrapESError(res, "索引文档", docID)
	}

	// 操作成功，记录 INFO 级别日志。
	// 将解析具体操作结果（如 "created", "updated"）的日志调整为 DEBUG 级别，
	// 因为在生产环境中，INFO 级别通常不需要这么详细的信息，但 DEBUG 时非常有用。
	repo.logger.Info("成功发送索引/更新请求到 Elasticsearch",
		zap.Uint64("post_id", doc.ID),
		zap.String("es_status", res.Status()), // HTTP 状态码，例如 "200 OK" 或 "201 Created"
	)

	// （可选）解析成功响应以获取更详细的操作结果 (created, updated, noop)。
	var resultDetails map[string]interface{}
	// 注意：由于上面的 Info 日志已经记录了成功，这里的解码和日志记录主要用于更细致的调试或审计。
	// 如果解码失败，不应将其视为整体操作的失败，因为 HTTP 状态码已表明成功。
	if err := json.NewDecoder(res.Body).Decode(&resultDetails); err == nil {
		if esResult, ok := resultDetails["result"].(string); ok {
			repo.logger.Debug("Elasticsearch 索引/更新操作的详细结果",
				zap.Uint64("post_id", doc.ID),
				zap.String("es_operation_result", esResult), // 例如 "created", "updated", "noop"
			)
		} else {
			repo.logger.Debug("成功索引/更新 Elasticsearch 文档，但无法从响应中解析具体的操作结果字段 'result'。",
				zap.Uint64("post_id", doc.ID),
				zap.Any("response_details", resultDetails), // 记录解析出的完整 map (如果不大)
			)
		}
	} else {
		repo.logger.Debug("成功索引/更新 Elasticsearch 文档，但解码响应体以获取详细结果时失败。",
			zap.Uint64("post_id", doc.ID),
			zap.Error(err), // 记录解码错误
		)
	}
	return nil
}

// DeletePost 根据文档 ID 从 Elasticsearch 中删除一个帖子文档。
// 此操作是幂等的：如果目标文档本就不存在 (Elasticsearch 返回 404 Not Found)，
// 则视为操作成功，因为“文档不存在”这个目标状态已经达成。
func (repo *esPostRepository) DeletePost(ctx context.Context, postID uint64) error {
	docID := strconv.FormatUint(postID, 10)
	repo.logger.Info("准备从 Elasticsearch 删除文档", zap.String("document_id", docID))

	req := esapi.DeleteRequest{
		Index:      repo.indexName,
		DocumentID: docID,
		Refresh:    "false", // 与 IndexPost 的 Refresh 参数含义类似。
	}

	res, err := req.Do(ctx, repo.client)
	if err != nil {
		repo.logger.Error("执行 Elasticsearch 删除请求时发生连接或客户端错误",
			zap.Uint64("post_id", postID),
			zap.Error(err),
		)
		return fmt.Errorf("Elasticsearch 删除请求 (ID: %d) 失败: %w", postID, err)
	}
	defer res.Body.Close()

	// 为什么特殊处理 404 (Not Found)?
	// 对于删除操作，如果目标文档本就不存在，那么“删除”这个动作的目标（确保文档不存在）实际上已经达成了。
	// 因此，将 404 视为成功可以使删除操作幂等，多次调用删除同一个不存在的ID不会产生错误，也不会阻塞流程。
	if res.StatusCode == 404 {
		repo.logger.Warn("尝试删除的文档在 Elasticsearch 中未找到，视为操作成功 (幂等性)",
			zap.Uint64("post_id", postID),
			zap.String("es_status", res.Status()), // 记录 "404 Not Found"
		)
		return nil // 文档不存在，删除操作的目标已达成，返回 nil 表示成功。
	}

	// 对于其他非 404 的错误状态码。
	if res.IsError() {
		return repo.logAndWrapESError(res, "删除文档", docID)
	}

	// 操作成功，记录 INFO 级别日志。
	repo.logger.Info("成功发送删除请求到 Elasticsearch (或文档本不存在)",
		zap.Uint64("post_id", postID),
		zap.String("es_status", res.Status()), // 例如 "200 OK"
	)

	// （可选）解析成功删除的响应，获取详细结果，日志级别为 DEBUG。
	var resultDetails map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&resultDetails); err == nil {
		if esResult, ok := resultDetails["result"].(string); ok && esResult == "deleted" {
			repo.logger.Debug("Elasticsearch 文档删除操作的详细结果",
				zap.Uint64("post_id", postID),
				zap.String("es_operation_result", esResult),
			)
		} else if ok && esResult == "not_found" { // 有时即使HTTP 200，结果也可能是 not_found
			repo.logger.Debug("Elasticsearch 文档删除操作结果为 'not_found' (HTTP 200)",
				zap.Uint64("post_id", postID),
				zap.String("es_operation_result", esResult),
			)
		} else {
			repo.logger.Debug("Elasticsearch 文档删除请求成功，但响应中的 'result' 字段非预期或无法解析",
				zap.Uint64("post_id", postID),
				zap.Any("response_details", resultDetails),
			)
		}
	} else {
		repo.logger.Debug("Elasticsearch 文档删除请求成功，但解码响应体以获取详细结果时失败。",
			zap.Uint64("post_id", postID),
			zap.Error(err),
		)
	}
	return nil
}

// SearchPosts 根据提供的搜索请求在 Elasticsearch 索引中执行查询。
// 此方法现在会尝试解析高亮结果。
func (repo *esPostRepository) SearchPosts(ctx context.Context, req models.SearchRequest) (*models.SearchResult, error) {
	repo.logger.Info("开始执行 Elasticsearch 搜索 (包含高亮请求)", // 日志更新
		zap.String("query_keywords", req.Query),
		zap.Int("page", req.Page),
		zap.Int("size", req.Size),
		zap.String("sort_by", req.SortBy),
		zap.String("sort_order", req.SortOrder),
		zap.String("filter_author_id", req.AuthorID),
		zap.Any("filter_status", req.Status),
	)

	queryJSON, err := buildSearchQuery(req) // buildSearchQuery 现在会加入 highlight 部分
	if err != nil {
		repo.logger.Error("构建 Elasticsearch 搜索查询 DSL 失败", zap.Any("search_request_params", req), zap.Error(err))
		return nil, fmt.Errorf("构建搜索查询失败: %w", err)
	}
	repo.logger.Debug("构建的 Elasticsearch 查询 DSL (含高亮)", zap.String("dsl_query", string(queryJSON))) // 日志更新

	searchReq := esapi.SearchRequest{
		Index:          []string{repo.indexName},
		Body:           bytes.NewReader(queryJSON),
		TrackTotalHits: true,
	}

	res, err := searchReq.Do(ctx, repo.client)
	if err != nil {
		repo.logger.Error("执行 Elasticsearch 搜索请求时发生连接或客户端错误", zap.String("query_keywords", req.Query), zap.Error(err))
		return nil, fmt.Errorf("Elasticsearch 搜索请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, repo.logAndWrapESError(res, "搜索文档", req.Query)
	}

	// 3. 解析成功的响应
	// 更新临时的匿名结构体以包含 highlight 字段
	var esResponse struct {
		Took int `json:"took"`
		Hits struct {
			Total struct {
				Value    int64  `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				Source    models.EsPostDocument `json:"_source"`             // 文档的实际内容
				Score     float64               `json:"_score,omitempty"`    // 文档的相关性评分 (可选)
				Highlight map[string][]string   `json:"highlight,omitempty"` // 新增：用于接收高亮结果
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&esResponse); err != nil {
		repo.logger.Error("解码 Elasticsearch 搜索响应体失败", zap.String("query_keywords", req.Query), zap.Error(err))
		return nil, fmt.Errorf("解码 Elasticsearch 搜索响应失败: %w", err)
	}

	// 4. 映射到应用程序的结果模型 (models.SearchResult)
	searchResult := &models.SearchResult{
		Hits:  make([]models.EsPostDocument, 0, len(esResponse.Hits.Hits)),
		Total: esResponse.Hits.Total.Value,
		Page:  req.Page,
		Size:  req.Size,
		Took:  int64(esResponse.Took),
	}

	for _, hit := range esResponse.Hits.Hits {
		doc := hit.Source // 从 _source 获取文档主体
		// 新增：如果存在高亮结果，则将其赋值给文档的 Highlights 字段
		if hit.Highlight != nil && len(hit.Highlight) > 0 {
			doc.Highlights = hit.Highlight
			repo.logger.Debug("为文档附加了高亮片段", zap.Uint64("doc_id", doc.ID), zap.Any("highlights", doc.Highlights))
		}
		searchResult.Hits = append(searchResult.Hits, doc)
	}

	repo.logger.Info("Elasticsearch 搜索成功完成 (含高亮处理)", // 日志更新
		zap.Int64("query_took_ms", searchResult.Took),
		zap.Int64("total_hits_found", searchResult.Total),
		zap.Int("returned_hits_count", len(searchResult.Hits)),
		zap.String("total_hits_relation", esResponse.Hits.Total.Relation),
		zap.Int("requested_page", req.Page),
		zap.Int("requested_size", req.Size),
		zap.String("query_keywords", req.Query),
	)

	return searchResult, nil
}
