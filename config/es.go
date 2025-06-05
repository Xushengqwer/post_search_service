package config

// IndexSpecificConfig 定义了单个 Elasticsearch 索引的特定配置，如分片和副本数。
// 我们将为每个需要独立配置的索引使用这个结构。
type IndexSpecificConfig struct {
	Name             string `mapstructure:"name" json:"name" yaml:"name"`                                     // 索引的名称
	NumberOfShards   int    `mapstructure:"numberOfShards" json:"numberOfShards" yaml:"numberOfShards"`       // 该索引的主分片数量
	NumberOfReplicas int    `mapstructure:"numberOfReplicas" json:"numberOfReplicas" yaml:"numberOfReplicas"` // 该索引的每个主分片的副本数量
}

// ESConfig 定义了 Elasticsearch 的连接和索引配置
type ESConfig struct {
	Addresses []string `mapstructure:"addresses" json:"addresses" yaml:"addresses"`
	Username  string   `mapstructure:"username" json:"username" yaml:"username"`
	Password  string   `mapstructure:"password" json:"password" yaml:"password"`

	// 主帖子索引的配置
	PrimaryIndex IndexSpecificConfig `mapstructure:"primaryIndex" json:"primaryIndex" yaml:"primaryIndex"`

	// 热门搜索词索引的配置
	HotTermsIndex IndexSpecificConfig `mapstructure:"hotTermsIndex" json:"hotTermsIndex" yaml:"hotTermsIndex"`
}
