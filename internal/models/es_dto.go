package models

import (
	"github.com/Xushengqwer/go-common/models/enums"
	"time"
)

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
	ContactQRCode  string            `json:"contact_qr_code"`                                          // 联系二维码的 URL 或标识符。
	UpdatedAt      time.Time         `json:"updated_at"`                                               // 文档在 Elasticsearch 中最后更新的时间戳。
}
