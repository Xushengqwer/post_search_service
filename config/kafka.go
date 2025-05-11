package config

import "time"

// ConsumerGroupConfig 包含 Sarama 消费者组客户端的一些特定配置项。
type ConsumerGroupConfig struct {
	SessionTimeoutMs int    `mapstructure:"sessionTimeoutMs" default:"30000"` // 会话超时时间（毫秒）。
	AutoOffsetReset  string `mapstructure:"autoOffsetReset" default:"latest"` // 起始消费策略 ("latest" 或 "earliest")。
	// HeartbeatIntervalMs int `mapstructure:"heartbeatIntervalMs" default:"3000"` // 心跳间隔，通常是 SessionTimeoutMs 的 1/3
}

// ProducerConfig 包含用于发送消息到 kafka（特指 DLQ）的生产者客户端配置。
type ProducerConfig struct {
	Acks           string        `mapstructure:"acks" default:"all"`           // 确认级别 ("all", "1", "0")。
	RequestTimeout time.Duration `mapstructure:"requestTimeout" default:"10s"` // 同步生产者发送请求的超时时间。
	// Compression    string        `mapstructure:"compression" default:"none"`   // 消息压缩类型 (none, gzip, snappy, lz4, zstd)
	// MaxMessageBytes int          `mapstructure:"maxMessageBytes" default:"1000000"` // 允许发送的最大消息大小
}

// KafkaConfig 包含 kafka 消费者及其关联的死信队列（DLQ）生产者的所有配置。
type KafkaConfig struct {
	Brokers          []string            `mapstructure:"brokers"`                                                          // kafka Broker 地址列表。
	GroupID          string              `mapstructure:"groupId"`                                                          // 消费者组 ID。
	SubscribedTopics []string            `mapstructure:"subscribedTopics" json:"subscribedTopics" yaml:"subscribedTopics"` // 新增：订阅的主题列表
	DLQTopic         string              `mapstructure:"dlqTopic"`                                                         // 死信队列主题名称。
	KafkaVersion     string              `mapstructure:"kafkaVersion" default:"2.8.0"`                                     // Kafka 集群版本 (例如 "2.8.0")，用于 Sarama 兼容性。
	MaxRetryAttempts uint64              `mapstructure:"maxRetryAttempts" default:"3"`                                     // 处理消息失败时的最大重试次数。
	ConsumerGroup    ConsumerGroupConfig `mapstructure:"consumerGroup"`                                                    // 消费者组详细设置。
	Producer         ProducerConfig      `mapstructure:"producer"`                                                         // DLQ 生产者设置。
}
