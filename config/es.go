package config

// ESConfig 定义了 Elasticsearch 的连接和索引配置
type ESConfig struct {
	Addresses        []string `mapstructure:"addresses" json:"addresses" yaml:"addresses"`
	Username         string   `mapstructure:"username" json:"username" yaml:"username"`
	Password         string   `mapstructure:"password" json:"password" yaml:"password"`
	IndexName        string   `mapstructure:"indexName" json:"indexName" yaml:"indexName"`
	NumberOfShards   int      `mapstructure:"numberOfShards" json:"numberOfShards" yaml:"numberOfShards"`       // 新增：分片数量
	NumberOfReplicas int      `mapstructure:"numberOfReplicas" json:"numberOfReplicas" yaml:"numberOfReplicas"` // 新增：副本数量
}
