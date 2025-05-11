package kafka

import (
	"fmt"
	"time"

	"github.com/IBM/sarama"                     // 导入 Sarama Kafka 客户端库
	"github.com/Xushengqwer/go-common/core"     // 假设这是你的日志库路径
	"github.com/Xushengqwer/post_search/config" // 假设这是你的配置包路径
	"go.uber.org/zap"
)

// ConfigureSarama 根据应用程序的 Kafka 配置，创建一个适用于消费者和生产者的 Sarama 配置对象。
// 此函数旨在将应用层配置（config.KafkaConfig）与 Sarama 库的配置细节解耦。
// 参数:
//   - cfg: 应用程序的 KafkaConfig 配置结构体。
//   - logger: 用于记录配置过程中的信息和警告的 ZapLogger 实例。
//
// 返回值:
//   - *sarama.Config: 配置好的 Sarama 配置对象。
//   - error: 如果配置过程中发生严重错误（例如无效的 Kafka 版本），返回错误。
func ConfigureSarama(cfg config.KafkaConfig, logger *core.ZapLogger) (*sarama.Config, error) {
	saramaCfg := sarama.NewConfig() // 初始化一个新的 Sarama 配置对象

	// --- Kafka 版本设置 ---
	// 为什么要配置 Kafka 版本?
	// Sarama 客户端需要知道与之通信的 Kafka Broker 的版本，以确保兼容性并启用相应的功能。
	// 显式配置版本有助于避免因版本不匹配导致的潜在问题或行为不一致。
	if cfg.KafkaVersion != "" {
		version, err := sarama.ParseKafkaVersion(cfg.KafkaVersion)
		if err != nil {
			logger.Error("无效的 Kafka 版本配置",
				zap.String("configured_version", cfg.KafkaVersion),
				zap.Error(err))
			return nil, fmt.Errorf("无效的 Kafka 版本配置 '%s': %w", cfg.KafkaVersion, err)
		}
		saramaCfg.Version = version
		logger.Info("使用 Kafka 版本", zap.String("version", version.String()))
	} else {
		// 如果未在应用配置中指定 Kafka 版本，Sarama 会使用其内部的默认版本。
		// 记录一条警告，因为显式配置通常是更安全的做法。
		logger.Warn("未在配置中指定 Kafka 版本，将使用 Sarama 的默认版本。建议显式配置以确保兼容性。")
	}

	// --- 消费者设置 ---

	// 为什么要设置重平衡策略?
	// 当消费者组中的消费者数量发生变化（例如，有新的消费者加入或离开）时，Kafka 会触发重平衡。
	// RebalanceStrategy 定义了分区如何重新分配给消费者。
	// `sarama.NewBalanceStrategyRoundRobin()`: 轮询策略，将分区逐个分配给消费者。这是一种简单且公平的策略，适用于大多数情况。
	// 其他策略如 `Sticky` (粘性策略) 可以减少重平衡时分区的移动，但配置更复杂。
	saramaCfg.Consumer.Group.Rebalance.Strategy = sarama.NewBalanceStrategyRoundRobin()

	// 为什么要配置初始偏移量 (auto.offset.reset)?
	// 当消费者组首次启动，或者其先前提交的偏移量在 Broker 上已过期/不可用时，此设置决定了从何处开始消费。
	// "earliest" (`sarama.OffsetOldest`): 从主题中最早的可用消息开始消费。
	// "latest" (`sarama.OffsetNewest`): 从主题中最新的消息开始消费（即忽略历史消息）。
	// 选择哪种策略取决于业务需求。对于需要处理所有历史数据的场景，选 "earliest"；如果只关心新消息，选 "latest"。
	if cfg.ConsumerGroup.AutoOffsetReset == "earliest" {
		saramaCfg.Consumer.Offsets.Initial = sarama.OffsetOldest
		logger.Info("消费者初始偏移量设置为 'earliest' (OffsetOldest)")
	} else {
		saramaCfg.Consumer.Offsets.Initial = sarama.OffsetNewest // 默认为 "latest"
		logger.Info("消费者初始偏移量设置为 'latest' (OffsetNewest)")
	}

	// 为什么要配置会话超时 (session.timeout.ms)?
	// Broker 使用此超时来检测消费者是否存活。如果 Broker 在此时间内未收到消费者的心跳，
	// 会认为消费者已死，并触发重平衡。
	// 应用配置中的 `SessionTimeoutMs` 允许灵活调整。
	if cfg.ConsumerGroup.SessionTimeoutMs > 0 {
		saramaCfg.Consumer.Group.Session.Timeout = time.Duration(cfg.ConsumerGroup.SessionTimeoutMs) * time.Millisecond
		logger.Info("消费者会话超时设置为", zap.Duration("timeout", saramaCfg.Consumer.Group.Session.Timeout))
	} else {
		// 如果应用未配置，提供一个合理的默认值。30秒是一个常见的选择。
		// 依赖 Sarama 库的默认值可能导致不同版本行为不一，显式设置更稳妥。
		saramaCfg.Consumer.Group.Session.Timeout = 30 * time.Second
		logger.Info("消费者会话超时使用默认值", zap.Duration("timeout", saramaCfg.Consumer.Group.Session.Timeout))
	}

	// 为什么要配置心跳间隔 (heartbeat.interval.ms)?
	// 消费者会定期向 Broker 发送心跳以表明其存活。此间隔通常应小于会话超时（推荐为其1/3）。
	// Sarama 通常会根据 Session.Timeout 自动计算一个合理的心跳间隔。
	// 如果需要精细控制，可以取消注释并设置:
	// saramaCfg.Consumer.Group.Heartbeat.Interval = saramaCfg.Consumer.Group.Session.Timeout / 3
	// logger.Info("消费者心跳间隔设置为", zap.Duration("interval", saramaCfg.Consumer.Group.Heartbeat.Interval))

	// 为什么要禁用自动提交偏移量 (enable.auto.commit)?
	// `saramaCfg.Consumer.Offsets.AutoCommit.Enable = false`
	// 这是实现可靠消息处理的关键！
	// 如果启用自动提交，Sarama 会在后台定期自动提交消费者获取到的最高偏移量。
	// 这可能导致消息在处理完成前就被错误地标记为“已提交”，若此时应用崩溃，消息会丢失。
	// 禁用自动提交后，应用程序需要在消息被成功处理后，手动调用 `MarkMessage` 和 `CommitOffsets` (或 `MarkOffset`) 来精确控制偏移量的提交时机。
	// 这样可以确保消息至少被处理一次 (at-least-once semantics)。
	saramaCfg.Consumer.Offsets.AutoCommit.Enable = false
	logger.Info("消费者偏移量自动提交已禁用，将由应用程序手动管理。")

	// 可选：调整 Fetch 设置，以优化消费性能和网络负载。
	// saramaCfg.Consumer.Fetch.Min = 1 // 每次 fetch 响应的最小字节数。增加此值可减少 fetch 次数，但可能增加延迟。
	// saramaCfg.Consumer.Fetch.Default = 1048576 // 1MB，每次 fetch 请求获取消息的最大字节数。
	// saramaCfg.Consumer.Fetch.MaxWaitTime = 500 * time.Millisecond // 如果没有足够数据满足 Fetch.Min，Broker 等待的最大时间。

	// --- 生产者设置 (主要用于向 DLQ 发送消息) ---

	// 为什么要设置 Return.Successes 和 Return.Errors?
	// `saramaCfg.Producer.Return.Successes = true`
	// `saramaCfg.Producer.Return.Errors = true`
	// 对于同步生产者 (`sarama.SyncProducer`)，这两个选项必须都设置为 `true`。
	// `Return.Successes`: 使得生产者在消息成功发送后，能从 `Successes` 通道接收到 `ProducerMessage`。
	// `Return.Errors`: 使得生产者在消息发送失败后，能从 `Errors` 通道接收到 `ProducerError`。
	// 这允许调用方明确知道每条消息的发送结果。
	saramaCfg.Producer.Return.Successes = true
	saramaCfg.Producer.Return.Errors = true
	logger.Info("生产者配置：将返回成功和失败的发送结果 (适用于 SyncProducer)。")

	// 为什么要配置生产者超时 (request.timeout.ms)?
	// `saramaCfg.Producer.Timeout`
	// 这是同步生产者等待 Broker 确认消息的最长时间。如果超时，发送将被视为失败。
	// 合理设置此值可以避免生产者长时间阻塞。
	if cfg.Producer.RequestTimeout > 0 { // 假设 RequestTimeout 是 time.Duration 类型
		saramaCfg.Producer.Timeout = cfg.Producer.RequestTimeout
		logger.Info("生产者请求超时设置为", zap.Duration("timeout", saramaCfg.Producer.Timeout))
	} else {
		saramaCfg.Producer.Timeout = 10 * time.Second // 提供一个合理的默认值
		logger.Info("生产者请求超时使用默认值", zap.Duration("timeout", saramaCfg.Producer.Timeout))
	}

	// 为什么要配置生产者确认级别 (acks)?
	// `saramaCfg.Producer.RequiredAcks`
	// ACKS 控制了生产者在认为消息发送成功之前需要等待多少个 Broker 副本的确认。
	// 这是消息持久性和吞吐量之间的权衡。
	// - `sarama.WaitForAll` (acks=-1): Leader 等待所有 ISR (In-Sync Replicas) 的确认。最高的数据持久性，但延迟较高。
	// - `sarama.WaitForLocal` (acks=1): Leader 写入本地日志后即发送确认。吞吐量较高，但如果 Leader 失败且消息未复制，可能丢失。
	// - `sarama.NoResponse` (acks=0): 生产者不等待任何确认。吞吐量最高，但数据丢失风险最大。
	// 对于 DLQ 这类可能需要高可靠性的场景，`WaitForAll` 通常是好的选择。
	originalAcks := cfg.Producer.Acks
	var acksModeStr string // 用于存储 RequiredAcks 的字符串表示
	switch originalAcks {
	case "all", "-1":
		saramaCfg.Producer.RequiredAcks = sarama.WaitForAll
		acksModeStr = "WaitForAll (-1)"
	case "1", "leader":
		saramaCfg.Producer.RequiredAcks = sarama.WaitForLocal
		acksModeStr = "WaitForLocal (1)"
	case "0", "none":
		saramaCfg.Producer.RequiredAcks = sarama.NoResponse
		acksModeStr = "NoResponse (0)"
	default:
		saramaCfg.Producer.RequiredAcks = sarama.WaitForAll // 默认使用 WaitForAll 以保证最高持久性
		acksModeStr = "WaitForAll (-1) [默认]"
		logger.Warn("无效的生产者 ACKS 配置，将使用 'all' (WaitForAll)",
			zap.String("configured_acks", originalAcks),
			zap.String("used_acks_description", acksModeStr),
		)
	}
	// 使用转换后的字符串 acksModeStr 进行日志记录
	logger.Info("生产者确认级别 (ACKS) 设置为",
		zap.String("acks_mode_description", acksModeStr),
		zap.String("configured_value", originalAcks),
		zap.Int16("acks_value_internal", int16(saramaCfg.Producer.RequiredAcks)), // 同时记录内部 int16 值
	)

	// 根据需要添加其他生产者配置, 例如:
	// saramaCfg.Producer.Compression = sarama.CompressionSnappy // 开启压缩以减少网络带宽
	// saramaCfg.Producer.MaxMessageBytes = 1000000 // 生产者能发送的最大消息大小
	// saramaCfg.Producer.Idempotent = true // 开启幂等生产者，防止消息重复 (需要 Broker 版本 >= 0.11.0.0 且 acks=all)

	// --- 安全设置 (示例 SASL/PLAIN, 根据需要取消注释和配置) ---
	// if cfg.Security.Enabled { // 假设 cfg 中有 Security 结构体
	//     saramaCfg.Net.SASL.Enable = true
	//     saramaCfg.Net.SASL.Mechanism = sarama.SASLTypePlaintext // 或 SCRAMSHA256, SCRAMSHA512
	//     saramaCfg.Net.SASL.User = cfg.Security.Username
	//     saramaCfg.Net.SASL.Password = cfg.Security.Password
	//     // saramaCfg.Net.TLS.Enable = true // 如果使用 TLS
	//     // Configure TLS settings...
	//     logger.Info("Kafka 安全配置已启用", zap.String("sasl_mechanism", string(saramaCfg.Net.SASL.Mechanism)))
	// }

	return saramaCfg, nil
}
