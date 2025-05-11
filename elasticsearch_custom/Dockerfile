# 使用官方的 Elasticsearch 8.13.4 镜像作为基础
FROM docker.elastic.co/elasticsearch/elasticsearch:8.13.4

# 将预先下载好的 IK 插件 ZIP 文件复制到镜像中的一个临时路径
COPY elasticsearch-analysis-ik-8.13.4.zip /tmp/elasticsearch-analysis-ik-8.13.4.zip

# 从镜像内的本地文件路径安装 IK 分词器插件
RUN elasticsearch-plugin install -b file:///tmp/elasticsearch-analysis-ik-8.13.4.zip

# 安装完成后删除临时的 ZIP 文件 (暂时注释掉，因为上一步权限问题导致失败)
# RUN rm -f /tmp/elasticsearch-analysis-ik-8.13.4.zip