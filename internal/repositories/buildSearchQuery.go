// FileName: repositories/query_builder.go
package repositories

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Xushengqwer/post_search/internal/models"
)

// buildSearchQuery 根据提供的搜索请求构建 Elasticsearch 查询的 JSON 体。
// 这个函数封装了分页、排序、主查询逻辑（match_all 或 multi_match）、可选的过滤逻辑以及高亮逻辑。
func buildSearchQuery(req models.SearchRequest) ([]byte, error) {
	from := (req.Page - 1) * req.Size
	if from < 0 {
		from = 0
	}

	sortClause := []map[string]map[string]string{
		{req.SortBy: {"order": req.SortOrder}},
	}
	if req.SortBy != "id" && req.SortBy != "_score" {
		sortClause = append(sortClause, map[string]map[string]string{"id": {"order": "asc"}})
	}

	var mainQueryDSL map[string]interface{}
	if strings.TrimSpace(req.Query) == "" {
		mainQueryDSL = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	} else {
		mainQueryDSL = map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  req.Query,
				"fields": []string{"title^3", "content", "author_username"}, // 您希望在高亮中也考虑这些字段
				"type":   "best_fields",
			},
		}
	}

	var filters []map[string]interface{}
	if req.AuthorID != "" {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{"author_id": req.AuthorID},
		})
	}
	if req.Status != nil {
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{"status": *req.Status},
		})
	}

	var finalQueryDSL map[string]interface{}
	if len(filters) > 0 {
		finalQueryDSL = map[string]interface{}{
			"bool": map[string]interface{}{
				"must":   mainQueryDSL,
				"filter": filters,
			},
		}
	} else {
		finalQueryDSL = mainQueryDSL
	}

	// --- 新增：高亮 (Highlighting) 配置 ---
	var highlightClause map[string]interface{}
	if strings.TrimSpace(req.Query) != "" { // 只有当有搜索关键词时才添加高亮
		highlightClause = map[string]interface{}{
			"pre_tags":  []string{"<strong>"},  // 定义包裹匹配词的前置标签 (HTML加粗)
			"post_tags": []string{"</strong>"}, // 定义包裹匹配词的后置标签
			"fields": map[string]interface{}{ // 指定要在哪些字段上进行高亮
				"title": map[string]interface{}{}, // 对 title 字段进行高亮，使用默认设置
				"content": map[string]interface{}{ // 对 content 字段进行高亮
					"fragment_size":       150, // 每个高亮片段的最大字符数 (大致)
					"number_of_fragments": 3,   // 最多返回多少个高亮片段
					// "no_match_size": 150, // 如果没有匹配的片段，但字段本身需要返回一部分内容时，可以指定长度
				},
				// "author_username": map[string]interface{}{}, // 如果也想高亮作者名
			},
			// "encoder": "html", // 确保特殊HTML字符被正确编码 (通常是默认行为)
			// "require_field_match": false, // 如果为true，则只有查询匹配的字段才会高亮。默认为false，可能会高亮其他字段（如果使用通配符字段名）
		}
	}
	// --- 结束新增部分 ---

	esQueryRequest := map[string]interface{}{
		"from":             from,
		"size":             req.Size,
		"sort":             sortClause,
		"query":            finalQueryDSL,
		"track_total_hits": true,
	}

	// 只有当 highlightClause 被创建时（即有搜索关键词时），才将其添加到请求中
	if highlightClause != nil {
		esQueryRequest["highlight"] = highlightClause
	}

	queryJSON, err := json.Marshal(esQueryRequest)
	if err != nil {
		return nil, fmt.Errorf("序列化 Elasticsearch 查询对象为 JSON 失败: %w", err)
	}

	return queryJSON, nil
}
