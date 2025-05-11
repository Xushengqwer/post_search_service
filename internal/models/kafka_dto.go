package models

import (
	"github.com/Xushengqwer/go-common/models/enums"
)

// KafkaPostAuditEvent 镜像了审计服务发送的帖子创建/更新事件的结构。
// 直接使用导入的枚举类型进行反序列化。
type KafkaPostAuditEvent struct {
	ID             uint64            `json:"id"`              // 帖子的唯一标识符。
	Title          string            `json:"title"`           // 帖子标题。
	Content        string            `json:"content"`         // 帖子内容。
	AuthorID       string            `json:"author_id"`       // 作者的用户 ID。
	AuthorAvatar   string            `json:"author_avatar"`   // 作者头像的 URL 或标识符。
	AuthorUsername string            `json:"author_username"` // 作者的用户名。
	Status         enums.Status      `json:"status"`          // 帖子状态，直接使用导入的枚举类型。
	ViewCount      int64             `json:"view_count"`      // 帖子浏览量。
	OfficialTag    enums.OfficialTag `json:"official_tag"`    // 官方标签，直接使用导入的枚举类型。
	PricePerUnit   float64           `json:"price_per_unit"`  // 每单位价格（如果适用）。
	ContactQRCode  string            `json:"contact_qr_code"` // 联系二维码的 URL 或标识符。
}

// KafkaPostDeleteEvent 镜像了帖子服务发送的帖子删除事件的结构。
type KafkaPostDeleteEvent struct {
	Operation string `json:"operation"` // 操作类型，期望值为 "delete"。
	PostID    uint64 `json:"post_id"`   // 需要删除的帖子的唯一标识符。
}
