package models

import (
	"github.com/Xushengqwer/go-common/models/enums"
	"time"
)

// ImageEventData defines the structure for essential image information in Kafka events.
type ImageEventData struct {
	ImageURL     string `json:"image_url"`               // Publicly accessible URL of the image
	ObjectKey    string `json:"object_key,omitempty"`    // Key of the image in object storage (optional, if consumers need it)
	DisplayOrder int    `json:"display_order,omitempty"` // Display order of the image (optional)
}

// EsPostDocument 表示存储在 Elasticsearch 中的帖子文档结构。
type EsPostDocument struct {
	ID             uint64            `json:"id"`                                                       // 帖子唯一标识符。使用 uint64 以兼容 ES 的 long 或 unsigned_long 类型。
	Title          string            `json:"title"`                                                    // 帖子标题。
	Content        string            `json:"content"`                                                  // 帖子内容。
	AuthorID       string            `json:"author_id"`                                                // 作者的用户 ID。
	AuthorAvatar   string            `json:"author_avatar"`                                            // 作者头像的 URL 或标识符。
	AuthorUsername string            `json:"author_username"`                                          // 作者的用户名。
	Status         enums.Status      `json:"status" swaggertype:"primitive,integer" example:"1"`       // 帖子状态，直接使用导入的枚举类型（建议在 ES 中存储为整数或映射为 keyword）。
	ViewCount      int64             `json:"view_count"`                                               // 帖子浏览量。
	OfficialTag    enums.OfficialTag `json:"official_tag" swaggertype:"primitive,integer" example:"0"` // 官方标签，直接使用导入的枚举类型（建议在 ES 中存储为整数或映射为 keyword）。
	PricePerUnit   float64           `json:"price_per_unit"`                                           // 每单位价格（如果适用）。
	ContactInfo    string            `json:"contact_info"`                                             // 联系方式
	CreatedAt      int64             `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`       // 文档在 Elasticsearch 中最后更新的时间戳。
	Images         []ImageEventData  `json:"images,omitempty"` // 图片列表

	// 新增：用于存储高亮片段的字段
	// 键是字段名 (如 "title", "content")，值是包含高亮HTML片段的字符串切片。
	// omitempty 表示如果 Highlights 为 nil 或空 map，则在JSON序列化时忽略此字段。
	// 我们不在 _source 中存储这个字段，它是由 Elasticsearch 在查询时动态生成的。
	// 因此，不需要 `json:"-"` 标签来阻止它被 Elasticsearch 索引，
	// 但在API响应中我们希望包含它，所以使用 `json:"highlights,omitempty"`。
	Highlights map[string][]string `json:"highlights,omitempty"`
}
