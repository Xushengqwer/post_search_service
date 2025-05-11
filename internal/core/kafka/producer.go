package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/Xushengqwer/go-common/core"     // 假设这是你的日志库路径
	"github.com/Xushengqwer/post_search/config" // 假设这是你的配置包路径
	"go.uber.org/zap"
	// "log" // 建议移除标准 log 包，统一使用 zap
)

// NewSyncProducer 初始化一个 Kafka 同步生产者。
// 同步生产者在发送消息后会阻塞，直到收到 Broker 的确认（确认级别取决于 Sarama 配置中的 Producer.RequiredAcks）。
// 这种类型的生产者通常用于发送那些需要确保已成功写入 Kafka 的重要消息，例如发送到 DLQ 的消息。
// 参数:
//   - cfg: 应用程序的 KafkaConfig 配置，主要用于获取 Broker 地址列表。
//   - clientConfig: 预先配置好的 Sarama 客户端通用配置对象，它会应用于此生产者。
//   - logger: 用于结构化日志记录的 ZapLogger 实例。
//
// 返回值:
//   - sarama.SyncProducer: 初始化成功的同步生产者实例。
//   - error: 如果生产者创建过程中发生错误，则返回错误。
func NewSyncProducer(cfg config.KafkaConfig, clientConfig *sarama.Config, logger *core.ZapLogger) (sarama.SyncProducer, error) {
	if logger == nil {
		// 如果没有 logger，关键的初始化信息和错误将无法记录。
		// 理论上，这里应该 panic 或者返回一个明确的错误，因为 logger 是核心依赖。
		// 为了与函数签名一致，我们返回错误。
		// 注意：由于 logger 为 nil，此特定错误本身无法通过该 logger 实例记录。
		return nil, errors.New("创建 Kafka 同步生产者失败：logger 实例不能为空")
	}
	if clientConfig == nil {
		logger.Error("创建 Kafka 同步生产者失败：Sarama 客户端配置 (clientConfig) 不能为空")
		return nil, errors.New("创建 Kafka 同步生产者失败：Sarama 客户端配置 (clientConfig) 不能为空")
	}
	if len(cfg.Brokers) == 0 {
		logger.Error("创建 Kafka 同步生产者失败：Broker 地址列表不能为空")
		return nil, errors.New("创建 Kafka 同步生产者失败：Broker 地址列表不能为空")
	}

	// 使用 Sarama 配置和 Broker 地址列表创建一个新的同步生产者。
	// sarama.NewSyncProducer 会尝试连接到指定的 Broker。
	producer, err := sarama.NewSyncProducer(cfg.Brokers, clientConfig)
	if err != nil {
		logger.Error("创建 Kafka 同步生产者失败",
			zap.Strings("brokers", cfg.Brokers),
			zap.Error(err),
		)
		return nil, fmt.Errorf("创建 Kafka 同步生产者失败，目标 Broker: %v, 错误: %w", cfg.Brokers, err)
	}

	logger.Info("Kafka 同步生产者初始化成功", zap.Strings("brokers", cfg.Brokers))
	return producer, nil
}

// SendToDLQ 将处理失败的消息发送到死信队列 (DLQ)。
// 此函数会构建一个新的 Kafka 消息，其中包含原始消息的内容以及描述处理失败上下文的头部信息。
// 使用同步生产者发送，以确保消息确实被 DLQ 接收。
// 参数:
//   - ctx: Context 对象，用于控制发送操作的超时或取消。
//   - producer: 已初始化的 Sarama 同步生产者实例。
//   - dlqTopic: 死信队列的主题名称。
//   - originalMessage: 从 Kafka 消费的原始消息，在处理过程中失败。
//   - processingError: 导致原始消息处理失败的具体错误。
//   - logger: 用于结构化日志记录的 ZapLogger 实例。
//
// 返回值:
//   - error: 如果发送消息到 DLQ 的过程中发生错误，或者上下文被取消，则返回错误。
func SendToDLQ(ctx context.Context,
	producer sarama.SyncProducer,
	dlqTopic string,
	originalMessage *sarama.ConsumerMessage,
	processingError error,
	logger *core.ZapLogger) error {

	// --- 输入参数有效性检查 ---
	// 为什么进行这些检查?
	// 确保核心组件（如 producer, dlqTopic, originalMessage, logger）都已正确提供，
	// 否则无法安全地执行发送到 DLQ 的逻辑。
	if logger == nil {
		// 如果 logger 为 nil，后续的错误和信息将无法记录。
		// 这是一个严重问题，理论上不应该发生如果上游正确注入了 logger。
		// 此处打印到标准错误输出作为最后的手段，并返回错误。
		fmt.Println("严重错误: SendToDLQ 函数接收到的 logger 实例为 nil")
		return errors.New("发送到 DLQ 失败：logger 实例不能为空")
	}
	if producer == nil {
		logger.Error("发送消息到 DLQ 失败：DLQ 生产者实例 (producer) 为空",
			zap.String("original_topic", originalMessage.Topic), // originalMessage 可能为 nil，需要检查
			zap.String("dlq_topic", dlqTopic),
		)
		return errors.New("发送到 DLQ 失败：DLQ 生产者实例 (producer) 未配置")
	}
	if dlqTopic == "" {
		logger.Error("发送消息到 DLQ 失败：DLQ 主题名称 (dlqTopic) 为空",
			zap.String("original_topic", originalMessage.Topic), // originalMessage 可能为 nil
		)
		return errors.New("发送到 DLQ 失败：DLQ 主题名称 (dlqTopic) 未配置")
	}
	if originalMessage == nil {
		logger.Error("发送消息到 DLQ 失败：原始消息 (originalMessage) 为空")
		return errors.New("发送到 DLQ 失败：原始消息 (originalMessage) 不能为空")
	}

	// --- 构建消息头部 ---
	// 为什么要在头部添加这么多信息?
	// 这些头部信息提供了关于原始消息失败的上下文，对于后续分析 DLQ 中的消息至关重要。
	// 它能帮助我们理解消息为什么失败、它来自哪里以及何时失败。
	headers := []sarama.RecordHeader{
		{Key: []byte("dlq_original_topic"), Value: []byte(originalMessage.Topic)},
		{Key: []byte("dlq_original_partition"), Value: []byte(strconv.FormatInt(int64(originalMessage.Partition), 10))},
		{Key: []byte("dlq_original_offset"), Value: []byte(strconv.FormatInt(originalMessage.Offset, 10))},
		{Key: []byte("dlq_timestamp_utc"), Value: []byte(time.Now().UTC().Format(time.RFC3339Nano))}, // 强调是 UTC 时间
	}
	if processingError != nil {
		headers = append(headers, sarama.RecordHeader{Key: []byte("dlq_processing_error"), Value: []byte(processingError.Error())})
	}
	if originalMessage.Key != nil {
		// 保留原始消息的 Key，有助于在 DLQ 中追踪或按 Key 进行特定处理。
		headers = append(headers, sarama.RecordHeader{Key: []byte("dlq_original_key"), Value: originalMessage.Key})
	}
	if originalMessage.Timestamp.IsZero() { // 如果原始消息的时间戳是零值
		headers = append(headers, sarama.RecordHeader{Key: []byte("dlq_original_message_timestamp_utc"), Value: []byte("original_timestamp_is_zero")})
	} else {
		headers = append(headers, sarama.RecordHeader{Key: []byte("dlq_original_message_timestamp_utc"), Value: []byte(originalMessage.Timestamp.UTC().Format(time.RFC3339Nano))})
	}

	// --- 创建生产者消息 ---
	dlqMessage := &sarama.ProducerMessage{
		Topic:   dlqTopic,                                  // 目标是 DLQ 主题。
		Value:   sarama.ByteEncoder(originalMessage.Value), // 消息体使用原始消息的 Payload。
		Headers: headers,                                   // 附加上下文头部信息。
		Key:     sarama.ByteEncoder(originalMessage.Key),   // 保留原始消息的 Key。
		// Timestamp 字段可以由 Sarama 自动设置，或者如果需要精确控制，可以设置为 time.Now()。
		// 如果原始消息的 Timestamp 很重要，也可以考虑将其作为 DLQ 消息的 Timestamp，但这取决于业务需求。
		// Timestamp: originalMessage.Timestamp, // 例如，如果想保留原始消息的时间戳
	}

	// --- 发送消息到 DLQ ---
	// 为什么要在 goroutine 中发送并使用 select 和 context?
	// `producer.SendMessage` 是一个同步（阻塞）调用。将其放入 goroutine 中，
	// 并结合 `select` 和 `ctx.Done()`，可以实现对发送操作的超时控制或允许其被外部取消。
	// 如果不这样做，当 Kafka Broker 无响应时，`SendMessage` 可能会无限期阻塞。
	sendResultChan := make(chan struct { // 使用结构体封装结果，更清晰
		partition int32
		offset    int64
		err       error
	}, 1)

	go func() {
		partition, offset, err := producer.SendMessage(dlqMessage)
		sendResultChan <- struct {
			partition int32
			offset    int64
			err       error
		}{partition, offset, err}
	}()

	select {
	case res := <-sendResultChan:
		if res.err != nil {
			logger.Error("发送消息到 DLQ 失败",
				zap.String("dlq_topic", dlqTopic),
				zap.String("original_topic", originalMessage.Topic),
				zap.Int64("original_offset", originalMessage.Offset),
				zap.Error(res.err),
			)
			return fmt.Errorf("发送消息到 DLQ 失败 (原始消息偏移量 %d，主题 '%s'): %w", originalMessage.Offset, originalMessage.Topic, res.err)
		}
		logger.Info("消息成功发送到 DLQ",
			zap.String("dlq_topic", dlqTopic),
			zap.Int32("dlq_partition", res.partition),
			zap.Int64("dlq_offset", res.offset),
			zap.String("original_topic", originalMessage.Topic),
			zap.Int64("original_offset", originalMessage.Offset),
		)
		return nil
	case <-ctx.Done(): // 监听上下文的取消信号
		logger.Warn("发送消息到 DLQ 操作因上下文取消或超时而中止",
			zap.String("dlq_topic", dlqTopic),
			zap.String("original_topic", originalMessage.Topic),
			zap.Int64("original_offset", originalMessage.Offset),
			zap.Error(ctx.Err()), // 记录是取消还是超时
		)
		// 注意：此时后台的 SendMessage goroutine 可能仍在尝试发送，或者已经完成（成功或失败）。
		// 由于上下文已取消，我们不再等待其结果，而是直接返回上下文错误。
		// 上层调用者需要根据此错误决定如何处理（例如，是否重试发送到 DLQ，或将失败持久化）。
		return fmt.Errorf("发送消息到 DLQ 操作因上下文取消或超时而中止 (原始消息偏移量 %d，主题 '%s'): %w", originalMessage.Offset, originalMessage.Topic, ctx.Err())
	}
}
