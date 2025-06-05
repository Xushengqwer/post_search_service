package models

import "time" // 如果 HotSearchTermES 中包含时间戳，则导入

// HotSearchTerm 定义 API 返回的热门搜索词的结构。
// 这个结构体用于API响应，告诉前端哪些词是热门的。
type HotSearchTerm struct {
	Term  string `json:"term"`            // 搜索词本身
	Count int64  `json:"count,omitempty"` // 搜索词的频率计数，omitempty表示如果为0则不在JSON中显示，可选
}

// HotSearchTermES 定义在 Elasticsearch 中存储热门搜索词统计数据的结构。
// 这个结构体用于在Elasticsearch中存储和聚合搜索词的频率。
type HotSearchTermES struct {
	Term           string    `json:"term"`             // 搜索词本身，通常会作为文档ID或一个主要字段
	Count          int64     `json:"count"`            // 该搜索词被搜索的总次数
	LastSearchedAt time.Time `json:"last_searched_at"` // 该搜索词最后一次被搜索的时间，UTC格式
}
