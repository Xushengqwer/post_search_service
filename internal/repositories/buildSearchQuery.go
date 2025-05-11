// FileName: repositories/query_builder.go
package repositories

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Xushengqwer/post_search/internal/models"
	// 假设你的 models.SearchRequest 中的 Status 字段使用了 enums 包
	// "github.com/Xushengqwer/go-common/models/enums" // 如果 Status 是枚举类型，需要导入
)

// buildSearchQuery 根据提供的搜索请求构建 Elasticsearch 查询的 JSON 体。
// 这个函数封装了分页、排序、主查询逻辑（match_all 或 multi_match）以及可选的过滤逻辑。
// 参数:
//   - req: models.SearchRequest 结构体，包含了分页、排序、查询关键词以及可能的过滤条件。
//
// 返回值:
//   - []byte: 构建好的 Elasticsearch 查询 DSL (JSON格式的字节切片)。
//   - error: 如果在构建或序列化过程中发生错误，则返回错误。
func buildSearchQuery(req models.SearchRequest) ([]byte, error) {
	// --- 1. 分页 (Pagination) ---
	// Elasticsearch 的 'from' 参数指定了从结果集中的哪个索引开始返回文档，用于分页。
	// 'from' 是基于0的索引，所以第一页是 from=0, 第二页是 from=size, 依此类推。
	from := (req.Page - 1) * req.Size
	if from < 0 {
		// 防止页码或每页数量不合理导致 from 为负数。
		from = 0
	}

	// --- 2. 排序 (Sorting) ---
	// 定义排序子句。Elasticsearch 允许按一个或多个字段排序。
	sortClause := []map[string]map[string]string{
		{req.SortBy: {"order": req.SortOrder}}, // 使用请求中指定的字段 (req.SortBy) 和顺序 (req.SortOrder) 进行主排序。
	}

	// 为什么添加辅助排序?
	// 当主排序字段的值相同时（例如，按“创建时间”排序，可能有多条记录在同一秒创建），
	// 为了确保每次搜索结果的顺序是确定的、一致的，通常会添加一个唯一的字段（如文档ID）作为辅助排序条件。
	// 如果主排序字段本身已经是唯一的（如 "id"）或已经是基于相关性评分 ("_score")，则不需要辅助排序。
	if req.SortBy != "id" && req.SortBy != "_score" {
		// 使用 "id" 字段升序作为辅助排序，确保结果的稳定性。
		sortClause = append(sortClause, map[string]map[string]string{"id": {"order": "asc"}})
	}

	// --- 3. 主查询 (Main Query) ---
	var mainQueryDSL map[string]interface{} // mainQueryDSL 代表 Elasticsearch 查询的主要匹配逻辑。

	// 根据请求中的查询关键词 (req.Query) 构建查询逻辑。
	if strings.TrimSpace(req.Query) == "" {
		// 如果查询关键词为空或只包含空白字符，则使用 "match_all" 查询。
		// "match_all" 查询会匹配索引中的所有文档，通常用于“浏览全部”或与其他过滤条件结合使用。
		mainQueryDSL = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	} else {
		// 如果查询关键词不为空，则使用 "multi_match" 查询。
		// "multi_match" 允许在多个字段上执行相同的全文搜索查询。
		mainQueryDSL = map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query": req.Query, // 要搜索的文本。
				// "fields" 指定了要在哪些字段中搜索。
				// 可以使用 "^" 符号给特定字段增加权重 (boost)，使其在评分中更重要。
				// 例如，"title^3" 表示标题字段的匹配得分将是其他字段的3倍。
				"fields": []string{"title^3", "content", "author_username"},
				// "type" 参数定义了 multi_match 查询如何组合各字段的得分。
				// "best_fields": （默认）查找匹配度最高的那个字段，并使用其得分。适用于不相关的字段。
				"type": "best_fields", // 这是一个常见的、合理的默认选择。
			},
		}
	}

	// --- 4. 过滤 (Filtering) ---
	// 过滤器 (filters) 用于精确匹配，它们不影响文档的评分 (_score)，并且通常会被 Elasticsearch 缓存，性能较好。
	// 我们将在这里构建一个过滤器列表，然后将其与主查询组合。

	var filters []map[string]interface{}

	// 假设 models.SearchRequest 结构体中定义了用于过滤的字段，例如:
	// AuthorID string       `form:"author_id,omitempty"` // 按作者ID过滤
	// Status   *enums.Status `form:"status,omitempty"`    // 按状态过滤 (使用指针以区分零值和未提供)
	// 注意：你需要确保你的 models.SearchRequest 结构体中实际定义了这些字段 (或类似的字段)
	// 并且它们在传入的 req 对象中被正确填充。

	// 示例：处理 AuthorID 过滤条件
	// 假设你的 models.SearchRequest 中有一个名为 AuthorIDFilter 的字段 (或者你直接使用 req.AuthorID)
	// 为了演示，我们假设 req 中有一个 AuthorIDFilter 字段。
	// if req.AuthorIDFilter != "" { // 检查 AuthorIDFilter 是否被提供
	//     filters = append(filters, map[string]interface{}{
	//         "term": {"author_id": req.AuthorIDFilter}, // term 查询用于精确匹配 keyword 类型的字段
	//     })
	// }
	// 根据你提供的 SearchRequest 模型，我们直接使用 AuthorID 字段 (如果它用于过滤)
	// 假设如果 req.AuthorID 非空，则表示需要按作者ID过滤
	if req.AuthorID != "" { // 假设 req.AuthorID 就是用于过滤的字段
		filters = append(filters, map[string]interface{}{
			"term": map[string]interface{}{"author_id": req.AuthorID},
		})
	}

	// 示例：处理 Status 过滤条件
	// 假设你的 models.SearchRequest 中有一个名为 Status 的字段，类型为 *enums.Status
	if req.Status != nil { // 检查 Status 是否被提供 (通过指针是否为 nil 判断)
		filters = append(filters, map[string]interface{}{
			// term 查询用于精确匹配。假设 status 字段在 Elasticsearch 中存储为可以精确匹配的值（例如整数或 keyword）。
			"term": map[string]interface{}{"status": *req.Status},
		})
	}

	// --- 5. 组合主查询和过滤器 ---
	var finalQueryDSL map[string]interface{}
	if len(filters) > 0 {
		// 如果存在过滤器，则使用 "bool" 查询来组合主查询和过滤器。
		// "bool" 查询允许组合多个查询子句（must, should, must_not, filter）。
		finalQueryDSL = map[string]interface{}{
			"bool": map[string]interface{}{
				// 主查询（如 multi_match 或 match_all）放在 "must" 子句中，它会影响评分。
				"must": mainQueryDSL,
				// 过滤器数组放在 "filter" 子句中。
				// "filter" 子句中的条件必须匹配，但它们在过滤上下文中执行，不影响评分，并且会被高效缓存。
				"filter": filters,
			},
		}
	} else {
		// 如果没有定义任何过滤器，则最终的查询就是之前构建的主查询。
		finalQueryDSL = mainQueryDSL
	}

	// --- 6. 组装最终的 Elasticsearch 查询请求体 ---
	// 这是发送给 Elasticsearch 的完整请求体结构。
	esQueryRequest := map[string]interface{}{
		"from":  from,          // 分页：指定从第几条结果开始返回。
		"size":  req.Size,      // 分页：指定每页返回多少条结果。
		"sort":  sortClause,    // 排序：定义结果的排序方式。
		"query": finalQueryDSL, // 查询：包含主查询逻辑，以及可能的过滤逻辑。
		// "_source": []string{"id", "title", "author_username", "updated_at"}, // 可选：指定只返回文档中的特定字段，以减少网络传输量和客户端处理负担。
		"track_total_hits": true, // 强烈建议设置为 true。确保 Elasticsearch 返回精确的总命中数，即使它超过了默认的10000条。
	}

	// --- 7. 序列化为 JSON ---
	// 将 Go 的 map 结构转换为 JSON 字节切片，以便通过 HTTP 发送给 Elasticsearch。
	queryJSON, err := json.Marshal(esQueryRequest)
	if err != nil {
		// 如果序列化失败（理论上很少发生，除非结构非常复杂或包含无法序列化的类型），
		// 此函数应返回错误，由调用者（Repository 层）记录具体的错误上下文。
		return nil, fmt.Errorf("序列化 Elasticsearch 查询对象为 JSON 失败: %w", err)
	}

	return queryJSON, nil
}
