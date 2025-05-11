package es

import (
	"context"
	"encoding/json" // 用于解析 Elasticsearch 的错误响应
	"fmt"
	"io" // 用于读取响应体
	"net/http"
	"strings" // 用于 strings.NewReader
	"time"

	"github.com/Xushengqwer/go-common/core"     // 假设这是你的日志库路径
	"github.com/Xushengqwer/post_search/config" // 假设这是你的配置包路径

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
	"go.uber.org/zap"
)

// ESClient 包含初始化后的 Elasticsearch 客户端及相关信息
type ESClient struct {
	Client    *elasticsearch.Client
	IndexName string
}

// getPostsIndexMapping 定义了 posts_index 的映射和设置。
// 注意: 对于中文内容，title 和 content 字段的 analyzer 已调整为 "ik_smart"。
// 你需要确保 Elasticsearch 中已安装并配置了相应的中文分词器 (例如 IK Analyzer)。
// numberOfShards: 主分片数量。在索引创建后不可更改。
// numberOfReplicas: 每个主分片的副本数量。可以动态调整。
func getPostsIndexMapping(numberOfShards, numberOfReplicas int) string {
	// 为了避免 JSON 注入或格式问题，确保 numberOfShards 和 numberOfReplicas 是整数。
	// 在实际使用中，这些值应来自配置，并在传入前进行验证。
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
    }`, numberOfShards, numberOfReplicas)
}

// NewESClient 初始化 Elasticsearch 客户端并执行基本检查（Ping 和索引存在性检查）。
// 如果索引不存在，它会尝试使用配置的分片和副本数创建索引。
func NewESClient(cfg config.ESConfig, logger *core.ZapLogger, transport http.RoundTripper) (*ESClient, error) {
	// 构建 Elasticsearch 客户端配置。
	esCfg := elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
		Transport: transport, // <--- 使用外部传入的 transport
		// 根据需要添加其他相关设置，例如：RetryOnStatus, MaxRetries 等。
	}

	// 创建新的 Elasticsearch 客户端实例。
	esClient, err := elasticsearch.NewClient(esCfg)
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
			logger.Error("Elasticsearch Ping 不成功，且无法读取错误响应体",
				zap.String("status", pingRes.Status()),
				zap.Error(readErr),
			)
		} else {
			logger.Error("Elasticsearch Ping 不成功",
				zap.String("status", pingRes.Status()),
				zap.String("response", errorBody.String()),
			)
		}
		return nil, fmt.Errorf("elasticsearch Ping 不成功: %s", pingRes.Status())
	}
	logger.Info("Elasticsearch 客户端连接成功 (Ping 成功)", zap.String("status", pingRes.Status()))

	// --- 索引检查与创建 ---
	ctxIndexCheck, cancelIndexCheck := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelIndexCheck()

	// 1. 检查索引是否存在
	existsRes, err := esClient.Indices.Exists(
		[]string{cfg.IndexName},
		esClient.Indices.Exists.WithContext(ctxIndexCheck),
	)
	if err != nil {
		logger.Error("检查索引是否存在时发生网络或请求错误",
			zap.String("index_name", cfg.IndexName),
			zap.Error(err),
		)
		return nil, fmt.Errorf("检查索引 '%s' 是否存在失败: %w", cfg.IndexName, err)
	}
	defer existsRes.Body.Close()

	if existsRes.StatusCode == 404 { // 索引不存在
		logger.Warn("目标索引不存在，将尝试创建...",
			zap.String("index_name", cfg.IndexName),
			zap.Int("configured_shards", cfg.NumberOfShards),
			zap.Int("configured_replicas", cfg.NumberOfReplicas),
		)

		// 2. 定义并创建索引
		// 使用从配置中读取的分片和副本数
		if cfg.NumberOfShards <= 0 {
			logger.Error("无效的分片数配置，必须大于0", zap.Int("configured_shards", cfg.NumberOfShards))
			return nil, fmt.Errorf("配置的分片数无效: %d，必须大于0", cfg.NumberOfShards)
		}
		if cfg.NumberOfReplicas < 0 {
			logger.Error("无效的副本数配置，必须大于或等于0", zap.Int("configured_replicas", cfg.NumberOfReplicas))
			return nil, fmt.Errorf("配置的副本数无效: %d，必须大于或等于0", cfg.NumberOfReplicas)
		}

		mapping := getPostsIndexMapping(cfg.NumberOfShards, cfg.NumberOfReplicas)
		createCtx, createCancel := context.WithTimeout(context.Background(), 10*time.Second) // 给创建操作更长的超时
		defer createCancel()

		createReq := esapi.IndicesCreateRequest{
			Index: cfg.IndexName,
			Body:  strings.NewReader(mapping),
		}
		createRes, err := createReq.Do(createCtx, esClient)
		if err != nil {
			logger.Error("发送创建索引请求失败",
				zap.String("index_name", cfg.IndexName),
				zap.Error(err),
			)
			return nil, fmt.Errorf("发送创建索引 '%s' 请求失败: %w", cfg.IndexName, err)
		}
		defer createRes.Body.Close()

		if createRes.IsError() {
			var errorBody strings.Builder
			var parsedError map[string]interface{}
			// 注意：在生产代码中，应妥善处理 io.ReadAll 可能返回的错误
			bodyBytes, _ := io.ReadAll(createRes.Body)
			errorBody.Write(bodyBytes)

			if err := json.Unmarshal(bodyBytes, &parsedError); err == nil {
				logger.Error("创建索引失败",
					zap.String("index_name", cfg.IndexName),
					zap.String("status", createRes.Status()),
					zap.Any("es_error_details", parsedError),
				)
			} else {
				logger.Error("创建索引失败，且无法解析JSON错误响应",
					zap.String("index_name", cfg.IndexName),
					zap.String("status", createRes.Status()),
					zap.String("raw_response", errorBody.String()),
					zap.Error(err), // JSON 解析错误
				)
			}
			return nil, fmt.Errorf("创建索引 '%s' 失败, 状态码: %s, 响应: %s", cfg.IndexName, createRes.Status(), errorBody.String())
		}
		logger.Info("成功创建索引及映射",
			zap.String("index_name", cfg.IndexName),
			zap.Int("shards_created", cfg.NumberOfShards),
			zap.Int("replicas_created", cfg.NumberOfReplicas),
		)

	} else if existsRes.IsError() { // 检查索引请求返回了其他错误
		var errorBody strings.Builder
		if _, readErr := io.Copy(&errorBody, existsRes.Body); readErr != nil {
			logger.Error("检查索引存在性时出错，且无法读取错误响应体",
				zap.String("index_name", cfg.IndexName),
				zap.String("status", existsRes.Status()),
				zap.Error(readErr),
			)
		} else {
			logger.Error("检查索引存在性时出错",
				zap.String("index_name", cfg.IndexName),
				zap.String("status", existsRes.Status()),
				zap.String("response", errorBody.String()),
			)
		}
		return nil, fmt.Errorf("检查索引 '%s' 存在性时出错: %s", cfg.IndexName, existsRes.Status())
	} else {
		// 索引已存在
		logger.Info("目标索引已存在", zap.String("index_name", cfg.IndexName))
		// （可选）可以在这里添加逻辑来检查现有索引的设置/映射是否与期望的一致，
		// 但这会增加复杂性。通常，如果服务期望特定的映射，则在索引不存在时创建它是主要目标。
	}

	// 返回初始化成功的客户端信息
	return &ESClient{
		Client:    esClient,
		IndexName: cfg.IndexName,
	}, nil
}
