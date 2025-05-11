# Post Search Service (帖子搜索服务)

本项目是一个基于 Go 语言构建的帖子搜索服务，利用 Kafka 作为消息队列处理帖子事件 (创建/更新、删除)，并将数据索引到 Elasticsearch 中以提供搜索功能。项目包含主应用程序、一个用于生成测试数据的 Kafka Seeder 工具，以及一套完整的 Docker Compose 环境，用于运行 Kafka, Elasticsearch 和相关的可视化工具 (Kafdrop, Kibana)。

## 项目特性

* **事件驱动架构**: 通过 Kafka 处理帖子数据变更事件。
* **全文搜索**: 使用 Elasticsearch 提供强大的帖子搜索能力。
* **中文分词**: Elasticsearch 集成了 IK Analyzer 中文分词插件。
* **Dockerized 环境**: 使用 Docker Compose 快速搭建和管理本地开发环境。
* **可视化工具**:
    * **Kafdrop**: 用于监控 Kafka 主题和消息。
    * **Kibana**: 用于可视化和探索 Elasticsearch 中的数据。
* **配置管理**: 通过 YAML 文件进行应用配置。
* **日志与追踪**: 集成了 Zap Logger 和 OpenTelemetry (配置为标准输出)。
* **API 文档**: 通过 Swagger 提供 API 文档。

## 技术栈

* **后端**: Go
* **消息队列**: Apache Kafka (KRaft 模式)
* **搜索引擎/数据库**: Elasticsearch
* **HTTP 框架**: Gin
* **Kafka 客户端 (Go)**: Sarama
* **Elasticsearch 客户端 (Go)**: go-elasticsearch
* **容器化**: Docker, Docker Compose
* **中文分词**: IK Analysis Plugin for Elasticsearch
* **Kafka 可视化**: Kafdrop
* **Elasticsearch 可视化**: Kibana

## 项目结构 (部分核心)

```
post_search/
├── cmd/
│   ├── main.go                 # 主应用程序入口
│   └── kafka_seeder/
│       └── main.go             # Kafka 测试数据生成器
├── config/
│   ├── config.development.yaml # 开发环境配置文件
│   ├── es.go
│   ├── kafka.go
│   └── postConfig.go
├── internal/
│   ├── api/                    # API 处理器 (HTTP Handlers)
│   ├── core/                   # 核心业务逻辑封装 (ES客户端, Kafka客户端/消费者/生产者)
│   ├── models/                 # 数据模型 (DTOs)
│   ├── repositories/           # 数据仓库层 (与ES交互)
│   └── service/                # 业务服务层
├── router/
│   └── router.go               # Gin 路由配置
├── Dockerfile                  # (如果主应用也需要容器化，当前未包含)
├── docker-compose.yaml         # Docker Compose 配置文件
├── elasticsearch_custom/       # Elasticsearch 自定义 Dockerfile 及相关文件
│   ├── Dockerfile
│   └── elasticsearch-analysis-ik-8.13.4.zip # IK分词器插件 (手动下载)
└── go.mod
```

## 环境准备

1.  **Docker 和 Docker Compose**: 确保您的机器上已安装 Docker Desktop (包含 Docker Compose)。
2.  **Go**: 确保已安装 Go 语言环境 (版本 >= 1.18，具体请参照 `go.mod` 文件)。
3.  **IK 分词器插件**:
    * 访问 [IK Analysis Plugin Releases](https://github.com/infinilabs/analysis-ik/releases) 页面。
    * 下载与 `docker-compose.yaml` 中 Elasticsearch 版本 (当前为 `8.13.4`) 对应的 `elasticsearch-analysis-ik-X.X.X.zip` 文件。
    * 将下载的 ZIP 文件放置到项目根目录下的 `elasticsearch_custom/` 文件夹中 (与该目录下的 `Dockerfile` 同级)。

## 快速开始

1.  **启动 Docker Compose 服务**:
    在项目根目录 (`E:\Doer_xyz\post_search\` 或您本地的路径) 打开终端，执行以下命令：
    ```bash
    # (可选) 如果是首次或 Dockerfile 有更改，先构建 Elasticsearch 自定义镜像
    docker-compose build elasticsearch

    # 启动所有服务 (Kafka, Elasticsearch, Kibana, Kafdrop)
    docker-compose up -d
    ```
    等待所有容器启动并健康运行。您可以通过 `docker-compose ps` 查看状态。

2.  **运行主应用程序 (`post_search`)**:
    打开一个新的终端，在项目根目录 (`E:\Doer_xyz\post_search\`) 执行：
    ```bash
    go run cmd/main.go # 或者 go run main.go (如果 main.go 在根目录，根据您的实际结构调整)
    ```
    或者，如果您更喜欢从 `cmd/main.go` 所在目录运行：
    ```bash
    cd cmd # (或者您 main.go 所在的具体路径)
    go run main.go -config ../config/config.development.yaml # 确保配置文件路径正确
    ```
    主应用启动后会开始监听 Kafka 主题，并提供 API 服务。

3.  **运行 Kafka Seeder (可选, 用于发送测试数据)**:
    如果您想向 Kafka 发送一些测试数据：
    打开一个新的终端，切换到 `kafka_seeder` 目录并运行：
    ```bash
    cd cmd/kafka_seeder
    go run main.go
    ```
    *注意：Seeder 使用的配置文件路径 (`../../config/config.development.yaml`) 是相对于 `cmd/kafka_seeder/` 目录的。*

## 访问服务和工具

* **主应用程序 API**:
    * Swagger UI: `http://localhost:8080/swagger/index.html` (假设您的 Go 应用监听 8080 端口，并配置了 Swagger)
    * 健康检查: `http://localhost:8080/api/v1/_health`
    * 搜索 API: `http://localhost:8080/api/v1/search?q=关键词`
* **Kafka (通过 Docker Compose 暴露)**: `localhost:9092`
* **Elasticsearch (通过 Docker Compose 暴露)**: `http://localhost:9200`
* **Kafdrop (Kafka 可视化)**: `http://localhost:9000` (在 `docker-compose.yaml` 中配置)
* **Kibana (Elasticsearch 可视化)**: `http://localhost:5601` (在 `docker-compose.yaml` 中配置)

## Kibana 使用提示

1.  首次访问 Kibana (`http://localhost:5601`)，点击 “自行探索”。
2.  点击左上角汉堡菜单 (☰)，选择 "Discover"。
3.  如果提示，创建一个数据视图 (Data View):
    * **Name**: 例如 `posts_index`
    * **Index pattern**: 输入 `posts_index`
    * **Timestamp field**: 选择 `updated_at` (如果可用)
    * 点击 "Create data view"。
4.  在 "Discover" 页面，调整右上角的时间范围选择器 (例如 "Last 7 days") 以查看数据。

## 注意事项

* 确保 `elasticsearch_custom/elasticsearch-analysis-ik-X.X.X.zip` 的版本与 `docker-compose.yaml` 中 Elasticsearch 的版本严格对应。
* `kafka_seeder` 程序每次运行时都会发送固定的测试数据。
* 主应用程序的 Kafka 消费者配置了 `auto.offset.reset: "latest"`，这意味着它只会处理在其启动后到达 Kafka 主题的新消息。如果想处理旧消息，需要先启动主应用再运行 Seeder，或者修改此配置/重置消费者组偏移量。

## 未来可改进点 (TODO)

* 完善单元测试和集成测试。
* 优化 Elasticsearch 查询和映射。
* 实现更复杂的搜索功能 (如高亮、聚合、建议等)。