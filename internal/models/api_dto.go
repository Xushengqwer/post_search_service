package models

import (
	"github.com/Xushengqwer/go-common/models/enums" // 确保 enums 包路径正确
)

//import "github.com/Xushengqwer/go-common/models/enums" // swag:import

// SearchRequest 定义搜索 API 请求的参数及验证规则.
type SearchRequest struct {
	Query     string `form:"q"`                                                          // 搜索关键词，非必需
	Page      int    `form:"page,default=1" binding:"omitempty,min=1"`                   // 页码，可选，默认为1，最小为1
	Size      int    `form:"size,default=10" binding:"omitempty,min=1,max=100"`          // 每页大小，可选，默认10，范围1-100
	SortBy    string `form:"sort_by,default=updated_at" binding:"omitempty"`             // 排序字段，可选，默认 updated_at
	SortOrder string `form:"sort_order,default=desc" binding:"omitempty,oneof=asc desc"` // 排序顺序，可选，默认 desc，必须是 asc 或 desc

	// --- 过滤器字段 ---
	// 这些字段用于根据精确条件筛选结果，不影响相关性评分。
	// 确保这些字段的名称和类型与前端请求参数一致，并且后端有相应的处理逻辑。
	AuthorID string        `form:"author_id" binding:"omitempty,uuid|alphanum"` // 可选，按作者ID筛选。binding 标签用于输入验证。
	Status   *enums.Status `form:"status" binding:"omitempty,min=0,max=2" swaggertype:"primitive,integer" example:"1"`
	// 你可以根据需要添加更多过滤字段，例如：
	// Tags     []string `form:"tags" binding:"omitempty"` // 按标签筛选 (如果帖子有标签字段)
	// StartDate *time.Time `form:"start_date" binding:"omitempty,datetime"` // 按起始日期筛选
	// EndDate   *time.Time `form:"end_date" binding:"omitempty,datetime"`   // 按结束日期筛选
}

// SearchResult 定义搜索 API 的响应数据结构.
type SearchResult struct {
	Hits  []EsPostDocument `json:"hits"`                           // 命中的帖子列表
	Total int64            `json:"total"`                          // 总命中数
	Page  int              `json:"page"`                           // 当前页码
	Size  int              `json:"size"`                           // 当前页大小
	Took  int64            `json:"took_ms,omitempty" example:"50"` // UPRAVENO: Doba trvání dotazu v milisekundách (typ int64)
	// json:"took_ms,omitempty" 表示在序列化为JSON时，字段名为 "took_ms"，如果值为零值则忽略。
}
