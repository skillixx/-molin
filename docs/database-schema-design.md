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

Migration `000067_align_token_usage_log_money_precision` 将 `token_usage_logs.sale_amount` 从 `DECIMAL(18,6)` 扩展为 `DECIMAL(20,8)`，与 G3 请求、钱包 hold 和流水的权威金额精度一致。该兼容汇总不参与扣费，迁移不补写历史记录；down 保留扩展后的精度，避免截断审计事实。
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

G7 不新增 Migration 或第二套账务表。可观测 Collector 与 `ai-gateway-reconcile` 只在 MySQL READ ONLY、REPEATABLE READ 事务中读取既有 `ai_requests`、`ai_price_versions`、`ai_price_skus`、`ai_execution_attempts`、`ai_usage_items`、`ai_request_wallet_links`、`wallets`、`wallet_holds`、`wallet_transactions`、`ai_outbox_events`、`ai_compensation_tasks` 和 `audit_logs`。对账输出三项 DECIMAL 差额、七类聚合异常及有限条 request_id/issue_code 明细，并核对快照与不可变价格/SKU、raw↔sale 数量、逐项金额、钱包 owner 与 `0≤settled≤held`；任何未释放 hold、活跃 Outbox 或补偿积压同样失败，禁止以 G7 CLI 直接写表修账。

G8 当前不新增 Migration。生产流量启动门禁只读聚合既有发布事实：5～8 个已发布文字模型、至少两个健康渠道、逐模型有效且已审批价格、健康路由、唯一生效安全策略、成本有效期和最低 15% 毛利。任一缺项只会拒绝生产流量启动，不修改模型、价格、路由、钱包或账本事实；结果未知请求禁止重试由执行链语义和回归测试保证，不以禁止全部安全重试代替。

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
| `ai_compensation_tasks` | pending/running/retry/completed/dead/manual_review 幂等补偿任务 |

预算预留不是第二套财务账本：reserved_amount 来自 G3 报价快照，settled_amount 只读取 G3 终态；日/月归属以准入时固化的 `daily_period_start/monthly_period_start` 为准，跨午夜结算不改变周期。Redis 不保存预算金额，只保存带 TTL 的并发与速率状态。000063 down 为事实保留型 no-op，应用回滚不得删除安全、预算和补偿记录。

#### 3.5.5 AI 网关 Phase 1 G6 用户客户旅程

Migration `000065_create_ai_gateway_g6_customer_journey` 新增 `ai_billing_disputes`，以 `(request_id,user_id)` 唯一约束保证同一用户对同一请求只能存在一条账单申诉。申诉只保存编号、request_id、用户说明、状态、处理意见和审计时间，不保存提示词、响应正文或任何密钥；外键限制请求、用户和处理人均可追溯。

Migration `000066_enforce_ai_dispute_request_owner` 在 `ai_requests(request_id,user_id)` 增加组合唯一索引，并让 `ai_billing_disputes(request_id,user_id)` 通过组合外键引用该索引。这样不论写入来自用户 API、管理脚本还是异步任务，数据库都会拒绝“用户 B 申诉用户 A 请求”的跨用户事实。该迁移的 down 同样采用事实保留策略，不自动移除客户权益约束。

`token_models` 增加模型介绍、API 文档和快速入门三个 URL 的健康状态，状态为 `unpublished/unknown/healthy/unhealthy`。静态网页正文仍由外部站点托管，墨灵不保存 Markdown 或 HTML。地址变化后状态自动回到 `unknown`；发布操作要求 API 文档和快速入门均为 `healthy`，健康状态随不可变模型发布快照冻结。历史 G5 快照没有该字段时，只在发布 URL 与当前 URL 完全一致时兼容迁移后的状态。

`ai_requests` 增加 `(user_id,execution_status,billing_status,created_at)` 查询索引，支持本人请求账本筛选。G6 不创建第二套 Usage 或财务表：用户用量、价格快照和钱包关联继续分别以 `ai_usage_items`、`ai_requests.price_snapshot_json` 和 `ai_request_wallet_links` 为事实源。000065 down 为事实保留型 no-op，不删除申诉和财务关联事实。

#### 3.5.6 图片网关 IMG-G1 Expand Schema

Migration `000068_expand_image_gateway_schema` 在保持旧 Chat 二进制兼容的前提下扩展图片数据底座：

- `ai_requests` 增加 `capability` 和 `delivery_status`；旧 Chat 默认 `chat.completions/not_applicable`，图片固定 `image.generate` 且不允许流式。
- `ai_usage_items` 增加 `record_kind`、价格版本、variant、计量单位、计量基数和币种；旧 Chat 使用 `legacy_chat` 与全零 variant hash，继续保持重复写入唯一约束。
- `ai_gateway_quotes` 保存用户、Project、请求指纹、不可变价格快照、CNY Decimal 金额、过期时间和一次消费关系。
- `ai_gateway_tasks` 保存图片执行、存储、审核和待对账状态，通过复合外键绑定 request、Quote、用户和 Project。
- `ai_gateway_assets` 保存主图、缩略图、审核副本和派生图元数据；`available` 必须审核通过、显式/隐式标识完成、对象与图片元数据完整。

关键唯一约束为 `(request_id,result_index,asset_role)`、每请求一个任务、每 Quote 一个任务和每 Quote 一个消费请求。新表禁止保存 Prompt、图片正文、Base64、长期签名 URL、Provider 原始响应和明文凭据。

000068 down 为事实保留型 no-op。应用回退必须保持图片流量关闭，禁止删除财务、审计、任务或已交付资产事实。详细字段、状态和隔离 MySQL 证据见 [`image-gateway-img-g1-schema.md`](./image-gateway-img-g1-schema.md)。

#### 3.5.7 图片网关 IMG-G2 价格与 Quote

Migration `000069_expand_image_pricing_quotes` 泛化既有价格版本：

- `ai_price_versions` 增加 `capability`、`pricing_template`、`limits_json`、`minimum_charge`、成本来源/版本和 `price_purpose`。
- 历史 Chat 默认映射为 `chat.completions/token/manual_cny/legacy/commercial`，继续要求正数 Token 上限。
- 图片价格使用 `image_variant` 或 `image_megapixel`，要求 Token 上限为空且规格限制 JSON 非空。
- `ai_price_skus` 新增 `image_count/image_megapixels` meter；图片 SKU 必须包含规范化 variant JSON 和64位小写 hash。
- `ai_gateway_quotes` 增加 `request_variant_hash`，与请求 HMAC 指纹、价格快照和一次消费关系共同冻结报价。
- `price_purpose=test_fixture` 只允许本地非商业金额验证，应用发布入口必须拒绝正式发布。

V2 快照按 `meter_type + variant_hash` 选择唯一行，只保存本次 `selected_lines`；历史 Chat V1 JSON 不改写。000069 down 保留价格、SKU、快照与 Quote 事实。详细合同见 [`image-gateway-img-g2-pricing-quote.md`](./image-gateway-img-g2-pricing-quote.md)。

#### 3.5.8 图片网关 IMG-G3 Repository 状态

Migration `000070_expand_image_task_asset_repository` 为 `ai_gateway_tasks` 和 `ai_gateway_assets` 增加 `version_no` 乐观锁，并为资产增加 `dispute_status/dispute_opened_at/dispute_resolved_at`。

争议打开时必须同时设置 legal hold；争议解决后仍保留 legal hold，释放保全必须由后续具备权限、二次认证和审计的独立动作完成。普通交付查询同时要求资产可用/审核/双标识和请求 `billing_status=settled + delivery_status=available`。000070 down 保留任务、资产、争议和legal hold事实。详细合同见 [`image-gateway-img-g3-task-asset-repository.md`](./image-gateway-img-g3-task-asset-repository.md)。

#### 3.5.9 图片网关 IMG-G5 调账与结算事实

Migration `000071_expand_image_billing_adjustments` 为 `ai_usage_items` 增加调账方向、原因、操作人和复核人。`adjustment` 必须为正数CNY、操作人与复核人不同；非调账行的审计字段必须为空。

钱包预占、结算、释放继续复用 `wallet_holds/wallet_transactions`，请求金额关联继续复用 `ai_request_wallet_links`，事件与补偿继续复用 `ai_outbox_events/ai_compensation_tasks`，不创建第二套财务账本。000071 down永久保留调账和既有财务事实。详细合同见 [`image-gateway-img-g5-billing-compensation.md`](./image-gateway-img-g5-billing-compensation.md)。

#### 3.5.10 图片网关 IMG-G6 HTTP合同

IMG-G6不新增migration，复用000068～000071。HTTP幂等依赖 `ai_requests(user_id,idempotency_key)`，Quote单消费依赖 `ai_gateway_quotes.consumed_request_id`，任务和资产归属继续依赖用户/Project/Key复合关系。

管理端创建图片 `image_variant` 价格时，Repository通过Omit把 `max_input_tokens/max_output_tokens` 写为SQL NULL，满足图片价格CHECK；Chat价格仍写非零Token上限。G6只允许 `price_purpose=test_fixture` 图片价格，发布Repository继续要求commercial，因此测试夹具无法正式发布。详细接口和证据见 [`image-gateway-img-g6-http-contract.md`](./image-gateway-img-g6-http-contract.md)。

#### 3.5.11 图片网关 IMG-G7 基础设施

IMG-G7同样不新增migration。RabbitMQ消息只保存request_id，Prompt留在有界进程内存；MinIO对象引用继续只落 `ai_gateway_assets.bucket/object_key`。清理Worker通过既有 `lifecycle_state + version_no + legal_hold + dispute_status` 进行CAS删除。尚未形成资产元数据的受控对象删除失败时，复用 `ai_compensation_tasks` 的 `image_object_cleanup` 类型，`task_key` 使用bucket和key的SHA-256，`aggregate_id` 只保存可重建的脱敏描述符，管理接口不得回传原始对象路径。详细合同见 [`image-gateway-img-g7-infrastructure.md`](./image-gateway-img-g7-infrastructure.md)。

#### 3.5.12 图片网关 IMG-G8 页面与目录聚合

IMG-G8不新增Migration。用户模型目录在既有发布快照、可见性和活动价格约束下同时聚合 `chat` 与 `image`：文字模型仍要求健康Bifrost路由，图片模型由独立图片运行时装配。页面读写继续使用 `000068～000071` 事实；前端不得直接修改钱包、Quote、Usage、任务或资产状态。详细合同见 [`image-gateway-img-g8-frontends.md`](./image-gateway-img-g8-frontends.md)。

#### 3.5.13 视频网关 VID-G1 Expand Schema

Migration `000072_expand_video_gateway_schema`以Expand-only方式把既有Chat/Image共享事实扩展到视频：

- `ai_requests`增加可空`operation`，允许`modality=video + capability=video.generate`；视频operation固定为`text_to_video/image_to_video`，旧Chat/Image保持`operation=NULL`。
- `ai_gateway_quotes`、`ai_gateway_tasks`和`ai_usage_items`分别持久化operation；禁止从JSON、输入数量或资产是否已删除反推。
- `ai_gateway_tasks`增加`bifrost_provider/bifrost_task_id/bifrost_compound_id`，但外部`video_id`只使用既有全局唯一`public_id`，内部和上游ID不公开。
- `ai_gateway_assets`增加`modality/duration_seconds/frame_rate/container/video_codec/audio_codec/has_audio/media_deleted_at`；Image继续只允许既有`primary_output`根角色，Video使用`content`根角色和`preview/thumbnail`派生物，媒体正文删除后继续保留财务、审计和低敏资产元数据。
- 价格版本/SKU只扩大`video_seconds/video_megapixel_seconds`模板、同名meter及variant JSON表达，不在VID-G1增加价格operation列；正式选价和Quote属于VID-G2。
- 新增`ai_upload_sessions`、`ai_gateway_input_assets`和`ai_gateway_task_inputs`，通过用户/Project/Key组合外键建立来源到不可变规范化快照的血缘；上传只接受`purpose=video_reference_image`和JPEG/PNG，并记录创建、上传、核验及四类终态。
- 新增`ai_gateway_task_events`、`ai_gateway_provider_callback_events`和`ai_gateway_task_payloads`，分别承载具有全局唯一`event_id`的追加式任务事件、按`(provider_code,provider_task_id,external_event_id)`去重的回调验签结果，以及按`(task_id,payload_kind)`唯一且不含密钥的密文信封。

`text_to_video`零输入、`image_to_video`恰好一张`reference_image`由Service在创建请求/任务/绑定的同一事务内校验；数据库`(task_id,role,ordinal)`唯一键只阻止重复序号，不伪称能够统计跨行数量。

VID-G1唯一写入边界为`CreateVideoSchemaFacts`：事务前完成纯校验，随后在一次事务中按Request→Task→图生唯一TaskInput顺序写入；任一写入或commit失败均不得留下部分事实。该接口只冻结事务合同，不实现Repository、CAS、Quote、钱包或Provider编排。

上传完成由`CompleteVideoUploadSession`在同一事务锁定会话、校验归属、`verifying`状态、未过期、ETag/VersionID和未完成事实，再插入snapshot并用回填ID完成会话；数据库额外要求`completed_at<=expires_at`。输入租约由`ReleaseVideoInputLeases`锁定Task、Request和TaskInput，只有任务进入安全终态且账单为`settled/released`的允许组合后才一次释放；`pending_reconcile`和非终态继续持有租约。

输入删除申请复用另一窄域事务守卫`RequestVideoInputPendingDelete`：按输入ID、用户和Project加锁读取并再次核对归属，限定`ready/rejected/quarantined`、拒绝legal hold、加锁确认活动租约为0后，才同时写入两个删除时间。TaskEvent通过BEFORE UPDATE/DELETE触发器保持追加式。所有视频operation、SKU JSON operation、ready输入和可用视频媒体字段在CHECK中显式`IS NOT NULL`；上传版本、对象定位、策略版本和视频Codec还必须trim后非空，禁止利用MySQL `UNKNOWN`或空白字符串绕过约束。

未释放TaskInput且任务非终态或处于`pending_reconcile`时构成输入执行租约。清理查询必须排除活动租约、legal hold和未到删除时间的输入资产；安全终结并完成对账后只允许写入一次`lease_released_at`。

Migration `000073_seed_video_gateway_permissions`幂等创建`video:view/model/price/task/safety/reconcile/resource/retention/secret/release`十类权限，并只自动映射全局`admin`角色。两个down均为事实保留式no-op，不删除列、表、权限、财务、任务、回调、资产或审计事实。

VID-G1没有HTTP路由、页面、Provider Adapter、Worker或钱包运行逻辑。Schema存在不表示`/v1/videos`或平台视频接口可调用。完整字段、不变量、回滚与验收矩阵见[`video-gateway-vid-g1-schema.md`](./video-gateway-vid-g1-schema.md)。

#### 3.5.14 视频网关 VID-G2 价格与Quote

VID-G2通过`000074_expand_video_pricing_quotes`扩展共享价格与Quote事实，不创建视频专用账本：

- active视频价格只允许`non_commercial_test_fixture`，第一版只启用`video_seconds`。
- 每个冻结variant显式包含operation、分辨率、时长、比例、帧率和音频；文生/图生分别存在唯一SKU。
- `ai_gateway_quotes`增加可空`command_kind/idempotency_key`，唯一键为`(user_id,project_id,command_kind,idempotency_key)`；旧Image与VID-G1遗留Video Quote允许两列同时为空并保持原值。
- Quote消费、hard项目预算检查、钱包Hold、Request、Task及I2V TaskInput在同一事务提交；失败零部分事实。
- down采用事实保留式回滚，不删除价格、快照、幂等、消费或金额事实。

完整合同见[`video-gateway-vid-g2-pricing-quote.md`](./video-gateway-vid-g2-pricing-quote.md)。

#### 3.5.15 视频网关 VID-G3 任务、资产与事件

Migration `000075_enforce_video_task_asset_events`在VID-G1共享表上增加可执行强约束，不创建平行视频账本：

- `ai_requests.billing_status`保留旧状态并增加`quoted/adjusted`，视频正常链固定为`unquoted→quoted→held→settlement_pending→settled|released→adjusted`。
- Task执行轴使用`ai_gateway_tasks.status/version_no`，计费与交付轴使用`ai_requests.version_no`；三个Repository迁移互不改写其他轴。
- TaskInput插入触发器锁定视频Task与ready InputAsset，沿UploadSession或GeneratedImageAsset来源核对Task API Key；T2V拒绝任何输入，I2V只接受一个`reference_image/ordinal=0`快照；更新触发器只允许安全终态后一次写入`lease_released_at`，DELETE永远拒绝。
- InputAsset来源、owner、原始hash和已形成规范化快照被冻结；`pending_delete`、隔离、过期、审核拒绝、hash/version漂移均不能进入Provider提交。
- Callback继续以`(provider_code,provider_task_id,external_event_id)`唯一，身份、body SHA-256、验签和owner一经写入不可修改；仅低敏应用结果可从received补充为applied/ignored/failed。
- Callback应用结果只能从`received`写入一次终态，之后不能UPDATE或DELETE。TaskEvent详情只允许四个结构化白名单键，禁止自由文本换名持久化。
- TaskPayload的AES-GCM信封通过UPDATE/DELETE触发器保持不可变；nonce固定12字节，AAD绑定Task/User/Project/Kind。Repository还强制Protector认证解密，不能把任意字节伪装成密文写入。
- 视频资产补齐`cover`，`content/preview`支持MP4，`cover/thumbnail/moderation_copy/derived`支持图片或MP4；父子、owner、对象位置、来源和hash被冻结。VID-G3的available迁移只校验既有审核/双标识事实，不生成这些结果。
- `deleted/media_deleted_at`只表示媒体正文已删，request、Quote、账单、hash、规格、生命周期和审计元数据继续保留。
- 视频Task普通`input_json`固定六个规范化规格键，VID-G3的`result_json/error_message_safe`必须为空；敏感正文只能进入认证AES-GCM信封。

完整Repository、状态矩阵、回滚和验收合同见[`video-gateway-vid-g3-task-asset-events.md`](./video-gateway-vid-g3-task-asset-events.md)。VID-G3没有HTTP、Provider Adapter、Worker、轮询、媒体抓取、审核或标识闭环。

由于`ai_gateway_tasks/ai_gateway_assets`从图片专用事实扩展为共享媒体事实，既有图片Repository和Service必须显式限定任务`capability=image.generate AND operation IS NULL`、资产`modality=image`，请求关联同时限定`modality=image + capability=image.generate`。图片列表、取消、Provider领取、恢复、结算、清理和观测均不得依赖“当前尚无视频运行时”而省略过滤。

#### 3.5.16 视频网关 VID-G4 Fake异步与媒体安全

Migration 000076不创建新表，只向共享ai_gateway_assets增加moderation_policy_version、explicit_label_version和implicit_label_version。

- 新形成的视频审核或标识结果必须携带版本。
- 从非available进入available时，审核必须passed，显式和隐式标识必须applied，三个版本字段必须非空。
- TaskEvent详情继续使用四键结构白名单，并加入G4固定低敏原因。
- Provider绑定在submitting→submitted事务内写provider_code/provider_task_id、递增Task和Request版本并追加TaskEvent。
- 媒体正文删除必须走available→expiring→deleting→deleted；只写media_deleted_at绕过生命周期会被CHECK拒绝。
- down为保留式回滚，不删除审核、标识、任务、回调、资产或媒体删除事实。

VideoRepositoryTaskLedger把Fake Worker桥回VID-G3 Repository。Prompt只从AES-GCM TaskPayload临时解密；Provider Content句柄只根据已绑定taskUUID在内存重建，不进入普通JSON或MySQL普通字段。

完整设计见[VID-G4 Fake异步与媒体安全](./video-gateway-vid-g4-fake-async-media-safety.md)。本阶段没有项目数据库、真实MinIO、RabbitMQ、Redis或远端部署。

#### 3.5.17 视频网关 VID-G5 财务实现（仅隔离验证）

VID-G4已通过PR #420合并。VID-G5已新增000077的共享请求幂等、Usage归属与追加约束、视频关联流水保护、Hold终态、补偿和交付约束；完整矩阵与隔离验证见开发合同，阶段结论以同源验收回执为准。人工财务合同已批准，本阶段未操作共享数据库或生产。

- 复用wallets、wallet_holds、wallet_transactions、ai_request_wallet_links、ai_requests、ai_usage_items、ai_outbox_events、ai_compensation_tasks与既有Task/Asset/Event，不新建平行视频账本。
- 保留现有user_id/idempotency_key请求唯一键；新增可空命令/键摘要及视频组合唯一约束，实现user_id/project_id/create_video/idempotency_key作用域，Chat/Image原索引和原数据语义不变。
- ai_usage_items新增可空task_id、quote_id、user_id、project_id、api_key_id、logical_model_code、capability；G5新行由Task/Request/Quote交叉校验完整归属，历史记录保持NULL，不伪造回填。Task、Quote、用户、Project和Key使用外键；视频Usage禁止UPDATE/DELETE。
- 视频请求钱包关联、原冻结/消费/解冻流水及Hold身份禁止修改或删除；Hold不能由released回到holding或settled，相反终态不能覆盖。保护范围通过既有请求钱包关联识别，不另建钱包表。
- 共享TaskEvent新增可空fact_sha256，Usage新增可空evidence_event_id外键；Provider确认成本必须与同任务事件摘要对应，分母为1，不能把只有事件ID的任意数值冒充确认成本。
- 共享TaskEvent新增可空failure_origin，仅G5原始execution_status_changed到failed时使用；来源、前状态、归属和原因封闭枚举由SQL校验。普通后补事件不能成为审核/标识失败退款证明；UPDATE/DELETE继续禁止。
- quarantine CHECK保留旧图片条件，仅video允许审核passed且任一标识failed进入隔离；不改写真实审核结论，也不因此自动退款，释放仍需确认成本及原执行原因。
- 复用Task已有cancel_requested_at，与唯一cancel_requested事件同事务CAS；时间不能清除/覆盖，意图先落库后不得再取得提交权。共享TaskEvent增加provider_no_product_confirmed及provider_result_conflict保留事件类型，仍使用既有fact_sha256，不新增表；无产物证明需同任务零成本终态确认，pending或已有矛盾观察不得补证。
- G5请求必须从unquoted/pending/pending建立；settled/released需实际Hold、请求关联、冻结/解冻/消费流水匹配。available还需独立交付Outbox且不存在未完成视频补偿；服务层在六资产交付事务前后执行完整对账。
- ai_compensation_tasks新增视频专用可空version_no、attempt_count、locked_by、lease_mode、last_safe_error_code、completed_at、review_maker_id、review_checker_id，旧类型不写新列。视频类型使用CAS、2分钟租约、8次上限与低敏错误枚举；原retry_count/locked_at继续保留并校验一致性。
- initial_billing_status在首次创建视频补偿时从当前请求冻结，SQL校验与请求一致且不可更新；它决定历史P事件是否必需，不能通过后改origin把缺失P伪装为合法终态补偿。执行未知、状态及P/C同事务；旧完成/耗尽任务保留，仅追加人工核对请求。
- 人工核对在共享TaskEvent追加review_maker_id/review_checker_id；人工租约需同版本不可变事件。completed需财务、交付及输入租约闭合，RecoverDelivery把正常交付与补偿complete放在同一事务，RecoverRelease把释放闭合与complete放在同一事务。

统一发布新增冻结的origin_error_code及仅供当前租约使用的delivery_request_version/delivery_prepared_at。Prepare只允许有效租约、已结算请求和匹配目标版本，升version_no但不增加attempt_count；重领清理旧标记。请求available的内部中间态仅允许匹配且未过期的发布标记，六资产发布与completed由同一事务闭合；外部读取仍要求无活动补偿及最终对账通过。
- 追加事实、相反终态互斥、Outbox低敏白名单和财务/交付门禁需数据库与服务双重验证。down必须保留已形成事实。

提交回执继续复用共享TaskEvent的`fact_sha256`：`submission_receipt_accepted`按请求固定唯一键保存首次规范化回执摘要，`submission_receipt_rejected`按请求与候选摘要去重。两类均要求已绑定的G5任务、唯一原queued→submitting证据、worker来源、空from/to及固定低敏原因；不保存原回执正文，不推进状态。拒绝记录必须有原接受记录。既有UPDATE/DELETE保护继续有效，down不删除审计。pending身份、接受与拒绝记录的通用Append入口均关闭。

视频调账复用已有adjustment方向、原因、maker/checker字段；000077仅追加可空的`ai_usage_items.adjustment_wallet_transaction_id`及唯一外键，旧图片模型不写入此列。非空引用要求同用户、同钱包、同金额和正确方向类型的新资金流水，禁止借用任何请求原冻结/消费/解冻。NULL表示没有资金闭合证明，不得通过对账。被引用流水与Usage只追加，不可改删。原Hold、Quote、sale_line、cost_line保持不变，调账的资金与按序号Outbox独立核验。

调账对账按request_id取得完整调整事件集合后再核aggregate_type，额外错误类型不能被查询过滤隐藏。共享Outbox领取器保留聚合类型隔离，并以字面量video_事件前缀拒绝坏聚合类型的视频事实；pending和过期publishing均适用，旧Chat/Image事件及前序顺序规则不变。

已经实现的列和约束以000077源码为准，其余合同见[VID-G5开发合同](./video-gateway-vid-g5-billing-outbox-reconcile.md)和[财务人审包](./video-gateway-vid-g5-finance-review.md)。部分数据库验证不能视为完整G5能力验收。

#### 3.5.18 视频网关 VID-G6 显式授权增量（开发中）

本地000078新增`api_keys.video_generate_allowed`，非空、默认0并限制为0/1。历史Key不自动授权视频；目标模型范围仍使用`api_key_model_scopes`。新增`ai_project_model_capability_grants`，按Project/模型/capability唯一，含用户、active/revoked、version_no、操作者及时间；Project/user、模型和操作者由外键约束。六个新增权限只自动映射admin，不为真实Project或用户开通业务。

视频模型要求目前使用已有发布快照JSON中的`video_contract`，显式区分未配置、无需资产、仅资产、指定权益和会员要求；不增加钱包、Usage或请求表。配置读取采用当前读，不能用旧RR快照绕过已提交撤权。000078的down保留结构和授权事实；当前只有隔离验证，没有共享库或生产迁移。完整字段与开发状态见[VID-G6合同](./video-gateway-vid-g6-http-project-sk-contract.md)。

#### 3.5.19 视频网关 VID-G6 权利事实增量（开发中）

`000079_video_rights_contract`新增两个非财务表，不预置条款或接受记录。`ai_video_rights_policies`保存版本、非商业用途、中文标题/正文、正文SHA256、有效期、显式合成接受TTL、状态及version_no；生成列active_slot的唯一键限制最多一个active版本。政策身份/正文/标题/期限不可变，退役/撤销必须递增版本，DELETE禁止。

`ai_project_video_rights_acceptances`为追加式Project接受回执：公开ID、user/project/accepted_by、policy_id/version/body_sha256、rights_accept命令域、幂等键SHA256、请求指纹、HTTP request_id、accepted_at及expires_at。唯一键为user/project/命令域/键hash；Project所有者复合FK、accepted_by=user_id检查和政策三元FK绑定归属与版本身份，UPDATE/DELETE触发器保留原事实。不存在有效政策时历史回执仍可查询，但不能恢复有效授权。TTL仅为显式合成配置，真实政策未批准时不启用I2V。down不删除表、数据或约束。

#### 3.5.20 视频网关 VID-G6 Quote/Request权利声明（开发中）

000080新增`ai_video_rights_declarations`授权审计表：command_kind为quote/generation；引用原quote_id及可空request_id、user/project/key；保存政策三元身份、政策期限、可空项目接受ID、来源、HTTP trace、原confirmed_at及verified_at。quote声明与生成声明分别唯一，用户/Project/Key、原Quote/Request及政策外键均保留；触发器校验图生操作、接受归属与原确认时刻，generation还必须匹配Quote实际consumed_request_id。UPDATE/DELETE禁止，down保留事实。此表不保存Prompt、不另建钱包/Usage，也不改变G2/G5定价指纹。

#### 3.5.21 视频网关 VID-G6 上传控制（开发中）

000081新增`ai_video_upload_controls`，以原UploadSession为主键并复合外键绑定user/project。保存创建指纹与命令hash、预期源SHA256、规范扩展名、预分配InputAsset公开ID、服务端规范化对象位置、固定上传到期、容量预留、version_no、只写一次的complete/cancel命令hash、工作租约、cleanup_pending/cleaned_at及低敏错误分类。无Prompt、图片正文、签名URL或凭据字段，不另建资产或视频账本。

000108只替换上述控制表的插入守卫，使原账本同时接受`platform_presigned`和`openai_inline_multipart` UploadSession；inline仍必须使用服务端生成的Target、相同容量/期限/版本/完整性约束和原Complete链。down恢复只允许新建platform控制记录，但保留全部既有inline会话、输入、任务和财务事实。详见[inline I2V合同](./video-gateway-vid-g6-inline-i2v-contract.md)。

创建唯一键为user/project/create_key_hash；InputAsset公开ID和规范化位置分别唯一。插入要求同归属created/platform_presigned会话，固定15分钟上传期限及原件大小加10MiB预留。更新禁止身份、位置、hash、期限、预留漂移，CAS版本须恰好加一；DELETE禁止。仅有G6控制行的原会话受新增状态触发器约束，旧G3/G5会话不自动迁移。completed只能绑定同会话同归属、指定对象及hash、ready/passed的640—4096尺寸PNG资产。down保留所有结构和事实；只在临时隔离MySQL验证，未执行共享库迁移。

#### 3.5.22 视频网关 VID-G6 来源导入控制增量（开发中）

000082创建`ai_video_input_imports`，以原InputAsset为主键并复合FK绑定User/Project，另复合FK指向原来源图片。只记录公开命令ID、命令hash/指纹、原Key、冻结源ID/version/hash/规格/位置、服务端目标位置、工作CAS租约、处理期限和清理状态，不是第二套InputAsset或财务账本。来源资产不设全局唯一，不同命令允许生成独立快照；同user/project/命令hash唯一，跨Key不能重放。

插入需对应gateway_asset_snapshot/normalizing且无规范化hash的原InputAsset和同Key源；更新保留身份及源/目标快照，版本恰好加一，终态不能互换，DELETE禁止。reserved_bytes初始10MiB，只有发布完成可改为实际InputAsset.size_bytes；待清理失败目标继续占额，cleaned_at只能首次设置。completed必须关联已规范化、审核通过且正确定位的输入。down保留结构与事实；只在临时MySQL验证，未部署。输入/上传容量检查共用两类控制记录并排除重复计入的InputAsset。

#### 3.5.23 视频网关 VID-G6 输入删除申请凭据（开发中）

000083创建`ai_video_input_deletion_requests`，主键指向原InputAsset，复合FK绑定User/Project，保存来源Key、命令hash、原版本、唯一删除后版本、规范化hash、审核版本、原到期时间和申请时间。插入必须对应同主体ready/passed且无保全/删除申请的输入；来源会话或图片必须匹配Key。记录禁止UPDATE/DELETE，同user/project/命令hash唯一，不是第二套资产账本。

删除申请在同一事务创建凭据并将InputAsset推进pending_delete，原TaskInput版本、hash和租约不变。新增触发器以共享当前锁读观察执行租约，活跃租约禁止expiring/deleting/deleted/delete_failed；进入pending_delete须有匹配的原/后版本凭据。额外版本漂移仍使执行复验失败。新Quote与新绑定继续严格要求ready，不放宽旧INSERT约束。down保留结构和保护规则，不回退已经形成的删除事实。

当前只实现申请与仓储复验基础，专用任务参考图读取、HTTP删除、保留窗和对象清理尚未闭环。不得将该迁移视为媒体删除已完成，也不得部署到共享环境。

#### 3.5.24 视频网关 VID-G6 输入清理完成事实（开发中）

000084创建`ai_video_input_cleanup_facts`，以原InputAsset为主键并引用原删除申请，记录归属、清理前后版本、规范化hash、策略版本、绑定保留秒数、来源种类、最早清理时刻及完成时刻。仅可追加，禁止UPDATE/DELETE；不是第二套资产账本。插入须匹配原删除版本、Input deleted/版本/hash/完成时间、原upload/import控制记录cleaned_at，并检查原输入到期和所有绑定安全终态及租约释放后的7天保护。

数据库只能证明完成记录与原事实一致，不能自行证明外部对象已删除。Service必须先取得同步存储的同目标删除及围栏确认，再把Input、容量控制和完成事实一起提交。存储成功而数据库失败不能回滚正文，须保持未完成并按原目标恢复；down保留结构与完成事实，禁止让已删媒体重新显示可用。当前仅在临时MySQL与同步Fake存储验证，未部署。

#### 3.5.25 视频网关 VID-G6 下载操作性租约（开发中）

000085增加`ai_video_download_scopes`和`ai_video_download_leases`。前者以范围类型/ID为主键，仅串行化用户与Project的下载名额计数；后者保存随机连接令牌、原用户/Project/Task/Asset、version CAS、创建/租约截止/释放时间，并以复合外键约束原Task及Asset归属。它们不是新的任务、资产或金融账本，不保存Prompt、Key、对象位置或Provider正文。

用户2、Project4的原子申请在已授权content事务、读取Store之前完成；所有Key共享用户额度。申请和续约均按User→Project锁范围，再以数据库UTC微秒时钟计数或校验租约。只锁租约行续期是不安全的：续约未提交跨旧TTL时，RC计数可能误补第三个名额；固定窗口测试覆盖该边界。60秒TTL为本阶段工程选择，不改变媒体保留政策。续约/释放均绑定原令牌、归属和version，过期令牌不得复活。释放只更新原操作性租约，不改Task、Asset、钱包或Hold；未知提交/释放失败采用期限保护，不能把未知状态认作已释放。down保留两表和历史记录，只回退应用并保持视频关闭。

#### 3.5.26 视频网关 VID-G6 用户取消命令（开发中）

000086创建`ai_video_cancellation_commands`，以用户/Project/固定cancel命令种类/幂等键SHA-256为主键，记录原Task、业务request_id、来源Key、首次处理结果和创建时刻。两个HTTP别名共享此命名空间，不因Key或路径创建另一套命令；精确Key访问控制独立执行。复合外键与INSERT守卫绑定原视频Task/Request、相同Key和初始状态；UPDATE/DELETE禁止，不能改写已接受意图。

回执与原G5未提交取消在同一外层事务提交。G5公开入口仍自行开启并重试事务；G6传入事务时只执行一次原金融apply，由最外层统一重试，禁止1213已回滚整笔事务后仅重试保存点。所有规则只作用于合成隔离库，未部署；down保留回执及原财务、任务、事件与输入租约事实。

#### 3.5.27 视频网关 VID-G6 媒体删除意图（验证中）

000087增加`ai_video_media_deletions`与`ai_video_media_delete_commands`。前者关联原Task/Request/User/Project/Key，保存不可变内部目标计划及其hash、版本和deleting/delete_failed/completed状态；后者只记录用户/Project/媒体删除幂等键到原Task的映射。它们不替代原资产或财务账本。INSERT守卫约束视频身份、精确Key及安全终态；空计划completed仅允许无产物非成功任务。UPDATE只能依版本按既定删除状态迁移，不能换计划、身份或时间；禁止删除原操作事实。

实际对象删除由存储边界确认，SQL记录不能单独证明正文消失。应用层必须核对真实资产树和五删一留角色、版本/hash/位置，完成前确认保留副本。确认事务失败时不撤销第一阶段隐藏意图，不恢复媒体；down只保留结构与事实。当前仅用于隔离测试，未部署；完整保存引用/清理竞争仍待后续验证。

### 3.5.28 视频保存协调草稿（VID-G6，未开放）

000088新增`ai_video_asset_save_scopes`范围锁、`ai_video_asset_saves`唯一Task的复制/容量协调记录和`ai_video_asset_save_commands`不可变幂等映射。长期资产仍存入原`user_assets`并写`asset_events`，协调表不是另一份视频或财务账本。保存操作固定原用户/Project/Key/Task/Request、存储商品/权益、明确单位与占用量、策略版本、五对象复制计划/hash；状态按version CAS迁移，完成时关联同用户的video_file资产，down保留事实。

本迁移仍处于开发：触发器创建或schema加载成功不代表INSERT、容量事务或复制已验证。真实协调INSERT、保存HTTP、容量结转、复制恢复已有局部证据，JSON五元素检查仍不能代替实际资产树或目标字节验证。完整恢复/竞争与长期读取尚未完成，详情见`video-gateway-vid-g6-asset-save-contract.md`。

### 3.5.29 未发布视频保存清理证据（VID-G6）

000089只在原`ai_video_asset_saves`增加cleanup_policy_version、cleanup_reason、cleanup_eligible_at、cleanup_started_at、cleanup_finished_at、cleanup_proof_sha256，全部是内部低敏恢复元数据。首次cleanup_pending必须绑定同Task/Request/Owner的实际Source期限或原权益/父资产期限，且已经到期；匹配用户资产拒绝，接受后的资格和策略不可修改。aborted必须有完成时间与摘要，不能提前写完成字段，down保留所有事实。

清理完成同时释放原存储权益reserved并在原父资产追加事件，不写生成钱包/账单。摘要和状态不是物理删除证明，服务重放及原结果删除仍核验五目标标记和唯一事件；未知Head失败不能当作不存在。仅临时MySQL与Fake边界验证，无部署或Worker运行。

### 3.5.30 保存时冻结存储权益类型（VID-G6）

000090在`ai_video_asset_saves`增加可空`storage_entitlement_type VARCHAR(64)`。新保存INSERT必须非空且与同用户、商品和原权益的类型逐字节一致；UPDATE禁止改变，包括大小写漂移。历史NULL不回填当前值，缺原始事实的长期读取失败关闭。down保留字段和保护触发器，不删除保存事实。

长期读取不使用当前新保存配置推断旧资产资格，而使用冻结类型、原权益ID、单位、当前商品/父资产/权益和全部completed保存容量聚合复验。URL及每片读取继续校验原Task、Quote、结算、六资产安全树及五份长期对象。合同见[长期副本读取](./video-gateway-vid-g6-saved-read-contract.md)，尚未完整阶段验收。

### 3.5.31 视频保存尝试身份（VID-G6，验证中）

000091在原保存协调体系支持同Task多次历史尝试：`ai_video_asset_saves.public_id`成为尝试主键，`attempt_no`与`previous_save_id`保留序号和前驱；`live_task_id`生成列唯一约束保证同Task最多一个非aborted尝试，completed也占位。原Task复合外键、计划、清理证明、用户资产和财务事实保留。

命令新增不可变`save_public_id`。迁移先在旧Task唯一关系仍成立时，按Task/User/Project/NULL安全Key确定性回填；孤儿或歧义拒绝，不猜latest。复合外键和触发器约束精确尝试，前驱必须同Task相邻已清理终态且最多一个后继。旧NULL权益类型不因本迁移回填。

39166证明隔离空库、重复up/down/up及基础新旧尝试服务流程。后续30701从89版真实旧列夹具验证四种保存状态、19表原列保留、九个ALTER后中断重入、最终索引/FK定义及身份拒绝，不代表旧二进制或所有触发器空窗验收。旧NULL权益类型不回填，未完成恢复必须失败关闭。迁移和回滚须关闭保存及依赖它的媒体DELETE/清理，或使用理解多尝试的兼容版本；down不丢弃历史结构。详见[保存新尝试合同](./video-gateway-vid-g6-save-reattempt-contract.md)。

### 3.5.32 平台单资产删除协调（VID-G6，验证中）

000092新增`ai_video_asset_deletions`（选定普通子资产唯一的删除见证）与`ai_video_asset_delete_commands`（用户/Project/幂等键对应原资产、版本及video/asset作用域）。根组复用000087媒体删除，不新增Task、钱包或资产账本。输入版本、目标计划/hash及归属不可变；状态按CAS迁移，completed必须对应真实资产deleted标记和版本。

INSERT将计划中的asset_id/public_id/role/bucket/object_key/hash/size/prepared_version绑定到当前资产；服务同样显式比较，不能用不含内部位置的普通元数据摘要替代目标检查。根组计划的PreDeleted为可选字段，旧计划省略它保持原摘要，新计划只能用已验证的单删见证跳过已删除子资产的版本推进。

down保留全部事实。回滚旧代码前关闭相关读取、保存与删除能力，或使用兼容单删见证的版本；详见[平台资产删除合同](./video-gateway-vid-g6-asset-delete-contract.md)。当前仅隔离验证，无共享库或生产迁移。

### 3.5.33 内部回调nonce事实（VID-G6，验证中）

000093新增`ai_video_callback_nonces`，主键为provider_code + nonce_sha256；保存完整规范签名请求的request_sha256、原callback_event_id、signed_at和created_at，不保存nonce原值、签名、密钥或正文。外键指向共享`ai_gateway_provider_callback_events`。INSERT守卫要求同Provider、valid验签、applied/ignored处理结果和完整任务归属；UPDATE/DELETE均拒绝。

回调原三元唯一键不变，nonce只是接收请求防重放事实，不建立第二套事件或财务账本。Task/Event、原Callback处理及nonce同事务，nonce写入失败必须一并回滚；提交结果未知只能按原事实重试。down保留全部事实并要求关闭内部接收入口，见[内部回调合同](./video-gateway-vid-g6-callback-contract.md)。

### 3.5.34 管理任务与输入查询（VID-G6，验证中）

输出管理与运行汇总也不新增表：输出读取原Asset→Task→Request/Key及同任务content父关联；汇总读取原视频请求、video_reconcile补偿、video_request Outbox及WalletLink/Hold/Wallet。均同RR一致性快照及当前管理员前后复验。汇总只展示运行事实，不领取补偿、不派发Outbox、不改变钱包；不能因不完整Hold关联而用聚合零值冒充正常。

管理列表不增加表或复制视频账本。任务列表复用原Task/Request/Quote/Hold/资产与对账事实；输入列表复用`ai_gateway_input_assets`、`ai_upload_sessions`以及原图片Asset/Task/Request，API Key从来源解析而非给InputAsset新增影子Key字段。

任务列表选页及当前锁定状态不一致时整页有界重试；跨用户钱包锁序已进入最终同源并发回归。输入管理查询在RR快照读取计数、条目与来源，并在管理员权限/MFA前后复验。查询不修改财务、业务事件或审批事实，不隐去停用/删除历史、不授予对象读取能力。完整字段及历史缺陷关闭矩阵见[管理查询合同](./video-gateway-vid-g6-admin-read-contract.md)。

### VID-G6最终本地Schema状态（2026-09-01）

本文较早VID-G6段落中的“开发中、局部、尚未完成”保留为历史过程。最终证据目录绑定的冻结副本已在一次性MySQL 8完成000001→000109、G6迁移up/down/re-up、事务/锁/CAS/100并发和Linux race全量验证；原请求、Quote、Hold、Usage、Task、资产、回调、审计与财务事实保持单一账本。未执行共享测试库或生产migration，应用回滚仍必须保留全部业务和财务事实。

### 3.5.35 管理员取消回执（VID-G6，验证中）

000094新增`ai_video_admin_cancellation_commands`，主键为actor_user_id+command_key_hash，引用原Task/Request/owner/Key和前后audit_logs。记录初始及最终Task版本、低敏初始结果、原因HMAC/长度、key version、nonce、AES-GCM密文、AAD SHA-256及密文SHA-256。没有原因明文列，不复用Task.prompt载荷类型，不新增钱包或任务账本。

INSERT核对审计操作者/动作/目标/摘要及当前原任务结果；执行状态从Task读取，计费与交付状态仍从原Request读取。回执UPDATE/DELETE拒绝。G5取消、审计及回执共用外层事务，任一失败全部回滚；down保留事实，只能关闭管理写入口，不能撤销已完成退款或删除原审计。详见[管理员取消合同](./video-gateway-vid-g6-admin-cancel-contract.md)。

### 3.5.36 管理员输入隔离回执（VID-G6，验证中）

000095新增`ai_video_admin_input_quarantines`。主键为actor_user_id+command_key_hash，引用原InputAsset归属、原Key及前后audit_logs；冻结initial_state、initial/final_version、原因AES-GCM信封/HMAC/长度及隔离发生时间created_at。初态只允许原矩阵四态，final_version=initial_version+1。INSERT校验原来源关系及隔离结果、操作者和审计摘要，UPDATE/DELETE禁止。

不新增输入账本，不修改原InputAsset的审核结论、规范化快照、来源、保全或expires_at；不修改TaskInput、任务或财务。旧取消原因AAD保持兼容，输入隔离采用独立领域。down仅保留事实并要求关闭入口，不能自动解除隔离。详见[输入隔离合同](./video-gateway-vid-g6-admin-input-quarantine-contract.md)。

### 3.5.37 管理员输出隔离凭据（VID-G6，验证中）

000096增加`ai_video_admin_output_quarantines`及原资产的可空`admin_quarantine_command_id`。命令prepared/version1→completed/version2，冻结原资产/Task/Request/owner/Key、初态及版本、安全快照SHA256、专用原因AES-GCM信封和前后审计。完成回执不可UPDATE/DELETE。资产首次指针绑定必须匹配prepared命令和完整OLD/NEW快照，完成必须证明实际隔离CAS及后审计。

不覆盖原成功审核和标识，不移除原隔离CHECK的普通路径；仅增加受凭据与触发器约束的video行政隔离例外。指针不能由普通UPDATE清除/替换或退出隔离。解除及到期隔离清理仍待独立凭据路径集成，不能解释为无限保留；down保留所有事实。详见[输出隔离合同](./video-gateway-vid-g6-admin-output-quarantine-contract.md)。

## 4. 关键状态

VID-G6 000107新增不可变`ai_project_key_commands`，保存视频Key issue/rotate/revoke的主体、HMAC命令键、意图指纹、源/结果Key、严格`{key_id,status=completed}`结果、审计ID及审计摘要SHA-256。没有Secret、KeyHash或可恢复密文；服务逐次验证完整低敏审计摘要和issue/rotate/revoke状态关系，命令禁止UPDATE/DELETE，down保留事实。详见[Key幂等合同](./video-gateway-vid-g6-project-key-idempotency-contract.md)。

VID-G6 000109新增单行`ai_video_queue_admission_guard`，只序列化原Task账本的queued容量读取，不保存第二套队列深度、任务或运行租约。down保留门闩，代码回滚即停止使用；G7不得把该行误作Redis/Provider并发事实。详见[排队容量合同](./video-gateway-vid-g6-queue-admission-contract.md)。

VID-G6 HTTP预算不新增表：复用G4策略、override与reservation表，通过同连接SAVEPOINT参与原G5事务。策略锁后按Project timezone和UTC时钟冻结账期/到期，cancel/settle/release在原财务终态事务内同步预算。详见[预算准入合同](./video-gateway-vid-g6-budget-admission-contract.md)。

VID-G6 000106新增不可变`ai_video_project_grant_commands`，冻结actor、owner、Project、模型、动作、初始/结果版本、意图摘要、原因信封、结果及前后审计。SQL约束JSON结果、摘要、key版本、nonce/密文及原因长度；服务首次写后重读核验原审计归属、密文和结果。授权本体仍为`ai_project_model_capability_grants`；命令禁止UPDATE/DELETE，down保留全部事实。详见[Project授权合同](./video-gateway-vid-g6-project-grant-contract.md)。

VID-G6 000105将原`ai_video_model_draft_commands.action`扩展为create/update/publish/unpublish/rollback，并增加单行`ai_video_model_publication_guard`串行默认模型决策。模型和发布事实仍在原表；down保留所有命令、版本和协调事实。详见[受控发布合同](./video-gateway-vid-g6-model-publication-contract.md)。

VID-G6 000104为`ai_video_model_draft_commands`增加可空`source_sha256`。仅历史接管（update、initial_version=0）必须提供64位小写SHA256，普通创建/更新为NULL。摘要绑定接管前的模型ID、配置、排序、发布及修改事实，并进入前后审计；down保留列及事实，不删除模型或重建历史。

VID-G6 000103新增原模型的`ai_video_model_draft_states`版本/摘要围栏，以及不可变`ai_video_model_draft_commands`。创建version0→1、受控更新n→n+1；按actor/action/key唯一，与模型变更、加密原因及前后审计同事务。命令禁止UPDATE/DELETE，down保留全部事实。详见[受控草稿合同](./video-gateway-vid-g6-model-draft-contract.md)。

VID-G6 000102为原`token_models`增加`video_contract_json JSON NULL`，作为视频七键合同工作副本。历史记录保持NULL，不从旧能力自动推断授权；非NULL时要求模型为video且JSON类型为OBJECT。严格七键及商品/权益组合由共享领域解析器校验，发布快照保存`video_contract`；down保留列及已有值，不改历史发布、任务或财务事实。完整管理写链仍在开发，详见[模型管理开发文档](./video-gateway-vid-g6-model-management-development.md)。

VID-G6 000101新增不可变`ai_video_adjustment_approvals`及`ai_video_adjustment_approval_executions`。申请冻结原Task/owner/Key、Task版本、CNY金额/方向/低敏原因码、不可复用序号、计划SHA256、专用原因信封、前后审计及15分钟操作期限。独立checker的prepared/version1→executed/version2与原G5 Usage、钱包流水、Outbox同事务，资金引用唯一且SQL核对原金额/方向/双主体。down保留全部事实，不改原settled/released。详见[调账双人审批合同](./video-gateway-vid-g6-admin-adjustment-contract.md)。

VID-G6 000100增加`ai_video_admin_archive_commands`，保存原Task/Request/owner/Key、Provider绑定摘要、原Task版本、归档代次/起始phase、原因专用AES-GCM信封、前后审计及期限。每任务一个running操作，running/version1→completed或unknown/version2；completed须对应同代次真实succeeded及围栏已清。归档成功、后审计和完成回执同事务；原unknown不被后续成功覆盖，down保留事实。参见[归档HTTP合同](./video-gateway-vid-g6-admin-archive-contract.md)。

VID-G6 000099仅扩展原Task归档围栏：archive_generation单调代次、archive_token_hash（原始32字节令牌不落库）、archive_lease_until、私有archive_phase。认领、phase推进和退让均增加原version_no并追加TaskEvent；Task执行轴不随技术phase回退。仓储使用锁后及提交前租约时钟，不把调用前的事件Now当有效租约证明。普通Worker/回调已识别围栏；上层管理员审计、实际IO与资产直写统一围栏仍待接入，见[归档恢复开发记录](./video-gateway-vid-g6-archive-recovery-development.md)。

VID-G6 000098增加`ai_video_admin_poll_commands`：原Task/Request/owner/Key外键、原版本、Provider绑定SHA256、原因专用AES-GCM、前后审计、30秒执行期限。running/version1只能变为completed或unknown/version2；生成列active_task_id唯一限制每任务一个运行中查询。完成后不可改写/删除，回退保留命令与已有业务事实。不复制原任务、Provider正文、Prompt或财务表，见[管理轮询合同](./video-gateway-vid-g6-admin-poll-contract.md)。

VID-G6新增000097解除输出隔离申请/复核：`ai_video_output_release_requests`保存不可变maker申请、原隔离命令/资产版本/安全摘要、原因信封、前后审计及15分钟操作审批期限；`ai_video_output_release_executions`保存独立checker的prepared→completed原子执行，原quarantine_id唯一消费。资产清指针及恢复原temporary/available必须有有效独立复核凭据；保留000096其他守卫及全部原事实，不改媒体保留期限。完整合同及未验证边界见[解除隔离合同](./video-gateway-vid-g6-admin-output-release-contract.md)。

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
