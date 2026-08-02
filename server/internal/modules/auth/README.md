# 认证模块

负责邮箱注册、手机号注册、邮箱登录、手机号登录、验证码、JWT、Refresh Token、退出登录和当前用户信息接口。

## DirectMail 邮件模板与验证码发送（Phase 2）

### 功能说明

本模块为平台管理员提供 DirectMail 模板只读镜像、五场景绑定、本地启停、全量原子同步、测试邮箱白名单、测试发送、概览与发送日志接口。认证流程通过稳定 `EmailOTPSender` 发送邮箱验证码；验证码只在供应商明确受理后变为可校验状态。

使用角色：具有对应 `email:template:*` 权限且已完成双重认证的管理员；公开注册、登录和找回密码用户；已登录的换绑邮箱用户。

核心规则：

- 固定 `register/login/reset_password/bind_email/admin_verify` 五场景，不允许运行时新增。
- 模板变量固定为 `Code` 与 `ExpireMinutes`，大小写必须完全一致。
- 发送时从已同步冻结的 `TemplateText` 本地渲染两个固定变量，`SingleSendMail` 只提交 `Subject + HtmlBody`，不得提交 `Template.TemplateId/Template.TemplateData`；供应商 TemplateId 仅用于绑定、日志和追踪。
- 发送前要求主题为有效 UTF-8、非空且不超过 100 个 Unicode 字符；渲染正文为有效 UTF-8、非空且按 UTF-8 字节不超过 80 KiB。缺变量、大小写错误、畸形/额外占位符或残留变量均失败关闭。
- DirectMail 模板在阿里云控制台维护，平台不调用创建、修改或删除模板 API。
- 正式 OTP 与测试发送只调用 `SingleSendMail`；`accepted` 只表示供应商受理，不表示最终送达。
- 完整邮箱只允许在合法请求处理内存中短暂存在；`verification_codes` 邮件行、白名单、发送日志和审计只保存 HMAC 或脱敏值，邮件行的 `target_value` 必须为 NULL。
- Phase 2 不包含生产凭据配置、真实外部发送、投递回执 Webhook、打开率或点击率。

管理接口为 `GET /api/admin/email/summary`、模板列表/详情/启停、场景列表/绑定、模板同步/同步记录、测试白名单、模板测试发送和发送日志。所有列表响应为 D-95 扁平分页。

### 开发结构

- `model/email.go`：五类邮件表模型。
- `repository/email_repo.go`：分页、乐观锁、全量同步事务、白名单和日志持久化。
- `service/email_adapter.go`：生产 DirectMail Adapter 与显式非生产 Mock；接口只暴露三个 RAM Allow action。
- `service/email_service.go`：五场景规则、幂等、同步、脱敏和 OTP 状态机。
- `handler/email_handler.go`：冻结的 `/api/admin/email/*` HTTP 契约。
- `route.go`：四权限码与管理员双重认证组合路由。

状态流转：邮件 OTP `pending -> accepted|failed`；测试发送日志内部 `pending -> accepted|failed`；公开日志仅 accepted/failed，pending 不进列表与概览；同步 `running -> succeeded|failed`；白名单 `active <-> revoked`。
同步running超过5分钟仅为陈旧候选，取得同一sync lease后才可收敛；原任务仍续租时不得标failed。同步事务首尾用run状态fencing，最终RowsAffected非1则整批回滚。测试发送pending按五分钟规则收敛。管理写动作采用attempt/result审计，清理失败可观测。

### 000055 增量 migration 精确交付清单

`000055_add_directmail_email_management.up.sql/down.sql` 已在真实 MySQL 8.4.10 通过核心验收：新库 1→55、旧库 54→55、55→54、ownership 预存组合、元数据冲突和三类未知引用均通过；partial-up 16/16、partial-down 15/15 共 31 个故障注入全部通过，注入均 `exit=1` 且 `information_schema`/ownership 状态与断点一致；另有无注入 up/down 各 1 次，均 `exit=0` 并恢复目标结构。schema 核对为 35 个 CHECK（业务+verification 33，ownership 2）、7 个外键、35 个索引（五业务表按 `(table_name,index_name)` 计 26，含 verification+ownership 共 35）；合法模板/绑定/白名单/accepted 日志 CRUD 通过，大写 SHA、非法外键、删除已绑定模板、accepted 日志缺少 RequestId 均被拒绝。54/55 状态库代表性备份恢复后 up/down 成功。最终工作树已用隔离 Go 1.25.0 与临时 modfile 重跑 `go test -count=1 ./...`、`go build ./...`、`go vet ./...` 全通过，仓库 `go.mod`、`go.sum` 未修改。000020 最小兼容修复已获产品经理批准、实施并通过：真实 `golang-migrate` + MySQL 8.4.10 空库 1→55 为 version55/dirty0，19→20→19→20 与 55→54→55 均 dirty0，三索引、五邮件表 0→5、ownership 4 正确，同一 v20 库继续到 55 未重放 000020；测试服务器 MySQL 8.0.46 只读审计为 version54/dirty0 且三索引正确。全程未使用 force，当前 up/down SHA256 为 `C91CB6A30CE6577C3CC88BE18CEADFC03406435172A03D61D39A7014EB8AB9A8` / `921521A7863E2FE7DC95A067267198C2E690537367D9A729C73F11D3FD81070C`，与 ADR 一致。Redis 基础设施 P1、其余四个邮件场景、RAM 否定矩阵和完整 E2E 尚未通过，上线前仍须逐项完成以下门禁。历史迁移边界为：除该 000020 修复外，不修改其他 000001-000054 migration；000055 自身可按本功能验收结果继续修正：

> 状态更正（2026-07-30）：上一段末尾“Redis 基础设施 P1、其余四个邮件场景尚未通过”属于早期历史状态，已被后续证据取代。当前五场景均已有一次真实 accepted、人工收件和一次业务消费；真实 Redis lease 已通过；000057 技术可逆周期已通过且两个新增隔离资产冻结保留。仍未关闭的是 000055/000056 剩余隔离矩阵、历史 Redis unknown 夹具 cleanup 与修复后新周期、五场景真实重放/过期、真实模板测试发送故障矩阵、RAM 最小权限/Deny、同窗运行时敏感扫描及最终 QA/PM 签署。accepted 仍只表示供应商受理，不等于最终送达。

1. up 保留旧 `verification_codes.code`，扩为 `VARCHAR(64) NULL`，并新增 `code_hash CHAR(64)`；历史歧义值全部安全失效。新应用只读写 code_hash，旧 code 仅供 down 后旧应用使用。
2. 新增状态与邮箱目标列。维护窗必须暂停全部发码并等待至少10分钟；所有历史 email 行置 failed/过期/已使用，逐行生成不可关联随机占位 target_hash（不用应用HMAC密钥）、统一 masked 占位并清空 target_value。只有新 email 行写真实HMAC；phone继续使用 target_value。
3. 按顺序创建 `email_provider_templates`、`email_scene_bindings`、`email_template_sync_runs`、`email_test_recipient_allowlist`、`email_send_logs` 五张业务表；发送日志状态必须包含 `pending/accepted/failed`，字段、CHECK、唯一键和普通索引逐项使用 `docs/database-schema-design.md §3.1.1`，不得弱化。
4. seed 五条 disabled 场景，映射 JSON 固定为 `{"code":"Code","expire_minutes":"ExpireMinutes"}`。
5. `migration_000055_permission_ownership` 是 migration-only 技术表，不是第六张业务表。up 先创建技术表并对四个冻结权限码各写一行取证，记录权限/admin 绑定是否预存；再只补缺失权限和缺失 admin 绑定，回填最终权限与关联 ID，并强断言四行、精确元数据、唯一 admin 角色和四条绑定一致。冲突一律 fail-closed。
6. MySQL DDL 存在隐式提交，整段 migration 不可事务回滚；每一步须可独立核对和人工恢复，失败后禁止继续执行后续 DDL。
7. down 只按 ownership 的 `admin_binding_created`、`permission_created` 标志精确删除本 migration 创建的记录，保留全部预存权限与绑定；本次创建权限存在未知角色授权、用户权限覆盖或分组权限引用时 fail-closed。写后断言 created 对象已清理后才删除 ownership 技术表，再按发送日志、白名单、同步、绑定、模板逆序删除五张业务表；最后删除 code_hash 等新增字段并保留旧 `code VARCHAR(64)`。

migration 验收必须覆盖新库 `000001 -> 000055` 与旧库仅执行 `000055` 两条路径、16 位历史截断值安全失效、64 位 hash 无截断、up/down 后手机码兼容、五场景/四权限 seed 幂等、所有 CHECK 否定写入以及 down 保持 `VARCHAR(64)`。

### 000055 版本、数据预检与故障恢复矩阵

执行前必须确认 `SELECT version,dirty FROM schema_migrations` 为 `54/0`，并核对不存在同名新表、`verification_codes` 仍为旧结构、无未过期验证码、发码流量为零。另需查询 `information_schema.tables/columns/statistics/table_constraints/check_constraints` 保存迁移前结构快照，并统计验证码各 target_type、code 长度、未过期/未使用行数及目标字段空值分布；任何结果不符都停止迁移。

| 失败阶段 | information_schema 核对 | 修复或回退 | dirty 处理 |
|---|---|---|---|
| `verification_codes` ALTER/历史数据改写 | 查询 columns、statistics、table_constraints/check_constraints，逐项确认已完成到哪一列、索引或约束；抽样只看长度、状态和空值，不导出敏感值 | 应用尚未部署时优先从已验证备份恢复；若选择前向修复，必须按 000055 原顺序补齐缺失 DDL 与数据失效步骤并复核全部约束 | 禁止直接 force；完整回退到旧结构后才 `./scripts/migrate.sh force 54`，完整达到 000055 目标后才 force 55 |
| 创建五张业务表或五场景 seed 失败 | 查询 tables、columns、statistics、table_constraints/check_constraints，识别已创建的表、半成品约束和场景行 | 无应用写入时按 down 的逆依赖顺序删除半成品表后恢复至 54；或严格补齐当前表再继续下一表，禁止跳过失败 DDL；最终业务表必须恰好五张 | 结构与数据验证完成后才 force 到对应完整版本，再执行 version 确认 dirty=0 |
| ownership 创建或四行取证失败 | 查询技术表 columns/CHECK，并核对四权限码行数、权限 ID、admin 关联 ID 与两个 created 标志 | 取证不完整或无法证明 up 前事实时从备份恢复，禁止重建并猜测归属；取证可信时才允许继续补权限 | ownership 与实际记录一致前不得 force 55 |
| 补权限、补 admin 绑定、回填 ID 或写后断言失败 | 对账 permissions、唯一 admin 角色、role_permissions 与 ownership 四行，不修改无关权限 | 仅补 ownership 标记的缺项并重跑强断言；预存对象 created 标志不得改变；元数据冲突保持 fail-closed | 四权限、四绑定、四行 ownership 全部一致后才可 force 55 |
| partial-down 权限清理失败 | 对账 ownership、permissions、role_permissions、user_permission_overrides、group_permissions 及 created 标志 | 仅继续删除 created=1 对象；未知角色、用户覆盖、分组三类引用任一存在即 fail-closed；预存对象必须保留 | 写后断言通过前不得删除 ownership 或 force 54 |
| partial-down ownership/业务表/verification 清理失败 | 查询 ownership 是否存在、五张业务表剩余状态及 verification columns/index/CHECK | 权限写后断言通过后才删 ownership；再逆依赖清五业务表，最后删除 verification 增量字段 | 五业务表与技术表全清理且旧结构完整后才 force 54 |
| 迁移命令中断或 `dirty=1` | 同时执行 migrate version、查询 schema_migrations，并按上述表逐项确认最后成功语句 | 先选择“完整前向修复至55”或“从备份/逆序清理恢复至54”，记录每个修复 SQL 与复核结果 | `force` 只修版本元数据，不执行 SQL；禁止盲目 force 或在结构半完成时重跑应用 |

故障恢复完成后必须再次执行版本查询、全部 information_schema 对账、五张业务表与一张 migration-only 技术表边界核对、五场景/四权限/四行 ownership 对账和验证码安全抽样；确认 `dirty=0` 后才允许进入应用部署。

### 发布与回滚门禁

1. 停止邮箱/手机 OTP 发码、OTP 校验、注册、登录流量并确认请求为零。
2. 等待 10 分钟，让全部旧 OTP 过期；随后停止全部 auth/API 实例。
3. 备份数据库并在隔离库验证可恢复，再执行版本、结构和数据预检。
4. 执行 000055 up，确认 version=55、dirty=0，并核对 schema、CHECK、五张业务表、一张 migration-only 技术表、五场景、四权限及 ownership 四行。
5. 部署全部新版本应用实例，依次核验 `/api/health`、`/api/ready`、应用版本、schema 版本和邮件配置。
6. 所有检查通过后才恢复流量；真实 DirectMail 验证仍走独立门禁。
7. 回滚固定为：停止上述流量 → 等待 10 分钟 → 停止全部实例 → 备份并验证可恢复 → 先执行 000055 down（删除 `code_hash` 并保留 `code VARCHAR(64) NULL`）→ 部署旧版本应用 → 核验 health 与 schema → 恢复流量。禁止滚动部署，禁止新旧应用共存，也禁止先启动旧应用访问新 schema。

本地验证命令：`go test ./internal/modules/auth/... ./internal/modules/audit/... ./internal/modules/iam/... ./internal/middleware/...`、`go test ./...`、`go build ./...`、`go vet ./...`。真实 RAM 三个 Allow 的显式 Deny 与三个禁用 action 探测必须在具备最小权限测试账号后由 QA 外部验证，Mock 结果不能替代。

### Phase 4 自动化覆盖与远端写保护

后端本地自动化新增以下不联网断言：

- 五个固定场景分别使用当前数据库绑定解析出的供应商 `TemplateId` 做日志追踪，并从当前模板镜像正文将固定 `Code`、`ExpireMinutes=10` 渲染为 `HtmlBody`；测试夹具中的五个已审核 ID 只用于对照，不进入生产业务常量。
- 供应商明确受理后，验证码和发送日志才允许收敛为 `accepted`；供应商失败时二者必须为 `failed`，且不得伪造供应商 RequestId。
- 发送日志只保存收件人 HMAC/脱敏值，不包含完整邮箱、OTP、AccessKey 或 TemplateData。
- 生产及未知环境即使误开调试开关，也不得在验证码发送响应中返回 `code`；只有显式安全非生产环境才允许调试回码。

`tests/email/phase2_email_api.py` 仍是管理接口黑盒资产，不是五场景初始化器：默认禁止写；显式写模式也只操作一个 `EMAIL_SCENE` 和一个平台模板 ID，并会在末尾停用该模板。Phase 4 不得直接用它批量导入或绑定五模板。真实模板必须先通过生产 Adapter 的同步接口导入镜像，再逐一核对供应商 ID、审核状态和变量，最后以当前平台模板 ID 与当前 binding version 精确绑定。禁止直接向邮件业务表批量插入供应商模板。

### schema 54/0 只读门禁与 000055 前后验证方案

以下查询只允许在运维已经通过安全渠道注入测试数据库连接后，以只读会话执行；命令行、报告和日志不得包含密码或连接串。查询本身不授权 migration、备份、恢复或任何业务写入。

```sql
SET SESSION TRANSACTION READ ONLY;
START TRANSACTION READ ONLY;

SELECT version, dirty FROM schema_migrations;

SELECT table_name
FROM information_schema.tables
WHERE table_schema = DATABASE()
  AND table_name IN (
    'email_provider_templates', 'email_scene_bindings',
    'email_template_sync_runs', 'email_test_recipient_allowlist',
    'email_send_logs', 'migration_000055_permission_ownership'
  )
ORDER BY table_name;

SELECT column_name, column_type, is_nullable
FROM information_schema.columns
WHERE table_schema = DATABASE()
  AND table_name = 'verification_codes'
ORDER BY ordinal_position;

SELECT target_type,
       COUNT(*) AS row_count,
       SUM(expires_at >= CURRENT_TIMESTAMP) AS unexpired_count,
       SUM(used_at IS NULL) AS unused_count,
       SUM(target_value IS NULL) AS target_value_null_count
FROM verification_codes
GROUP BY target_type;

SELECT COUNT(*) AS invalid_target_type_count
FROM verification_codes
WHERE target_type NOT IN ('email', 'phone');

SELECT COUNT(*) AS admin_role_count FROM roles WHERE code = 'admin';

SELECT code, name, resource, action
FROM permissions
WHERE code IN (
  'email:template:view', 'email:template:manage',
  'email:template:sync', 'email:template:test'
)
ORDER BY code;

COMMIT;
```

迁移前必须同时满足：`version=54`、`dirty=0`；六个 000055 新表均不存在；`verification_codes.code` 存在且 `code_hash` 不存在；`admin` 角色恰好一个；权限无冻结元数据冲突；无非法 target type；停止全部发码、校验、注册和登录流量并等待至少 10 分钟后，所有历史验证码 `unexpired_count=0`。任何一项不满足立即阻断，不得 `force`。

获得独立授权后，执行顺序固定为：停止流量并确认请求为零 → 等待 10 分钟 → 停止全部实例 → 备份并在隔离库验证恢复 → 重跑上述只读快照 → 执行一次 000055 up → 只读核对 `55/0`、五业务表、一 ownership 表、五场景、四权限和四条 admin 绑定 → 部署全部新实例 → 核对 health、应用版本、schema 与邮件配置 → 再恢复流量。禁止滚动部署和新旧实例共存。

迁移后只读验证还必须覆盖：`verification_codes.code VARCHAR(64) NULL` 与 `code_hash CHAR(64) NOT NULL`；历史行全部 failed/过期/已使用；历史 email 的 `target_value` 全空且目标摘要/脱敏占位完整；历史 phone 继续保留 `target_value`；五场景初始均 disabled；五业务表、ownership、CHECK、外键和索引数量与 migration SSOT 一致。检查只统计状态、长度和空值，禁止导出完整邮箱、OTP、HMAC 或其他敏感值。

任何真实同步、绑定、白名单、测试发送或 OTP 发码必须同时具备：独立的显式总确认开关、本轮 UUID 前缀、单一精确平台模板 ID/场景/version/脱敏测试账号，以及可复核的精确清理清单。不得使用通配删除、`KEYS *`、`FLUSHDB` 或扫描后批量回滚；模板停用属于有意安全动作，不得自动恢复覆盖其他管理员的新版本。

### Redis unknown 历史夹具精确清理门禁

`TestEmailUnknownTombstoneSurvivesRedisRestart` 的 `cleanup` 阶段只用于清理本轮已经由正式只读门禁确认归属的历史测试夹具，不是通用数据清理工具。

- cleanup 在建立 MySQL 或 Redis 连接前先使用 `Lstat` 检查状态文件：必须是非符号链接的普通文件、权限精确为 `0600`，并在 Linux 测试服务器上由当前有效 UID 持有；非 Linux 的实际 cleanup 所有权检查默认失败关闭，离线测试只能通过注入元数据验证控制流。
- 状态 JSON 拒绝重复键、未知字段和尾随内容；其内容必须是 `version=1`、`phase=phase1_created`，`nonce` 为 32 位小写十六进制，Redis `run_id` 为 40 位小写十六进制；操作员、模板、白名单、原日志和意外日志五个主键都必须为正数，两个日志主键必须不同。任一字段不满足时，不建立 Redis 或数据库连接。
- 历史正式只读门禁已经确认派生锁键 `EXISTS=0`。cleanup 只在执行前再次确认该键仍不存在，不执行 `DEL`、`KEYS`、`SCAN`、`FLUSHDB` 或模式删除；数据库事务提交后再次要求 `EXISTS=0`。
- 数据库事务先以 `FOR UPDATE` 锁定同一幂等 scope 的恰好两条日志，再按完整冻结谓词分别锁定两条日志、一条测试白名单和一条模板镜像。日志同时核对供应商、供应商模板号和 `verification_code_id IS NULL`；白名单同时核对脱敏邮箱、状态、版本和创建/更新人；模板同时核对供应商、正文摘要、变量、审核、本地启用、missing 和版本等固定属性。
- 四项删除复用与锁定阶段完全相同的归属谓词，每项 `RowsAffected` 必须严格等于 1；任一归属漂移、缺行、多行或数据库错误都会回滚整个数据库事务。
- 只有数据库事务成功且 Redis 后验仍为 0 时才删除状态文件。数据库提交后 Redis 后验查询失败时保留状态文件作为人工对账证据，禁止自动重试或据此宣称数据库未发生写入。
- cleanup 不触碰 migration、000057 隔离库、备份、周期证据、其他 Redis key 或其他业务数据；正式 wrapper 仍需独立核对测试环境、`schema_migrations=57/dirty=0`、恢复点、冻结测试二进制 SHA-256 和 000057 资产前后摘要。

离线故障注入覆盖连接前非法状态、符号链接、owner 不匹配、重复/未知 JSON 字段、缺失主键、重复日志主键、Redis key 仍存在、归属漂移、事务后续失败、数据库提交后 Redis 查询失败，以及全部成功后状态文件只删除一次。远程执行前必须再运行这些定向 Go 测试，且不得用静态检查代替真实 Go 编译结果。
