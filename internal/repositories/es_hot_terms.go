// FileName: repositories/es_hot_terms_repository.go
package repositories

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io" // 确保导入 io 包
	// "strconv" // strconv 不再直接在此文件中使用
	// "strings" // strings 不再直接在此文件中使用
	"time"

	"github.com/Xushengqwer/go-common/core"
	"github.com/Xushengqwer/post_search/internal/models"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"go.uber.org/zap"
)

// HotSearchTermRepository 定义了与热门搜索词统计数据在 Elasticsearch 中交互的操作接口。
type HotSearchTermRepository interface {
	IncrementSearchTermCount(ctx context.Context, term string) error
	GetHotSearchTerms(ctx context.Context, limit int) ([]models.HotSearchTerm, error)
}

// esHotSearchTermRepository 是 HotSearchTermRepository 接口针对 Elasticsearch 的具体实现。
type esHotSearchTermRepository struct {
	client    *elasticsearch.Client // 注入的 Elasticsearch Go 客户端实例。
	logger    *core.ZapLogger       // 注入的 Logger 实例，用于结构化日志记录。
	indexName string                // 新增：此仓库操作的目标 Elasticsearch 索引名称。
}

// NewESHotSearchTermRepository 创建一个新的 esHotSearchTermRepository 实例。
// 参数:
//   - client: 一个初始化完成且可用的 *elasticsearch.Client 实例。
//   - logger: 一个 *core.ZapLogger 实例，用于日志记录。
//   - indexName: 此仓库将要操作的 Elasticsearch 索引的名称。
//
// 返回值:
//   - HotSearchTermRepository: 返回一个符合 HotSearchTermRepository 接口的 esHotSearchTermRepository 实例。
func NewESHotSearchTermRepository(client *elasticsearch.Client, logger *core.ZapLogger, indexName string) HotSearchTermRepository {
	if logger == nil {
		panic("创建 esHotSearchTermRepository 失败：Logger 实例不能为 nil")
	}
	if client == nil {
		logger.Fatal("创建 esHotSearchTermRepository 失败：Elasticsearch 客户端实例 (client) 不能为 nil。")
	}
	if indexName == "" { // 新增：检查 indexName 是否为空
		logger.Fatal("创建 esHotSearchTermRepository 失败：热门搜索词索引名称 (indexName) 不能为空。")
	}
	logger.Info("Elasticsearch HotSearchTermRepository 初始化成功",
		zap.String("target_index_for_hot_terms", indexName), // 使用传入的 indexName
	)
	return &esHotSearchTermRepository{
		client:    client,
		logger:    logger,
		indexName: indexName, // 存储传入的 indexName
	}
}

// logAndWrapESErrorForHotTerms 是一个针对热门搜索词仓库的辅助函数
// ... (这个函数保持不变，它内部不直接使用索引名) ...
func (repo *esHotSearchTermRepository) logAndWrapESErrorForHotTerms(res *esapi.Response, operationDesc string, contextIdentifier interface{}) error {
	var errorBodyContent string
	if res.Body != nil {
		bodyBytes, err := io.ReadAll(res.Body) // io.ReadAll 需要导入 "io"
		if err == nil {
			errorBodyContent = string(bodyBytes)
		}
	}

	logFields := []zap.Field{
		zap.Any("context_identifier", contextIdentifier),
		zap.String("es_status", res.Status()),
	}
	if errorBodyContent != "" {
		logFields = append(logFields, zap.String("es_error_response_body", errorBodyContent))
	}

	repo.logger.Error(fmt.Sprintf("Elasticsearch 热门搜索词操作 '%s' 失败", operationDesc), logFields...)

	if errorBodyContent != "" {
		return fmt.Errorf("Elasticsearch 热门搜索词操作 '%s' 失败，状态码: %s，响应: %s", operationDesc, res.Status(), errorBodyContent)
	}
	return fmt.Errorf("Elasticsearch 热门搜索词操作 '%s' 失败，状态码: %s", operationDesc, res.Status())
}

// IncrementSearchTermCount 递增给定搜索词在 Elasticsearch 中的计数。
func (repo *esHotSearchTermRepository) IncrementSearchTermCount(ctx context.Context, term string) error {
	docID := term

	scriptSource := "ctx._source.count += params.count_val; ctx._source.last_searched_at = params.now; ctx._source.term = params.term_val;"
	scriptParams := map[string]interface{}{
		"count_val": 1,
		"now":       time.Now().UTC(),
		"term_val":  term,
	}
	upsertDoc := models.HotSearchTermES{
		Term:           term,
		Count:          1,
		LastSearchedAt: time.Now().UTC(),
	}
	updateBody := map[string]interface{}{
		"script": map[string]interface{}{
			"source": scriptSource,
			"lang":   "painless",
			"params": scriptParams,
		},
		"upsert": upsertDoc,
	}

	payload, err := json.Marshal(updateBody)
	if err != nil {
		repo.logger.Error("序列化热门搜索词更新请求体失败", zap.String("term", term), zap.Error(err))
		return fmt.Errorf("序列化热门搜索词更新请求体 (term: %s) 失败: %w", term, err)
	}
	repo.logger.Debug("准备更新的热门搜索词请求体", zap.String("term", term), zap.ByteString("payload", payload))

	req := esapi.UpdateRequest{
		Index:      repo.indexName, // 使用结构体中的 indexName
		DocumentID: docID,
		Body:       bytes.NewReader(payload),
		Refresh:    "false",
	}

	res, err := req.Do(ctx, repo.client)
	if err != nil {
		repo.logger.Error("执行 Elasticsearch 热门搜索词更新请求时发生连接或客户端错误", zap.String("term", term), zap.Error(err))
		return fmt.Errorf("Elasticsearch 热门搜索词更新请求 (term: %s) 失败: %w", term, err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return repo.logAndWrapESErrorForHotTerms(res, "更新热门搜索词计数", term)
	}

	repo.logger.Debug("成功发送热门搜索词计数更新请求到 Elasticsearch", zap.String("term", term), zap.String("es_status", res.Status()))
	return nil
}

// GetHotSearchTerms 从 Elasticsearch 中检索最热门的 N 个搜索词。
func (repo *esHotSearchTermRepository) GetHotSearchTerms(ctx context.Context, limit int) ([]models.HotSearchTerm, error) {
	if limit <= 0 {
		limit = 10
	}
	repo.logger.Info("准备从 Elasticsearch 检索热门搜索词", zap.Int("limit", limit), zap.String("index_name", repo.indexName))

	query := map[string]interface{}{
		"size": limit,
		"sort": []map[string]interface{}{
			{"count": map[string]string{"order": "desc"}},
		},
	}

	queryJSON, err := json.Marshal(query)
	if err != nil {
		repo.logger.Error("序列化热门搜索词查询 DSL 失败", zap.Error(err))
		return nil, fmt.Errorf("序列化热门搜索词查询 DSL 失败: %w", err)
	}
	repo.logger.Debug("构建的热门搜索词查询 DSL", zap.String("dsl_query", string(queryJSON)))

	searchReq := esapi.SearchRequest{
		Index: []string{repo.indexName}, // 使用结构体中的 indexName
		Body:  bytes.NewReader(queryJSON),
	}

	res, err := searchReq.Do(ctx, repo.client)
	if err != nil {
		repo.logger.Error("执行 Elasticsearch 热门搜索词搜索请求时发生连接或客户端错误", zap.Error(err))
		return nil, fmt.Errorf("Elasticsearch 热门搜索词搜索请求失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, repo.logAndWrapESErrorForHotTerms(res, "检索热门搜索词", fmt.Sprintf("limit: %d on index %s", limit, repo.indexName))
	}

	var esResponse struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				Source models.HotSearchTermES `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(res.Body).Decode(&esResponse); err != nil {
		repo.logger.Error("解码 Elasticsearch 热门搜索词响应体失败", zap.Error(err))
		return nil, fmt.Errorf("解码 Elasticsearch 热门搜索词响应失败: %w", err)
	}

	hotTermsAPI := make([]models.HotSearchTerm, 0, len(esResponse.Hits.Hits))
	for _, hit := range esResponse.Hits.Hits {
		hotTermsAPI = append(hotTermsAPI, models.HotSearchTerm{
			Term:  hit.Source.Term,
			Count: hit.Source.Count,
		})
	}

	repo.logger.Info("成功从 Elasticsearch 检索热门搜索词",
		zap.Int("retrieved_count", len(hotTermsAPI)),
		zap.Int64("total_stats_docs_in_es", esResponse.Hits.Total.Value),
		zap.String("index_name", repo.indexName),
	)

	return hotTermsAPI, nil
}
