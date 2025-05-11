package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Xushengqwer/go-common/core"
	"github.com/Xushengqwer/post_search/internal/models"
	"go.uber.org/zap"

	"github.com/IBM/sarama"
	"github.com/cenkalti/backoff/v4"
)

// Handler 实现了 sarama.ConsumerGroupHandler 接口，负责处理从 Kafka 接收到的消息。
// 它包含以下主要职责：
// 1. 消息路由：根据消息的主题将其分发给特定的处理函数。
// 2. 业务逻辑调用：通过注入的 EventService 执行实际的业务处理。
// 3. 错误处理与重试：对可重试的错误执行指数退避重试策略。
// 4. 死信队列 (DLQ) 处理：在最终处理失败后，将消息发送到 DLQ。
// 5. 生命周期管理：通过 Setup, Cleanup 方法管理每个消费者会话的生命周期，并通过 Ready 通道发出就绪信号。
type Handler struct {
	eventService   *EventService                 // 业务服务层实例，用于处理消息的实际业务逻辑。
	dlqProducer    sarama.SyncProducer           // 用于发送消息到死信队列 (DLQ) 的同步生产者。
	dlqTopic       string                        // 死信队列 (DLQ) 的主题名称。
	maxRetry       uint64                        // 消息处理的最大重试次数。
	topicToHandler map[string]MessageHandlerFunc // 将主题名称映射到具体的处理函数。
	ready          chan bool                     // 用于发出 handler 已准备好消费信号的通道。此通道由 Setup 方法关闭。
	logger         *core.ZapLogger               // 结构化日志记录器。
}

// MessageHandlerFunc 定义了处理特定 Kafka 消息的函数的签名。
// 每个主题的消息处理器都应符合此函数原型。
type MessageHandlerFunc func(ctx context.Context, message *sarama.ConsumerMessage) error

// NewHandler 创建并初始化一个新的 Kafka 消息处理程序 (Handler) 实例。
// 参数:
//   - eventSvc: 业务事件服务 (*EventService) 的实例。
//   - producer: 用于发送到 DLQ 的 sarama.SyncProducer 实例。
//   - dlqTopic: 死信队列的主题名称。
//   - auditTopic: 帖子审计事件的主题名称。
//   - deleteTopic: 帖子删除事件的主题名称。
//   - logger: *core.ZapLogger 实例。
//   - maxRetries: 消息处理的最大重试次数。
//
// 返回值:
//   - *Handler: 初始化完成的消息处理程序实例。
func NewHandler(
	eventSvc *EventService,
	producer sarama.SyncProducer,
	dlqTopic string,
	auditTopic string,
	deleteTopic string,
	logger *core.ZapLogger,
	maxRetries uint64,
) *Handler {
	// 为什么进行这些检查?
	// 确保核心依赖项已正确提供，否则 Handler 无法正常工作。
	if logger == nil {
		// Logger 是关键依赖，如果缺失，服务将无法记录重要信息和错误，可能导致难以诊断问题。
		// 在这种情况下 panic 可以快速失败，防止服务以不完整状态运行。
		panic("致命错误 [Kafka Handler]: Logger 实例不能为 nil")
	}
	if eventSvc == nil {
		// EventService 包含了核心的业务处理逻辑，如果缺失，Handler 将无法处理任何消息。
		logger.Error("创建 Kafka Handler 失败: EventService 实例不能为 nil")
		panic("致命错误 [Kafka Handler]: EventService 实例不能为 nil")
	}
	// 注意：dlqProducer 和 dlqTopic 可能是可选的，取决于是否启用了 DLQ 功能。
	// 如果它们是必需的，也应该在这里进行检查。
	// 当前设计中，SendToDLQ 函数内部会检查 producer 是否为 nil。
	if producer == nil && dlqTopic != "" {
		logger.Warn("DLQ 主题已配置，但 DLQ 生产者未提供。DLQ 功能可能无法正常工作。", zap.String("dlq_topic", dlqTopic))
	}
	if producer != nil && dlqTopic == "" {
		logger.Warn("DLQ 生产者已提供，但 DLQ 主题未配置。DLQ 功能可能无法正常工作。")
	}

	h := &Handler{
		eventService: eventSvc,
		dlqProducer:  producer,
		dlqTopic:     dlqTopic,
		maxRetry:     maxRetries,      // 从参数获取最大重试次数，增强了可配置性。
		ready:        make(chan bool), // 初始化 ready 通道，用于 Setup 完成的信号。
		logger:       logger,
	}

	// 初始化主题到处理函数的映射。
	// 这种映射方式使得 Handler 能够根据消息来源的主题动态选择正确的处理逻辑，
	// 方便未来扩展新的主题和对应的处理器。
	h.topicToHandler = map[string]MessageHandlerFunc{
		auditTopic:  h.handlePostAuditEvent,  // "帖子审计事件" 主题的消息将由 h.handlePostAuditEvent 方法处理。
		deleteTopic: h.handlePostDeleteEvent, // "帖子删除事件" 主题的消息将由 h.handlePostDeleteEvent 方法处理。
	}
	logger.Info("Kafka Handler 初始化完成",
		zap.Strings("subscribed_topics_for_handler", []string{auditTopic, deleteTopic}), // 记录 Handler 实际配置处理的主题
		zap.Uint64("max_processing_retries", maxRetries),                                // 记录配置的最大重试次数
		zap.Bool("dlq_producer_configured", producer != nil),                            // 记录 DLQ 生产者是否配置
		zap.String("dlq_topic_configured", dlqTopic),                                    // 记录 DLQ 主题是否配置
	)
	return h
}

// Ready 返回一个只读通道，用于外部（例如 ConsumerGroup）等待此 Handler 准备就绪。
// 当 Handler 的 Setup 方法成功完成时，此通道将被关闭，任何监听此通道的 goroutine 将会解除阻塞。
// 这是实现 ConsumerGroup 等待 Handler 初始化完成的同步机制。
func (h *Handler) Ready() <-chan bool {
	return h.ready
}

// Setup 在新的消费者组会话开始时，由 Sarama 在每个声明的 claim (分区分配) 之前调用一次。
// 主要用途是执行任何必要的会话级别初始化，并发出 Handler 已准备好处理消息的信号。
// 对于此 Handler 实现，它通过关闭 `ready` 通道来发出信号。
func (h *Handler) Setup(session sarama.ConsumerGroupSession) error {
	h.logger.Info("Kafka Handler 开始执行 Setup...", zap.String("member_id", session.MemberID()))
	// 关闭 ready 通道，以发出 handler 已准备就绪的信号。
	// 每个 Handler 实例的生命周期内，ready 通道只应关闭一次。
	// 为了处理可能的重平衡（Sarama 可能会在同一个 Handler 实例上多次调用 Setup/Cleanup，尽管不常见），
	// 需要确保不会重复关闭已关闭的通道，否则会导致 panic。
	// 使用 select 来安全地尝试关闭。
	select {
	case <-h.ready:
		// 如果通道已经关闭（例如，在之前的 Setup 调用中，或者 Handler 实例被异常复用），则不执行任何操作。
		h.logger.Info("Kafka Handler 的 ready 通道已被关闭，Setup 跳过关闭操作。", zap.String("member_id", session.MemberID()))
	default:
		// 如果通道尚未关闭，则关闭它。这是正常流程。
		close(h.ready)
		h.logger.Info("Kafka Handler 的 ready 通道已成功关闭。", zap.String("member_id", session.MemberID()))
	}
	h.logger.Info("Kafka Handler Setup 完成，已准备好消费消息。", zap.String("member_id", session.MemberID()))
	return nil // 返回 nil 表示 Setup 成功。
}

// Cleanup 在消费者组会话结束时，或在处理完一个 claim 后且准备释放它之前调用。
// 用于执行任何必要的会话级别清理工作，例如释放资源、刷新缓冲区、关闭连接等。
func (h *Handler) Cleanup(session sarama.ConsumerGroupSession) error {
	h.logger.Info("Kafka Handler 开始执行 Cleanup...", zap.String("member_id", session.MemberID()))
	// 注意：一旦 `ready` 通道被关闭，它就不能再被“重新打开”或用于后续会话的信号（如果 Handler 实例被重用）。
	// 当前设计中，通常 ConsumerGroup 会为每个 Start 调用创建一个新的 Handler 实例，
	// 或者 ConsumerGroup 的 Start/Close 周期对应 Handler 的完整生命周期。
	// 如果 Handler 实例在多次重平衡中被 Sarama 内部复用（不常见），则 ready 信号机制可能需要更复杂的处理。
	// 对于本示例，Cleanup 中没有特别的资源需要释放。
	h.logger.Info("Kafka Handler Cleanup 完成。", zap.String("member_id", session.MemberID()))
	return nil // 返回 nil 表示 Cleanup 成功。
}

// ConsumeClaim 是消息处理的核心循环，由 Sarama 为每个分配给此消费者的分区声明 (claim) 调用。
// 此方法会持续从 `claim.Messages()` 通道中拉取消息并进行处理，
// 直到该通道关闭（通常在会话结束或重平衡时）或会话的上下文被取消。
func (h *Handler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	topic := claim.Topic()
	partition := claim.Partition()
	initialOffset := claim.InitialOffset() // 分区声明的初始偏移量，Sarama 会从这里开始（或从上次提交的偏移量）

	h.logger.Info("开始消费来自特定分区的消息",
		zap.String("topic", topic),
		zap.Int32("partition", partition),
		zap.Int64("initial_offset", initialOffset),
	)

	// 为什么使用 for-range 循环 claim.Messages()?
	// `claim.Messages()` 返回一个 `<-chan *sarama.ConsumerMessage`。
	// for-range 会持续从这个通道接收消息，直到通道被关闭。
	// 通道关闭通常意味着当前消费者会话结束，或者这个特定的分区声明被撤销（例如，在重平衡期间）。
	for message := range claim.Messages() {
		offset := message.Offset
		// 在生产环境中，为了减少日志量，成功接收消息的日志通常使用 Debug 级别。
		// 记录详细信息有助于追踪消息的生命周期。
		h.logger.Debug("收到 Kafka 消息",
			zap.String("topic", message.Topic),
			zap.Int32("partition", message.Partition), // message.Partition 应该与 claim.Partition() 一致
			zap.Int64("offset", offset),
			zap.ByteString("key", message.Key), // 记录消息的 Key，有助于按 Key 追踪或分区
			zap.Int("value_length", len(message.Value)),
			zap.Time("kafka_timestamp", message.Timestamp), // 记录 Kafka 消息自身的时间戳 (由生产者设置或 Broker 追加)
		)

		// 根据消息的主题从映射中获取对应的处理函数。
		// 这是实现消息路由的关键。
		handlerFunc, ok := h.topicToHandler[message.Topic]
		if !ok {
			// 如果没有为该主题注册处理函数，这通常表示配置错误或接收到了非预期的消息。
			// 记录警告并跳过该消息，同时确保标记消息以避免重复消费。
			h.logger.Warn("未找到针对该主题注册的消息处理函数，将跳过此消息",
				zap.String("topic", message.Topic),
				zap.Int64("offset", offset),
				zap.Int32("partition", message.Partition),
			)
			session.MarkMessage(message, "") // 必须标记，否则 Sarama 会认为此消息未处理。
			continue                         // 继续处理来自该分区的下一条消息。
		}

		// 使用 processWithRetry 方法处理消息，该方法封装了重试逻辑。
		// session.Context() 用于传递给业务逻辑，允许其响应超时或取消。
		// 这确保了长时间运行的业务逻辑也能被优雅地中断。
		processingCtx := session.Context()
		processErr := h.processWithRetry(processingCtx, message, handlerFunc)

		// 根据消息处理的最终结果进行后续操作。
		if processErr != nil {
			// 如果在所有重试尝试后消息仍然处理失败，记录错误。
			h.logger.Error("消息在所有重试尝试后处理失败，准备发送到死信队列 (DLQ)",
				zap.String("topic", message.Topic),
				zap.Int64("offset", offset),
				zap.Int32("partition", message.Partition),
				zap.Error(processErr), // 记录导致处理失败的根本原因
			)

			// 尝试将处理失败的消息发送到 DLQ。
			// 为 DLQ 发送操作创建一个独立的、带超时的上下文，
			// 避免因 DLQ 生产者阻塞而导致整个消费者卡住。
			dlqCtx, dlqCancel := context.WithTimeout(context.Background(), 10*time.Second) // 例如，10秒超时
			dlqErr := SendToDLQ(dlqCtx, h.dlqProducer, h.dlqTopic, message, processErr, h.logger)
			dlqCancel() // 及时释放 dlqCtx 的资源，无论 SendToDLQ 成功与否。

			if dlqErr != nil {
				// 如果发送到 DLQ 也失败，这是一个严重问题，可能表示 DLQ 系统本身不可用。
				// 记录更高级别的错误，并强调需要人工介入。
				h.logger.Error("发送消息到死信队列 (DLQ) 失败，可能导致消息丢失，需要人工关注！",
					zap.String("topic", message.Topic),
					zap.Int64("offset", offset),
					zap.Int32("partition", message.Partition),
					zap.NamedError("original_processing_error", processErr), // 记录原始处理错误，便于关联
					zap.NamedError("dlq_send_error", dlqErr),                // 记录 DLQ 发送错误
				)
				// 决策点：即使发送 DLQ 失败，是否仍标记原消息为已处理？
				// - 标记为已处理：优点是避免阻塞后续消息的处理，保证消费流的继续；缺点是当前消息可能永久丢失。
				// - 不标记：优点是尝试保留消息（如果错误是暂时的）；缺点是可能导致消息在后续被重复处理（如果消费者重启），或者如果问题持续，消费者会卡在这个消息上。
				// 通常选择标记并发出严重告警，以保证整体流程的可用性，同时依赖监控和告警来处理丢失的消息。
				session.MarkMessage(message, "")
			} else {
				// 消息成功发送到 DLQ。
				h.logger.Info("消息已成功发送到死信队列 (DLQ)",
					zap.String("original_topic", message.Topic),
					zap.Int64("original_offset", offset),
					zap.Int32("original_partition", message.Partition),
					zap.String("dlq_topic", h.dlqTopic),
				)
				session.MarkMessage(message, "") // 成功发送到 DLQ 后，标记原始消息为已处理。
			}
		} else {
			// 消息处理成功（可能在某次重试后成功）。
			session.MarkMessage(message, "") // 标记消息为已处理。
			h.logger.Debug("消息处理成功",         // 成功处理的日志通常使用 Debug 级别，以减少生产环境日志量
				zap.String("topic", message.Topic),
				zap.Int64("offset", offset),
				zap.Int32("partition", message.Partition),
			)
		}

		// 在每次消息处理（无论成功、失败或发送到 DLQ）后，检查会话上下文是否已被取消。
		// 这允许消费者在处理长时间运行的任务时（虽然 Kafka 消息处理通常应设计为快速的）也能及时响应外部的关闭信号。
		if session.Context().Err() != nil {
			h.logger.Info("会话上下文在消息处理后被取消，准备停止消费此分区",
				zap.String("topic", topic),                 // 使用 claim 的 topic
				zap.Int32("partition", partition),          // 使用 claim 的 partition
				zap.Int64("last_processed_offset", offset), // 记录最后处理的消息偏移量
				zap.Error(session.Context().Err()),         // 记录上下文错误的原因 (Canceled 或 DeadlineExceeded)
			)
			return session.Context().Err() // 返回上下文错误，这将导致 Sarama 停止处理此 claim。
		}
	}

	// 当 `claim.Messages()` 通道关闭时，表示此分区的所有消息（在该会话中）都已处理完毕，
	// 或者会话因重平衡等原因而结束。此时 for-range 循环会自然退出。
	h.logger.Info("已完成消费分区中的所有消息（或会话结束）",
		zap.String("topic", topic),
		zap.Int32("partition", partition),
	)
	return nil // 正常退出 ConsumeClaim 方法，表示此 claim 的处理已完成。
}

// processWithRetry 使用指数退避策略执行消息处理函数，并在发生可重试错误时进行重试。
// 参数:
//   - ctx: 上下文对象，传递给实际的消息处理函数，用于控制其执行（例如超时或取消）。
//   - message: 当前正在处理的 Kafka 消息。
//   - handlerFunc: 实际执行消息处理逻辑的函数。
//
// 返回值:
//   - error: 如果在所有配置的重试次数后消息处理仍然失败，则返回最后一次遇到的错误。
//     如果消息处理成功（可能在某次重试后），则返回 nil。
func (h *Handler) processWithRetry(ctx context.Context, message *sarama.ConsumerMessage, handlerFunc MessageHandlerFunc) error {
	// 配置指数退避策略。
	// NewExponentialBackOff() 创建一个具有默认参数的策略（例如，初始间隔500ms，乘数1.5，随机因子0.5等）。
	bo := backoff.NewExponentialBackOff()
	// bo.InitialInterval = 500 * time.Millisecond // 首次重试前的等待时间，后续可从配置读取
	// bo.MaxInterval = 30 * time.Second          // 最大重试间隔，后续可从配置读取
	// bo.Multiplier = 1.5                        // 每次重试间隔的乘数，后续可从配置读取
	// bo.RandomizationFactor = 0.5               // 随机化因子，避免惊群效应

	// MaxElapsedTime = 0 表示不设置总的重试时间上限。
	// 重试次数由 backoff.WithMaxRetries(bo, h.maxRetry) 控制。
	// 如果同时设置了 MaxElapsedTime 和 MaxRetries，则以先达到的为准。
	bo.MaxElapsedTime = 0

	// 定义一个闭包作为重试操作，该闭包调用实际的消息处理函数。
	// backoff 库会重复调用这个函数直到它返回 nil (成功) 或返回 backoff.Permanent(err) (永久性错误)，
	// 或者达到最大重试次数/时间。
	retryableOperation := func() error {
		// 调用注入的 MessageHandlerFunc 来处理消息。
		// 将上下文传递给处理函数，使其能够响应外部的取消或超时。
		err := handlerFunc(ctx, message)
		if err != nil {
			// 如果处理函数返回错误，判断该错误是否为永久性错误。
			// 永久性错误（如数据验证失败、反序列化失败）不应重试，因为重试不太可能成功。
			if isPermanentError(err) {
				h.logger.Error("消息处理遇到永久性错误，将停止重试并标记为最终失败",
					zap.String("topic", message.Topic),
					zap.Int64("offset", message.Offset),
					zap.Int32("partition", message.Partition),
					zap.Error(err),
				)
				// 使用 backoff.Permanent 包装错误，通知重试库这是一个不可重试的错误，应立即停止。
				return backoff.Permanent(err)
			}
			// 对于非永久性错误（可能是临时性问题，如网络抖动、下游服务暂时不可用），
			// 记录警告并返回原始错误，以触发 backoff 库的下一次重试。
			h.logger.Warn("消息处理失败，将基于退避策略尝试重试",
				zap.String("topic", message.Topic),
				zap.Int64("offset", message.Offset),
				zap.Int32("partition", message.Partition),
				zap.Error(err),
			)
			return err // 返回原始错误，通知重试库进行下一次重试。
		}
		return nil // 返回 nil 表示操作成功，无需重试。
	}

	// 定义一个通知函数，在每次重试尝试之前被调用。
	// 这对于监控和调试重试行为非常有用，可以了解重试的频率和原因。
	notifyFunc := func(err error, nextRetryDuration time.Duration) {
		h.logger.Warn("准备重试消息处理操作",
			zap.String("topic", message.Topic),
			zap.Int64("offset", message.Offset),
			zap.Int32("partition", message.Partition),
			zap.Duration("next_retry_in", nextRetryDuration), // 指示下一次重试将在多久之后发生
			zap.Error(err), // 本次失败的原因
		)
	}

	// 使用配置的退避策略和最大重试次数来执行操作。
	// backoff.WithMaxRetries 将指数退避与固定的最大重试次数 (h.maxRetry) 结合起来。
	// RetryNotify 会在每次重试前调用 notifyFunc。
	err := backoff.RetryNotify(retryableOperation, backoff.WithMaxRetries(bo, h.maxRetry), notifyFunc)

	// 返回重试过程后最终的错误状态。如果所有重试都失败，err 将是最后一次尝试的错误。
	// 如果某次尝试成功，err 将为 nil。
	return err
}

// --- 特定主题的消息处理函数实现 ---

// handlePostAuditEvent 是处理 "帖子审计事件" 主题消息的具体实现。
// 它负责反序列化消息内容为 models.KafkaPostAuditEvent，然后调用 EventService 进行处理。
func (h *Handler) handlePostAuditEvent(ctx context.Context, message *sarama.ConsumerMessage) error {
	var event models.KafkaPostAuditEvent // 准备用于反序列化的事件结构体

	// 尝试将消息的 Value (字节流) 反序列化为 KafkaPostAuditEvent 结构体。
	if err := json.Unmarshal(message.Value, &event); err != nil {
		// 反序列化失败通常是由于消息格式不正确或与期望的结构不符。
		// 这类错误通常是永久性的，因为消息内容本身不太可能在重试时发生变化。
		h.logger.Error("反序列化 'PostAuditEvent' 消息失败，数据格式可能不正确或与模型不匹配",
			zap.String("topic", message.Topic),
			zap.Int64("offset", message.Offset),
			zap.Int32("partition", message.Partition),
			zap.ByteString("raw_value_snippet", message.Value[:min(1024, len(message.Value))]), // 记录原始消息体片段，便于排查，避免过长
			zap.Error(err),
		)
		// 使用 backoff.Permanent 包装错误，以避免不必要的重试。
		return backoff.Permanent(fmt.Errorf("反序列化 PostAuditEvent 失败 (主题: %s, 偏移量: %d): %w", message.Topic, message.Offset, err))
	}

	// 根据用户提供的模型，KafkaPostAuditEvent 没有 EventType 字段，因此移除相关日志。
	h.logger.Debug("成功反序列化 PostAuditEvent，准备交由 EventService 处理",
		zap.Uint64("event_post_id", event.ID), // 使用事件中的 ID 进行日志记录
		zap.String("topic", message.Topic),
		zap.Int64("offset", message.Offset),
	)

	// 调用 EventService 的方法来处理已反序列化的审计事件。
	// EventService 内部会包含具体的业务逻辑，如数据验证、与 Elasticsearch 交互等。
	// EventService 返回的错误将被 processWithRetry 进一步判断是否为永久性错误。
	return h.eventService.HandlePostAuditEvent(ctx, event)
}

// handlePostDeleteEvent 是处理 "帖子删除事件" 主题消息的具体实现。
// 它负责反序列化消息内容为 models.KafkaPostDeleteEvent，然后调用 EventService 进行处理。
func (h *Handler) handlePostDeleteEvent(ctx context.Context, message *sarama.ConsumerMessage) error {
	var event models.KafkaPostDeleteEvent // 准备用于反序列化的事件结构体

	if err := json.Unmarshal(message.Value, &event); err != nil {
		h.logger.Error("反序列化 'PostDeleteEvent' 消息失败，数据格式可能不正确或与模型不匹配",
			zap.String("topic", message.Topic),
			zap.Int64("offset", message.Offset),
			zap.Int32("partition", message.Partition),
			zap.ByteString("raw_value_snippet", message.Value[:min(1024, len(message.Value))]), // 记录片段
			zap.Error(err),
		)
		return backoff.Permanent(fmt.Errorf("反序列化 PostDeleteEvent 失败 (主题: %s, 偏移量: %d): %w", message.Topic, message.Offset, err))
	}

	// 验证操作类型，根据用户模型，KafkaPostDeleteEvent 有 Operation 字段。
	// 这是业务层面的验证，确保我们只处理期望的 "delete" 操作。
	expectedOperation := "delete"
	if event.Operation != expectedOperation {
		h.logger.Warn("收到的 PostDeleteEvent 操作类型与预期不符，将跳过处理此消息",
			zap.String("topic", message.Topic),
			zap.Int64("offset", message.Offset),
			zap.Int32("partition", message.Partition),
			zap.Uint64("event_post_id", event.PostID),
			zap.String("received_operation", event.Operation), // 使用 event.Operation
			zap.String("expected_operation", expectedOperation),
		)
		// 返回 nil 表示此消息被识别为不适用（对于此特定逻辑）并已“处理”完毕（即跳过）。
		// 它不会被重试，也不会被发送到 DLQ。
		return nil
	}

	// 根据用户提供的模型，KafkaPostDeleteEvent 没有 EventType 字段。
	h.logger.Debug("成功反序列化 PostDeleteEvent 并验证通过，准备交由 EventService 处理",
		zap.Uint64("event_post_id", event.PostID),
		zap.String("operation_type", event.Operation), // 记录 Operation
		zap.String("topic", message.Topic),
		zap.Int64("offset", message.Offset),
	)

	// 调用 EventService 的方法来处理已反序列化的删除事件。
	return h.eventService.HandlePostDeleteEvent(ctx, event)
}

// isPermanentError 判断给定的错误是否为永久性错误，即不应进行重试的错误。
// 永久性错误通常包括：
// 1. 上下文取消或超时错误 (context.Canceled, context.DeadlineExceeded)。
// 2. 数据验证相关的业务逻辑错误 (例如，EventService 返回的 ErrInvalidPostID, ErrEmptyTitle)。
// 3. 消息格式或反序列化错误 (例如，json.Unmarshal 失败，或包装后的 ErrInvalidEventFormat)。
// 4. 来自下游系统（如 Elasticsearch）的某些特定永久性错误（例如，认证失败、索引映射冲突）。
//
// 参数:
//   - err: 需要判断的错误。
//
// 返回值:
//   - bool: 如果错误是永久性的，则返回 true；否则返回 false（表示可以尝试重试）。
func isPermanentError(err error) bool {
	if err == nil {
		return false // 没有错误，自然不是永久性错误。
	}

	// 1. 检查上下文相关的错误。
	// 如果操作是因为上下文被取消或超时而失败，那么对于当前这次尝试而言是“永久的”，不应重试相同的操作。
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// 2. 检查由 EventService（或 kafka 包内定义的，如 ErrInvalidPostID 等）产生的已知永久性业务/验证错误。
	// 使用 errors.Is 来正确处理错误链（如果错误被包装过）。
	// 确保这些哨兵错误变量在此函数可访问的范围内定义（例如，包级别变量）。
	if errors.Is(err, ErrInvalidPostID) ||
		errors.Is(err, ErrEmptyTitle) ||
		errors.Is(err, ErrMissingAuthorID) || // ErrMissingAuthorID 在 EventService 同一个包中定义
		errors.Is(err, ErrInvalidEventFormat) { // ErrInvalidEventFormat 也在 EventService 同一个包中定义
		return true
	}

	// 3. 检查底层的 JSON 反序列化错误。
	// 尽管各个 handleXxxEvent 方法会尝试用 backoff.Permanent 包装这类错误，
	// 但在这里也进行检查，可以作为一道额外的防线，或者处理其他可能产生此类错误而未被包装的情况。
	// errors.As 用于检查错误链中是否存在特定类型的错误，并可以将错误赋给指定类型的变量。
	var syntaxError *json.SyntaxError
	var unmarshalTypeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &unmarshalTypeError) {
		// JSON 格式错误或类型不匹配错误通常是永久性的，因为消息内容本身有问题，重试无法解决。
		return true
	}

	// 4. TODO: 检查来自更下游系统（如 PostRepository/Elasticsearch）的特定永久性错误。
	//    这需要 PostRepository 定义并返回可识别的错误类型，或者 EventService 在调用 repo 后对错误进行分类和包装。
	//    例如:
	//    if errors.Is(err, repositories.ErrElasticsearchAuthFailed) || errors.Is(err, repositories.ErrElasticsearchMappingConflict) {
	//        return true
	//    }

	// 5. 默认行为：如果错误不属于上述任何一种已知的永久性错误，
	//    则假定它可能是暂时的（例如网络波动、ES 临时过载、数据库连接池耗尽等），
	//    因此返回 false，允许 `processWithRetry` 进行重试。
	return false
}

// min 是一个辅助函数，返回两个整数中较小的一个。
// 用于在记录原始消息体时截断，避免日志过长。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
