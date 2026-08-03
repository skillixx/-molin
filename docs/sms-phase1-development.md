# 阿里云短信验证码阶段 1 开发说明

## 1. 代码结构

| 目录/文件 | 作用 |
|---|---|
| `server/migrations/000058_add_sms_phase1_foundation.*.sql` | 验证码兼容迁移与三张短信基础表 |
| `server/internal/modules/sms/model` | 模板、场景绑定、脱敏发送日志模型 |
| `server/internal/modules/sms/repository` | 有效数据库绑定查询和发送日志写入 |
| `server/internal/modules/sms/sender` | 稳定 Sender 端口、Mock 和阿里云 V2 Go SDK 适配器 |
| `server/internal/modules/sms/service` | 配置/白名单/绑定校验、提交和日志编排 |
| `server/internal/modules/auth/service/verification_service.go` | 手机验证码 `pending → accepted/failed` 状态机 |
| `server/internal/config/config.go` | 短信配置加载与启动前 fail-closed 校验 |

## 2. 数据库变更

`000055` 已提供统一的 `code_hash CHAR(64)`、`send_status`、`accepted_at` 和 `business_request_no`。`000058` 只新增短信专属的 `provider`、`provider_request_id` 和查询索引，短信与邮件统一使用 `pending → accepted/failed`，避免形成两套验证码消费规则。

新建：

- `sms_templates`：数据库模板快照。
- `sms_scene_bindings`：五个固定场景的唯一绑定。
- `sms_send_logs`：只保存脱敏手机号、独立 HMAC、模板/签名快照、请求标识、提交终态和安全失败摘要。

migration 同时提供 up/down。down 只删除短信专属字段、索引和三张短信表，必须保留归属 `000055` 的邮件验证码基础字段与约束。集成测试要求 `SMS_MIGRATION_TEST_DSN` 指向名称以 `molin_sms_test_` 开头的隔离 MySQL 数据库，防止误操作共享测试库或生产库。

## 3. 配置契约

阶段 1 支持并校验：

- `SMS_ENABLED`
- `SMS_PROVIDER`
- `SMS_ALIYUN_ACCESS_KEY_ID`
- `SMS_ALIYUN_ACCESS_KEY_SECRET`
- `SMS_ALIYUN_SIGN_NAME`
- `SMS_ALIYUN_ENDPOINT`
- `SMS_PHONE_HMAC_SECRET`
- `SMS_TEST_MODE`
- `SMS_TEST_PHONE_WHITELIST`

`infra/.env.example` 默认 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`。开启时供应商只能为 `aliyun`，HMAC 密钥至少 32 字节；测试模式白名单为空时拒绝启动。旧凭证键 `SMS_ACCESS_KEY`、`SMS_ACCESS_SECRET`、`SMS_SIGN_NAME` 只做键存在性检查，不读取为新配置，也不会自动兼容或回退。运行时代码完全不读取 `SMS_TEMPLATE_CODE_*`；旧模板键只在本次交付的静态环境文件审计中识别。

现场检查发现 `infra/.env.test` 仍包含旧短信键名；本次没有覆盖其中任何私密值。若后续启用短信，必须先迁移到新键并删除旧键，否则启动校验会 fail-closed。

## 4. Sender 与错误归一化

`Sender` 只暴露统一请求与结果，不让 auth 直接依赖阿里云 SDK。阿里云适配器使用官方 `dysmsapi20170525` V2 Go SDK，关闭 SDK 自动重试并设置连接/读取超时。阶段 1 默认开关关闭，因此启动时不会构造或调用真实 Sender。

错误分为：超时、限流、签名、模板、欠费/账户、网络和其他拒绝。对外和落库只使用中文安全摘要，不保存完整供应商错误。Dispatcher 同时维护不含敏感字段的受理/失败进程内计数，供后续监控适配器采集；进程重启后计数清零，数据库发送日志仍是持久化排障依据。

## 5. 测试入口

```powershell
cd server
go test ./internal/config ./internal/modules/sms/... ./internal/modules/auth/...
go test -v ./migrations -run TestSMSPhase1MigrationUpDown
go test ./...
go vet ./...
```

第二条命令在未配置隔离 DSN 时会明确跳过，不能登记为 migration 已通过。Windows 当前 `CGO_ENABLED=0`，`go test -race` 无法执行，应交由带 CGO/C 工具链的 Linux CI 补跑。

2026-08-03 已使用 SHA-256 校验通过的 MySQL 8.0.46 官方免安装包，在系统临时目录创建仅监听 `127.0.0.1` 的一次性实例和 `molin_sms_test_` 前缀数据库；`000058` up/down、64 位哈希、三张短信表、唯一约束、并发重复写入及非 `accepted`/过期验证码不可消费全部通过。测试服务结束后关闭，临时目录未纳入仓库，也未连接项目现有数据库。

## 6. 阶段门禁

代码完成后必须先由测试工程师执行隔离 MySQL migration 升降级、全量 auth 业务回归和敏感信息扫描，再由产品经理确认。两道验收均通过前不得进入阶段 2，不得开启真实短信、增加 `/api/admin/sms/*`、开发管理页面或执行生产迁移。

## 7. 当前验收状态

2026-08-03，短信阶段 1 已在最新 `origin/main` 上形成集成提交 `71018e9`：保留 DirectMail 的统一 `code_hash` 与 `pending → accepted/failed` 安全模型，短信复用同一消费状态机。全量测试、71 张表的全新数据库 migration、`000058` up/down/up、五类手机验证码关闭态、五类邮箱验证码回归、数据库敏感数据检查以及桌面/移动端浏览器验证均通过。测试工程师复核和产品经理本地业务确认均通过，详见 `docs/sms-phase1-acceptance-report.md`。

阶段 1 已通过 PR #314 完成远端检查、正式评审并压缩合并到 `main`，合并提交为 `3aa8f3e`，Git 流程已经闭环。`SMS_ENABLED` 继续保持 `false`；该结论仍不代表已部署或生产可用。阶段 2 仅允许在基于最新 `main` 的独立分支内开发，真实短信、测试环境变更和远端操作继续执行独立授权门禁。
