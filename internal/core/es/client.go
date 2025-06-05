package es

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Xushengqwer/go-common/core"
	"github.com/Xushengqwer/post_search/config" // 确保导入了更新后的 config 包

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"go.uber.org/zap"
)

// ESClient 包含初始化后的 Elasticsearch 客户端及相关信息
type ESClient struct {
	Client          *elasticsearch.Client
	PrimaryIndexCfg config.IndexSpecificConfig // 存储主索引的配置，方便其他地方引用（如果需要）
	// HotTermsIndexCfg config.IndexSpecificConfig // 热门搜索词索引的配置也可以在这里存储，或者直接在 main.go 中传递给其仓库
}

// getPostsIndexMapping 定义了主帖子索引的映射和设置。
// 参数:
//   - shards: 主分片数量。
//   - replicas: 每个主分片的副本数量。
func getPostsIndexMapping(shards int, replicas int) string {
	return fmt.Sprintf(`{
       "settings": {
          "number_of_shards": %d,
          "number_of_replicas": %d
       },
       "mappings": {
          "properties": {
             "id": { "type": "unsigned_long" },
             "title": { "type": "text", "analyzer": "ik_smart" },
             "content": { "type": "text", "analyzer": "ik_smart" },
             "author_id": { "type": "keyword" },
             "author_avatar": { "type": "keyword", "index": false },
             "author_username": {
                "type": "text",
                "analyzer": "standard",
                "fields": {
                   "keyword": { "type": "keyword", "ignore_above": 256 }
                }
             },
             "status": { "type": "integer" },
             "view_count": { "type": "long" },
             "official_tag": { "type": "integer" },
             "price_per_unit": { "type": "double" },
             "contact_qr_code": { "type": "keyword", "index": false },
             "updated_at": { "type": "date" }
          }
       }
    }`, shards, replicas)
}

// getHotSearchTermsIndexMapping 定义了热门搜索词索引的映射和设置。
// 参数:
//   - shards: 主分片数量。
//   - replicas: 每个主分片的副本数量。
func getHotSearchTermsIndexMapping(shards int, replicas int) string {
	return fmt.Sprintf(`{
        "settings": {
            "number_of_shards": %d,
            "number_of_replicas": %d
        },
        "mappings": {
            "properties": {
                "term": { "type": "keyword" },
                "count": { "type": "long" },
                "last_searched_at": { "type": "date" }
            }
        }
    }`, shards, replicas)
}

// createIndexIfNotExists 是一个辅助函数，用于检查索引是否存在，如果不存在则创建它。
func createIndexIfNotExists(
	ctx context.Context,
	esClient *elasticsearch.Client,
	indexCfg config.IndexSpecificConfig,
	mappingFunc func(shards, replicas int) string,
	logger *core.ZapLogger,
	indexLogicalName string, // 用于日志记录的逻辑名称，例如 "主帖子" 或 "热门搜索词"
) error {
	// 验证配置
	if indexCfg.Name == "" {
		logger.Error(fmt.Sprintf("未配置%s索引的名称 (indexCfg.Name 为空)", indexLogicalName))
		return fmt.Errorf("%s索引名称未在配置中指定", indexLogicalName)
	}
	if indexCfg.NumberOfShards <= 0 {
		logger.Error(fmt.Sprintf("%s索引的分片数配置无效，必须大于0", indexLogicalName),
			zap.String("index_name", indexCfg.Name),
			zap.Int("configured_shards", indexCfg.NumberOfShards),
		)
		return fmt.Errorf("%s索引 '%s' 配置的分片数无效: %d，必须大于0", indexLogicalName, indexCfg.Name, indexCfg.NumberOfShards)
	}
	if indexCfg.NumberOfReplicas < 0 {
		logger.Error(fmt.Sprintf("%s索引的副本数配置无效，必须大于或等于0", indexLogicalName),
			zap.String("index_name", indexCfg.Name),
			zap.Int("configured_replicas", indexCfg.NumberOfReplicas),
		)
		return fmt.Errorf("%s索引 '%s' 配置的副本数无效: %d，必须大于或等于0", indexLogicalName, indexCfg.Name, indexCfg.NumberOfReplicas)
	}

	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	defer checkCancel()

	existsRes, err := esClient.Indices.Exists(
		[]string{indexCfg.Name},
		esClient.Indices.Exists.WithContext(checkCtx),
	)
	if err != nil {
		logger.Error(fmt.Sprintf("检查%s索引是否存在时发生网络或请求错误", indexLogicalName),
			zap.String("index_name", indexCfg.Name), zap.Error(err))
		return fmt.Errorf("检查%s索引 '%s' 是否存在失败: %w", indexLogicalName, indexCfg.Name, err)
	}
	defer existsRes.Body.Close()

	if existsRes.StatusCode == 404 { // 索引不存在
		logger.Warn(fmt.Sprintf("%s索引不存在，将尝试创建...", indexLogicalName),
			zap.String("index_name", indexCfg.Name),
			zap.Int("shards", indexCfg.NumberOfShards),
			zap.Int("replicas", indexCfg.NumberOfReplicas),
		)

		mapping := mappingFunc(indexCfg.NumberOfShards, indexCfg.NumberOfReplicas)
		createCtx, createCancel := context.WithTimeout(ctx, 10*time.Second)
		defer createCancel()

		createReq := esapi.IndicesCreateRequest{
			Index: indexCfg.Name,
			Body:  strings.NewReader(mapping),
		}
		createRes, err := createReq.Do(createCtx, esClient)
		if err != nil {
			logger.Error(fmt.Sprintf("发送创建%s索引请求失败", indexLogicalName),
				zap.String("index_name", indexCfg.Name), zap.Error(err))
			return fmt.Errorf("发送创建%s索引 '%s' 请求失败: %w", indexLogicalName, indexCfg.Name, err)
		}
		defer createRes.Body.Close()

		if createRes.IsError() {
			var errorBody strings.Builder
			var parsedError map[string]interface{}
			bodyBytes, _ := io.ReadAll(createRes.Body)
			errorBody.Write(bodyBytes)
			if jsonErr := json.Unmarshal(bodyBytes, &parsedError); jsonErr == nil {
				logger.Error(fmt.Sprintf("创建%s索引失败", indexLogicalName),
					zap.String("index_name", indexCfg.Name),
					zap.String("status", createRes.Status()),
					zap.Any("es_error_details", parsedError),
				)
			} else {
				logger.Error(fmt.Sprintf("创建%s索引失败，且无法解析JSON错误响应", indexLogicalName),
					zap.String("index_name", indexCfg.Name),
					zap.String("status", createRes.Status()),
					zap.String("raw_response", errorBody.String()),
					zap.Error(jsonErr),
				)
			}
			return fmt.Errorf("创建%s索引 '%s' 失败, 状态码: %s, 响应: %s", indexLogicalName, indexCfg.Name, createRes.Status(), errorBody.String())
		}
		logger.Info(fmt.Sprintf("成功创建%s索引及映射", indexLogicalName),
			zap.String("index_name", indexCfg.Name),
			zap.Int("shards_created", indexCfg.NumberOfShards),
			zap.Int("replicas_created", indexCfg.NumberOfReplicas),
		)
	} else if existsRes.IsError() { // 检查索引请求返回了其他错误
		var errorBody strings.Builder
		if _, readErr := io.Copy(&errorBody, existsRes.Body); readErr != nil {
			logger.Error(fmt.Sprintf("检查%s索引存在性时出错，且无法读取错误响应体", indexLogicalName),
				zap.String("index_name", indexCfg.Name),
				zap.String("status", existsRes.Status()),
				zap.Error(readErr),
			)
		} else {
			logger.Error(fmt.Sprintf("检查%s索引存在性时出错", indexLogicalName),
				zap.String("index_name", indexCfg.Name),
				zap.String("status", existsRes.Status()),
				zap.String("response", errorBody.String()),
			)
		}
		return fmt.Errorf("检查%s索引 '%s' 存在性时出错: %s", indexLogicalName, indexCfg.Name, existsRes.Status())
	} else {
		logger.Info(fmt.Sprintf("%s索引已存在", indexLogicalName), zap.String("index_name", indexCfg.Name))
	}
	return nil
}

// NewESClient 初始化 Elasticsearch 客户端并执行基本检查（Ping 和索引存在性检查）。
// 如果配置的索引不存在，它会尝试创建它们。
func NewESClient(cfg config.ESConfig, logger *core.ZapLogger, transport http.RoundTripper) (*ESClient, error) {
	esClientCfg := elasticsearch.Config{ // 变量名修改以避免与参数 cfg 冲突
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
		Transport: transport,
	}

	esClient, err := elasticsearch.NewClient(esClientCfg)
	if err != nil {
		logger.Error("创建 Elasticsearch 客户端失败", zap.Error(err))
		return nil, fmt.Errorf("创建 Elasticsearch 客户端失败: %w", err)
	}
	logger.Info("Elasticsearch 客户端配置完成", zap.Strings("addresses", cfg.Addresses))

	// --- Ping 检查 ---
	ctxPing, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	pingRes, err := esClient.Ping(esClient.Ping.WithContext(ctxPing))
	if err != nil {
		logger.Error("Ping Elasticsearch 失败", zap.Error(err))
		return nil, fmt.Errorf("ping Elasticsearch 失败: %w", err)
	}
	defer pingRes.Body.Close()
	if pingRes.IsError() {
		var errorBody strings.Builder
		if _, readErr := io.Copy(&errorBody, pingRes.Body); readErr != nil {
			logger.Error("Elasticsearch Ping 不成功，且无法读取错误响应体", zap.String("status", pingRes.Status()), zap.Error(readErr))
		} else {
			logger.Error("Elasticsearch Ping 不成功", zap.String("status", pingRes.Status()), zap.String("response", errorBody.String()))
		}
		return nil, fmt.Errorf("elasticsearch Ping 不成功: %s", pingRes.Status())
	}
	logger.Info("Elasticsearch 客户端连接成功 (Ping 成功)", zap.String("status", pingRes.Status()))

	// 使用后台上下文进行索引创建，因为这通常是启动过程的一部分
	backgroundCtx := context.Background()

	// --- 检查并创建主帖子索引 ---
	err = createIndexIfNotExists(backgroundCtx, esClient, cfg.PrimaryIndex, getPostsIndexMapping, logger, "主帖子")
	if err != nil {
		return nil, err // 如果创建主索引失败，则直接返回错误
	}

	// --- 检查并创建热门搜索词索引 ---
	err = createIndexIfNotExists(backgroundCtx, esClient, cfg.HotTermsIndex, getHotSearchTermsIndexMapping, logger, "热门搜索词")
	if err != nil {
		// 如果创建热门搜索词索引失败，也返回错误。
		// 或者，您可以根据业务需求决定是否将其视为致命错误。
		// 例如，如果热门搜索词是可选功能，可以只记录警告。但通常最好是失败。
		return nil, err
	}

	return &ESClient{
		Client:          esClient,
		PrimaryIndexCfg: cfg.PrimaryIndex, // 存储主索引配置
	}, nil
}
