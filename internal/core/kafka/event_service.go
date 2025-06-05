package kafka // 通常 EventService 应该在 service 包下，但根据你提供的路径，它在 kafka 包下

import (
	"context"
	"errors" // 用于错误检查，例如 errors.Is
	"fmt"

	"github.com/Xushengqwer/go-common/models/kafkaevents" // <-- 新增导入

	"github.com/Xushengqwer/go-common/core"
	// "github.com/Xushengqwer/post_search/internal/models" // <-- 移除或修改，确保不引用旧的 Kafka DTOs
	"github.com/Xushengqwer/post_search/internal/models" // <-- 仍然需要这个来引用 EsPostDocument
	"github.com/Xushengqwer/post_search/internal/repositories"

	"go.uber.org/zap"
)

// 包级别定义的哨兵错误 (sentinel errors)，用于表示特定的、可预期的错误条件。
// 上层调用者（如 Kafka 消息处理器）可以使用 errors.Is() 来检查这些错误类型，
// 并据此决定后续行为（例如，对于永久性错误，发送到死信队列而不是重试）。
var (
	ErrMissingAuthorID    = errors.New("帖子作者ID不能为空") // 如果 AuthorID 是必需的，则定义此错误
	ErrInvalidPostID      = errors.New("无效的帖子ID")
	ErrEmptyTitle         = errors.New("帖子标题不能为空")
	ErrInvalidEventFormat = errors.New("无效的事件格式或缺少关键数据") // 注意：此错误在当前代码片段中已定义但尚未使用，如果需要，请在适当的逻辑中加入。
)

// EventService 封装了处理与帖子相关的 Kafka 事件的业务逻辑。
// 它依赖于 PostRepository 与 Elasticsearch 进行交互。
type EventService struct {
	postRepo repositories.PostRepository // postRepo 存储了与帖子数据持久化相关的操作接口。
	logger   *core.ZapLogger             // logger 用于结构化日志记录。
}

// NewEventService 创建 EventService 的新实例。
// 参数:
//   - postRepo: 实现了 PostRepository 接口的实例，用于与帖子数据存储交互。
//   - logger: ZapLogger 实例，用于日志记录。
//
// 注意：如果关键依赖项 (postRepo, logger) 为 nil，此函数会 panic，
// 因为服务在这种情况下无法正常运行。这是一种快速失败的策略，防止服务以损坏状态启动。
func NewEventService(postRepo repositories.PostRepository, logger *core.ZapLogger) *EventService {
	if postRepo == nil {
		// 对于服务启动时的关键依赖，如果缺失，则 panic 以阻止服务以不正确状态运行。
		panic("致命错误 [事件服务]: PostRepository 依赖注入失败，实例不能为 nil")
	}
	if logger == nil {
		panic("致命错误 [事件服务]: ZapLogger 依赖注入失败，实例不能为 nil")
	}
	return &EventService{
		postRepo: postRepo,
		logger:   logger,
	}
}

// HandlePostApprovedEvent 处理帖子审核通过的 Kafka 事件 (替换 HandlePostAuditEvent)
// 它会验证事件数据，将其转换为 Elasticsearch 文档模型，然后调用仓库层进行索引。
// 参数:
//   - ctx: 上下文，用于控制超时和取消。
//   - event: 从 Kafka 消费到的帖子审核通过事件数据 (类型已更新为 kafkaevents.PostApprovedEvent)。
//
// 返回值:
//   - error: 如果处理过程中发生错误（如验证失败、索引失败），则返回错误。
//     返回的错误可能包装了预定义的哨兵错误（如 ErrInvalidPostID, ErrEmptyTitle），
//     以便上层调用者可以进行类型检查。
func (s *EventService) HandlePostApprovedEvent(ctx context.Context, event *kafkaevents.PostApprovedEvent) error {
	// 2. 从 event.Post 中获取核心数据
	postData := event.Post
	s.logger.Info("开始处理帖子审核通过事件 (PostApprovedEvent)",
		zap.String("event_id", event.EventID),
		zap.Uint64("post_id", postData.ID))

	// --- 输入数据验证 ---
	// 为什么进行输入验证?
	// 确保进入系统的事件数据符合基本要求，避免无效数据污染下游系统或导致处理失败。
	// 对于来自外部系统（如 Kafka）的数据，进行严格验证是一种良好的防御性编程实践。
	if postData.ID <= 0 {
		s.logger.Error("处理 PostApprovedEvent 失败：事件中包含无效的帖子 ID",
			zap.String("event_id", event.EventID),
			zap.Uint64("post_id", postData.ID),
			zap.String("校验规则", "ID 必须大于 0"),
		)
		// 返回包装后的哨兵错误，指明这是一个永久性错误。
		return fmt.Errorf("处理帖子审核通过事件失败，帖子 ID '%d' 无效: %w", postData.ID, ErrInvalidPostID)
	}
	if postData.Title == "" {
		s.logger.Error("处理 PostApprovedEvent 失败：事件中的帖子标题为空",
			zap.String("event_id", event.EventID),
			zap.Uint64("post_id", postData.ID),
		)
		// 返回包装后的哨兵错误。
		return fmt.Errorf("处理帖子审核通过事件失败，帖子 ID '%d' 的标题为空: %w", postData.ID, ErrEmptyTitle)
	}
	// 可以在此处添加对 event.Post 其他关键字段的验证，例如 AuthorID 等。
	// if postData.AuthorID == "" { ... return fmt.Errorf("...: %w", ErrMissingAuthorID) }

	// --- 数据转换/映射 ---
	// 将从 Kafka 事件模型 (kafkaevents.PostData) 转换为 Elasticsearch 文档模型 (models.EsPostDocument)。
	// 这样做可以解耦事件的格式和存储的格式。
	postDoc := models.EsPostDocument{
		ID:             postData.ID,
		Title:          postData.Title,
		Content:        postData.Content,
		AuthorID:       postData.AuthorID,
		AuthorAvatar:   postData.AuthorAvatar,
		AuthorUsername: postData.AuthorUsername,
		Status:         postData.Status, // 直接使用 common/enums.Status 类型
		ViewCount:      postData.ViewCount,
		OfficialTag:    postData.OfficialTag, // 直接使用 common/enums.OfficialTag 类型
		PricePerUnit:   postData.PricePerUnit,
		ContactInfo:    postData.ContactInfo,
		// UpdatedAt: time.Now(), // 通常 ES 会自动处理时间戳，或者从事件中获取。
		// kafkaevents.PostData 包含 CreatedAt 和 UpdatedAt (int64)，可以按需映射到 EsPostDocument
		// 例如: EsPostDocument 如果有 CreatedAt int64 字段，则： postDoc.CreatedAt = postData.CreatedAt
	}
	s.logger.Debug("已将 Kafka 事件数据映射到 EsPostDocument 模型",
		zap.String("event_id", event.EventID),
		zap.Uint64("post_id", postData.ID))

	// --- 调用 Elasticsearch 仓库操作 ---
	// 尝试将帖子文档索引到 Elasticsearch。
	err := s.postRepo.IndexPost(ctx, postDoc)
	if err != nil {
		s.logger.Error("调用 PostRepository 的 IndexPost 操作失败",
			zap.String("event_id", event.EventID),
			zap.Uint64("post_id", postData.ID),
			// zap.Any("post_document", postDoc), // 记录尝试索引的文档内容，有助于调试 (可能含敏感信息，按需开启)
			zap.Error(err), // 记录底层的具体错误信息
		)
		// 将底层错误包装后向上传递。
		// 上层调用者（Kafka 消费者处理器）可以根据此错误决定是否重试或发送到 DLQ。
		return fmt.Errorf("索引帖子 ID '%d' 到 Elasticsearch 失败: %w", postData.ID, err)
	}

	s.logger.Info("成功处理并索引帖子审核通过事件",
		zap.String("event_id", event.EventID),
		zap.Uint64("post_id", postData.ID))
	return nil // 表示成功处理
}

// HandlePostDeleteEvent 处理帖子删除的 Kafka 事件。
// 它会验证事件数据，然后调用仓库层从 Elasticsearch 中删除相应的文档。
// 参数:
//   - ctx: 上下文，用于控制超时和取消。
//   - event: 从 Kafka 消费到的帖子删除事件数据 (类型已更新为 kafkaevents.PostDeletedEvent)。
//
// 返回值:
//   - error: 如果处理过程中发生错误（如验证失败、删除失败），则返回错误。
func (s *EventService) HandlePostDeleteEvent(ctx context.Context, event *kafkaevents.PostDeletedEvent) error {
	s.logger.Info("开始处理帖子删除事件 (PostDeleteEvent)",
		zap.String("event_id", event.EventID),
		zap.Uint64("post_id", event.PostID))

	// --- 输入数据验证 ---
	if event.PostID <= 0 {
		s.logger.Error("处理 PostDeleteEvent 失败：事件中包含无效的帖子 ID",
			zap.String("event_id", event.EventID),
			zap.Uint64("post_id", event.PostID),
			zap.String("校验规则", "ID 必须大于 0"),
		)
		return fmt.Errorf("处理帖子删除事件失败，帖子 ID '%d' 无效: %w", event.PostID, ErrInvalidPostID)
	}

	// --- 调用 Elasticsearch 仓库操作 ---
	// 尝试从 Elasticsearch 中删除帖子文档。
	err := s.postRepo.DeletePost(ctx, event.PostID)
	if err != nil {
		// 根据之前的讨论，postRepo.DeletePost 应该已经处理了 "文档未找到" (404) 的情况，
		// 并且在这种情况下不应返回错误，或者返回一个特定的、可识别的错误，以便在这里可以忽略它。
		// 如果 DeletePost 对于 404 也返回普通错误，那么这里可能需要进一步判断错误类型。
		// 例如： if errors.Is(err, repositories.ErrDocumentNotFound) { s.logger.Warn(...); return nil }
		s.logger.Error("调用 PostRepository 的 DeletePost 操作失败",
			zap.String("event_id", event.EventID),
			zap.Uint64("post_id", event.PostID),
			zap.Error(err),
		)
		return fmt.Errorf("从 Elasticsearch 删除帖子 ID '%d' 失败: %w", event.PostID, err)
	}

	s.logger.Info("成功处理并删除帖子事件",
		zap.String("event_id", event.EventID),
		zap.Uint64("post_id", event.PostID))
	return nil // 表示成功处理
}
