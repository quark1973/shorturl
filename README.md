# 短链接服务

基于 go-zero 实现的短链接微服务，支持长链转短链、短链跳转、Leaf 号段发号器、Redis 缓存治理和基础压测。

## 功能概览

- 长链接转短链接：`POST /convert`
- 短链接访问跳转：`GET /:shortUrl`，返回 `302 Found`
- 同一个长链接重复转换返回同一个短链
- Leaf segment 号段发号器，支持双 buffer 预加载
- base62 编码生成短码
- MySQL 持久化长短链映射
- Redis 缓存 `surl -> lurl`
- 缓存穿透治理：BloomFilter + 空值缓存
- 缓存击穿治理：singleflight 合并同一短码回源
- 缓存雪崩治理：TTL 随机抖动
- Redis 异常降级 MySQL
- 支持 Redis 单机和 Redis Cluster 配置切换
- 飞书机器人事件回调：群聊长链自动转短链并回复
- Redis 记录飞书短链归属，便于后续按企业、群聊、用户维度统计

## 技术栈

- Go
- go-zero
- MySQL
- Redis
- go-redis
- singleflight
- 飞书开放平台 API

## 核心流程

### 转短链流程

```text
POST /convert
  -> validator 校验参数
  -> 校验 longUrl 可访问
  -> longUrl 计算 md5
  -> 根据 md5 查 MySQL，存在则直接返回原短链
  -> 判断输入不能是已生成短链
  -> Leaf 双 buffer 发号器获取数字 ID
  -> base62 编码生成 shortCode
  -> 写入 short_url_map
  -> 写 Redis 缓存 shorturl:surl:{shortCode} -> longUrl
  -> shortCode 加入 BloomFilter
  -> 返回 ShortDomain + shortCode
```

### 短链访问流程

```text
GET /:shortUrl
  -> validator 校验 path 参数
  -> BloomFilter 判断 shortCode 是否可能存在
  -> Redis 查询 shorturl:surl:{shortCode}
  -> 命中正常值：302 跳转 longUrl
  -> 命中空值：返回短链接不存在
  -> 未命中：singleflight 合并同 shortCode 并发请求
  -> 回源 MySQL 查询 short_url_map
  -> 查到：回写 Redis 并 302 跳转
  -> 查不到：写 60 秒空值缓存并返回短链接不存在
```

### 飞书群聊转链流程

```text
飞书群聊发送包含长链接的文本消息
  -> 飞书开放平台回调 POST /feishu/event
  -> 校验 VerificationToken
  -> 只处理 im.message.receive_v1 文本消息
  -> 从消息 content.text 中提取 http/https 长链接
  -> 跳过当前 ShortDomain，避免把短链再次转链
  -> 复用 convert 逻辑生成短链接
  -> 写 Redis 记录短链归属 shorturl:feishu:attr:{shortCode}
  -> 调用飞书消息回复 API，把短链回复到原消息所在群聊
```

## Leaf 号段发号器

本项目参考美团 Leaf segment 模式实现发号器。

数据库只保存每个业务已经分配到的最大 ID：

```text
biz_tag = short_url
max_id  = 已分配最大 ID
step    = 每次申请的号段长度
```

应用内存中使用双 buffer：

```text
current buffer：当前正在发号的号段
next buffer：后台预加载的下一段
```

当当前号段消耗到 75% 时，后台异步申请下一段。当前号段用完后，如果 next buffer 已经准备好，直接切换，减少号段切换时请求线程等待 MySQL 的延迟抖动。

## Redis 缓存设计

### Key 设计

```text
shorturl:surl:{shortCode} -> longUrl
shorturl:bloom:surl       -> BloomFilter bitmap
```

### TTL

- 正常短链缓存：`ShortUrlCacheTTL`，默认 86400 秒
- TTL 随机抖动：额外增加 0 到 10 分钟，降低同一时间大量 key 过期导致的雪崩风险
- 空值缓存：60 秒，用于缓解不存在短码反复访问造成的缓存穿透

### Redis 部署

本地单机 Redis：

```yaml
Redis:
  Addrs:
    - localhost:6379
  Cluster: false
  Password: "123456"
  DB: 0
```

Redis Cluster：

```yaml
Redis:
  Addrs:
    - 10.0.0.1:7000
    - 10.0.0.2:7001
    - 10.0.0.3:7002
  Cluster: true
  Password: "your-password"
  DB: 0
```

业务代码使用 go-redis 的 `UniversalClient`，单机和集群只通过配置切换。

### 飞书归属缓存

飞书机器人生成短链后，会额外写入一条归属缓存：

```text
shorturl:feishu:attr:{shortCode} -> {
  "tenantKey": "企业/租户标识",
  "chatId": "群聊 ID",
  "chatType": "group",
  "senderId": "发送用户 open_id/user_id/union_id",
  "messageId": "原始消息 ID",
  "longUrl": "原始长链",
  "shortUrl": "生成短链",
  "createdAt": 1710000000
}
```

这条数据目前先放 Redis，方便演示“按企业、群聊、用户维度统计”的设计思路。生产环境如果要做长期报表，可以后续把点击日志和归属关系同步到 MySQL、ES 或 ClickHouse。

## 建表 SQL

### 号段表

```sql
CREATE TABLE `sequence` (
    `biz_tag` varchar(128) NOT NULL COMMENT 'business key',
    `max_id` bigint(20) unsigned NOT NULL DEFAULT '0' COMMENT 'allocated max id',
    `step` int(10) unsigned NOT NULL DEFAULT '1000' COMMENT 'segment size',
    `description` varchar(256) NOT NULL DEFAULT '' COMMENT 'description',
    `update_time` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`biz_tag`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='leaf segment sequence table';

INSERT INTO `sequence` (`biz_tag`, `max_id`, `step`, `description`)
VALUES ('short_url', 0, 1000, 'short url id segment')
ON DUPLICATE KEY UPDATE `step` = VALUES(`step`);
```

### 长短链映射表

```sql
CREATE TABLE `short_url_map` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT '主键',
    `create_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `create_by` varchar(64) NOT NULL DEFAULT '' COMMENT '创建人',
    `is_del` tinyint unsigned NOT NULL DEFAULT '0' COMMENT '是否删除: 0 正常, 1 删除',
    `lurl` varchar(2048) DEFAULT NULL COMMENT '长链接',
    `md5` char(32) DEFAULT NULL COMMENT '长链接 MD5',
    `surl` varchar(11) DEFAULT NULL COMMENT '短链接码',
    PRIMARY KEY (`id`),
    KEY `idx_is_del` (`is_del`),
    UNIQUE KEY `uniq_md5` (`md5`),
    UNIQUE KEY `uniq_surl` (`surl`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='长短链映射表';
```

## API

```go
syntax = "v1"

type ConvertRequest {
    LongUrl string `json:"longUrl" validate:"required"`
}

type ConvertResponse {
    ShortUrl string `json:"shortUrl"`
}

type ShowRequest {
    ShortUrl string `path:"shortUrl" validate:"required"`
}

type ShowResponse {
    LongUrl string `json:"longUrl"`
}

service shortener-api {
    @handler COnvertlHandler
    post /convert (ConvertRequest) returns (ConvertResponse)

    @handler FeishuEventHandler
    post /feishu/event

    @handler ShowHandler
    get /:shortUrl (ShowRequest) returns (ShowResponse)
}
```

## 配置示例

```yaml
Name: shortener-api
Host: 0.0.0.0
Port: 8888

ShortUrlDB:
  DSN: root:123456@tcp(localhost:3306)/url?charset=utf8mb4&parseTime=True&loc=Local

Sequence:
  DSN: root:123456@tcp(localhost:3306)/url?charset=utf8mb4&parseTime=True&loc=Local
  BizTag: short_url
  Step: 1000

ShortDomain: qimi.cn/

Redis:
  Addrs:
    - localhost:6379
  Cluster: false
  Password: "123456"
  DB: 0

ShortUrlCacheTTL: 86400
ShortUrlBloomBits: 20000000

Feishu:
  AppID: ""
  AppSecret: ""
  VerificationToken: ""
  APIBase: https://open.feishu.cn/open-apis
```

## 启动

安装依赖：

```bash
go mod tidy
```

启动服务：

```bash
go run shortener.go
```

启动成功：

```text
Starting server at 0.0.0.0:8888...
```

## 本地验证

### Redis 连通性

如果 Redis 运行在 Docker，容器名为 `myredis`：

```bash
docker exec myredis redis-cli -a 123456 PING
```

返回：

```text
PONG
```

### 转短链

PowerShell：

```powershell
Invoke-RestMethod `
  -Uri "http://localhost:8888/convert" `
  -Method Post `
  -ContentType "application/json" `
  -Body '{"longUrl":"https://www.baidu.com"}'
```

响应示例：

```json
{"shortUrl":"qimi.cn/wh"}
```

### 查看 Redis 缓存

```bash
docker exec myredis redis-cli -a 123456 GET shorturl:surl:wh
docker exec myredis redis-cli -a 123456 TTL shorturl:surl:wh
```

示例：

```text
https://www.baidu.com
86590
```

### 短链跳转

```bash
curl -i -L --max-redirs 0 http://localhost:8888/wh
```

响应示例：

```text
HTTP/1.1 302 Found
Location: https://www.baidu.com
```

### 不存在短链

```bash
curl -i http://localhost:8888/notexist123456
```

响应示例：

```text
HTTP/1.1 400 Bad Request
短链接不存在
```

### 飞书事件回调

1. 在飞书开放平台创建企业自建应用，并开启机器人能力。
2. 在事件订阅中配置请求地址：

```text
https://你的公网域名/feishu/event
```

本地开发可以使用内网穿透，把公网地址转发到 `http://localhost:8888/feishu/event`。

3. 在应用配置里填写：

```yaml
Feishu:
  AppID: "cli_xxx"
  AppSecret: "xxx"
  VerificationToken: "xxx"
  APIBase: https://open.feishu.cn/open-apis
```

4. 订阅事件：

```text
im.message.receive_v1
```

5. 申请并开通机器人读取消息、发送消息或回复消息相关权限，然后发布应用版本到企业。

6. 把机器人拉入群聊，在群里发送：

```text
帮我转一下 https://www.baidu.com
```

机器人会回复类似：

```text
已生成短链接：
https://www.baidu.com -> qimi.cn/wh
```

飞书平台第一次保存事件订阅地址时会发送 URL 验证请求，服务会返回：

```json
{"challenge":"飞书传入的 challenge"}
```

如果消息里没有可转的长链接、消息不是文本类型，或者事件不是 `im.message.receive_v1`，服务会直接返回成功，不会回复群消息。

## 测试与压测

### 自动化测试

```bash
go test ./...
```

覆盖内容：

- base62 编码
- Leaf 发号器并发唯一性
- Leaf 双 buffer 预加载
- `/convert` 写 Redis 缓存和 Bloom
- 重复转链返回同一个短链
- `/show` Redis 命中
- Redis 断连降级 MySQL
- 空值缓存
- singleflight 合并并发回源
- BloomFilter 拦截不存在短码
- 飞书 URL 验证
- 飞书消息文本解析和长链提取

### 发号器 benchmark

```bash
go test ./pkg/leaf -bench=BenchmarkSegmentGeneratorNext -benchmem
```

本机一次结果：

```text
BenchmarkSegmentGeneratorNext-24    26506508    46.24 ns/op    0 B/op    0 allocs/op
```

### 接口压测

压测短链跳转：

```bash
go run ./cmd/loadtest -url http://localhost:8888/wh -method GET -c 32 -n 1000
```

压测转链：

```bash
go run ./cmd/loadtest -url http://localhost:8888/convert -method POST -body '{"longUrl":"https://www.baidu.com"}' -c 16 -n 200
```

输出包含：

```text
Requests
Concurrency
Success
Failed
Elapsed
QPS
P50
P95
P99
```

本地验证过的结果示例：

```text
GET /wh
Success: 1000
Failed: 0
QPS: 1812.90
P50: 15.7072ms
P95: 19.3837ms
P99: 73.2669ms
```

```text
POST /convert
Success: 200
Failed: 0
QPS: 6419.29
P50: 1.1395ms
P95: 13.1964ms
P99: 13.8085ms
```

## 项目亮点

- 使用 Leaf segment 双 buffer 发号器，降低每次转链访问数据库发号的压力。
- 短码使用 base62 编码，比 Snowflake 直接转字符串更短。
- 使用 Redis 缓存热点短链访问，MySQL 作为最终数据源。
- 针对缓存穿透、击穿、雪崩分别使用 BloomFilter、空值缓存、singleflight 和 TTL 抖动治理。
- Redis 使用 go-redis UniversalClient，支持本地单机和生产 Redis Cluster。
- 接入飞书开放平台事件回调，实现企业内部群聊长链自动转短链。
- 通过 Redis 记录飞书短链归属，为后续点击统计按企业、群聊、用户聚合打基础。
- 提供自动化测试和压测工具，便于验证核心链路。

## 后续可优化

- 对不存在短链返回更规范的 HTTP 404。
- `connect.Get` 支持 301/302 跳转，并增加 SSRF 防护。
- BloomFilter 可进一步改成批量 pipeline 写入，优化启动加载速度。
- 增加 Docker Compose，一键启动 MySQL、Redis 和服务。
- 飞书归属关系可以落库，并结合 Nginx access 日志、EFK 或 ClickHouse 做点击报表。
