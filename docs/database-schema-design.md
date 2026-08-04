# 数据库表设计

## 1. 数据库基础约定

数据库使用 MySQL 8。

基础约定：

- 字符集：`utf8mb4`
- 排序规则：`utf8mb4_0900_ai_ci`
- 金额字段：`DECIMAL(18,6)`
- 时间字段：`DATETIME`；auth 邮件时间冻结为 UTC 秒级墙钟，应用显式写入，不依赖数据库会话时区默认值
- 主键：`BIGINT UNSIGNED AUTO_INCREMENT`
- 状态字段统一使用 `status`
- JSON 数据使用 `JSON` 类型

## 2. 安全约定

在实现任何表结构之前，必须确认以下安全约定：

**身份证号**

- 严禁明文存储。
- 严禁使用 SHA-256 或 MD5 直接 hash（身份证号格式已知，可被穷举）。
- 必须使用 `HMAC-SHA256(id_card_no, server_secret)`，字段名为 `id_card_no_hmac`。
- `server_secret` 通过环境变量 `ID_CARD_HMAC_SECRET` 注入，不入库、不入配置文件、不入代码仓库。
- 同时保存 `id_card_no_masked`（保留前6后4，中间替换为 `*`），用于管理后台展示。

**Refresh Token**

- 必须持久化到 `user_sessions` 表，否则退出登录和封禁用户时无法吊销。
- 数据库只存储 `HMAC-SHA256(refresh_token, server_secret)`，不存明文。
- 密钥通过环境变量 `REFRESH_TOKEN_SECRET` 注入。

**Token 供应商 API Key**

- 使用 `AES-256-GCM` 加密存储，字段名为 `api_key_encrypted`。
- 加密密钥通过环境变量 `TOKEN_PROVIDER_KEY` 注入。
- 建议在表中增加 `key_version` 字段，便于密钥轮换迁移。

**支付回调报文**

- `payment_callbacks.notify_body` 存储原始回调报文，建议加密存储（同上 AES-256-GCM）。
- 用于审计和幂等重放，不能随意清理。

## 3. 表分组

### 3.1 账号、会话、实名、权限（第一阶段）

- `users`
- `user_sessions`
- `verification_codes`
- `user_login_logs`
- `identity_verifications`
- `identity_verification_logs`
- `roles`
- `permissions`
- `user_roles`
- `role_permissions`
- `user_permission_overrides`
- `role_change_logs`
- `audit_logs`
- `email_provider_templates`
- `email_scene_bindings`
- `email_template_sync_runs`
- `email_test_recipient_allowlist`
- `email_send_logs`
- `email_admin_verify_bootstrap_receipts`

`migration_000055_permission_ownership` 是 000055 up/down 专用的 migration-only 技术表，仅记录四个权限及 admin 绑定的创建归属，
不属于 DirectMail 第六张业务表，不提供业务模型、repository 或 API。DirectMail 业务表始终只有上述五张。

000056 为首次配置 `admin_verify` 增加一张安全凭据表 `email_admin_verify_bootstrap_receipts` 和一张 migration-only 权限归属表。前者不是模板管理业务列表资源，不提供 CRUD；后者只用于 `email:template:bootstrap` 权限及 admin 绑定的精确 down。两者都不改变 000055 的“五张邮件业务表 + 一张 000055 ownership 表”历史口径。

**000056 `email_admin_verify_bootstrap_receipts` 一次性成功凭据设计**

| 字段 | 类型 | 约束/说明 |
|---|---|---|
| id | BIGINT UNSIGNED | 自增主键 |
| scope | VARCHAR(32) | 非空且唯一，固定 `admin_verify`；保证全环境只有一次成功配置 |
| provider | VARCHAR(32) | 非空，固定 `aliyun_directmail` |
| provider_template_id | VARCHAR(64) | 非空，只保存已通过 1-64 字节 ASCII 十进制正整数校验的精确供应商资源标识，不保存模板正文 |
| template_id | BIGINT UNSIGNED | 非空，外键到 `email_provider_templates.id`，删除受限 |
| idempotency_key_hash | CHAR(64) | 非空且唯一，包含 admin_id 与 raw_key 的 HMAC |
| request_fingerprint | CHAR(64) | 非空，覆盖 admin_id、method、path、provider_template_id 与 scope |
| completed_by | BIGINT UNSIGNED | 非空，外键到 `users.id`，记录实际管理员操作者 |
| created_at | DATETIME | 非空，UTC 秒级创建时间；000057 后无数据库默认值，由应用显式写入 |

表级字符集/排序规则固定 `utf8mb4/utf8mb4_0900_ai_ci`。约束固定为：scope/provider 封闭枚举；两个摘要列单独使用 `ascii_bin` 且必须为 64 位小写十六进制；`template_id`、`completed_by` 外键均 `ON DELETE RESTRICT ON UPDATE RESTRICT`。唯一 scope 保证只存在一条成功凭据，唯一 idempotency_key_hash 保证同 key 只能对应该凭据。表中只允许成功凭据，不保存 running/failed；失败尝试由 `audit_logs` 记录。

000057 只修正时间列结构，不修改 000055/000056：`email_scene_bindings.created_at/updated_at` 保持 `DATETIME` 秒精度但移除 `CURRENT_TIMESTAMP` 与 `ON UPDATE`，`email_admin_verify_bootstrap_receipts.created_at` 从 `DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3)` 收紧为无默认值的 `DATETIME NOT NULL`。三列此后均由 auth 邮件仓储显式写入 UTC 秒级墙钟。迁移不推断或换算历史时区；对非零小数秒 receipt，先写入专用持久表 `migration_000057_email_receipt_time_backup`，再只把 `created_at` 截到秒。备份表固定含一条 `receipt_id=0` manifest（记录预期数据行数，0 行也必须存在）以及每条受影响 receipt 的主键、原 `DATETIME(3)`、对应秒值和非时间字段 SHA-256 指纹，不保存模板正文、Token、邮箱、OTP 或其他业务字段。表结构固定为六列及其顺序、类型、unsigned、可空性、默认值、extra、列排序规则，固定 `InnoDB/utf8mb4_0900_ai_ci`、单列非前缀主键 `receipt_id`，并固定 manifest/receipt 形状、毫秒/秒值和小写十六进制指纹三项已启用 CHECK。up 写后保留备份表；down 只从完整 schema57、完整 manifest/数据行及指纹一致状态开始，先扩回 `DATETIME(3)`、按主键恢复原毫秒并验证，再恢复 000055/000056 默认值，最后才删除备份表。

000057 的 DDL 隐式提交恢复矩阵固定为：①备份表创建前中断，可从完整 schema56 重试；②建表、manifest 或数据复制中断，保留 dirty 和备份表，依据 expected_count/主键/原值/指纹恢复或从已验证全库备份重来，禁止删除空表后盲重跑；③秒级 UPDATE 后、ALTER 前中断，依据 original/second/指纹确认每行状态后前向完成或恢复；④任一 ALTER 后中断，按 information_schema 与持久备份定位断点，禁止 force；⑤ down 扩列、恢复数据或恢复默认值期间中断，备份表必须保留，只有原毫秒、非时间指纹和最终列结构全部通过才可删除。up 更新后、up 最终、down 恢复后、down 删除备份前四道数据门禁均必须由备份表反向 `LEFT JOIN` receipt，并同时校验 manifest 唯一、备份行数和 `expected_count`、匹配到的源回执行数及 `r.id IS NULL`；不能用 INNER JOIN 静默过滤孤儿备份。任何未知 partial、缺 manifest、expected_count 不符、缺行、孤儿主键、原值或指纹不符均失败关闭。

down 的备份表结构断言在 `information_schema.statistics` 派生表中同时投影 `index_name, non_unique`，确保 `HAVING non_unique=0` 在真实 MySQL 作用域内合法。CHECK 比较前仅使用 `REPLACE(normalized_clause, CONCAT(CHAR(92), CHAR(39)), CHAR(39))` 把反斜杠单引号规范化为单引号；禁止全量删除 `CHAR(92)`，避免破坏正则表达式中的合法反斜杠。

离线测试只冻结上述 SQL 结构与预期模型，尚未证明目标 MySQL 8 版本对 `information_schema.check_constraints.CHECK_CLAUSE` 的规范化文本完全一致；特别是括号、字符集标记以及 `REGEXP` 可能展示为 `regexp_like`。该兼容性必须在后续授权的 MySQL 8.0.46 隔离环境验证，未验证前不得把 000057 标记为可部署。

成功事务中的 `email.admin_verify.bootstrap.result` 审计必须以 `target_type=email_admin_verify_bootstrap_receipt`、`target_id=receipt` 内部十进制 ID 关联本表成功凭据；不得把供应商 TemplateId、管理员 ID 或 scene 写成 result 的审计目标。该约束不新增 receipt 字段。

000056 不持久化 bootstrap 网络安全配置。应用启动校验必须分别规范化 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS` 与 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`：两个 bootstrap 列表之间存在规范化后完全相同的 CIDR 条目时启动失败，不同前缀仅部分重叠允许；与平台既有 INTERNAL/TRUSTED CIDR 同值或重叠允许。两个 bootstrap 列表还必须各自计算 CIDR 地址并集；除单条 `/0` 及等价写法外，任一列表通过多条非零前缀覆盖完整 IPv4 或 IPv6 地址族（如 `0.0.0.0/1,128.0.0.0/1`、`::/1,8000::/1`）也必须启动失败，但不跨两个不同语义列表合并计算全地址族并集。

000056 不扩容 000055 已建立的 `email_provider_templates.provider_template_id` 和 `email_send_logs.provider_template_id`，两者继续保持 `VARCHAR(64)`。bootstrap Body 的空值、全零、65 字节及以上、非数字、符号、小数、指数或任何空白必须在 attempt 审计、Adapter 与数据库之前按 `400/40000` 拒绝，三者增量均为 0。

写事务行锁后复查receipt；重放必须completed_by、key hash、fingerprint全匹配。同admin同key并发第二个即使已Describe也返回原成功；跨admin固定409且不泄露操作者。

000056 同时新增 `email:template:bootstrap`，精确元数据为 name=`首次配置管理员邮箱认证模板`、resource=`email_template`、action=`bootstrap`。它不包含在既有 view/manage/sync/test 四权限中，也不授权普通邮件管理接口。code=`admin` 的角色必须恰好一行；0 行或多行、预存权限元数据不一致均失败关闭。

000056 新建 `migration_000056_permission_ownership`，不得复用或修改 000055 ownership。字段精确为：`permission_code VARCHAR(191)` 主键；`permission_id BIGINT UNSIGNED NULL` 且唯一；`permission_created TINYINT(1)` 非空且只能 0/1；`admin_role_permission_id BIGINT UNSIGNED NULL` 且唯一；`admin_binding_created TINYINT(1)` 非空且只能 0/1；`created_at DATETIME` 非空且默认 CURRENT_TIMESTAMP。表使用 `utf8mb4/utf8mb4_0900_ai_ci`，不加外键且只允许 `email:template:bootstrap` 一行。up 必须在插入权限/绑定前记录两项是否预存，完成后回填最终 ID；预存权限/绑定保持原 ID，只有缺失项可创建。

迁移断言临时表固定命名为 `migration_000056_assertions`，字段为 `assertion_name VARCHAR(191)` 主键与 `passed TINYINT(1)` 非空，CHECK 要求 passed=1。up/down 开始均要求其不存在，成功结束删除；partial 遗留时作为断点证据，不得直接清表或伪造 passed。

up 前 receipt 与 000056 ownership 都必须不存在。任一同名表、权限冲突或 admin 非唯一失败关闭。MySQL DDL 隐式提交，partial-up/partial-down 必须按 information_schema、permission、role_permission、ownership 和 receipt 的实际断点恢复，禁止盲目重跑或 force；优先恢复已验证备份，获批前向修复只能补缺并重跑写后断言。

常规 down 首先断言 receipt 行数为 0；存在成功 receipt 时在任何删除前失败关闭。无 receipt 时，仅删除 ownership 标记为本 migration 创建的精确 admin 绑定；本 migration 创建的权限还必须不存在其他 role_permissions、user_permission_overrides、group_permissions 引用才可删除。预存权限/绑定必须保留，写后断言通过才删除 receipt 空表和 ownership 表。回滚矩阵固定为：A）000056 未执行，执行原 000055 down；B）000056 已执行且无成功 receipt，先 000056 down、核验后再 000055 down；C）存在成功 receipt，应用回滚保留 schema 55+56、receipt、模板镜像和绑定，不执行任一 down。C 类确需回到 55 前必须另立高风险变更，先完成备份恢复验证、不可变审计留证和 QA/产品经理/运维联合批准，解除全部引用后依次 down 000056、down 000055；禁止 force。

#### 3.1.1 DirectMail 邮件模板管理（Phase 2 后端与真实 MySQL 核心已验收）

> 000055 已在真实 MySQL 8.4.10 通过存量 54→55、55→54、ownership 预存组合、元数据冲突、三类未知引用、代表性备份恢复、partial-up 16/16 与 partial-down 15/15，共 31 个故障注入；注入均 `exit=1` 且 `information_schema`/ownership 与断点一致，另有无注入 up/down 各 1 次，均 `exit=0` 并恢复目标结构。schema 口径为 35 个 CHECK（业务+verification 33，ownership 2）、7 个外键、35 个索引（五业务表按 `(table_name,index_name)` 计 26，含 verification+ownership 共 35）。000020 最小兼容修复已获产品经理批准、实施并通过：真实 `golang-migrate` + MySQL 8.4.10 空库 1→55 为 version55/dirty0 且三索引正确；19→20→19→20 均 dirty0 且索引所有权正确；同一 v20 库继续到 55 未重放 000020；55→54→55 均 dirty0，五邮件表 0→5、ownership 4；测试服务器 MySQL 8.0.46 只读审计为 version54/dirty0 且三索引正确。全程未使用 force，当前 up/down SHA256 为 `C91CB6A30CE6577C3CC88BE18CEADFC03406435172A03D61D39A7014EB8AB9A8` / `921521A7863E2FE7DC95A067267198C2E690537367D9A729C73F11D3FD81070C`，与 ADR 一致。Redis 基础设施 P1、余下四场景、RAM 否定矩阵和完整 E2E 尚未验收。所有邮箱入库前先做 trim + 完整地址小写规范化，并拒绝显示名、多地址和换行注入；
> 测试白名单和发送日志只保存 `HMAC-SHA256(normalized_email, EMAIL_ADDRESS_HMAC_SECRET)` 与脱敏值，禁止保存完整邮箱。

**既有 `verification_codes` 的全停机迁移（PM B 方案）**

已核对 `server/migrations/000001_create_core_tables.up.sql:36-48` 与
`server/internal/modules/auth/model/verification.go:6-15`：当前表把 `code` 定义为 `VARCHAR(16)`，model 只有 Code/ExpiresAt/UsedAt 等字段，
但现有 VerificationService 已写入 64 字符 SHA-256 hex；现有表和 model 都没有 `sent` 字段。后续 migration 必须基于真实结构执行以下扩展，代码不得直接假定 `sent` 存在：

| 变更后字段 | 类型 | 约束/说明 |
|---|---|---|
| code | VARCHAR(64) | 保留旧列并扩容、允许 NULL；仅旧应用回滚路径使用，新应用不再写入 |
| code_hash | CHAR(64) | 新增列；新写入只保存 SHA-256 hex，必须满足 64 位小写十六进制 |
| send_status | VARCHAR(16) | 非空，pending / accepted / failed；新邮件显式 pending，新手机显式 accepted |
| business_request_no | VARCHAR(64) | 可空且唯一；新邮件 OTP 必填，历史记录和手机验证码可为 null |
| idempotency_scope | VARCHAR(191) | 可空；新邮件 OTP 必填，历史记录和手机验证码可为 null |
| request_fingerprint | CHAR(64) | 可空；新邮件 OTP 必填，历史记录和手机验证码可为 null |
| accepted_at | DATETIME | 可空；新建且 accepted 的邮件 OTP 必填，pending/failed 为 null；所有迁移前历史行统一回填为 failed 且保持 NULL |
| target_value | VARCHAR(191) | 改为可空；仅 phone 保留规范化手机号，email 必须为 null，禁止保存完整邮箱 |
| target_hash | CHAR(64) | 可空并建立索引；email 必填，为 `HMAC-SHA256(normalized_email, EMAIL_ADDRESS_HMAC_SECRET)`，phone 为 null |
| target_masked | VARCHAR(191) | 可空；email 必填且仅为脱敏展示值，phone 为 null |

PM B 不支持滚动兼容：必须停止发码、校验、注册、登录相关流量，等待至少10分钟，停止全部 auth/API 实例，完成数据库备份并验证可恢复后才执行 up。up 保留并扩容旧 `code VARCHAR(64) NULL`，新增 `code_hash` 及状态字段；所有历史 email 行置 failed/过期/已使用，逐行生成不可关联随机占位 target_hash、写统一 masked 占位并清空 target_value。所有历史 phone 行同样统一置 `send_status=failed`、`accepted_at=NULL`、过期且已使用，仅保留 `target_value` 供回滚兼容；只有迁移后的新 phone 行才显式 accepted。只有全新应用写 code_hash，新旧应用禁止共存。

约束：`CHECK (code_hash REGEXP '^[0-9a-f]{64}$')`、
`CHECK (send_status IN ('pending','accepted','failed'))`、唯一索引
`uk_verification_business_request(business_request_no)`，另加 `CHECK (target_type IN ('email','phone'))` 和
email accepted 必须有 accepted_at、email pending/failed 必须无 accepted_at 的条件约束；验证码校验 SQL 增加 `send_status='accepted'`。
索引 `idx_verification_email_idempotency(idempotency_scope, created_at)` 支持在 scope 锁内查找冷却窗口记录；新邮件行的
business_request_no/idempotency_scope/request_fingerprint 必须同时非空，历史与 phone 行允许三者同时为 null。新增条件约束必须保证
email 行 `target_value IS NULL AND target_hash IS NOT NULL AND target_masked IS NOT NULL`，phone 行 `target_value IS NOT NULL AND target_hash IS NULL AND target_masked IS NULL`；
邮箱发送与校验 SQL 一律按 `target_hash` 查询，手机号继续按 `target_value` 查询。
down 同样要求全停机：停相关流量、等待10分钟、停全部实例并验证备份后，先执行 down 删除 `code_hash` 等新增结构并保留旧 `code VARCHAR(64)`，再部署旧应用。所有新旧在途 OTP 均明确失效，禁止尝试恢复历史明文。

**`email_provider_templates`：DirectMail 模板本地镜像**

| 字段 | 类型 | 约束/说明 |
|---|---|---|
| id | BIGINT UNSIGNED | 主键自增 |
| provider | VARCHAR(32) | 非空，当前固定 `aliyun_directmail` |
| provider_template_id | VARCHAR(64) | 非空，DirectMail `TemplateId`，不可硬编码在业务代码 |
| name | VARCHAR(64) | 非空，模板名称 |
| subject | VARCHAR(256) | 非空，模板主题 |
| sender_nickname | VARCHAR(64) | 可空，发信人昵称 |
| template_text | MEDIUMTEXT | 非空，同步后的模板正文，不包含实际验证码/收件人变量值 |
| variables_json | JSON | 非空，只保存模板变量名数组 |
| content_sha256 | CHAR(64) | 非空，用于判断内容是否变化，不用于安全凭据 |
| provider_status | VARCHAR(16) | draft / pending / approved / rejected；missing 单独由布尔字段表达 |
| review_comment | VARCHAR(512) | 可空，只存归一化审核意见，不存供应商原始响应 |
| variables_complete | TINYINT(1) | 非空默认 0；同步时仅在 Code 与 ExpireMinutes 均存在时置 1 |
| local_enabled | TINYINT(1) | 非空默认 0；新同步模板需管理员显式启用，供应商同步不得覆盖 |
| missing | TINYINT(1) | 非空默认 0；完整同步未见时置 1 |
| missing_since | DATETIME | 可空；missing 首次由 0 变 1 时写入，重新出现时清空 |
| provider_created_at | DATETIME | 可空，供应商创建时间 |
| last_synced_at | DATETIME | 非空，最近成功同步时间 |
| version | BIGINT UNSIGNED | 非空默认 1，镜像变化时递增 |
| created_at / updated_at | DATETIME | 非空；沿用 000055 的 `CURRENT_TIMESTAMP` 默认值和 updated_at 自动更新时间，000057 不修改本表 |

唯一约束 `uk_email_templates_provider_id(provider, provider_template_id)`；索引
`idx_email_templates_status(provider_status, local_enabled, missing, last_synced_at)`、
`idx_email_templates_missing_cleanup(missing, missing_since)`。约束：
`CHECK (provider='aliyun_directmail')`、`CHECK (provider_status IN ('draft','pending','approved','rejected'))`、
`CHECK (variables_complete IN (0,1))`、`CHECK (local_enabled IN (0,1))`、`CHECK (missing IN (0,1))`，以及
`CHECK ((missing=1 AND missing_since IS NOT NULL) OR (missing=0 AND missing_since IS NULL))`。
同步不得保存 DirectMail 原始 JSON/XML，也不得覆盖 `local_enabled`；供应商字段变化时递增 version。
本地启停必须执行 `UPDATE ... SET local_enabled=?,version=version+1 WHERE id=? AND version=?`，影响行数为 0 返回 40900。

**`email_scene_bindings`：固定五场景模板绑定**

| 字段 | 类型 | 约束/说明 |
|---|---|---|
| id | BIGINT UNSIGNED | 主键自增 |
| scene | VARCHAR(32) | 非空且唯一；只允许 register/login/reset_password/bind_email/admin_verify |
| provider | VARCHAR(32) | 非空，当前固定 `aliyun_directmail` |
| template_id | BIGINT UNSIGNED | 可空，逻辑关联 email_provider_templates.id |
| enabled | TINYINT(1) | 非空默认 0 |
| variable_mapping_json | JSON | 非空，固定 `{"code":"Code","expire_minutes":"ExpireMinutes"}` |
| version | BIGINT UNSIGNED | 非空默认 1，乐观锁字段 |
| updated_by | BIGINT UNSIGNED | 可空，管理员用户 ID |
| created_at / updated_at | DATETIME | 非空、UTC 秒级墙钟；000057 后无默认值或自动更新时间，应用显式写入 |

唯一约束 `uk_email_scene_bindings_scene(scene)`；索引 `idx_email_scene_bindings_template(template_id, enabled)`。
未来 migration 需 seed 五条 disabled 记录，不允许运行时新增其他 scene。更新必须使用 `scene + version` 条件并原子递增版本。
约束：`CHECK (scene IN ('register','login','reset_password','bind_email','admin_verify'))`、
`CHECK (provider='aliyun_directmail')`、`CHECK (enabled IN (0,1))`。绑定或启用前必须重新读取模板并要求 approved、local_enabled=1、missing=0、variables_complete=1。

**`email_template_sync_runs`：模板同步幂等与审计摘要**

| 字段 | 类型 | 约束/说明 |
|---|---|---|
| id | BIGINT UNSIGNED | 主键自增 |
| provider | VARCHAR(32) | 非空 |
| idempotency_scope | VARCHAR(128) | 非空，固定 `admin-email-template-sync:aliyun_directmail`，跨管理员全局语义 |
| idempotency_key_hash | CHAR(64) | 非空，只存 key 的 SHA-256，不存调用方原 key |
| request_fingerprint | CHAR(64) | 非空，请求规范化后 SHA-256；用于识别同 key 不同请求 |
| status | VARCHAR(16) | running / succeeded / failed |
| created_count / updated_count / missing_count / unchanged_count | INT UNSIGNED | 非空默认 0 |
| error_code | VARCHAR(64) | 可空，仅 failed 可有，归一化错误分类 |
| error_message | VARCHAR(255) | 可空，仅 failed 可有，可安全展示的中文消息，不存供应商错误正文 |
| created_by | BIGINT UNSIGNED | 非空，管理员用户 ID |
| started_at / completed_at / created_at | DATETIME | completed_at 可空 |

唯一约束 `uk_email_sync_idem(idempotency_scope, idempotency_key_hash)`；索引 `idx_email_sync_status(status, started_at)`、
`idx_email_sync_completed(status, completed_at)`，后者用于 summary 查询最近 succeeded.completed_at。
约束：`CHECK (provider='aliyun_directmail')`、`CHECK (status IN ('running','succeeded','failed'))`；running/succeeded 时 error_code/error_message 均为 null，
failed 时 error_code/error_message 均非 null；running 时 completed_at 为 null，succeeded/failed 时 completed_at 非 null。
同步先完整读取供应商数据，再于单一事务更新镜像和本记录；失败时镜像不变。
running 超过5分钟仅是陈旧候选：必须先成功取得同一全局同步 lease，原任务仍持锁续租时不得标 failed。同步事务开始 `FOR UPDATE` 锁定并确认 run 仍为 running，最终条件更新必须检查 RowsAffected=1，否则整批镜像回滚。

**`email_test_recipient_allowlist`：测试邮箱白名单**

| 字段 | 类型 | 约束/说明 |
|---|---|---|
| id | BIGINT UNSIGNED | 主键自增 |
| email_hmac | CHAR(64) | 非空且唯一，使用独立环境密钥 HMAC-SHA256 |
| email_masked | VARCHAR(191) | 非空，只用于后台展示 |
| status | VARCHAR(16) | active / revoked |
| version | BIGINT UNSIGNED | 非空默认 1，恢复/撤销时递增 |
| created_by / updated_by | BIGINT UNSIGNED | 非空，管理员用户 ID |
| created_at / updated_at / revoked_at | DATETIME | revoked_at 可空 |

唯一约束 `uk_email_test_allowlist_hmac(email_hmac)`；清理索引
`idx_email_test_allowlist_cleanup(status, revoked_at)`。约束：
`CHECK (status IN ('active','revoked'))`，以及 active 时 revoked_at 为 null、revoked 时 revoked_at 非 null。
重复新增 active 记录返回冲突；重新添加尚未清理的 revoked 邮箱执行恢复并递增 version，不创建第二条。
revoked 满 30 天后物理删除该行，审计日志继续保留不含完整邮箱的撤销事实；完整邮箱只存在于单次请求内存中，不落库。

**`email_send_logs`：邮件供应商同步受理日志**

| 字段 | 类型 | 约束/说明 |
|---|---|---|
| id | BIGINT UNSIGNED | 主键自增 |
| business_request_no | VARCHAR(64) | 非空且唯一，平台生成的业务请求号；用于排查与幂等结果关联 |
| verification_code_id | BIGINT UNSIGNED | 可空且唯一；正式 OTP 关联 verification_codes，模板测试为 null |
| template_id | BIGINT UNSIGNED | 非空，关联发送时使用的平台模板镜像 |
| provider_template_id | VARCHAR(64) | 非空，发送时 DirectMail TemplateId 快照 |
| scene | VARCHAR(32) | 非空，固定五场景 |
| purpose | VARCHAR(16) | otp / test |
| recipient_hmac | CHAR(64) | 非空，不可逆匹配收件人 |
| recipient_masked | VARCHAR(191) | 非空，仅脱敏展示 |
| idempotency_scope | VARCHAR(191) | 非空，按五个正式入口或模板测试入口生成的稳定作用域 |
| idempotency_key_hash | CHAR(64) | 非空，正式 OTP 由业务请求号派生，模板测试由请求 Idempotency-Key 派生 |
| request_fingerprint | CHAR(64) | 非空，用于同 key 不同请求冲突检测 |
| provider | VARCHAR(32) | 非空 |
| provider_request_id | VARCHAR(128) | 可空，阿里云 RequestId；失败未返回时为 null |
| status | VARCHAR(16) | pending / accepted / failed；pending 是调用供应商前落库的幂等占位，accepted/failed 为最终同步结果 |
| failure_reason | VARCHAR(64) | 可空，归一化安全失败原因；accepted 时必须为 null |
| expires_at | DATETIME | `purpose=otp` 非空并与验证码到期时间一致；`purpose=test` 必须为 NULL，匹配 000055 CHECK，不得为 unknown 墓碑写入非空值 |
| submitted_at | DATETIME | 非空，提交供应商请求的时间 |
| created_at | DATETIME | 非空 |

唯一约束：`uk_email_send_logs_business_request(business_request_no)`、
`uk_email_send_logs_verification(verification_code_id)`、
`uk_email_send_logs_idem(idempotency_scope, idempotency_key_hash)`。索引：
`idx_email_send_logs_scene(scene, purpose, submitted_at)`、
`idx_email_send_logs_status(status, submitted_at)`、
`idx_email_send_logs_submitted_at(submitted_at)`、
`idx_email_send_logs_template(template_id, submitted_at)`。
summary 的“今日”先按 Asia/Shanghai 计算 `[00:00,次日00:00)`，再转换成 UTC submitted_at 半开区间；
submitted_today_count 使用 submitted_at 索引仅统计 accepted+failed，内部 pending 不计入；failed_today_count 使用 status+submitted_at 索引。
约束：`CHECK (scene IN ('register','login','reset_password','bind_email','admin_verify'))`、
`CHECK (provider='aliyun_directmail')`、`CHECK (purpose IN ('otp','test'))`、`CHECK (status IN ('pending','accepted','failed'))`，以及
pending 时 provider_request_id/failure_reason 均为空、accepted 时 provider_request_id 非空且 failure_reason 为空、failed 时 failure_reason 非空；
`purpose='otp'` 时 expires_at 非空，`purpose='test'` 时 expires_at 必须为 NULL。
正式 OTP 的 scope 按 register/login/reset_password/bind_email/admin_verify 五入口固定生成，业务请求号由服务端冷却窗口记录提供；
模板测试 scope 固定为 `admin-email-template-test:admin:{admin_id}:template:{id}:scene:{scene}:recipient:{recipient_hmac}`，
首次接收 Idempotency-Key 时生成 business_request_no。两类请求都必须在调用供应商前得到非空 scope/key_hash/fingerprint，
模板测试必须先创建 pending 日志占位再调用供应商。明确 accepted/rejected 使用
`WHERE id=? AND status='pending'` 条件更新唯一收敛 accepted/failed；条件更新失败时读取已有终态返回，不得覆盖。
响应未知或超时时，在同一数据库事务中把原 pending 行条件更新为 failed、
`failure_reason=provider_outcome_unknown` 并保留 `idempotency_scope`；正式 OTP 同事务把关联
`verification_codes.send_status` 置 failed。不得新增墓碑表。

unknown failed 行的统一派生截止为 `cooldown_until`：`purpose=otp` 取 `expires_at`，`purpose=test` 取
`submitted_at + INTERVAL 10 MINUTE`；test 的 `expires_at` 始终为 NULL。`cooldown_until` 是查询派生值，不新增数据库列。
每次新外呼在取得 Redis 锁后、调用 Adapter 前，必须按同 scope 查询 `cooldown_until > NOW()` 的 pending 或
`failure_reason=provider_outcome_unknown` failed 行；命中即阻断，因此 Redis 重启或锁 key 丢失不能绕过。

原 unknown 请求返回 `502/51002「供应商响应未知，请在验证码过期后重试」`；同一旧 Idempotency-Key 重放原
502/51002 且 `idempotent=true`。`cooldown_until` 前新 key 返回
`409/40900「邮件发送结果确认中，请在验证码过期后重试」` 且不调用 Adapter；到期后仅新 key 可重新发送，旧 key
仍重放原失败；任何重试均以派生 `cooldown_until` 为唯一截止口径。

pending 仅为数据库内部短暂幂等状态：管理端列表 SQL 强制过滤 pending，查询参数也只允许 accepted/failed，概览不统计 pending。
OTP 只有在供应商明确 accepted、验证码记录原子置 `send_status=accepted` 且日志为 accepted 后才可校验；明确 rejected 或 unknown 均写 failed，验证码置 failed。accepted 不是最终送达证明；当前范围不保存最终送达、打开率、点击率状态或事件。

**数据保留与清理**

- 模板镜像：在供应商侧 missing 后至少保留 180 天；仍被场景绑定或被发送日志引用时不得物理删除。
- 同步记录：保留 180 天；过期后批量删除，不影响模板镜像。
- 测试白名单：active 期间持续保留；revoked 30 天后按清理索引物理删除整行，但审计日志仍保留不含完整邮箱的操作记录。
- 发送日志：保留 180 天；过期后批量删除，审计统计只保留不含收件人标识的聚合值。
- 验证码仍按既有 10 分钟失效和一次性消费规则；发送日志保留期不延长验证码可用期。

**000055 up/down 与 MySQL 隐式提交恢复要求**

up：按 PM B 全停机门禁后，先改造 `verification_codes`（保留 `code VARCHAR(64) NULL`，新增 `code_hash`，邮箱 `target_value` 置空并写占位 `target_hash/target_masked`），再创建 `email_provider_templates` → `email_scene_bindings` → `email_template_sync_runs` →
`email_test_recipient_allowlist` → `email_send_logs` 五张业务表并 seed 五场景 disabled 记录。随后创建 migration-only 技术表
`migration_000055_permission_ownership`：先按四个冻结权限码各写一行 up 前取证，记录权限是否预存及 admin 绑定是否预存；再只补缺失权限、
只补缺失 admin 绑定，回填最终 `permission_id` 与 `admin_role_permission_id`；最后强断言四行 ownership、四份精确权限元数据、唯一 admin
角色及四条 admin 绑定全部一致。元数据冲突、ownership 预存在或任一断言失败均 fail-closed，不得继续部署。

MySQL DDL 会隐式提交，禁止声称整段 migration 可由事务自动回滚。执行前必须确认 `schema_migrations=54/dirty=0`、目标表不存在、verification 基线和历史数据统计符合预期、备份已在隔离库恢复。`migrate force` 只允许用于 000055 自身 dirty、尚未执行 000056、且确认不存在任何 bootstrap receipt 的灾难恢复，并且仅在结构与数据已完整恢复到 54 或完整达到 55 后设置对应版本；该例外不适用于 000056 或 C 类回滚，C 类及其高风险例外全程禁止 force，任何半完成结构也禁止清 dirty。

| partial-up/down 阶段 | `information_schema` 与数据核对 | 恢复要求 |
|---|---|---|
| verification ALTER 或历史失效中断 | 核对 columns、statistics、CHECK、旧/新字段空值和失效行统计 | 从已验证备份恢复，或严格按 up 顺序补齐缺失列、数据失效、索引和 CHECK；禁止跳过 |
| 五张业务表或五场景 seed 中断 | 核对 tables、columns、statistics、table/check constraints 与五场景行 | 无应用写入时按逆依赖清除半成品，或补齐当前表后继续；业务表必须最终恰好五张 |
| ownership 创建或四行取证中断 | 核对技术表是否存在、列/CHECK 是否完整、四权限码是否恰好四行及 created 标志是否仍对应 up 前事实 | 缺少可信四行取证时不得重建猜测归属；优先恢复备份。取证可信时才可补齐后续权限步骤 |
| 补权限、补 admin 绑定或 ID 回填中断 | 对账 permissions、roles、role_permissions 与 ownership 的 ID/created 标志 | 只补缺项并重新执行写后强断言；不得把预存记录改标为本 migration 创建 |
| partial-down 权限清理中断 | 核对 ownership、permissions、role_permissions、user_permission_overrides、group_permissions | 仅按 created 标志继续精确清理；本次创建权限存在未知角色、用户覆盖或分组引用时 fail-closed |
| partial-down 技术表或业务结构清理中断 | 核对 ownership 是否已通过写后断言并删除，以及五业务表、verification 新列的剩余状态 | 必须先完成权限写后断言再删 ownership；随后按逆依赖删五业务表，最后还原 verification 增量结构 |

down：先验证 ownership 恰好四行、最终 ID 和冻结元数据一致。仅当 `admin_binding_created=1` 时删除对应 admin 关联，仅当
`permission_created=1` 时删除对应权限；预存权限与预存 admin 绑定必须保留。对本 migration 创建的权限，若发现未知角色授权、
`user_permission_overrides` 或 `group_permissions` 三类未知引用，必须 fail-closed，禁止误删。权限与绑定删除后执行写后强断言，确认所有
created 标记对象均已清理，才删除 `migration_000055_permission_ownership`。随后按
`email_send_logs` → `email_test_recipient_allowlist` →
`email_template_sync_runs` → `email_scene_bindings` → `email_provider_templates` 的逆依赖顺序删除。
最后删除 `code_hash` 等新增列并保留旧 `code VARCHAR(64)`。down 前必须明确告警会删除邮件发送日志并使所有在途邮箱/手机验证码失效，
生产环境只允许在备份和变更审批后执行。

发布顺序固定为：停止邮箱/手机 OTP 发码、OTP 校验、注册、登录流量 → 等待 10 分钟 → 停止全部 auth/API 实例 → 备份并验证可恢复 → 执行 000055 up 并完整核验 → 执行 000056 up 并完整核验 → 执行 000057 up 并核验三列结构与历史数据保持 → 部署全部新版本应用实例 → 核验 health、ready、应用版本、schema 版本与配置 → 恢复流量。从 schema57 回滚时必须先执行并核验 000057 down，再按上述 A/B/C 矩阵选择是否继续回退；不得跳过版本直接 down 000056/000055，也不得在成功 receipt 存在时执行 000056/000055 常规 down。禁止滚动部署，禁止新旧应用共存。

### 3.2 商品、订单、钱包、计费（第一阶段）

- `products`
- `product_plans`
- `product_prices`
- `product_role_access`
- `product_provision_handlers`
- `product_billing_rules`
- `product_consumption_records`
- `orders`
- `order_items`
- `payment_callbacks`
- `wallets`
- `wallet_transactions`

### 3.3 用户资产、会员、应用、内容（第一阶段）

- `user_assets`
- `user_entitlements`
- `asset_events`
- `membership_levels`
- `membership_benefits`
- `user_memberships`
- `product_membership_rules`
- `applications`
- `application_adapters`
- `announcements`
- `help_categories`
- `help_articles`

> **注意：不单独维护 `application_plans`、`application_prices`、`application_role_access`**。  
> 应用的套餐、价格和角色权限统一走 `product_plans`、`product_prices`、`product_role_access`，通过 `products.business_ref_id = applications.id` 关联。

### 3.4 GPU（第三阶段）

- `gpu_devices`
- `gpu_rentals`
- `gpu_device_events`

### 3.5 Token 网关、Agent、Skills（第二阶段）

- `agent_templates`
- `user_agents`
- `agent_customization_orders`
- `agent_usage_logs`
- `skills`
- `skill_versions`
- `user_skill_installs`
- `agent_skill_bindings`
- `token_providers`
- `token_models`
- `token_model_routes`
- `token_usage_logs`
- `token_quota_accounts`

#### 3.5.1 AI 网关 Phase 1 G0/G1 Expand Schema

Migration `000060_create_ai_gateway_ledger_expand` 新增以下商业请求账本表，但不切换旧 `token_usage_logs` 读写：

- `ai_projects`：用户与 Project 的消费归集边界，冻结预算模式、月预算和 IANA 时区。
- `ai_requests`：请求主记录，保存 request_id、Project、SK、逻辑/执行模型及审核、执行、计费三个正交状态。
- `ai_usage_items`：标准化 Usage 与不可变计费行，数量为 `DECIMAL(30,10)`，单价和金额为 `DECIMAL(20,8)`；`source=provider_cost` 专门保存输出审核拒绝时的平台成本，不得进入用户销售额汇总。
- `ai_execution_attempts`：Native/Bifrost 的执行尝试、Provider、内部端点、上游请求 ID、耗时和 Usage 摘要。

关键唯一约束：`ai_requests.request_id`、`(user_id,idempotency_key)`、`(request_id,meter_type,source,sequence_no)`、`(request_id,attempt_no)`。新表禁止保存提示词、响应正文和明文密钥。G1 应用回滚保留 Expand Schema 与审计记录，物理清理由后续单独审批的 Contract Migration 承担。

`ai_projects` 和现有 `api_keys` 分别增加 `(id,user_id)` 复合唯一键，`ai_requests` 通过对应复合外键保证 Project、SK 和请求用户属于同一租户。

#### 3.5.2 AI 网关 Phase 1 G2 Project SK Expand Schema

Migration `000061_add_ai_gateway_g2_projects_keys` 为 `api_keys` 增加 `project_id`、`scope_mode`、`expires_at` 和 `rotated_from_id`，并新增 `api_key_model_scopes`。新 Project SK 默认 `scope_mode=allowlist`，空表记录表示拒绝全部；只有显式选择才使用 `all`。旧 SK 标记为 `legacy_all`，保留旧 `model_scope` 行为。

数据库使用 `(id,project_id,user_id)`、`(project_id,user_id)` 和 `(api_key_id,project_id,user_id)` 复合唯一键/外键，强制 Project、SK、权限行和 `ai_requests` 属于同一用户。轮换通过 `rotated_from_id` 保留内部追踪，但任何表都不保存 SK 明文。

G2 正式写入 `ai_requests`、`ai_execution_attempts` 和 `ai_usage_items`；`billing_status` 固定为 `unquoted`，所有价格和金额字段为空。000061 down 保留 Project SK、权限和请求审计事实，物理清理需单独审批。

Project 预算合法组合固定为：`disabled + monthly_budget=NULL`，或 `soft/hard + monthly_budget>0`。`soft` 超限仅告警并继续，`hard` 在预计消费越限时于上游调用前拒绝；月周期按 Project 的 IANA 时区从当地月初计算。G1 只冻结约束，准确预占、并发控制和拒绝逻辑在 G3/G4 实现。

#### 3.5.3 AI 网关 Phase 1 G3 价格与可靠结算

Migration `000062_create_ai_gateway_g3_billing` 新增：

- `ai_price_versions`：逻辑模型价格版本、审批/发布时间、生效区间、成本有效期、最低毛利、汇率、取整和失败收费规则。
- `ai_price_model_locks`：每个逻辑模型一行的并发发布互斥锁，避免两个已审批版本同时通过重叠检查。
- `ai_price_skus`：输入、输出、缓存、推理四类成本价与销售价；唯一键 `(price_version_id,meter_type,variant_hash)`。
- `ai_request_wallet_links`：请求与唯一 hold、freeze/settle/release 流水及报价、预占、实结金额的关联。
- `ai_outbox_events`：事务事件、重试次数、下次时间、租约、处理时间和脱敏错误分类。

`wallets`、`wallet_transactions`、`wallet_holds` 金额扩为 `DECIMAL(20,8)`；数据库 CHECK 保证钱包可用/冻结余额非负及结算金额不超过 hold。请求 ID、hold 和三类钱包流水在关联表中唯一，防止重复财务终态。

价格发布使用逻辑模型共享行锁校验审批、四 SKU 和时间区间重叠；报价读取价格与 SKU 使用同一一致性事务。已发布版本不提供原地改价接口，只能暂停或创建新版本。000062 down 保留所有财务表和事实，不执行 DROP 或数据删除。

完整字段与状态契约见 [`ai-gateway-g0-g1-contract.md`](./ai-gateway-g0-g1-contract.md)。

#### 3.5.4 AI 网关 Phase 1 G4 内容安全与资源治理

Migration `000063_create_ai_gateway_g4_governance` 新增以下 expand 表：

| 表 | 事实与约束 |
|---|---|
| `ai_safety_policy_versions` | 不可变安全策略版本，draft/active/retired |
| `ai_safety_events` | 只存摘要、分类、规则和处置，不存原始内容 |
| `ai_safety_subject_actions` | 用户或 SK 暂停、撤销和过期事实 |
| `ai_safety_appeals` | 每用户每事件唯一申诉及乐观锁版本 |
| `ai_resource_policies` | user/project/api_key/model 四层并发、RPM、TPM 覆盖 |
| `ai_budget_policies` | Project/SK disabled/soft/hard 日月预算 |
| `ai_budget_overrides` | 有原因、操作人和有效期的临时增额 |
| `ai_budget_reservations` | request_id 唯一的 held/settled/released/expired 预算预留 |
| `ai_budget_alerts` | 主体、周期、80/90/100 阈值唯一提醒事实 |
| `ai_compensation_tasks` | pending/running/retry/dead/manual_review 幂等补偿任务 |

预算预留不是第二套财务账本：reserved_amount 来自 G3 报价快照，settled_amount 只读取 G3 终态。Redis 不保存预算金额，只保存带 TTL 的并发与速率状态。000063 down 为事实保留型 no-op，应用回滚不得删除安全、预算和补偿记录。

## 4. 关键状态

用户状态：

```text
active
disabled
```

实名状态：

```text
unverified
pending
verified
rejected
```

订单状态：

```text
pending
paid
cancelled
refunded
failed
```

资产状态：

```text
active
expired
frozen
cancelled
```

商品状态：

```text
draft
active
inactive
archived
```

GPU 设备状态：

```text
available
reserved
deploying
running
expired
releasing
maintenance
fault
offline
```

GPU 租赁状态：

```text
pending
active
releasing
released
cancelled
```

## 5. 关键索引约定

以下字段必须建索引：

| 表 | 字段 | 索引类型 | 原因 |
|---|---|---|---|
| users | email | UNIQUE | 登录唯一标识 |
| users | phone | UNIQUE | 登录唯一标识 |
| user_sessions | user_id | INDEX | 按用户查会话 |
| user_sessions | refresh_token_hash | UNIQUE | 刷新令牌校验 |
| verification_codes | target_type, target_value, scene | INDEX | 仅手机号验证码查询；email 的 target_value 必须为 null |
| verification_codes | target_type, target_hash, scene | INDEX | 邮箱验证码按规范化邮箱 HMAC 查询，不保存完整邮箱 |
| identity_verifications | user_id | INDEX | 按用户查实名 |
| identity_verifications | id_card_no_hmac | INDEX | 查重校验 |
| user_roles | user_id | INDEX | 权限查询 |
| role_permissions | role_id | INDEX | 权限查询 |
| products | product_type, status | INDEX | 商品列表 |
| product_plans | product_id | INDEX | 套餐查询 |
| product_prices | product_plan_id, role_id | INDEX | 价格查询 |
| product_consumption_records | idempotency_key | UNIQUE | 防重复扣费 |
| product_consumption_records | user_id, product_id, created_at | INDEX | 消费记录查询 |
| orders | order_no | UNIQUE | 订单号唯一 |
| orders | user_id, status, created_at | INDEX | 用户订单查询 |
| wallet_transactions | wallet_id, created_at | INDEX | 流水查询 |
| wallet_transactions | user_id, created_at | INDEX | 流水查询 |
| payment_callbacks | provider, provider_trade_no | UNIQUE | 支付回调幂等 |
| user_assets | user_id, asset_type, status | INDEX | 资产查询 |
| user_entitlements | user_id, product_id, status | INDEX | 权益查询 |
| audit_logs | operator_id, module, created_at | INDEX | 审计查询 |
| verification_codes | business_request_no | UNIQUE | 新邮件 OTP 业务请求号关联；历史/手机行允许 null |
| verification_codes | idempotency_scope, created_at | INDEX | scope 锁内查找正式邮件 OTP 冷却窗口记录 |
| email_provider_templates | provider, provider_template_id | UNIQUE | 防止同一供应商模板重复镜像 |
| email_provider_templates | provider_status, local_enabled, missing, last_synced_at | INDEX | 模板状态、平台启停与失联筛选 |
| email_provider_templates | missing, missing_since | INDEX | missing 满保留期后的清理扫描 |
| email_scene_bindings | scene | UNIQUE | 固定五场景一对一绑定 |
| email_scene_bindings | template_id, enabled | INDEX | 模板失效时定位受影响场景 |
| email_template_sync_runs | idempotency_scope, idempotency_key_hash | UNIQUE | 全局模板同步 scope 内幂等 |
| email_template_sync_runs | status, started_at | INDEX | 排查运行中/失败同步 |
| email_template_sync_runs | status, completed_at | INDEX | summary 查询最近成功同步时间 |
| email_test_recipient_allowlist | email_hmac | UNIQUE | 测试邮箱白名单判定，避免存完整邮箱 |
| email_test_recipient_allowlist | status, revoked_at | INDEX | revoked 满 30 天后的物理清理 |
| email_send_logs | business_request_no | UNIQUE | 业务请求号唯一，便于排查与幂等结果关联 |
| email_send_logs | idempotency_scope, idempotency_key_hash | UNIQUE | 发送幂等；request_fingerprint 识别同 key 不同请求 |
| email_send_logs | verification_code_id | UNIQUE | 一个验证码只对应一条最终发送日志 |
| email_send_logs | scene, purpose, submitted_at | INDEX | 按场景、用途和提交时间查询日志 |
| email_send_logs | status, submitted_at | INDEX | 内部三态收敛；公开查询只使用 accepted/failed |
| email_send_logs | submitted_at | INDEX | 上海自然日内全部 submitted 日志统计 |
| email_send_logs | template_id, submitted_at | INDEX | 按模板定位发送日志 |
| gpu_device_events | device_id, created_at | INDEX | 设备事件查询 |
| token_usage_logs | user_id, created_at | INDEX | 用量查询 |
| token_usage_logs | request_id | UNIQUE | 请求去重 |

## 6. 大表增长预警与处理策略

第一阶段不做分库分表，但需预留字段和索引，便于后期扩展。

增长最快的表（按风险排序）：

```text
1. product_consumption_records    -- Token 调用、GPU 按量、agent 调用量会很大
2. token_usage_logs               -- 每次模型调用写一条
3. wallet_transactions            -- 每次充值/消费/退款写一条
4. audit_logs                     -- 后台所有敏感写操作
5. user_login_logs                -- 每次登录写一条
6. gpu_device_events              -- 5 万设备频繁上报状态
7. asset_events                   -- 资产状态变更
8. orders                         -- 随用户增长
```

处理策略见 [数据量和分库分表规划](data-scale-sharding-plan.md)。

## 7. 建表脚本

建表脚本保存为 migration 文件：

```text
server/migrations/000001_create_core_tables.up.sql
```

使用方式：

```bash
chmod +x scripts/create_mysql_tables.sh
./scripts/create_mysql_tables.sh
```

默认读取环境变量：

```text
MYSQL_HOST
MYSQL_PORT
MYSQL_DATABASE
MYSQL_USER
MYSQL_PASSWORD
```

核心表建表顺序（注意外键依赖）：

```text
1. users
2. wallets（依赖 users）
3. user_sessions（依赖 users）
4. verification_codes
5. user_login_logs（依赖 users）
6. identity_verifications（依赖 users）
7. identity_verification_logs（依赖 identity_verifications）
8. roles
9. permissions
10. user_roles（依赖 users、roles）
11. role_permissions（依赖 roles、permissions）
12. user_permission_overrides（依赖 users、permissions）
13. role_change_logs（依赖 users、roles）
14. audit_logs
15. email_provider_templates
16. email_scene_bindings（依赖 email_provider_templates、users）
17. email_template_sync_runs（依赖 users）
18. email_test_recipient_allowlist（依赖 users）
19. email_send_logs（依赖 verification_codes、email_provider_templates）
20. applications
21. products（依赖 applications 可选）
22. product_plans（依赖 products）
23. product_prices（依赖 product_plans）
24. product_role_access（依赖 products、roles）
25. product_provision_handlers
26. application_adapters（依赖 applications）
27. product_billing_rules（依赖 products）
28. orders（依赖 users）
29. order_items（依赖 orders）
30. payment_callbacks（依赖 orders）
31. wallet_transactions（依赖 wallets、orders）
32. product_consumption_records（依赖 wallet_transactions）
33. membership_levels
34. membership_benefits（依赖 membership_levels）
35. user_memberships（依赖 users、membership_levels）
36. product_membership_rules（依赖 products、membership_levels）
37. user_assets（依赖 users、products）
38. user_entitlements（依赖 users、user_assets）
39. asset_events（依赖 user_assets）
40. announcements
41. help_categories
42. help_articles（依赖 help_categories）
```

## 阿里云短信验证码阶段 1 增量

阶段 1 migration `000058` 建立 `sms_templates`、`sms_scene_bindings`、`sms_send_logs` 三张表。短信与 DirectMail 共用 `verification_codes` 的 `code_hash`、`send_status`、`accepted_at` 和 `business_request_no`；`000058` 只增加 `provider`、`provider_request_id` 以及短信查询索引，禁止重复创建或在回滚时删除 `000055` 的邮件基础字段。

- `sms_templates`：阿里云模板只读快照，`provider + template_code` 唯一。
- `sms_scene_bindings`：五个短信场景唯一绑定模板与签名，默认关闭。
- `sms_send_logs`：只保存脱敏手机号、独立 HMAC、模板/签名快照和平台/供应商请求标识，不保存验证码或完整手机号。

## 阿里云短信验证码阶段 2 增量

阶段 2 migration `000059` 在阶段 1 三张短信表上扩展管理控制面，不修改 `verification_codes` 的统一 OTP 状态机。

- `sms_templates` 新增模板类型、变量 JSON、拒绝原因和供应商更新时间；同步以 `(provider, template_code)` 唯一约束保证幂等。
- `sms_scene_bindings` 新增创建人，并继续以 `scene` 唯一约束保护五个固定场景；所有更新使用 `version` 乐观锁。启用或换绑时，事务先锁定目标 `sms_templates` 行，再查询是否存在 `template_id` 相同、`enabled=1` 且 `scene` 不同的绑定；命中即拒绝。同一模板的并发请求由模板行锁串行化，保证最多一个启用场景获胜；停用操作不执行该冲突检查，以便整改历史共用数据。
- `sms_send_logs` 新增 `purpose=otp/test`、提交/完成时间、内部 `retry_after_seconds` 及测试发送幂等摘要；阶段 1 历史行的 `submitted_at` 必须由原 `created_at` 回填，不能使用升级时间。`(idempotency_scope, idempotency_key_hash)` 固定业务请求，`idempotency_owner_key_hash` 额外保证同一管理员复用同一 key 修改参数时发生冲突；恢复秒数仅用于精确重放首次 429，不对管理列表公开；这些字段都不保存幂等键明文、验证码或完整手机号。
- `sms_template_sync_locks` 仅保存阿里云模板同步单例锁。供应商查询必须在事务外全部完成，事务内取得行锁后一次应用完整快照。
- `sms_phase2_permission_ownership` 记录 migration 新增权限及 admin 绑定的所有权，使 down 只删除本 migration 创建且未被其他主体引用的数据。

新增权限为 `sms:template:view`、`sms:template:manage`、`sms:template:sync`、`sms:template:test`。生产迁移前必须在 MySQL 8 隔离库验证 `1→59` 与 `58→59→58→59`；本地无 MySQL/Docker 时，静态契约测试不能替代该门禁。
- 手机验证码状态复用 `pending → accepted/failed`；只有 `accepted`、未使用且未过期的记录可以被消费。
