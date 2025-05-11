package config

import "github.com/Xushengqwer/go-common/config"

type PostSearchConfig struct {
	Server              config.ServerConfig `mapstructure:"server" json:"server" config.development.yaml:"server"`
	ZapConfig           config.ZapConfig    `mapstructure:"zapConfig" json:"zapConfig" config.development.yaml:"zapConfig"`
	TracerConfig        config.TracerConfig `mapstructure:"tracerConfig" json:"tracerConfig" yaml:"tracerConfig"`
	KafkaConfig         KafkaConfig         `mapstructure:"kafkaConfig" json:"kafkaConfig" config.development.yaml:"kafkaConfig"`
	ElasticsearchConfig ESConfig            `mapstructure:"elasticsearchConfig" json:"elasticsearchConfig" config.development.yaml:"elasticsearchConfig"`
}
