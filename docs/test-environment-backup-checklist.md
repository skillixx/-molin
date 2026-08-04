# 测试环境备份内容与执行清单

> 适用环境：Molin 测试服务器及使用测试服务器基础服务的开发环境。
>
> 目标：每次数据库 migration、重要部署、批量数据操作或多模态网关升级前，明确备份哪些数据、如何验证、保存多久，以及按什么顺序恢复。

## 1. 什么时候必须备份

以下操作开始前必须创建一次带时间戳的完整恢复点：

- 执行任何 up/down migration。
- 修改钱包、订单、资产、权限、实名或 AI 网关数据结构。
- 批量导入、清理、修复或重算业务数据。
- 更换数据库、MinIO、RabbitMQ 或 Redis 版本。
- 部署包含数据库写入逻辑变化的后端版本。
- 修改 `TOKEN_PROVIDER_KEY`、JWT、HMAC、支付、MinIO 等密钥。
- 上线 image/audio/video/embedding 等会创建新资产和异步任务的能力。
- 产品经理或测试工程师开始阶段性验收前。

普通前端样式修改、不涉及后端和数据结构的部署，可以不额外创建全量数据库备份，但仍要记录代码提交号。

## 2. 每次备份的内容总表

| 备份对象 | 每次必备 | 内容 | 主要用途 |
|---|---:|---|---|
| MySQL | 是 | 全库结构、数据、索引、migration 版本 | 恢复业务真相源 |
| 环境变量与密钥 | 是 | 加密/签名/连接密钥的安全副本 | 解密数据库密文、恢复鉴权和服务连接 |
| Git 与部署清单 | 是 | commit、branch、镜像摘要、migration 版本 | 找回与数据匹配的代码版本 |
| MinIO | 有文件时是 | bucket 对象、策略、生命周期和版本信息 | 恢复附件、图片、音视频和生成结果 |
| RabbitMQ 定义 | 使用队列时是 | exchange、queue、binding、policy、用户权限 | 重建异步拓扑 |
| RabbitMQ 消息 | 队列非空时是 | 未完成任务对应的消息或一致性快照 | 防止异步任务丢失/重复 |
| Redis | 通常否 | 缓存、封禁、限流、一次性票据 | 无缝回滚时保留短期状态 |
| 应用日志 | 建议 | API、worker、migration、部署日志 | 追查失败原因，不作为业务恢复源 |
| 备份清单 | 是 | 文件名、大小、SHA-256、时间、操作者、校验结果 | 证明备份完整且可追溯 |

## 3. MySQL：每次必须全库备份

### 3.1 为什么不能只备份新表

项目模块存在跨表关系：购买会产生订单、钱包流水、资产、权益和消费记录；AI 网关会关联用户、API Key、商品、钱包、用量和任务。只备份 `token_models` 或新建的多模态表，无法形成可恢复的一致状态。

因此 migration 前默认执行 `molin` 全库逻辑备份，不按模块挑表。

### 3.2 全库备份必须包含的数据域

#### 账号、权限与安全

- 用户、会话、验证码和登录日志。
- 角色、权限、用户角色、动态授权和用户分组。
- 实名认证记录及审核日志。
- 审计日志和封禁相关真相数据。
- 平台 API Key 的 hash、scope/capability、状态和撤销时间。

#### 商品、订单与财务

- 商品、套餐、价格、角色价格和会员价格。
- 商品可见规则、开通处理器和按量计费规则。
- 订单、订单明细和订单状态。
- 钱包、钱包流水、冻结/预占记录。
- 支付回调、消费事件和消费记录。
- 幂等键、回调状态和对账字段。

#### 用户资产与应用

- 用户资产、权益额度、额度预占和资产事件。
- 会员等级、会员权益和用户会员关系。
- 应用、应用适配器和访问配置。
- 公告、帮助文档及其他内容元数据。

#### AI 网关与工作台

- 上游渠道、加密后的供应商 API Key。
- 模型目录、逻辑模型与上游模型映射、可见范围。
- Token 用量流水及计费金额。
- Agent、Skill、插件、MCP 和会话元数据。
- 多模态阶段新增的任务、资产、上传会话和用量指标。
- 供应商任务 ID、回调幂等键、预占和结算状态。

#### 数据库自身状态

- `schema_migrations` 当前版本和 dirty 状态。
- 表结构、索引、外键、视图、触发器、事件和存储过程（如存在）。
- 字符集、排序规则和必要的表级配置。

### 3.3 MySQL 备份文件要求

- 文件格式：`sql.gz`。
- 使用 `--single-transaction`，保证 InnoDB 一致性。
- 使用 `--hex-blob`，避免二进制字段损坏。
- 使用 `--default-character-set=utf8mb4`，避免中文乱码。
- 使用 `--set-gtid-purged=OFF`，避免恢复到不同实例时产生 GTID 冲突。
- 备份期间禁止执行 migration、批量修复或清理任务。
- 钱包、订单和异步任务仍有高频写入时，应先进入维护窗口或暂停相关 worker。

### 3.4 MySQL 校验

每次备份后至少完成：

```text
[ ] mysqldump 退出码为 0
[ ] gzip -t 校验通过
[ ] 文件大小大于 0
[ ] 能看到 CREATE TABLE 语句
[ ] 能看到 schema_migrations 数据
[ ] 生成并记录 SHA-256
[ ] 记录备份开始和结束时间
[ ] 定期恢复到临时数据库做演练
```

## 4. 环境变量和密钥：每次必须确认可恢复

数据库中存在加密或 HMAC 后的数据。只有数据库备份、没有对应密钥，恢复后仍可能无法登录、无法解密渠道密钥或无法验证历史数据。

### 4.1 核心恢复密钥

必须保存当前测试环境的安全副本：

```text
JWT_SECRET
REFRESH_TOKEN_SECRET
API_KEY_HMAC_SECRET
ID_CARD_HMAC_SECRET
TOKEN_PROVIDER_KEY
PLUGIN_SECRET_KEY
INTERNAL_API_TOKEN
NOTIFY_BODY_KEY
MOLIN_TRUST_SECRET
```

特别注意：

- `TOKEN_PROVIDER_KEY` 丢失后，`token_channels.api_key_encrypted` 无法解密。
- `PLUGIN_SECRET_KEY` 丢失后，插件凭据可能无法解密。
- `API_KEY_HMAC_SECRET` 改变后，已有平台 sk 无法继续匹配。
- `ID_CARD_HMAC_SECRET` 改变后，历史实名 hash 无法保持一致。
- JWT/Refresh 密钥改变会让已有登录状态失效。

### 4.2 基础服务连接配置

```text
MYSQL_HOST / MYSQL_PORT / MYSQL_DATABASE / MYSQL_USER / MYSQL_PASSWORD
REDIS_ADDR / REDIS_PASSWORD / REDIS_DB
RABBITMQ_URL
MINIO_ENDPOINT / MINIO_ACCESS_KEY / MINIO_SECRET_KEY / MINIO_BUCKET / MINIO_USE_SSL
```

### 4.3 外部服务配置

按实际启用情况保存：

- 支付商户号、证书序列号、支付私钥和平台公钥。
- 短信服务 Access Key、Secret、签名和模板编号。
- SMTP 地址、账号、密码和发件人。
- Presenton、上游 AI、插件、MCP 等共享密钥。
- 多模态供应商 webhook secret、回调地址和沙箱/正式环境标记。

### 4.4 密钥备份规则

- 不允许放入 Git、普通聊天记录、公开网盘或未加密压缩包。
- 使用受控密码库、Secret Manager 或加密离线介质。
- 备份清单只记录“已备份”和密钥版本/指纹，不记录明文。
- 环境文件权限至少为 `600`。
- 密钥轮换前必须同时保留旧版本，直到所有旧密文完成重加密或确认不再需要恢复。

## 5. MinIO：有业务文件时必须备份

### 5.1 数据内容

- 当前 `molin` bucket 中的所有对象。
- 用户上传附件、应用图标、帮助文档媒体。
- 实名附件（如实际使用 MinIO）。
- AI 图片输入、蒙版、生成图片。
- 音频输入、语音合成结果和字幕文件。
- 视频输入、生成视频和缩略图。
- 对象版本、删除标记（启用版本控制时）。

### 5.2 配置内容

- bucket 列表和 bucket policy。
- lifecycle、retention、versioning 和 quota。
- CORS 配置。
- 服务账号和访问策略的安全记录。

### 5.3 一致性要求

MySQL 中的对象元数据和 MinIO 对象必须属于同一个恢复点：

```text
暂停新上传/worker
  → 记录时间点
  → 备份 MySQL
  → 同步 MinIO
  → 记录对象数量和总大小
  → 恢复写入
```

多模态任务上线后，不能只恢复 MySQL 而不恢复 MinIO，否则成功任务会指向不存在的结果对象。

## 6. RabbitMQ：保存拓扑，处理未完成消息

### 6.1 每次保存 RabbitMQ 定义

- virtual host。
- exchange。
- queue。
- binding 和 routing key。
- policy、死信配置和 TTL。
- 用户与权限配置（不在普通清单中记录密码）。

### 6.2 队列消息的处理原则

RabbitMQ 消息不应作为唯一业务真相，任务状态必须保存在 MySQL。备份前：

1. 暂停生产者和 worker。
2. 记录每个队列的 ready、unacked、consumer 数量。
3. 能安全处理完的消息先排空。
4. 队列非空且必须保留时，停止 RabbitMQ 后做 volume 一致性快照。
5. 恢复后由 MySQL 任务状态与幂等键决定是否重新投递。

禁止在 worker 运行中直接复制 RabbitMQ 数据目录，也禁止只恢复消息而不恢复对应的 MySQL 任务状态。

## 7. Redis：默认可重建，特殊场景再备份

当前 Redis 主要保存：

- IAM 权限缓存。
- 用户封禁/Token 吊销的快速检查状态。
- 登录失败计数和接口限流。
- 一次性应用启动票据。
- 对话上下文热缓存。

MySQL 是主要真相源，因此普通 migration 备份不要求 Redis RDB。恢复后可以清空缓存并要求测试用户重新登录。

以下场景才备份 Redis：

- 要求无缝回滚且不能让登录状态、封禁快速状态和一次性票据丢失。
- 正在验证依赖 Redis 的并发或限流问题，需要保留现场。
- Redis 将来承载不可重建状态。

如果备份 Redis，应记录 RDB/AOF 文件、Redis 版本、DB 编号和持久化配置。恢复旧 Redis 后仍要主动清理可能与新数据库版本不一致的权限缓存。

## 8. Git、镜像和部署清单：每次必须记录

每次恢复点必须记录：

- 仓库地址。
- branch 和完整 commit SHA。
- 工作区是否干净。
- 后端二进制 SHA-256。
- 前端构建版本或 Docker image digest。
- 当前 migration 版本和 dirty 状态。
- 部署时间、操作者和变更说明。
- 使用的 `.env` 配置版本/指纹，不记录明文。

仅保存源码压缩包不够；必须记录完整 commit，确保可以重新构建相同版本。

## 9. 日志与诊断材料

日志不是业务恢复源，但建议在重要变更前后保存：

- API 日志。
- AI worker 日志。
- migration 完整输出。
- 部署脚本输出。
- RabbitMQ 队列状态。
- MySQL/MinIO/RabbitMQ/Redis 健康检查结果。
- 失败请求的 request_id、task_id 和安全错误分类。

日志中不得包含密码、平台 sk、Token、身份证号、上游 Key、文件正文或长期签名 URL。

## 10. 备份清单文件

每个恢复点创建一个不含密钥明文的 manifest，例如：

```yaml
backup_id: test-20260716-153000-before-multimodal-migration
environment: test
started_at: 2026-07-16T15:30:00+08:00
finished_at: 2026-07-16T15:38:00+08:00
reason: 多模态 AI 网关 migration 前备份
git_branch: feature/ai-gateway-foundation
git_commit: <完整 SHA>
migration_version: <版本号>
migration_dirty: false
mysql:
  file: molin-20260716-153000.sql.gz
  size: <字节数>
  sha256: <摘要>
  verified: true
minio:
  snapshot: minio-20260716-153000
  object_count: <对象数量>
  total_size: <字节数>
  verified: true
rabbitmq:
  definitions: rabbitmq-definitions-20260716-153000.json
  ready_messages: <数量>
  unacked_messages: <数量>
redis:
  backed_up: false
  reason: 可重建缓存，允许测试用户重新登录
secrets:
  vault_version: <密钥库版本或指纹>
  plaintext_in_manifest: false
verification_result: passed
operator: <操作者>
```

## 11. 备份频率与保留周期

| 类型 | 频率 | 建议保留 |
|---|---|---:|
| migration 前恢复点 | 每次 migration 前 | 至少 90 天 |
| 重要后端部署前 | 每次 | 至少 30 天 |
| 每日自动备份 | 每天 | 最近 7～14 份 |
| 每周完整备份 | 每周 | 最近 4～8 份 |
| 阶段验收基线 | 每阶段验收前 | 至少保留到下一阶段验收完成 |
| 故障现场 | 发生 P0/P1 时 | 问题关闭后至少 90 天 |

至少保存两份：

1. 测试服务器或专用备份目录一份。
2. 不在同一物理主机上的加密副本一份。

备份与源数据放在同一块磁盘上不能视为完整备份。

## 12. 恢复顺序

发生回滚时按以下顺序：

```text
停止 API、worker 和写入流量
  → 恢复对应版本的环境密钥
  → 恢复 MySQL
  → 恢复 MinIO
  → 恢复 RabbitMQ 定义
  → 根据 MySQL 状态重投未完成任务
  → 按需恢复或清理 Redis
  → 部署 manifest 对应的代码/镜像
  → 执行健康检查和业务冒烟测试
  → 恢复写入流量
```

恢复后重点对账：

- 钱包余额与钱包流水。
- 订单、支付回调、资产和权益。
- AI 任务状态、占额、用量和结算记录。
- MySQL 资产记录与 MinIO 对象是否一致。
- RabbitMQ 是否存在重复或孤立消息。
- 渠道密钥能否使用原 `TOKEN_PROVIDER_KEY` 正常解密。

## 13. 每次执行 Checklist

### 备份前

```text
[ ] 明确备份原因和回滚目标
[ ] 记录 branch、commit 和 migration 版本
[ ] 确认工作区状态
[ ] 暂停 migration、批量任务和必要的 worker
[ ] 记录 RabbitMQ 队列状态
[ ] 确认磁盘空间充足
```

### 备份中

```text
[ ] 创建 MySQL 全库备份
[ ] 同步 MinIO 数据和配置
[ ] 导出 RabbitMQ 定义
[ ] 队列非空时完成排空或一致性快照
[ ] 确认密钥库中存在当前环境版本
[ ] 按需保存 Redis 和日志现场
```

### 备份后

```text
[ ] 校验压缩包和 SHA-256
[ ] 记录文件大小、对象数、队列数
[ ] 创建 manifest
[ ] 复制第二份加密备份到异机位置
[ ] 做恢复抽查或定期完整恢复演练
[ ] 备份通过后才允许执行 migration/部署
[ ] 恢复暂停的服务和 worker
```

## 14. 安全红线

- 备份文件包含用户、财务、实名和密钥密文，应按敏感数据管理。
- 禁止提交到 Git，禁止放在 Web 根目录和公开对象 bucket。
- 禁止在命令行直接使用 `-p明文密码`，优先使用受控环境变量或客户端配置文件。
- 备份传输和异机保存必须加密。
- 定期验证备份能恢复；只有文件存在但从未验证，不能视为有效备份。
- 清理过期备份时记录操作者、时间和删除范围，避免误删最新恢复点。
