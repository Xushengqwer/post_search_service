package main

import (
	"encoding/json"
	"flag"
	"log" // 标准日志库，用于早期错误输出
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/Xushengqwer/go-common/core"
	"github.com/Xushengqwer/go-common/models/enums" // 导入您的枚举类型
	"github.com/Xushengqwer/post_search/config"
	internalKafka "github.com/Xushengqwer/post_search/internal/core/kafka" // 为内部 kafka 包使用别名
	"github.com/Xushengqwer/post_search/internal/models"
	"go.uber.org/zap"
)

func main() {
	// --- 0. 配置和基础设置 ---
	var configFile string
	defaultConfigPath := filepath.Join("..", "..", "config", "config.development.yaml")

	flag.StringVar(&configFile, "config", defaultConfigPath, "指定配置文件的路径 (相对于当前工作目录或绝对路径)")
	flag.Parse()

	if !filepath.IsAbs(configFile) {
		absPath, err := filepath.Abs(configFile)
		if err != nil {
			log.Fatalf("无法将配置文件路径 '%s' 转换为绝对路径: %v", configFile, err)
		}
		configFile = absPath
	}
	log.Printf("使用的配置文件: %s", configFile)

	// --- 1. 加载配置 ---
	var cfg config.PostSearchConfig
	if err := core.LoadConfig(configFile, &cfg); err != nil {
		log.Fatalf("致命错误: 加载配置文件 '%s' 失败: %v", configFile, err)
	}
	log.Println("配置文件加载成功。")

	// --- 2. 初始化 Logger ---
	logger, loggerErr := core.NewZapLogger(cfg.ZapConfig)
	if loggerErr != nil {
		log.Fatalf("致命错误: 初始化 ZapLogger 失败: %v", loggerErr)
	}
	defer func() {
		logger.Info("正在同步所有日志条目...")
		if err := logger.Logger().Sync(); err != nil {
			log.Printf("警告: ZapLogger Sync 操作失败: %v\n", err)
		}
	}()
	logger.Info("Kafka Seeder 的 Zap Logger 初始化成功。")

	// --- 3. 准备 Kafka 生产者 ---
	kafkaCfg := cfg.KafkaConfig
	if len(kafkaCfg.SubscribedTopics) == 0 {
		logger.Fatal("Kafka 配置错误：未在 subscribedTopics 中找到用于 Seeder 的目标主题。")
	}
	if len(kafkaCfg.SubscribedTopics) < 2 {
		logger.Fatal("Kafka 配置错误：subscribedTopics 至少需要包含两个主题 (一个用于审计，一个用于删除)。")
	}

	auditTopic := kafkaCfg.SubscribedTopics[0]  // 第一个主题用于 PostAudit 事件
	deleteTopic := kafkaCfg.SubscribedTopics[1] // 第二个主题用于 PostDelete 事件

	logger.Info("Kafka Seeder 将使用以下主题",
		zap.String("审计事件主题 (PostAudit)", auditTopic),
		zap.String("删除事件主题 (PostDelete)", deleteTopic),
	)

	saramaConfig, err := internalKafka.ConfigureSarama(kafkaCfg, logger)
	if err != nil {
		logger.Fatal("配置 Sarama (Kafka 客户端库) 失败", zap.Error(err))
	}

	producer, err := sarama.NewSyncProducer(kafkaCfg.Brokers, saramaConfig)
	if err != nil {
		logger.Fatal("创建 Kafka 同步生产者 (SyncProducer) 失败", zap.Error(err))
	}
	defer func() {
		logger.Info("正在关闭 Kafka 同步生产者...")
		if err := producer.Close(); err != nil {
			logger.Error("关闭 Kafka 同步生产者时发生错误", zap.Error(err))
		} else {
			logger.Info("Kafka 同步生产者已成功关闭。")
		}
	}()
	logger.Info("Kafka 同步生产者 (SyncProducer) 初始化成功并已连接。", zap.Strings("Brokers地址", kafkaCfg.Brokers))

	// --- 4. 定义帖子创建/更新的测试数据 (PostAuditEvents) ---
	testPostAuditEvents := []models.KafkaPostAuditEvent{
		{
			ID:             401, // 保留原有的
			Title:          "Seeder新增: 学习 Go 语言微服务",
			Content:        "这是通过 Seeder 添加的关于 Go 语言微服务开发的测试帖子。",
			AuthorID:       "go_micro_dev_01",
			AuthorAvatar:   "http://example.com/avatars/go_micro.png",
			AuthorUsername: "Go微服务大师",
			Status:         enums.Status(1),
			ViewCount:      200,
			OfficialTag:    enums.OfficialTag(1),
			PricePerUnit:   0.0,
			ContactQRCode:  "http://example.com/qr/go_micro_contact.png",
		},
		{
			ID:             402, // 保留原有的
			Title:          "Seeder新增: Kafka 消息队列实践",
			Content:        "通过 Seeder 添加的 Kafka 实践帖子，讨论消息传递模式。",
			AuthorID:       "kafka_guru_02",
			AuthorAvatar:   "http://example.com/avatars/kafka_guru.png",
			AuthorUsername: "Kafka专家",
			Status:         enums.Status(1),
			ViewCount:      450,
			OfficialTag:    enums.OfficialTag(0),
			PricePerUnit:   19.99,
			ContactQRCode:  "",
		},
		{ // 新增数据 1
			ID:             403,
			Title:          "探索 Elasticsearch 的聚合功能",
			Content:        "本文将深入探讨 Elasticsearch 中强大的聚合功能及其在数据分析中的应用。",
			AuthorID:       "es_analyzer_03",
			AuthorAvatar:   "http://example.com/avatars/es_agg.png",
			AuthorUsername: "数据分析师艾拉",
			Status:         enums.Status(1), // 已发布
			ViewCount:      320,
			OfficialTag:    enums.OfficialTag(0), // 普通
			PricePerUnit:   0.0,
			ContactQRCode:  "http://example.com/qr/es_agg_contact.png",
		},
		{ // 新增数据 2
			ID:             404,
			Title:          "Docker 与 Kubernetes：容器编排实战",
			Content:        "从 Docker 基础到 Kubernetes 高级部署策略，一步步掌握容器编排技术。",
			AuthorID:       "cloud_native_04",
			AuthorAvatar:   "http://example.com/avatars/k8s_pro.png",
			AuthorUsername: "云原生小王子",
			Status:         enums.Status(0), // 草稿状态
			ViewCount:      15,
			OfficialTag:    enums.OfficialTag(0),
			PricePerUnit:   49.50,
			ContactQRCode:  "",
		},
		{ // 新增数据 3
			ID:             405,
			Title:          "React 前端开发入门与进阶",
			Content:        "全面介绍 React 框架，从 JSX 语法到 Redux 状态管理，助您成为前端高手。",
			AuthorID:       "frontend_dev_05",
			AuthorAvatar:   "http://example.com/avatars/react_dev.png",
			AuthorUsername: "React爱好者莉莉",
			Status:         enums.Status(1), // 已发布
			ViewCount:      880,
			OfficialTag:    enums.OfficialTag(1), // 官方推荐
			PricePerUnit:   0.0,
			ContactQRCode:  "http://example.com/qr/react_course.png",
		},
	}

	// --- 5. 发送帖子创建/更新事件到 Kafka ---
	logger.Info("开始发送帖子创建/更新 (PostAudit) 事件到 Kafka...", zap.Int("消息数量", len(testPostAuditEvents)))
	for _, postEvent := range testPostAuditEvents {
		payloadBytes, err := json.Marshal(postEvent)
		if err != nil {
			logger.Error("序列化 KafkaPostAuditEvent 为 JSON 时发生错误",
				zap.Uint64("帖子ID", postEvent.ID),
				zap.Error(err))
			continue
		}
		eventKey := strconv.FormatUint(postEvent.ID, 10)
		msg := &sarama.ProducerMessage{
			Topic: auditTopic, // 发送到审计主题
			Key:   sarama.StringEncoder(eventKey),
			Value: sarama.ByteEncoder(payloadBytes),
		}
		logger.Debug("准备发送的消息详情 (PostAudit)",
			zap.String("消息键(Key)", eventKey),
			zap.ByteString("消息体片段(Value snippet)", payloadBytes[:min(100, len(payloadBytes))]))
		partition, offset, err := producer.SendMessage(msg)
		if err != nil {
			logger.Error("发送 PostAudit 事件到 Kafka 失败",
				zap.String("目标主题", auditTopic),
				zap.Uint64("帖子ID", postEvent.ID),
				zap.Error(err),
			)
		} else {
			logger.Info("PostAudit 事件成功发送到 Kafka",
				zap.String("目标主题", auditTopic),
				zap.Uint64("帖子ID", postEvent.ID),
				zap.Int32("分区(Partition)", partition),
				zap.Int64("偏移量(Offset)", offset),
				zap.Time("发送时间戳", time.Now()),
			)
		}
		time.Sleep(100 * time.Millisecond)
	}
	logger.Info("所有 PostAudit 事件已发送（或已尝试发送）到 Kafka。")

	// --- 6. 定义帖子删除的测试数据 (PostDeleteEvents) ---
	// 假设我们要删除上面创建的第一个帖子 (ID: 401) 和一个可能存在的旧帖子 (ID: 105)
	testPostDeleteEvents := []models.KafkaPostDeleteEvent{
		{
			Operation: "delete",
			PostID:    401, // 删除我们刚刚创建的帖子之一
		},
		{
			Operation: "delete",
			PostID:    105, // 尝试删除一个可能存在的旧帖子ID，用于测试删除不存在文档的情况
		},
		// 您可以根据需要添加更多删除事件
	}

	// --- 7. 发送帖子删除事件到 Kafka ---
	logger.Info("开始发送帖子删除 (PostDelete) 事件到 Kafka...", zap.Int("消息数量", len(testPostDeleteEvents)))
	for _, deleteEvent := range testPostDeleteEvents {
		payloadBytes, err := json.Marshal(deleteEvent)
		if err != nil {
			logger.Error("序列化 KafkaPostDeleteEvent 为 JSON 时发生错误",
				zap.Uint64("帖子ID", deleteEvent.PostID),
				zap.Error(err))
			continue
		}
		eventKey := strconv.FormatUint(deleteEvent.PostID, 10) // 删除事件通常也使用 PostID 作为 Key
		msg := &sarama.ProducerMessage{
			Topic: deleteTopic, // 发送到删除主题
			Key:   sarama.StringEncoder(eventKey),
			Value: sarama.ByteEncoder(payloadBytes),
		}
		logger.Debug("准备发送的消息详情 (PostDelete)",
			zap.String("消息键(Key)", eventKey),
			zap.ByteString("消息体片段(Value snippet)", payloadBytes[:min(100, len(payloadBytes))]))
		partition, offset, err := producer.SendMessage(msg)
		if err != nil {
			logger.Error("发送 PostDelete 事件到 Kafka 失败",
				zap.String("目标主题", deleteTopic),
				zap.Uint64("帖子ID", deleteEvent.PostID),
				zap.Error(err),
			)
		} else {
			logger.Info("PostDelete 事件成功发送到 Kafka",
				zap.String("目标主题", deleteTopic),
				zap.Uint64("帖子ID", deleteEvent.PostID),
				zap.Int32("分区(Partition)", partition),
				zap.Int64("偏移量(Offset)", offset),
				zap.Time("发送时间戳", time.Now()),
			)
		}
		time.Sleep(100 * time.Millisecond)
	}
	logger.Info("所有 PostDelete 事件已发送（或已尝试发送）到 Kafka。")

	logger.Info("所有测试数据均已处理完毕。")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func getWorkingDir() string { // 这个函数在当前版本中未被直接使用，但保留以备将来参考
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("无法获取当前工作目录: %v", err)
	}
	return wd
}
