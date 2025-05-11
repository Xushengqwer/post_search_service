package kafka

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/Xushengqwer/go-common/core"     // 假设这是你的日志库路径
	"github.com/Xushengqwer/post_search/config" // 假设这是你的配置包路径
	"go.uber.org/zap"
)

// ConsumerGroup 代表一个 Sarama 消费者组及其关联的处理程序 (handler)。
// 它封装了消费者组的生命周期管理、消息消费循环以及优雅关闭的逻辑。
type ConsumerGroup struct {
	cg      sarama.ConsumerGroup        // Sarama 库提供的消费者组客户端实例。
	handler sarama.ConsumerGroupHandler // 用户定义的消息处理逻辑，需实现 Sarama 的接口。
	topics  []string                    // 当前消费者组实例订阅的 Kafka 主题列表。
	// ready 通道用于从 handler 的 Setup 方法中发出准备就绪信号。
	// 这样做是为了确保在 Start 方法认为消费者完全启动前，handler 已经完成了必要的初始化。
	// ready   chan bool // 此字段在之前的代码中有，但如果 handler 内部管理其就绪状态并通过 Ready() 方法暴露，则 ConsumerGroup 本身可能不需要直接持有此通道。
	// 我们将依赖 handler 实现一个 Ready() <-chan bool 的方法，如 Start 方法中所示。
	wg      *sync.WaitGroup // WaitGroup 用于同步，确保在关闭时等待消费循环 goroutine 安全退出。
	logger  *core.ZapLogger // 注入的 Logger 实例，用于结构化日志记录。
	groupID string          // 存储消费者组的 Group ID，主要用于日志记录，方便追踪。
}

// NewConsumerGroup 初始化并设置 Kafka 消费者组实例。
// 它负责创建 Sarama 消费者组客户端，并配置其订阅的主题和消息处理器。
// 参数:
//   - cfg: 应用程序的 KafkaConfig 配置结构体，包含了 Broker 地址、Group ID、订阅主题等信息。
//   - clientConfig: 预先配置好的 Sarama 配置对象 (通常由 ConfigureSarama 函数生成)。
//   - handler: 实现了 sarama.ConsumerGroupHandler 接口的消息处理器。
//   - logger: 用于日志记录的 ZapLogger 实例。
//
// 返回值:
//   - *ConsumerGroup: 初始化成功的消费者组实例。
//   - error: 如果初始化过程中发生任何错误（如配置缺失、连接 Broker 失败等），则返回错误。
func NewConsumerGroup(
	cfg config.KafkaConfig,
	clientConfig *sarama.Config, // 注意：此参数是 *sarama.Config
	handler sarama.ConsumerGroupHandler,
	logger *core.ZapLogger,
) (*ConsumerGroup, error) {
	// --- 依赖检查 ---
	// 为什么要做这些检查?
	// 为了确保 ConsumerGroup 能够正确、安全地运行，必要的依赖项必须提供。
	// 例如，没有 logger 会导致无法记录重要信息和错误；没有 handler 则无法处理消息。
	if logger == nil {
		// 如果 logger 为空，后续操作将无法记录日志，这是一个严重的问题。
		// 返回错误而不是 panic，让调用方有机会处理这个问题。
		return nil, errors.New("初始化消费者组失败：logger 实例不能为空")
	}
	if handler == nil {
		logger.Error("初始化消费者组失败：消息处理器 (handler) 不能为空")
		return nil, errors.New("初始化消费者组失败：消息处理器 (handler) 不能为空")
	}
	if cfg.GroupID == "" {
		logger.Error("初始化消费者组失败：消费者组 ID (GroupID) 不能为空")
		return nil, errors.New("初始化消费者组失败：消费者组 ID (GroupID) 不能为空")
	}
	if clientConfig == nil {
		logger.Error("初始化消费者组失败：Sarama 客户端配置 (clientConfig) 不能为空")
		return nil, errors.New("初始化消费者组失败：Sarama 客户端配置 (clientConfig) 不能为空")
	}

	// --- 主题配置检查 ---
	// 为什么检查订阅主题?
	// 消费者组必须订阅至少一个有效的主题才能开始消费消息。
	// 如果配置的主题列表为空或包含无效的主题名称，消费者将无法工作。
	if len(cfg.SubscribedTopics) == 0 {
		logger.Error("初始化消费者组失败：订阅的主题列表 (SubscribedTopics) 不能为空")
		return nil, errors.New("初始化消费者组失败：订阅的主题列表 (SubscribedTopics) 不能为空")
	}
	validTopics := make([]string, 0, len(cfg.SubscribedTopics))
	for _, topic := range cfg.SubscribedTopics {
		if topic == "" {
			logger.Error("初始化消费者组失败：订阅的主题列表中包含空主题名称", zap.Strings("configured_topics", cfg.SubscribedTopics))
			return nil, errors.New("初始化消费者组失败：订阅的主题列表中包含空主题名称")
		}
		validTopics = append(validTopics, topic)
	}
	logger.Info("消费者将订阅以下主题", zap.Strings("topics", validTopics), zap.String("group_id", cfg.GroupID))

	// --- 创建 Sarama 消费者组客户端 ---
	// 为什么需要 NewConsumerGroup?
	// 这是 Sarama 库提供的用于创建消费者组客户端的函数。
	// 它会连接到 Kafka Broker 并准备好加入指定的消费者组。
	cg, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.GroupID, clientConfig)
	if err != nil {
		logger.Error("创建 Kafka 消费者组客户端失败",
			zap.String("group_id", cfg.GroupID),
			zap.Strings("brokers", cfg.Brokers),
			zap.Error(err),
		)
		return nil, fmt.Errorf("创建 Kafka 消费者组 '%s' 失败: %w", cfg.GroupID, err)
	}
	logger.Info("Kafka 消费者组客户端初始化成功", zap.String("group_id", cfg.GroupID))

	return &ConsumerGroup{
		cg:      cg,
		handler: handler,
		topics:  validTopics, // 使用经过验证和记录的 validTopics
		wg:      new(sync.WaitGroup),
		logger:  logger,
		groupID: cfg.GroupID,
	}, nil
}

// Start 在一个单独的 goroutine 中启动消费者组的消费循环。
// 此方法是非阻塞的。它会启动一个后台 goroutine 来处理消息的拉取和消费。
// 它还会尝试等待消息处理器 (handler) 准备就绪（如果 handler 提供了 Ready() 信号）。
// 参数:
//   - ctx: 上下文对象，用于控制消费循环的生命周期。当 ctx 被取消时，消费循环应优雅退出。
func (c *ConsumerGroup) Start(ctx context.Context) {
	c.logger.Info("准备启动消费者组",
		zap.String("group_id", c.groupID),
		zap.Strings("topics", c.topics),
	)
	c.wg.Add(1) // 增加 WaitGroup 计数器，表示有一个新的 goroutine 即将运行

	go func() {
		defer c.wg.Done() // 当 goroutine 退出时，减少 WaitGroup 计数器
		c.logger.Info("消费者组的消费 goroutine 已启动", zap.String("group_id", c.groupID))

		// 为什么使用无限循环?
		// 消费者通常需要持续运行以处理传入的消息，直到被明确停止。
		// Sarama 的 Consume 方法在正常情况下（如重平衡）会返回，循环确保在这些情况下会重新尝试 Consume。
		for {
			// Consume 方法是阻塞的，它会处理与 Broker 的连接、分区分配以及将消息传递给 handler。
			// 它只在发生不可恢复的错误、上下文被取消或消费者组关闭时返回错误。
			// 在重平衡 (rebalance) 期间，Consume 可能会正常返回 nil 错误，此时循环会再次调用 Consume 以重新加入消费者组。
			if err := c.cg.Consume(ctx, c.topics, c.handler); err != nil {
				// 检查错误类型，以决定是正常退出还是记录错误并重试。
				if errors.Is(err, sarama.ErrClosedConsumerGroup) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					// 这些是预期的错误，通常表示消费者组正在关闭或上下文已被取消。
					c.logger.Info("消费者组的消费循环已优雅停止",
						zap.String("group_id", c.groupID),
						zap.Error(err), // 记录导致停止的具体原因
					)
					return // 退出 goroutine
				}
				// 对于其他类型的错误，可能是暂时的网络问题或 Broker 问题。
				// 记录错误并尝试在短暂延迟后重试。
				c.logger.Error("消费者组 Consume 操作出错，将在短暂延迟后重试",
					zap.String("group_id", c.groupID),
					zap.Error(err),
				)

				// 为什么要有重试延迟?
				// 避免在发生持续性问题时进入快速失败的紧密循环 (tight loop)，这会消耗大量 CPU 和日志资源。
				// 延迟给予系统恢复的时间。
				select {
				case <-time.After(5 * time.Second): // 等待 5 秒
					c.logger.Info("延迟结束，尝试重新执行 Consume 操作", zap.String("group_id", c.groupID))
				case <-ctx.Done(): // 在延迟期间，如果外部上下文被取消，也应立即退出。
					c.logger.Info("消费者组在重试延迟期间，上下文被取消，将退出",
						zap.String("group_id", c.groupID),
						zap.Error(ctx.Err()),
					)
					return // 退出 goroutine
				}
			}

			// 为什么在这里再次检查 ctx.Err()?
			// Consume 方法可能因为重平衡等原因正常返回 (err == nil)。
			// 但此时外部服务可能已经请求关闭 (ctx 被取消)。
			// 如果不检查，循环会继续，这可能不是期望的行为。
			if ctx.Err() != nil {
				c.logger.Info("上下文已取消，退出消费者组的消费循环",
					zap.String("group_id", c.groupID),
					zap.Error(ctx.Err()),
				)
				return // 退出 goroutine
			}
			// 如果 Consume 正常返回 (通常是因为重平衡)，记录信息并继续循环，以便重新加入消费。
			c.logger.Info("Consume 调用正常结束 (可能发生重平衡)，将重新尝试加入消费", zap.String("group_id", c.groupID))
		}
	}()

	// 等待 handler 准备就绪的信号。
	// 为什么需要这个?
	// 有些 handler 可能需要在其 Setup 方法中执行一些异步初始化操作（例如，连接数据库、加载缓存）。
	// 通过提供一个 Ready() 通道，handler 可以通知 ConsumerGroup 它已准备好处理消息。
	// 这可以防止 ConsumerGroup 过早地认为服务已完全启动。
	// 类型断言用于检查 handler 是否实现了这个可选的 Ready() 接口。
	if chProvider, ok := c.handler.(interface{ Ready() <-chan bool }); ok {
		c.logger.Info("正在等待消费者消息处理器 (handler) 准备就绪...", zap.String("group_id", c.groupID))
		select {
		case <-chProvider.Ready(): // 等待 handler 发送就绪信号
			c.logger.Info("消费者消息处理器 (handler) 已准备就绪", zap.String("group_id", c.groupID))
		case <-ctx.Done(): // 如果在等待 handler 就绪时上下文被取消
			c.logger.Warn("在等待消息处理器 (handler) 就绪时，上下文被取消",
				zap.String("group_id", c.groupID),
				zap.Error(ctx.Err()),
			)
			// 这里的行为取决于业务需求：
			// 1. 可以选择让 Start 方法返回，表示启动未完全成功。
			// 2. 或者像当前这样，记录警告并继续，让消费 goroutine 尝试运行。
			//    如果 handler 未就绪但消费 goroutine 开始了，handler 内部需要能处理这种情况。
		}
	} else {
		// 如果 handler 没有实现 Ready() 方法，则跳过等待。
		c.logger.Info("消费者消息处理器 (handler) 未提供 Ready() 通道，跳过就绪状态确认", zap.String("group_id", c.groupID))
	}

	c.logger.Info("消费者组已启动，消费 goroutine 正在运行",
		zap.String("group_id", c.groupID),
		zap.Strings("subscribed_topics", c.topics),
	)
}

// Close 优雅地关闭消费者组。
// 它会首先尝试关闭底层的 Sarama 消费者组客户端，然后等待所有内部的消费 goroutine 完成。
// 返回值:
//   - error: 如果在关闭 Sarama 客户端时发生错误，则返回该错误。
func (c *ConsumerGroup) Close() error {
	c.logger.Info("开始关闭消费者组...", zap.String("group_id", c.groupID))

	// 为什么先关闭 Sarama Consumer Group?
	// 调用 cg.Close() 会通知 Sarama 停止从 Kafka 拉取新消息，并开始关闭与 Broker 的连接。
	// 这通常会使得正在进行的 Consume 调用返回 sarama.ErrClosedConsumerGroup 错误，从而使消费 goroutine 优雅退出。
	closeErr := c.cg.Close() // 将错误存储起来，以便后续处理
	if closeErr != nil {
		// 即使关闭 Sarama 客户端失败，也应继续尝试等待 goroutine 退出。
		c.logger.Error("关闭 Sarama 消费者组客户端时发生错误",
			zap.String("group_id", c.groupID),
			zap.Error(closeErr),
		)
	} else {
		c.logger.Info("Sarama 消费者组客户端已成功请求关闭", zap.String("group_id", c.groupID))
	}

	// 等待后台的消费 goroutine 退出。
	// 为什么需要带超时的等待?
	// c.wg.Wait() 会阻塞，直到消费 goroutine 调用了 c.wg.Done()。
	// 如果消费 goroutine 由于某种原因卡住，不带超时的 Wait 会导致 Close 方法无限期阻塞。
	// 使用一个单独的 goroutine 来执行 Wait，并配合 select 和 time.After 实现超时，是一种更健壮的做法。
	c.logger.Info("正在等待消费者组的消费 goroutine 退出...", zap.String("group_id", c.groupID))
	finished := make(chan struct{})
	go func() {
		c.wg.Wait()     // 等待 Add(1) 对应的 Done() 被调用
		close(finished) // 当 Wait 完成后，关闭 finished 通道以发出信号
	}()

	waitTimeout := 15 * time.Second // 定义超时时间
	select {
	case <-finished:
		c.logger.Info("消费者组的消费 goroutine 已成功退出", zap.String("group_id", c.groupID))
	case <-time.After(waitTimeout):
		c.logger.Warn("等待消费者组的消费 goroutine 退出超时",
			zap.String("group_id", c.groupID),
			zap.Duration("timeout_duration", waitTimeout),
		)
		// 如果 cg.Close() 没有错误，但这里超时了，我们需要返回一个新的错误表明 goroutine 未能正常退出。
		if closeErr == nil {
			return fmt.Errorf("关闭消费者组 '%s' 时，等待内部 goroutine 退出超时 (%v)", c.groupID, waitTimeout)
		}
		// 如果 cg.Close() 本身就有错误，那么优先返回那个错误，但可以附加超时信息。
		// 或者，如果希望超时错误更优先，可以覆盖 closeErr。这里我们选择优先返回 closeErr，但日志已记录超时。
	}

	// 返回在 cg.Close() 时发生的错误（如果有的话）
	if closeErr != nil {
		return fmt.Errorf("关闭消费者组 '%s' 失败 (Sarama 客户端关闭错误): %w", c.groupID, closeErr)
	}

	c.logger.Info("消费者组已成功关闭", zap.String("group_id", c.groupID))
	return nil
}
