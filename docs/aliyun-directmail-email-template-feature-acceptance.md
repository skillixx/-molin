# 阿里云 DirectMail 邮件模板与邮箱验证码功能及整体验收

> 文档性质：邮件模板管理、邮箱验证码发送和阶段验收的中文权威说明。
>
> 当前状态：Phase 1 契约评审、Phase 2 本地实现证据和 Phase 3 前端验收已经形成；Phase 4 真实测试环境与 Phase 5 生产上线均未通过。
>
> 证据边界：本文只记录当前仓库实现和已经留存的验收事实。Mock、静态检查、离线单元测试、供应商 `accepted` 均不能替代真实收件、真实 Redis、RAM 否定测试或生产验证。

## 1. 功能说明

本功能把阿里云 DirectMail 邮件模板映射为墨灵平台内的只读镜像，并由墨灵负责固定业务场景绑定、本地启停、模板同步、测试邮箱白名单、测试发送、发送日志和邮箱验证码状态控制。

管理员在“消息中心 → 邮件模板”维护墨灵侧配置。注册、邮箱验证码登录、找回密码、换绑邮箱和管理员邮箱双重认证通过统一邮件发送能力获取验证码。验证码只有在供应商明确受理后才允许校验。

本功能不在墨灵后台创建、修改或删除阿里云模板，不提供 DirectMail 凭据录入页面，也不提供 Adapter 切换页面。

## 2. 使用角色

| 角色 | 使用能力 |
|---|---|
| 具有 `email:template:view` 且完成手机、邮箱双重认证的管理员 | 查看概览、模板镜像、五场景、同步记录、测试白名单和发送日志 |
| 具有 `email:template:manage` 且完成双重认证的管理员 | 本地启停模板、维护五场景绑定、维护测试邮箱白名单 |
| 具有 `email:template:sync` 且完成双重认证的管理员 | 从 DirectMail 同步模板镜像 |
| 具有 `email:template:test` 且完成双重认证的管理员 | 向生效白名单邮箱发送模板测试邮件 |
| 经批准且具有 `email:template:bootstrap`、有效手机 MFA 与内部双闸的运维管理员 | 一次性配置 `admin_verify` 投递通道；不完成邮箱 MFA，无前端入口 |
| 未登录用户 | 获取注册、登录、找回密码场景的邮箱验证码 |
| 已登录用户 | 获取换绑邮箱验证码并完成换绑 |
| 管理员 | 向当前账号已绑定邮箱发送管理员双重认证验证码；客户端不能指定收件邮箱 |

所有管理接口都同时检查 Bearer Token、管理员手机与邮箱双重认证以及对应邮件权限。未完成双重认证固定返回 `403/40003「请先完成管理员双重认证」`；普通权限不足仍使用 `403/40003`，但不得误导用户重新认证。

## 3. 页面入口与前端范围

管理后台入口固定为：

```text
管理后台
  → 消息中心
      → 邮件模板
```

页面路由为 `/message/email-templates`，由 `email:template:view` 控制菜单和路由准入。页面包含：

1. 概览：模板总数、审核通过数、本地启用数、未绑定场景数、今日提交数、今日失败数、最近成功同步时间。
2. 模板：查询镜像、查看审核与安全状态、本地启停、查看详情和隔离预览。
3. 场景绑定：展示固定五场景、选择合规模板、启停绑定、提交当前版本。
4. 同步记录：执行同步并查看运行中、成功、失败记录。
5. 测试白名单：新增测试邮箱、查看脱敏邮箱、撤销生效记录。
6. 发送日志：按场景、用途、状态、模板和时间筛选公开日志。

管理页面必须具备加载中、空数据、错误、无权限降级和正常五态。模板详情的 404、500 等错误在详情弹窗内展示安全中文消息和重试入口，不能形成空白弹窗。桌面、平板和手机分别按 1440、768、390 像素视口验收；手机端使用可关闭抽屉展示导航。

用户登录页保留邮箱密码登录，同时增加邮箱验证码登录。两种方式共用登录成功后的 Token、用户详情、权限加载和跳转流程；增加验证码登录不得替换或破坏既有密码登录。

## 4. 固定五场景与变量映射

场景是封闭枚举，后台不能新增、删除或重命名。

| scene | 中文名称 | 发码入口 | 消费行为 |
|---|---|---|---|
| `register` | 注册 | 公开邮箱发码 | 统一注册，仅可消费一次 |
| `login` | 邮箱验证码登录 | 公开邮箱发码 | 邮箱验证码登录，仅可消费一次；邮箱密码登录继续保留 |
| `reset_password` | 找回密码 | 公开邮箱发码 | 重置密码并按认证规则吊销旧会话 |
| `bind_email` | 换绑邮箱 | 登录态专属发码 | 只能换绑当前流程提交的目标邮箱 |
| `admin_verify` | 管理员邮箱双重认证 | 管理员专属发码，无请求体 | 只能发送到当前管理员已绑定邮箱 |

模板变量映射固定且只读：

| 墨灵业务字段 | DirectMail 模板变量 |
|---|---|
| `code` | `Code` |
| `expire_minutes` | `ExpireMinutes` |

变量名大小写必须完全一致。绑定、模板启用、模板测试和正式 OTP 发送四个入口都重新校验变量；发送端从冻结镜像 `TemplateText` 本地渲染，兼容 `{Code}`、`${Code}`、`{{ Code }}` 三类既有语法及对应的 `ExpireMinutes`，只替换这两个固定变量。缺少变量、大小写错误、畸形/嵌套占位符、额外变量或渲染后残留受支持变量均固定返回 `422/51001「邮件模板变量不完整」`。

## 5. 阿里云与墨灵职责边界

| 责任方 | 负责 | 不负责 |
|---|---|---|
| 阿里云 DirectMail | 在供应商控制台维护模板；提供模板列表、模板详情和单封邮件发送；返回供应商审核状态与请求受理结果 | 不决定墨灵的场景绑定、本地启停、平台权限、验证码是否可消费或测试白名单 |
| 墨灵平台 | 只读同步模板镜像；维护本地启停和五场景绑定；生成验证码与过期时间；控制幂等、锁、限流、审计、脱敏、白名单和验证码消费资格 | 不从管理后台创建、修改或删除供应商模板；不把 `accepted` 解释成最终送达 |

生产 Adapter 仅使用以下供应商能力：

- 模板列表查询。
- 模板详情查询。
- 单封邮件发送。

Mock Adapter 只允许在显式安全非生产环境使用。Mock 成功只能证明本地代码路径，不证明 DirectMail 配置、RAM 权限、外部网络、供应商受理或用户收件。

## 6. 可发送条件

正式 OTP 或模板测试发送必须按顺序满足以下条件：

1. scene 属于固定五场景，且调用端点有权使用该 scene。
2. 生产环境必须选择 Production Adapter；未知环境失败关闭。
3. DirectMail、邮件 HMAC、幂等、Redis 和来源边界配置完整。
4. 正式 OTP 已存在启用的场景绑定；模板测试的路径模板存在。
5. 模板为 `approved`、本地启用、非 `missing`，并完整包含 `Code` 与 `ExpireMinutes`；主题为有效 UTF-8、非空且不超过 100 个 Unicode 字符，本地渲染后的 HtmlBody 为有效 UTF-8、非空且按 UTF-8 字节不超过 80 KiB。
6. 正式 OTP 的供应商模板 ID 从当前绑定解析；模板测试从路径中的平台模板镜像 ID 解析。
7. 收件人是单个合法裸邮箱地址；正式 OTP 必须属于当前业务流程目标，测试发送必须命中 active 白名单。
8. IP 与账号维度限流通过。
9. Redis 分布式锁已取得，且幂等 scope、key 和请求指纹不冲突。
10. 正式 OTP 验证码和发送日志在外呼前以 `pending` 原子落库；测试发送日志在外呼前以 `pending` 占位。
11. attempt 审计已经成功写入，日志和审计脱敏策略可用。
12. 只有供应商明确受理才可收敛为 `accepted`；明确失败或结果未知都收敛为 `failed`。

任一前置条件失败都必须失败关闭，不能退化为“本地生成可用验证码但不发邮件”。主要错误语义如下：

| 条件 | HTTP/code | 固定消息 |
|---|---|---|
| 邮件资源不存在 | `404/40400` | `邮件资源不存在` |
| 场景无绑定 | `409/40900` | `邮件场景未绑定模板` |
| 场景停用 | `409/40900` | `邮件场景已停用` |
| 模板本地停用 | `409/40900` | `邮件模板已停用` |
| 模板为 draft | `409/40900` | `邮件模板尚未提交审核` |
| 模板为 pending | `409/40900` | `邮件模板正在审核` |
| 模板为 rejected | `409/40900` | `邮件模板审核未通过` |
| 模板为 missing | `409/40900` | `邮件模板在供应商侧不存在` |
| 模板变量不完整 | `422/51001` | `邮件模板变量不完整` |
| DirectMail 或 RAM 调用失败 | `502/51002` | 平台安全中文消息，不返回供应商原始错误 |
| Redis 锁、生产 Adapter 或必要配置未就绪 | `503/51003` | `邮件发送服务未就绪` |

## 7. OTP 与发送日志状态

内部状态流转固定为：

```text
verification_codes.send_status
  pending
    ├─ DirectMail 明确受理 → accepted
    └─ 明确失败、拒绝、超时或结果未知 → failed

email_send_logs.status
  pending
    ├─ DirectMail 明确受理 → accepted
    └─ 明确失败、拒绝、超时或结果未知 → failed
```

验证码校验必须同时满足：

- `send_status=accepted`。
- 未使用。
- 未过期。
- scene、目标邮箱摘要和验证码摘要匹配。

消费与 `used_at` 更新必须在同一事务中原子完成，并发提交同一码最多一次成功。`pending`、`failed`、已使用、已过期、scene 不匹配或结果未知的验证码均不能认证。

管理端发送日志只公开 `accepted` 和 `failed`。内部 `pending` 不进入列表、筛选和概览统计。

### 7.1 accepted 的精确定义

`accepted` 只表示“供应商已受理发送请求”，不等于：

- 邮件已送达收件箱。
- 用户已看到邮件。
- 邮件已打开或链接已点击。

当前范围不接入投递回执 Webhook，也不提供最终送达、打开率或点击率。供应商 `accepted` 与用户确认收件必须作为两条独立证据记录。

### 7.2 响应未知与持久化阻断

外呼开始后发生超时或结果未知时，原 `pending` 行收敛为 `failed`，安全失败原因为 `provider_outcome_unknown`。数据库中的该记录在冷却期内阻断新旧请求，即使 Redis 重启或锁键丢失也不能自动重发。

- 原请求或旧 Idempotency-Key 重放返回原安全失败，不再次外呼。
- 冷却期内新 key 返回结果确认中的 409，不再次外呼。

### 7.3 UTC 时间一致性

邮件验证码、发送日志、模板镜像、场景绑定、模板同步记录、测试收件人白名单和 bootstrap receipt 的 MySQL `DATETIME` 统一解释为 UTC 墙上时间。现有全局连接仍使用 `loc=Local` 时，auth 邮件仓储负责对称转换。000057 移除三列数据库会话时区默认值并把 receipt 收紧到秒；非零小数秒先以主键、原值、秒值和非时间指纹写入专用持久备份，再受控归一，down 按主键恢复后才删除备份。迁移不猜测历史时区。

- 新建 `pending`、收敛 `accepted/failed`、OTP 消费写入 `used_at` 与所有过期条件使用同一 UTC 边界。
- 进程时区为 `Asia/Shanghai` 时，数据库参数和扫描比较不得产生八小时偏移。
- `expires_at == now` 固定视为已过期；只有 `expires_at > now` 的 accepted、未使用验证码可以原子消费。
- 部署前旧 failed/unknown 行在原真实十分钟冷却窗口结束后不得继续阻断；本轮不需要 migration，也不修改历史数据。
- `EmailTemplateSyncRun` 的 `started_at/created_at/completed_at` 在 create、stale 查询、stale 收敛、成功 apply、失败收敛和扫描返回各边界必须对称转换；`started_at` 恰等于五分钟前的秒级边界时仍不是 stale，早一秒才可收敛。
- 000057 的 manifest 即使非零毫秒行数为0也必须存在；缺 manifest、expected_count/主键/原值/指纹不一致均失败关闭。up 更新后、up 最终、down 恢复后、down 删除备份前四道门禁均由备份反向 LEFT JOIN receipt，源回执缺失或孤儿备份不得被 INNER JOIN 隐藏。down 还必须精确校验备份表六列、引擎、表/列排序规则、主键和三项已启用 CHECK，最终恢复门禁通过后才可删表。MySQL DDL 隐式提交期间任何 partial 都保留 dirty 与备份表，禁止 force、盲目重跑或擅自删备份。
- down 结构断言的 statistics 派生表必须显式投影 non_unique；CHECK_CLAUSE 比较只能把反斜杠单引号窄化为单引号，禁止全量删除反斜杠，以保留正则中的合法转义。字符集 introducer 只能按明确白名单移除：兼容既有 `_utf8mb4`，并兼容 MySQL 8.0.46 三项备份表 CHECK 实际返回的 `_latin1\'...\'`；不得用正则或宽泛规则删除其他下划线标识符。
- 000057 down 当前文件 SHA-256 冻结为 `EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB`。离线回归 fixture 覆盖三个实际 `_latin1` clause、既有 `_utf8mb4`、反斜杠单引号窄化、白名单外 introducer 保留和 `non_unique` 投影。
- 当前自动化只提供离线 SQL 与模型证据；本次未重新连接数据库或执行 migration。修复后仍须在后续授权的 MySQL 8.0.46 隔离库重跑完整 up/down，确认 CHECK_CLAUSE 的括号、字符集标记及 REGEXP/regexp_like 运行时规范化结果；未验证不等于 migration 可部署。

### 7.4 供应商安全拒绝观测

DirectMail 明确拒绝只在既有 `failure_reason` 保存严格枚举的安全类别和 HTTP 状态族：

```text
provider_rejected_{auth|permission|sender|recipient|content|rate_limited|request|other}_{http_2xx|http_3xx|http_4xx|http_5xx|http_other}
```

原始 Code 只有命中代码内严格白名单时才映射到固定类别，未命中一律归 `other`。错误对象、应用日志、审计、响应和数据库都不得包含供应商 Message/raw、原始 Code、请求字段值、正文、OTP、完整邮箱、AccessKey、Secret 或 Authorization。该分类不改变 `accepted` 定义：只有明确成功且存在供应商 RequestId 才能 accepted。

`DescTemplate` 的模板不存在同样使用独立精确白名单；禁止通过包含 `template`、`notfound` 或 `notexist` 等片段做模糊判断。伪装、扩展或未知 Code 一律按通用上游失败和安全 `other` 处理。
- 冷却结束后只有新业务动作或新 key 可以重新发送；旧 key 仍返回原失败。

## 8. 权限与管理员双重认证

| 权限码 | 能力 |
|---|---|
| `email:template:view` | 查看概览、模板、场景、同步记录、白名单和发送日志 |
| `email:template:manage` | 本地启停模板、修改绑定、维护测试白名单 |
| `email:template:sync` | 执行模板同步 |
| `email:template:test` | 执行模板测试发送 |
| `email:template:bootstrap` | 仅调用默认关闭的一次性内部入口，不授权普通 13 个邮件接口 |

普通四权限由 000055 migration 建立或确认，并精确绑定平台管理员角色。管理路由同时执行：登录校验、管理员手机认证、管理员邮箱认证和权限校验。bootstrap 专用权限由 000056 独立建立并记录 ownership，只用于默认关闭的内部入口，要求手机 MFA 但不能替代邮箱 MFA。

前端分别控制页面入口和管理、同步、测试按钮。前端不消费 bootstrap 权限，也不提供入口。后端权限是最终安全边界，不能依赖前端隐藏按钮代替。

## 9. 13 个管理接口

以下 13 个接口构成管理后台邮件模块的完整公开面。所有列表均返回 D-95 扁平分页 `{items,page,page_size,total}`。

| 序号 | 方法与路径 | 权限 | 请求与结果摘要 |
|---:|---|---|---|
| 1 | `GET /api/admin/email/summary` | view | 返回固定七个概览字段 |
| 2 | `GET /api/admin/email/templates` | view | 按关键字、审核状态、本地状态、变量、missing、场景分页查询 |
| 3 | `GET /api/admin/email/templates/{id}` | view | 返回模板快照详情、正文、变量和内容摘要 |
| 4 | `PATCH /api/admin/email/templates/{id}/status` | manage | 提交 `local_enabled` 与当前 `version`，成功后版本递增 |
| 5 | `GET /api/admin/email/scenes` | view | 固定返回五场景，但仍使用 D-95 |
| 6 | `PUT /api/admin/email/scenes/{scene}` | manage | 全量提交 `template_id`、`enabled`、当前 `version` |
| 7 | `POST /api/admin/email/templates/sync` | sync | 请求体固定供应商；Header 必须带 Idempotency-Key |
| 8 | `GET /api/admin/email/template-sync-runs` | view | 按同步状态分页查询，不公开供应商原始错误 |
| 9 | `GET /api/admin/email/test-recipient-allowlist` | view | 分页返回脱敏邮箱与状态 |
| 10 | `POST /api/admin/email/test-recipient-allowlist` | manage | 提交单个邮箱；成功返回 active 脱敏对象 |
| 11 | `DELETE /api/admin/email/test-recipient-allowlist/{id}` | manage | 提交当前 `version`；成功返回 revoked 脱敏对象 |
| 12 | `POST /api/admin/email/templates/{id}/test-send` | test | 提交 scene 与白名单邮箱；Header 必须带 Idempotency-Key；成功只返回 accepted 和脱敏结果 |
| 13 | `GET /api/admin/email/send-logs` | view | 按场景、用途、accepted/failed、模板和时间分页查询 |

模板同步和测试发送每次明确的新用户动作生成新 Idempotency-Key；网络层无响应的重试复用原 key。相同 key 与相同指纹返回原结果，不重复同步或发送；相同 key 与不同指纹返回 `409/40900`。

## 10. 邮箱 OTP 与登录相关接口

以下接口不计入上述 13 个管理接口，但属于同一邮件业务闭环：

| 场景 | 关键接口 |
|---|---|
| 公开发码 | `POST /api/auth/verification-codes/email` |
| 邮箱验证码登录 | `POST /api/auth/login/email/code` |
| 邮箱密码登录兼容 | `POST /api/auth/login/email` |
| 注册消费 | `POST /api/auth/register` |
| 找回密码消费 | `POST /api/auth/password/reset` |
| 换绑邮箱发码 | `POST /api/me/verification-codes/email` |
| 换绑邮箱消费 | `PATCH /api/me/email` |
| 管理员邮箱发码 | `POST /api/admin/auth/verification-codes/email` |
| 管理员邮箱消费 | `POST /api/admin/auth/verify-email` |

公开、换绑和管理员发码首次成功统一返回 `sent` 与 `expires_in`，生产响应不能包含验证码。邮箱验证码登录 Body 只允许 `email` 和 `code`；额外字段固定按请求参数错误处理。

邮箱密码与邮箱验证码登录共用失败保护：同一规范化邮箱累计失败达到阈值后锁定 15 分钟；任一邮箱登录方式成功都清除失败计数。普通邮箱验证码登录不能设置管理员双重认证状态。

## 11. 数据库表与状态数据

### 11.1 五张邮件业务表

| 表 | 用途 |
|---|---|
| `email_provider_templates` | DirectMail 模板只读镜像、本地启停、审核状态、变量完整性、missing 和版本 |
| `email_scene_bindings` | 固定五场景到平台模板镜像的绑定、启停和乐观锁版本 |
| `email_template_sync_runs` | 同步任务、原子结果、计数和幂等记录 |
| `email_test_recipient_allowlist` | 测试邮箱 HMAC、脱敏值、active/revoked 和版本 |
| `email_send_logs` | 正式 OTP 与测试发送的 pending/accepted/failed、模板快照、幂等和安全失败原因 |

### 11.2 现有表增量与支撑表

| 表 | 本功能用途 |
|---|---|
| `verification_codes` | 增加验证码摘要、发送状态、业务请求号、幂等 scope、请求指纹、目标 HMAC/脱敏值和 accepted 时间；保留旧 `code VARCHAR(64) NULL` 兼容 down |
| `permissions`、`role_permissions` | 四个邮件权限及平台管理员绑定 |
| `audit_logs` | 管理写动作 attempt/result 审计 |
| `migration_000055_permission_ownership` | 仅供 migration 精确记录权限与管理员绑定所有权，不是第六张业务表 |

五场景由 000055 migration 预置为 disabled。白名单 revoked 记录满 30 天后可物理删除；模板镜像、同步记录和发送日志按设计保留 180 天。仍被绑定或被历史发送日志引用的模板不得物理删除。

## 12. 前后端关键目录

### 12.1 后端

| 路径 | 职责 |
|---|---|
| `server/internal/modules/auth/model/email.go` | 邮件业务模型 |
| `server/internal/modules/auth/dto/email_dto.go` | 管理接口请求和响应 DTO |
| `server/internal/modules/auth/repository/email_repo.go` | 分页、乐观锁、同步事务、白名单和日志持久化 |
| `server/internal/modules/auth/service/email_adapter.go` | Production DirectMail Adapter 与显式非生产 Mock |
| `server/internal/modules/auth/service/email_service.go` | 五场景、同步、锁、幂等、脱敏、测试发送和 OTP 状态机 |
| `server/internal/modules/auth/handler/email_handler.go` | 13 个管理 HTTP 接口 |
| `server/internal/modules/auth/route.go` | 管理接口权限、MFA 和邮箱发码/登录路由 |
| `server/internal/modules/auth/service/email_metrics.go` | 邮件 Adapter 低基数指标 |
| `server/migrations/000055_add_directmail_email_management.*.sql` | 邮件数据结构、五场景、四权限与精确回滚 |

### 12.2 管理后台

| 路径 | 职责 |
|---|---|
| `web/admin-console/src/views/email/EmailManagementView.vue` | 六个业务页签、五态、操作流程和响应式布局 |
| `web/admin-console/src/components/email/SafeEmailHtmlPreview.vue` | 不可信模板 HTML 清洗、CSP 与空 sandbox 预览 |
| `web/admin-console/src/api/email.ts` | 13 个管理接口封装 |
| `web/admin-console/src/types/email.ts` | 邮件页面类型与 D-95 泛型 |
| `web/admin-console/src/components/layout/SideMenu.vue` | “消息中心 → 邮件模板”权限菜单 |
| `web/admin-console/src/router/index.ts` | `/message/email-templates` 路由、MFA 和 view 权限准入 |
| `web/admin-console/src/api/http.ts` | 固定 MFA 文案与普通 403 的精确分流 |

### 12.3 用户控制台

| 路径 | 职责 |
|---|---|
| `web/user-console/src/views/auth/LoginView.vue` | 邮箱密码、邮箱验证码和手机验证码登录交互 |
| `web/user-console/src/api/auth.ts` | 邮箱发码、邮箱密码登录和邮箱验证码登录接口 |
| `web/user-console/src/stores/auth.ts` | 统一登录成功链路、Token、用户资料和权限 |
| `web/user-console/src/types/auth.ts` | 邮箱验证码登录严格请求类型 |

### 12.4 测试与基础设施

| 路径 | 职责 |
|---|---|
| `tests/email/` | 13 接口黑盒、Redis 锁原语、五场景、unknown、RAM 和敏感扫描资产 |
| `infra/nginx/` | 公开来源 Header 与内部 metrics 代理边界 |
| `infra/prometheus/` | 邮件 Adapter 指标抓取和告警规则 |
| `scripts/configure-directmail-test.ps1` | 测试环境安全配置向导；不回显配置值 |

## 13. 配置键清单

本节只列键名，不记录、示例或推断任何值。实际值必须通过被 Git 忽略的测试环境文件、CI Secret 或部署平台安全配置注入。

### 13.1 应用和邮件

- `APP_ENV`
- `DIRECTMAIL_ACCESS_KEY_ID`
- `DIRECTMAIL_ACCESS_KEY_SECRET`
- `DIRECTMAIL_REGION`
- `DIRECTMAIL_ACCOUNT_NAME`
- `DIRECTMAIL_FROM_ALIAS`
- `DIRECTMAIL_ENDPOINT`
- `EMAIL_ADAPTER`
- `EMAIL_ADDRESS_HMAC_SECRET`
- `EMAIL_IDEMPOTENCY_SECRET`
- `EMAIL_DEBUG_RETURN_CODE`
- `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`
- `EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN`
- `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS`
- `EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`

验证码调试回码只允许在 `APP_ENV` 被显式设置、经 trim+小写后精确属于 `local/development/dev/test/testing`，且 `EMAIL_DEBUG_RETURN_CODE` 经 trim 后精确等于小写 `true` 时开启。`APP_ENV` 缺失、空白、`staging`、未知值或 `production` 均失败关闭；配置对象的本地默认值不构成显式安全环境证明。调试开关的大写、混合大小写、数字、单字母、yes/on 或其他宽松布尔别名均视为关闭。

### 13.2 数据库、Redis、认证和来源边界

- `MYSQL_HOST`
- `MYSQL_PORT`
- `MYSQL_DATABASE`
- `MYSQL_USER`
- `MYSQL_PASSWORD`
- `REDIS_ADDR`
- `REDIS_PASSWORD`
- `REDIS_DB`
- `ADMIN_VERIFY_EXPIRE_HOURS`
- `TRUSTED_PROXY_IPS`
- `INTERNAL_ALLOWED_IPS`
- `INTERNAL_TRUSTED_PROXY_IPS`
- `INTERNAL_API_TOKEN`

### 13.3 监控

- `PROMETHEUS_CONTAINER_IP`
- `PROMETHEUS_SUBNET`
- `PROMETHEUS_PORT`

禁止在文档、前端代码、日志、审计、截图或测试报告中填写上述密钥、Token、连接密码和发信地址实际值。

## 14. 核心业务流程

### 14.1 模板同步

```text
管理员点击同步
  → 前端生成或复用 Idempotency-Key
  → 后端检查 MFA、sync 权限和全局同步锁
  → 创建 running 同步记录
  → 分页读取 DirectMail 模板列表和每个模板详情
  → 全部远端读取成功后开启数据库事务
  → upsert 全部镜像并标记完整结果中消失的模板 missing
  → 同步记录收敛 succeeded，提交事务

任一远端或数据库步骤失败
  → 镜像和 missing 保持原快照
  → 同步记录收敛 failed
```

同步禁止逐页提交。供应商同步只更新远端镜像字段，不覆盖墨灵 `local_enabled`。

邮件仓储内所有无时区 `DATETIME` 统一按 UTC 秒级墙钟读写，包括模板镜像、场景绑定、同步记录、测试收件人白名单、发送日志、验证码及一次性 bootstrap receipt。`Asia/Shanghai` 进程下 bootstrap 写入后读回不得出现加八小时；模板同步比较 `provider_created_at` 前必须先归一数据库扫描值，连续两次相同同步均应计为 `unchanged` 且不得递增版本。

### 14.2 场景绑定与本地启停

```text
管理员读取五场景和合规模板
  → 选择 approved、本地启用、非 missing、变量完整模板
  → 提交 template_id、enabled、当前 version
  → 后端条件更新 version
  → 成功后 version+1；冲突返回 409 并要求刷新
```

模板本地启用需要模板合规；停用必须允许立即执行，即使模板变量已经不完整。停用会立即阻断绑定发送和模板测试。

### 14.3 测试白名单与测试发送

```text
管理员新增单个测试邮箱
  → 后端规范化并只保存 HMAC 和脱敏值
  → active 白名单可用于测试发送

管理员选择模板、固定场景和白名单邮箱
  → 前端生成或复用 Idempotency-Key
  → 后端校验权限、白名单、模板、Redis 锁和幂等
  → 服务端生成无认证用途测试码并从冻结镜像本地渲染 HtmlBody
  → 写 pending 发送日志
  → 调用 SingleSendMail
  → 明确受理收敛 accepted；其他结果收敛 failed
```

测试码不写入 `verification_codes`，响应不返回测试码。

### 14.4 真实 OTP

```text
业务入口请求发码
  → 校验端点与 scene、业务目标、限流和 Redis 锁
  → 从当前场景绑定解析模板
  → 服务端生成验证码、业务请求号和过期时间，并从冻结镜像本地渲染 HtmlBody
  → 原子写 verification_codes=pending 与 email_send_logs=pending
  → 调用 SingleSendMail
  → 明确受理：验证码和日志原子收敛 accepted
  → 失败或未知：验证码和日志原子收敛 failed

用户提交验证码
  → 原子检查 accepted、未使用、未过期和全部摘要匹配
  → 条件更新 used_at
  → 只有一次消费成功
```

## 15. 审计、脱敏与安全预览

### 15.1 允许记录

- 操作人 ID、内部模板/绑定/同步/白名单/发送日志 ID。
- scene、版本、业务请求号、request_id。
- 脱敏邮箱。
- accepted/failed 和归一化安全失败原因。
- 阿里云 RequestId。

### 15.2 禁止记录或展示

- 验证码和测试码。
- 完整邮箱。
- AccessKey、签名、内部 Token 和锁所有权 token。
- 模板变量实际值与本地渲染后的完整 HtmlBody。
- 供应商原始响应和原始错误详情。
- 完整 Idempotency-Key。

合法发码、白名单和测试请求可以在受控请求内存中短暂包含邮箱或验证码，但不得进入响应、持久化、日志、审计、指标、前端 console、埋点或浏览器持久缓存。

模板正文是不可信 HTML。管理端只能使用独立 iframe `srcdoc`、空 sandbox、禁止外部网络的 CSP 和节点/属性清洗进行预览；禁止在主文档使用 `v-html`。脚本、表单、嵌套 iframe、对象、嵌入内容、base、meta refresh、事件属性、外部图片和链接跳转能力都必须被移除或阻断。

## 16. 测试矩阵与当前证据

| 矩阵 | 必须断言 | 当前证据状态 |
|---|---|---|
| 13 个管理接口 | 未认证请求、MFA、四权限、方法/路径、D-95、字段和错误码 | 未认证请求访问 13 个接口已在测试环境返回 401；完成双因素认证后，管理页六类只读数据已连接真实后端。四类隔离角色和四个隔离账号已完成固定无副作用权限矩阵 48/48；认证使用短时调试回码，邮件适配器为显式非生产 Mock，因此只证明测试环境的平台 RBAC 与接口门禁，不证明供应商受理、真实收件或生产可用 |
| 模板与概览 | 七字段、本地启停、版本冲突、missing、详情 404/500 | 测试环境已通过 Production Adapter 同步并逐项读取五个模板镜像、详情和安全预览；五个模板均审核通过、变量完整且供应商资源存在，missing=0。详情 404/500 与版本冲突仍留 Phase 4 |
| 五场景绑定 | 恰好五场景、只选合规模板、变量映射只读、version 冲突 | 测试环境五个固定场景已分别绑定对应模板并启用；四个新增绑定与模板启用均有 attempt/result 审计。version 冲突实测仍留 Phase 4 |
| 同步 | 全量原子、全局锁、幂等、失败不改镜像 | 测试环境已执行一次真实 DirectMail 同步并成功：新增 4、更新 1、缺失 0、未变 0；attempt/result 审计成对存在。真实 Redis、失败不改镜像、幂等并发和 RAM 否定仍留 Phase 4 |
| 白名单 | 单裸邮箱、HMAC/脱敏、active/revoked、版本冲突、30 天清理 | 实现与前端验收通过；真实测试数据清理留 Phase 4 |
| 测试发送 | accepted/failed、同 key 重放、不同指纹冲突、pending/unknown 阻断 | 生产 Go 测试新增 accepted 同 key 重放不二次外呼、并发同 key 仅一次外呼、白名单 active/revoked/missing、统一前置失败零外呼/零持久化/零审计及幂等键只存摘要；既有测试覆盖 unknown 墓碑、冷却和外呼前丢锁。Phase 4 相关顶层测试 8/8 通过，全量 Go 测试与 vet 通过；真实供应商测试发送仍需单独授权 |
| 五场景 OTP | pending→accepted/failed、一次消费、重放、过期、调用恰好一次 | 五个固定场景均已分别完成真实 accepted、人工收件与一次业务消费；生产 Go 测试逐场景覆盖成功、拒绝、超时、accepted 后仅消费一次、重放拒绝和 `expires_at == now` 严格失效。Phase 4 相关顶层测试 8/8 通过；真实数据库业务效果仍以既有 E2E 证据为准 |
| 邮箱验证码登录 | 密码登录保留、严格 Body、一次消费、锁定、会话和 MFA 隔离 | 前后端实现与 Phase 3 前端验收通过；真实邮件闭环留 Phase 4 |
| Redis | SET NX PX、续租、所有权释放、外呼前复核、Redis 重启后数据库阻断 | 远程测试 Redis 的唯一前缀原语已实测通过并完成精确清理；Go 服务级 fencing、外呼前复核、故障关闭及 Redis 重启后数据库墓碑阻断仍留 Phase 4 |
| RAM | 三个最小 Allow、三个显式 Deny、Create/Modify/Delete 被拒绝 | 测试矩阵已定义；真实最小权限账号验证留 Phase 4 |
| Nginx 与 metrics | 来源 Header、Token+IP 双闸、21 个低基数序列、告警 | 测试 API 已补齐内部可信代理配置，并实测 Token+IP 双闸、伪造来源拒绝、非 GET 拒绝、安全响应头和 21 个固定低基数序列；Prometheus 已使用固定镜像独立部署，目标 UP，三条告警规则运行时加载且健康 |
| 敏感扫描 | 响应、日志、审计、数据库、telemetry、前端均无敏感值 | 同一时间窗本地扫描覆盖 1003 个文本文件，扫描器自测 4/4，结果 `FAIL=0`、读取错误为 0、`REVIEW=3`。人工复核确认三项分别为非验证码业务字段、合成脱敏示例和受统一响应门禁约束的内部回码载体，未发现已知真实秘密值；便携 Go 全量编译测试与 vet 已通过，但运行时日志、真实响应和部署产物复扫仍留 Phase 4 |
| 管理端本地专项 | 邮件管理、安全预览、失效模板阻断、严格 MFA、刷新失败闭环 | 邮件专项契约测试 11/11、管理员认证验证 7/7，`type-check`、`lint`、`build` 均通过；安全预览、失效模板阻断、严格 MFA 与无循环 Token 策略的双轴 review findings 已全部关闭。该证据属于本地静态、契约和构建验证，不替代浏览器或真实环境 E2E |
| 用户端本地专项 | D-93/D-94、重复发码、持久错误态、触控区 | 邮箱 OTP 契约测试 15/15，`type-check`、`lint`、`build` 均通过；覆盖 D-93、D-94、重复发码前置互斥、持久错误态与 44px 触控区修复。该证据属于本地静态、契约和构建验证，不替代浏览器或真实邮件闭环 |
| 前端响应式与浏览器 | 1440、768、390、移动抽屉、五态、安全预览 | 双轴 review findings 已全部关闭；一次性本地 qa-only 浏览器组件已构建并通过离线自测。未认证管理端以及用户登录、注册、找回密码公开页均有 1440/768/390 证据；认证态邮件管理六页签、手机抽屉、平板收列、手机模板/日志卡片均已实测。加载中、正常和空数据三态通过；错误态与 view、view+manage、view+sync、view+test 四类页面降级已在本地回环构建、合成会话和受控 Mock 接口下通过，五个用例均无写请求。真实测试环境受控错误与四类真实角色会话仍待复验。模板安全预览 sandbox 阻断有效；原 ISSUE-001 已通过无业务脚本的最小对照页确认为 qa-only `addInitScript` 工具噪声，不属于产品缺陷。httpOnly Cookie 跨页面重载的静默刷新仍受当前架构限制，不得记为已关闭 |
| migration | 新库/旧库、up/down、CHECK、外键、索引、ownership、备份恢复 | 测试主库已到 schema57/dirty0，应用重启后健康；000057 全新隔离库的完整 Down→Up 可逆周期仍未形成通过证据，不能视为 migration 矩阵完成 |

`SKIP`、`BLOCKED` 或 `FAIL` 均不能计为通过。供应商 `accepted` 与人工确认收件必须分别记录。

## 17. 阶段状态与 Phase 4/5 门禁

### 17.1 当前阶段记录

| 阶段 | 当前结论 | 边界 |
|---|---|---|
| Phase 1：契约与设计 | 已由 QA/PM 书面通过 | `docs/aliyun-directmail-email-template-phase1-design-review.md` 是当时快照；其中“Phase 2 待验收”等表述反映当时状态，不覆盖本文最新阶段记录 |
| Phase 2：后端、本地数据库与自动化实现 | 当前仓库已具备 Go、migration、隔离 MySQL、离线测试和测试资产证据 | 不能据此声称真实 Redis、五场景 DirectMail、RAM 或完整 E2E 通过 |
| Phase 3：前端接入与产品验收 | 2026-07-23 QA 与 PM 通过，P0/P1/P2 为 0，允许进入 Phase 4 | 通过的是前端契约消费、交互和静态/Mock 浏览器证据，不是外部投递验收 |
| Phase 4：真实测试环境 | 未通过 | 必须满足下方全部门禁 |
| Phase 5：生产灰度与上线批准 | 未通过 | Phase 4 书面通过前不得开始 |

### 17.1.1 2026-07-29 Phase 4 局部实测记录

- 管理员已使用恢复后的新绑定邮箱重新登录，并依次完成手机 MFA 与邮箱 MFA；邮箱验证码由真实 DirectMail 链路发送，用户人工确认收件并成功消费，随后进入 `/message/email-templates`。
- 管理页面真实后端读取正常：概览、模板列表、模板详情、场景绑定、同步记录、测试白名单和发送日志均可读取，浏览器控制台未发现错误。
- 已在明确授权下执行且仅执行一次真实模板同步。同步任务成功，新增 4、更新 1、缺失 0、未变 0；审计日志存在 `email.template.sync.attempt` 与 `email.template.sync.result`，result 仅记录安全计数和成功状态。
- 当前五个镜像依次为 `molin_register_code_v1`/`437227`、`molin_login_code_v1`/`437228`、`molin_reset_password_code_v1`/`437229`、`molin_bind_email_code_v1`/`437230`、`molin_admin_verify_code_v1`/`437231`。五个模板均审核通过、变量包含 `Code` 与 `ExpireMinutes`、供应商资源存在，最近同步时间一致。
- 五个模板现均为本地启用；`register`→`437227`、`login`→`437228`、`reset_password`→`437229`、`bind_email`→`437230`、`admin_verify`→`437231` 五个场景均已绑定并启用。四次模板启用和四次场景绑定各自存在 attempt/result 成功审计，概览显示本地启用 5、未绑定场景 0。
- 模板列表的 Element Plus 开关外层曾缺少 `aria-checked`，浏览器语义快照因此误读为未选中；只读 DOM 复核确认五组桌面/移动重复渲染开关均具备 `is-checked` 且内部 input.checked=true，场景接口、概览接口与审计记录相互一致。本地前端现已为桌面模板开关、移动模板开关和场景开关补充动态 `aria-label` 与 `aria-checked`，并增加契约测试；修复后的构建产物尚未重新部署到测试环境，因此只记录为本地修复，不冒充测试环境已生效。
- 发送日志仅展示脱敏邮箱和安全状态，并明确提示 `accepted` 只表示供应商受理、不代表最终送达。本次人工收件是独立于 accepted 的第二条证据。
- `register` 场景仅触发一次真实发码：发送日志新增一条“注册 / 验证码”记录，DirectMail TemplateId 为 `437227`，供应商状态为 accepted 且存在 RequestId；用户随后人工确认收到邮件，并在注册页面成功提交验证码完成注册。该证据证明本次验证码可校验并已被注册流程消费，但尚未覆盖同码重放和过期拒绝矩阵。
- `login` 场景曾因测试服务器仍提供旧用户端构建而缺少邮箱验证码登录入口。获得单独部署授权后，已先在回环候选端口验证冻结产物，再原子切换测试服务器唯一 `molin-user` 容器；公网 `/login`、主 JS/CSS 和 LoginView 资源均返回 HTTP 200，静态资源同时包含“邮箱登录”“密码登录”“邮箱验证码”和 `/auth/login/email/code`。旧容器与旧镜像均保留可回滚标签。当前浏览器会话已有用户登录态，访问 `/login` 会呈现商品市场，因此仍需用户在退出登录或无痕会话中人工确认真实渲染入口；部署验证未调用后端登录接口、未请求验证码、未发送邮件。
- 用户随后在新版登录页的邮箱验证码模式仅点击一次发码。管理端脱敏发送日志新增“登录 / 验证码”记录，平台模板 ID 为 `4`、DirectMail TemplateId 为 `437228`、供应商状态为 accepted 且存在阿里云 RequestId；用户随后人工确认收件，并在登录页成功提交验证码完成登录。该证据证明本次验证码可校验并已被登录流程消费，但尚未覆盖同码重放和过期拒绝矩阵。
- 为辅助尚未接入真实短信供应商的测试环境手机流程，测试 API 曾短时启用验证码调试回传；发现 `login` 前端部署门禁后已恢复原环境并重启，单实例、health、ready、version 均正常，`EMAIL_DEBUG_RETURN_CODE=false`，临时环境回滚快照已删除。该调试能力未在生产启用，也未在本文或工具输出记录验证码。
- `reset_password` 前端契约复核曾发现测试服旧构建把新密码误限制为“至少 8 位”且未设置 72 位上限，与 D-94 的 `6–72` 位不一致。本地已改为 `min=6/max=72`，验证码校验改为六位数字，补充输入上限、重复发码前置互斥和移动端 44px 触控区；新增用户端邮件 OTP 契约测试 5/5 通过，`type-check`、`lint`、`build` 亦通过。经单独部署授权，修复构建已先在测试服务器回环候选端口验证 `/login`、`/reset-password` 与两项静态文件 SHA，再切换为唯一 `molin-user` 实例；公网两个页面和 ResetPasswordView 分包均返回 HTTP 200，浏览器真实渲染找回密码首步正常。原容器已按时间戳重命名并与原镜像共同保留为回滚点。现有浏览器因保留用户登录态，访问 `/login` 会按路由规则进入商品市场，因此本轮不把登录页未登录态视觉呈现记为已验证。部署过程未调用 API、migration、Bootstrap 或邮件接口。此前用户在旧构建上单独授权并仅触发一次 `reset_password` 真实发码；管理端新增“找回密码 / 验证码”脱敏日志，平台模板 ID 为 `3`、DirectMail TemplateId 为 `437229`、供应商状态为 accepted，且存在阿里云 RequestId、无安全失败原因。用户随后人工确认收件，并在找回密码页面成功提交验证码和新密码完成密码重置；该证据证明本次验证码可校验并已被重置流程消费，但尚未覆盖同码重放、过期拒绝和旧会话吊销的独立只读核验。
- `bind_email` 场景经用户单独授权仅触发一次真实发码；管理端新增“换绑邮箱 / 验证码”脱敏日志，平台模板 ID 为 `2`、DirectMail TemplateId 为 `437230`、供应商状态为 accepted，且存在阿里云 RequestId、无安全失败原因。用户随后人工确认收件，并在换绑页面成功提交验证码完成邮箱换绑；该证据证明本次验证码可校验并已被换绑流程消费，但尚未覆盖同码重放和过期拒绝矩阵。
- 本轮重新执行管理前端 `type-check`、`lint`、`build`、邮件专项契约测试 11/11 和管理员认证验证 7/7，均通过；覆盖安全预览、失效模板阻断、严格 MFA、刷新失败闭环与无循环 Token 策略。四权限离线矩阵 48/48 通过且声明 `external_access=false`、`mutations=false`、`provider_calls=false`。这些本地静态、契约和构建证据不替代浏览器或真实权限账号矩阵；httpOnly Cookie 跨页面重载静默刷新仍是当前架构限制。
- 测试 API 已在受控回滚点下补齐内部 metrics 可信代理与 Prometheus 网络配置，并保持原二进制、原工作目录和原运行用户重启。重启后 API 单实例，health、ready、version、schema 57/dirty 0、Redis 及 Bootstrap 八种方法 404 均通过。隔离客户端实测无 Token、错误 Token、重复 Token、非允许来源和伪造来源头均返回 403，七种非 GET 方法均返回 405；成功响应仅包含 `email_adapter_calls_total`，恰有 21 个固定序列，标签集合无敏感值，连续读取计数不变，验证过程未调用邮件 Adapter。随后使用固定镜像 `prom/prometheus:v3.12.0-distroless` 在独立 Compose project 部署 Prometheus，仅绑定 `127.0.0.1:19090`，内部 Token 只以 UID/GID 65532、mode 0400 的 Compose secret 投影，不进入容器环境。Prometheus ready 返回 200，邮件指标目标为 UP，抓取样本恰为 21 个封闭序列，三条精确告警规则均已运行时加载且 `health=ok`；跨抓取周期计数不变，未触发邮件 Adapter。独立复核再次确认 ready、唯一目标 UP、序列数 21、规则数 3、固定镜像和回环绑定均正确。部署前后原五个业务容器、唯一 API、schema 57/dirty 0、Redis 与 Bootstrap 八方法 404 均保持正常；Docker daemon 与代理未修改或重启。API/config 回滚点为 `20260729T025201Z`，独立监控回滚点为 `/home/pc/molin-monitoring/20260729T033530Z`。
- 同日再次执行用户端 `type-check`、`lint`、`build` 和邮箱 OTP 契约测试 15/15，均通过；覆盖 D-93、D-94、重复发码前置互斥、持久错误态与 44px 触控区修复。管理端和用户端双轴 review findings 已全部关闭，但结论仅限本地静态、契约和构建层；一次性本地 qa-only 浏览器组件随后完成构建，安全会话注入器通过 PowerShell 5.1 语法检查与完全离线自测，固定结论为 `argv_exposed=false`、`output_exposed=false`、`file_exposed=false`、`network=false`。未认证管理端登录页、用户端登录页、注册页与找回密码页均已取得 1440/768/390 三宽度截图，浏览器控制台无错误；用户端空邮箱发码前置校验未产生网络请求，直接访问个人中心会正确重定向到带原目标的登录地址。用户重新安全注入已完成双 MFA 的短期管理员会话后，认证态邮件管理六页签全部读取正常；概览、模板、五场景绑定、同步记录、脱敏白名单和脱敏发送日志均取得截图，测试发送和新增白名单弹窗仅打开后取消，未提交任何写请求。桌面、平板和手机布局通过，手机 `scrollWidth` 与 390 像素视口一致；加载、正常、空数据三态通过。模板详情出现的 sandbox 阻断消息已通过无业务脚本的最小对照页确认来自 qa-only 浏览器 `context.addInitScript`，原 ISSUE-001 排除为工具噪声，不属于产品缺陷。随后使用当前管理端构建、回环预览、合成会话与受控 Mock 接口补齐本地浏览器层：错误态显示 Toast、持久错误条和重新加载；view、view+manage、view+sync、view+test 的独立按钮与开关降级全部符合契约，五个用例写请求均为 0。该证据不替代测试环境受控故障和四类真实角色会话，因此完整 E2E 仍未通过。权限矩阵明确未访问外部服务、未修改数据且未调用供应商。当前已使用经 Go 官方 SHA-256 校验的 Go 1.25.12 Windows amd64 一次性便携工具链完成全量 `go test ./... -count=1` 与 `go vet ./...`，二者均通过；真实 Redis 集成项和容器验证仍未执行，不能记为通过。离线仓库模式扫描未发现 JWT 形态值、阿里云 AccessKey 形态值、私钥头、硬编码六位 OTP 或赋值形式的 Secret。扫描发现 5 个无未提交改动的既有非邮件测试文件复用了同一个公网域邮箱夹具；已仅将域名机械替换为 RFC 保留测试域 `example.com`，保留本地部分和测试语义不变，Python AST 解析、格式检查与复扫均通过。该结果只覆盖仓库静态字面量，不替代测试环境响应、日志、审计、数据库及 telemetry 的运行时全链路扫描。
- 远程测试 Redis 原语使用本轮 UUID 唯一前缀执行一次可审计验证：三条精确 key 创建前 `EXISTS=0` 为 3/3，分别验证 `SET NX PX` 互斥、错误所有者续租为 0、正确所有者续租为 1、错误所有者释放为 0、正确所有者释放为 1；finally 仅按已记录 key 精确 `DEL`，新连接复核清理后 `EXISTS=0` 为 3/3，退出码为 0。新增隔离运行器离线自测 6/6 通过，AST 只允许 12 个固定 Redis 命令调用点且越界为 0，未使用 `FLUSHDB`、`FLUSHALL`、`KEYS`、`SCAN` 或模式删除。该证据只证明真实 Redis 原语；本地生产 Go 单元测试与 vet 已通过，既有测试覆盖服务级 fencing、外呼前复核和故障关闭；但 `TestEmailRedisLeaseIntegration` 仍因未启用真实 Redis 集成门禁而 SKIP，Redis 重启与数据库墓碑阻断也未执行。
- 四类最小权限 RBAC 专项已在明确授权的测试环境完成：一次性执行器通过管理 API 创建并保留四个隔离角色和四个隔离账号，权限查询返回 29 项，四个账号完成双因素认证；`view`、`view+manage`、`view+sync`、`view+test` 固定无副作用矩阵 48/48 通过。认证使用短时调试回码，邮件适配器为显式非生产 Mock，因此证据等级仅为“测试环境调试 MFA + Mock 下的平台 RBAC 与接口门禁实测”，不证明供应商受理、真实收件或生产可用。执行器已恢复原环境并重启，恢复后调试回码与 bootstrap 均关闭，health、ready 均为 200，响应不再包含明文验证码；四个隔离账号会话及操作管理员会话共 5/5 吊销成功，操作管理员原访问与刷新凭据的独立复核均返回 401。角色和账号按验收要求保留；远端权限模式为 600 的输入材料已在获得单独授权后依次通过普通文件、非符号链接、当前用户属主和权限模式四项门禁精确删除，并经只读复核确认不存在。该证据不包含任何身份标识、联系方式、认证材料、验证码或随机路径。
- 本地新增的 Python 进程内参考模型矩阵 9/9 通过。五场景覆盖 success、reject、timeout，包含验证码状态可用性、成功后单次消费、重放拒绝、严格过期边界、冷却和 Adapter 调用次数；模板测试发送覆盖同 key accepted 重放、八并发单外呼、unknown 墓碑及新 key、600 秒冷却边界、白名单 active/revoked/missing 和前置失败零副作用。该矩阵完全离线，不访问测试服务器、数据库、外部 Redis、真实邮件或文件系统业务状态，不证明生产 Go 实现、真实并发、供应商受理或最终送达。随后补充生产 Go Phase 4 缺口测试：五场景逐一覆盖供应商拒绝、超时、accepted 后严格一次消费、重放拒绝和 `expires_at == now` 失效；模板测试发送覆盖 accepted 同 key 重放不二次外呼、并发同 key 仅一次外呼、白名单 active/revoked/missing、统一前置失败零外呼/零持久化/零审计，以及幂等键仅存摘要。Phase 4 顶层测试 8/8 通过且 0 SKIP，全量 Go 测试与 vet 均通过；新增测试不连接数据库或外部 Redis，因此真实业务数据效果、真实 Redis 并发和供应商外部结果仍须独立门禁。
- 同一时间窗本地敏感扫描覆盖 1003 个文本文件并包含当时前端构建产物，扫描器自测 4/4；结果 `FAIL=0`、读取错误为 0、`REVIEW=3`。三项 REVIEW 经人工只读复核，分别属于非验证码业务字段、合成脱敏示例和受统一响应门禁约束的内部回码载体，未发现已知真实秘密值。配置与 CIDR 双轴静态复评 findings 已全部关闭：`APP_ENV` 必须显式提供，调试开关只接受经 trim 后精确小写 `true`，安全环境权威集合包含 `testing` 而不包含 `staging`，CIDR 覆盖检测采用有界剪枝。随后从 Go 官方元数据选择 Go 1.25.12 Windows amd64 ZIP，在系统临时目录校验官方 SHA-256 后解压为一次性便携工具链；`go test ./... -count=1` 与 `go vet ./...` 均以退出码 0 完成。邮件相关顶层测试精确计数为 65 PASS、6 SKIP、0 FAIL；6 个 SKIP 是未启用真实 MySQL/Redis 门禁的集成项，不能计为通过。该结论仍不能替代容器验证、运行时日志、真实响应与部署产物的发布前复扫。
- 前端关卡 0 已与本机 `main` 对账：当前邮件分支 HEAD 为 `288599f`，本机 `main` 为 `608172e`，merge-base 为 `288599f`；`HEAD..main` 仅包含用户端布局、Agent 聊天和普通聊天三个非邮件文件，未发现邮件契约或页面 delta。由于本轮未执行 `fetch` 或更新远端引用，该证据只证明本机 `main` 对账，前端最终完成报告前仍须在获得 Git 只读联网授权后确认最新远端 `main`，不得将本机引用表述为最新远端状态。
- 管理员 QA 会话重新安全注入后再次只读复核真实测试页面：目标路由不再跳转登录，邮件模板概览显示 view/manage/sync/test 四权限均已授权，模板总数、审核通过和本地启用均为 5，未绑定场景为 0，浏览器控制台无错误；已保存 `admin-email-reinjected-session.png`。该证据只确认全权限管理员正常态，不替代四类最小权限真实角色会话或测试环境受控错误态。
- 获得四类真实 RBAC 页面复验授权后，测试工程师先执行只读前置门禁并安全停止：四个隔离账号和角色仍保留，但上一轮已授权删除的唯一 600 输入材料确认不存在，本机也没有四账号登录输入；现有执行器仅支持创建新资产，不能重新登录保留账号。本轮禁止重建账号、改库或扩权，因此未开启调试返回、未重启、未生成会话、未执行浏览器或任何远程写入。API 仍为唯一进程且 health/ready 均为 200。解除该门禁必须由账号所有者通过既有安全通道重新提供四个现有账号的登录输入，不能以新建账号替代。

### 17.2 Phase 4 真实测试环境门禁

以下项目全部通过，且无 P0/P1/P2 未关闭缺陷，才允许 QA 和 PM 签署 Phase 4：

- 在授权测试环境执行 000055，验证真实 MySQL 版本、结构、数据失效、五业务表、一技术表、五场景、四权限和精确 down。
- 在授权测试环境执行 000056，验证 bootstrap 专用权限、ownership、一次性 receipt、默认 404、内部 Token/CIDR、管理员 JWT、手机 MFA、并发唯一和成功后关闭入口；不得直接写 MFA 时间戳或数据库绑定替代该流程。
- 在授权隔离 MySQL 执行 000057 up/down，覆盖0/1/多条非零毫秒、完整备份与原值恢复、每个 DDL/DML/断言中断、缺行/孤儿/指纹篡改、重复执行和未知 partial；离线 SQL 摘要、故障注入与模型证据不能替代真实 MySQL 验证。
- 使用远程测试 Redis 验证 Go 服务的加锁、续租、所有权释放、外呼前复核、fencing、故障关闭和重启后的数据库阻断。
- 使用 Production Adapter 从 DirectMail 同步真实模板镜像，不直接向业务表导入供应商模板。
- 逐场景完成 register、login、reset_password、bind_email、admin_verify 的真实发送、人工收件、一次消费、重放和过期矩阵。
- 完成模板测试发送、同 key 幂等、并发、unknown 墓碑和冷却到期矩阵。
- 使用最小权限 RAM 账号执行三个 Allow、三个显式 Deny以及创建/修改/删除模板的越权否定测试。
- 验证 Nginx 公开来源 Header、内部 metrics Token+IP 双闸、Prometheus 目标和告警规则。
- 对响应、日志、审计、数据库、metrics/telemetry 和前端执行全链路敏感扫描。
- 完成管理端 13 接口、用户端五业务流与移动/桌面页面的真实环境完整 E2E。
- 形成可复核测试报告，明确供应商 accepted 与用户确认收件两条证据，无任何凭据或 OTP。

### 17.2.1 Phase 4 当前证据与剩余执行顺序

下表只记录截至 2026-07-29 已取得的证据。`部分通过` 不得计为阶段通过，离线契约、代码存在、供应商 accepted 和人工收件也不能互相替代。

| 门禁 | 当前状态 | 已有证据 | 剩余动作与专项授权 |
|---|---|---|---|
| 000055 | 部分通过 | 测试主库已演进到更高版本，邮件五业务表、一技术表、五场景和四权限已被后续运行链路使用 | 仍需补齐与冻结 SQL 一致的独立结构、数据失效和精确 down 证据，不得在主库回滚 |
| 000056 Bootstrap | 部分通过 | 一次性 `admin_verify` 初始化成功，receipt、模板镜像、场景绑定、审计与入口关闭均已有证据；当前 Bootstrap 八种方法均 404 | 并发唯一、ownership 组合和完整失败矩阵仍需隔离验证 |
| 000057 UTC 秒级时间 | 部分通过 | 测试主库为 schema 57/dirty 0，API 正常；离线 Up/Down 哈希与唯一目标保护契约通过 | 仍需在全新隔离恢复库完成 0/1/多条毫秒数据、故障注入及 Up→Down→Up 原值恢复，不得复用旧 dirty 库 |
| 真实 Redis | 部分通过 | 唯一前缀三 key 的 SET NX PX、错误/正确所有者续租与释放、创建前及清理后二次 `EXISTS=0` 已在远程测试 Redis 实测通过。使用已校验的 Go 1.25.12 显式执行 `TestEmailRedisLeaseIntegration`，未 SKIP；锁竞争、TTL 续租、ownership fencing、非所有者释放保护和结束前精确清理均通过，`cleanup_exists_zero=true` | Redis lease 与精确清理子门禁已关闭；Redis 重启与数据库 unknown 墓碑阻断仍需独立维护窗口和专项授权 |
| Production Adapter 同步 | 通过 | 一次真实同步成功，五模板审核/变量/missing/快照与同步审计均已核验 | 不需要重复同步；后续 RAM Deny 矩阵应使用隔离窗口并验证失败不改快照 |
| 五场景真实 OTP | 部分通过 | 五场景均有真实 accepted、人工收件和一次成功业务消费；生产 Go 测试逐场景覆盖 success/reject/timeout、accepted 后单次消费、重放拒绝和严格过期，Phase 4 顶层测试 8/8、全量测试和 vet 均通过 | 新测试无数据库，仅证明 OTP 业务门一次性放行；真实建用户、建会话、修改密码、换绑邮箱和 MFA 时间戳仍以既有测试环境 E2E 证据为准，真实重放/过期故障矩阵仍需独立门禁 |
| 模板测试发送 | 部分通过 | 生产 Go 测试已覆盖 accepted 同 key 重放、并发同 key 单外呼、unknown 墓碑、外呼前丢锁、白名单三态、统一前置失败零副作用和幂等键摘要；新增测试 6 项均通过且 0 SKIP | 真实模板测试发送、真实 Redis 并发及供应商故障注入仍须单独授权；单元测试不得替代外部受理或最终送达 |
| RAM 最小权限与 Deny | 未通过 | 最小权限策略范围已冻结；安全探针已完成离线分类、六 action 请求字段门禁、Go 测试、vet 和敏感扫描。真实最小权限基线中 QueryTemplateByParam 与 DescTemplate 已通过；缺少全部邮件业务字段的 SingleSendMail 返回 HTTP 4xx、18 字节未知 Code，安全摘要与冻结候选及阿里云官方公开错误码页面中的同长度候选均不匹配，探针立即停止，没有发送邮件，三个模板写 action 未执行 | 不得猜测原始 Code 或扩展白名单；需要阿里云工单/官方说明确认该安全摘要对应的错误语义，或改用能够证明 `dm:SingleSendMail` Allow 且不产生额外邮件的官方权限验证方式。确认后才能继续 Create/Modify/Delete 越权拒绝和六个显式 Deny；不得记录凭据、供应商原始响应、未知原始 Code 或请求字段值 |
| Nginx 与 Prometheus | 通过 | Token+IP 双闸、伪造来源拒绝、非 GET 405、回环监听、target UP、21 个封闭序列和三条健康告警规则均已实测 | 保留独立监控回滚点；无需重复触发 Adapter |
| 全链路敏感扫描 | 部分通过 | 最新工作树扫描覆盖 1134 个文本文件；扫描器自测 4/4，清除一个未跟踪浏览器 QA 审计产物中的 JWT 残留后复扫得到 `FAIL=0`、读取错误为 0、`REVIEW=4`、`INFO=271`。四项 REVIEW 已只读确认分别为两个非验证码业务编码字段、手机号脱敏文档示例和受安全非生产环境加显式调试开关双门禁约束的内部回码载体，未发现其他已知真实秘密值 | 仍需对同一时间窗的运行时响应、应用日志、审计导出、数据库安全投影、Prometheus/telemetry 和实际部署前端产物形成完整扫描证据 |
| 真实 RBAC 与 E2E | 部分通过 | 测试环境固定无副作用权限矩阵 48/48 通过；四类真实角色会话的 1440/768/390 页面权限降级已完成，共形成 12 张截图。最终 `view+test` 专项 3/3 通过，替代账号 logout、禁用和操作管理员 logout 均为 1/1；原环境恢复，调试、Mock、Bootstrap 均关闭，API 单实例且 health/ready 为 200，输入与回滚文件不存在。测试服务器现有前端构建的受控 503 错误态也已在三宽度通过，业务 API 访问和写请求均为 0。管理端邮件专项 11/11、管理员认证验证 7/7、用户端邮箱 OTP 15/15，双端 `type-check`、`lint`、`build` 均通过 | 真实四角色三宽度及已部署前端受控错误态两个子门禁均已关闭，无需重复创建账号或修改测试 API。其余五业务流 E2E、后端运行时故障矩阵及 httpOnly Cookie 架构限制仍按既有门禁处理 |

2026-07-29 替代账号补验更新：通过管理 API 创建并复用四个既有角色，四账号均在短时调试回码与 Mock 邮件适配器下完成手机、邮箱双 MFA。真实测试环境已完成 `view`、`view+manage`、`view+sync` 三角色的 1440/768/390 共 9 项只读页面降级并形成截图；`view+test` 在首个截图前被旧执行器统一归类为 `browser_acceptance_failed`，未重试，因此真实四角色门禁仍为部分通过。原环境逐字节恢复，调试与 Bootstrap 关闭，API 单实例且 health/ready 为 200；四替代账号 4/4 禁用，操作管理员 Access Token 为 401，600 输入和回滚快照已删除。旧执行器因先禁用再 logout，四账号显式 logout 被封禁中间件拒绝；`BanUser` 已关闭账号访问并尝试吊销 Refresh 会话，但缺四个旧 Access Token 的逐项 401 证据。执行器现已改为先 logout 再禁用并输出固定安全错误分类，离线验证通过，尚未获得再次真实执行授权。

同日单账号 `view+test` 复验获得授权后，执行器在只读角色列表门禁即返回 `role_list_failed`，说明本轮管理员 Access Token 无法满足当前管理 API 调用；执行未开启调试、未创建账号、未启动浏览器。环境保持调试与 Bootstrap 关闭，API health/ready 为 200，无回滚文件；无效 600 输入已删除。该失败不增加任何 `view+test` 验收证据。

管理员随后通过受控 10 分钟 Mock 调试窗口取得新的双 MFA 会话，窗口正常恢复原配置。第二次单账号 `view+test` 复验通过角色冻结、账号创建和 Mock 双 MFA，但浏览器精确失败为 `test_action_visibility_mismatch`。静态复核证明旧 QA 脚本在页面默认“概览”状态下先检查仅存在于“模板”页签的测试发送按钮，属于测试页签顺序缺陷；该结果既不能判为产品失败，也不能判为 `view+test` 通过。finally 已完成替代账号 logout 1/1、禁用 1/1、管理员 logout 1/1，环境与敏感文件清理通过。浏览器脚本已改为先进入“模板”，新增页签顺序契约后自测 8/8、远端哈希一致；修复后尚未再次真实执行。

最终复验更新：在再次获得单账号专项授权后，修复版 `--view-test-only` 状态机完整通过。唯一 `view+test` 角色冻结为 3 个权限项，替代账号通过 Mock 手机、邮箱双 MFA；1440、768、390 三宽度浏览器检查 3/3 通过。finally 完成替代账号 logout 1/1、禁用 1/1、操作管理员 logout 1/1，原环境恢复且调试、Mock、Bootstrap 均关闭。独立后验确认 API 单实例、health/ready 为 200、输入和回滚文件不存在；四角色共 12 张真实测试环境截图齐全。随后直接加载测试服务器现有前端构建，在浏览器内拦截全部 API 并对邮件概览构造固定 503/51003，三宽度的 Toast、持久错误条、重试入口和无横向溢出均通过，业务 API 访问和写请求为 0。真实 Redis Go lease 集成也已显式执行且未 SKIP，ownership fencing、续租和精确清理通过。至此“真实四角色三宽度权限降级”“已部署前端受控错误态”和“真实 Redis lease”三个子门禁均通过，但 Phase 4 总门禁仍需依据 Redis 重启/数据库墓碑、RAM Deny、最新 main 对账及测试/产品确认逐项判断。

Redis 重启/数据库墓碑门禁的分阶段 Go 测试资产已经冻结：使用真实 `EmailRepository`、真实 Redis 和计数 Mock Adapter，phase1 只创建本轮 unknown 墓碑，phase2 必须通过 Redis `run_id` 变化证明真实重启，再验证旧/新幂等键均由数据库墓碑阻断且 Adapter 增量为 0；phase2 固定保留测试数据和恢复状态，不再隐式清理。独立 cleanup 阶段要求额外双重确认，且门禁位于数据库和 Redis 连接之前；通过后仅按状态文件记录的主键及单一锁键精确清理并验证不存在，禁止 `FLUSHDB`、`KEYS`、`SCAN` 或模式删除。测试强制 schema57/dirty0、测试环境、Mock Adapter、双重确认和 600 状态文件。资产冻结时的离线静态契约、敏感扫描、gofmt、禁用集成开关时的专项 Go 测试、全量 `go test ./... -count=1` 与 `go vet ./...` 均通过；后续真实周期证据及当前状态如下，cleanup 涉及数据库删除时必须另行确认。

随后在测试环境启动一次真实 Redis 重启周期。phase1 在建立并验证单事务 MySQL 恢复点后通过：schema57/dirty0、Mock Adapter 调用 1 次、发送日志收敛为 `failed/provider_outcome_unknown`，未调用 DirectMail。唯一 Redis 容器重启成功，PING 恢复且 `run_id` 已变化。首次 phase2 失败；只读安全投影确认原日志仍为 unknown 且同 scope 已有 2 行，但数据库计算的原日志年龄约为 8 小时。根因是该集成测试连接错误使用 `Loc: time.UTC`，而仓储 UTC 墙钟写入契约与生产全局 DSN 均固定为 `loc=Local`；上海进程下因此把测试时间再提前 8 小时，墓碑被立即判为超过十分钟冷却窗，新 key 进入计数 Mock Adapter 并形成第二条隔离日志。现有证据不指向生产连接路径缺陷。

测试资产已在本地修复：连接改为 `Loc: time.Local`，写入夹具前新增 MySQL UTC 墙钟偏差不超过 5 秒的门禁；phase2 在任何 Adapter 调用前精确核验原墓碑、至少 120 秒冷却余量和既存新-key日志，逐步输出真实旧 key、新 key 与调用计数；version 1 状态文件新增可选的意外日志精确主键并保持向后兼容，cleanup 对原日志与可选第二日志执行完整归属校验后才按主键删除。修复后状态兼容测试、静态契约、三文件敏感扫描、全量 Go 测试与 vet 均通过。修复后二进制随后在原失败现场执行 phase2：在任何 Adapter 调用前识别既存第二日志，固定输出 `adapter_calls=0` 与 `unexpected_log_recorded=true`，把第二日志精确主键写入 600 状态文件后安全 `BLOCKED`。当前两条隔离日志、白名单、模板、唯一 Redis 键、状态文件和数据库恢复点均保留；禁止直接重跑 phase1，必须先取得数据库精确清理授权。该子门禁仍为未通过。

获得一次专项授权后，远程周期在首个写门禁 `remote_asset_directory` 失败并立即停止：SSH 在创建本轮唯一资产目录时被远端关闭，退出码非 0。SCP 与维护控制器均未启动，因此数据库恢复点、phase1、Redis 重启和 phase2 均未执行，没有数据库状态文件或隔离测试数据；唯一资产目录是否已创建无法由本轮证据确认，记为 unknown。依据“任一门禁失败立即停止”要求，本轮没有追加 SSH 复核、自动清理或重试，不能增加墓碑门禁通过证据。

随后仅基于本地调用记录完成事故复盘，未再次联网。失败调用使用固定 SSH 参数和单个远端命令参数，没有 TTY、stdin、here-string 或 BOM；目录后缀只含固定前缀、十六进制和短横线，并以单引号包裹。输出只有连接关闭，没有权限拒绝、shell 语法或 mkdir 错误，因此当前安全分类为 `transport_or_session_closed`，更可能是连接/会话瞬断；精确退出码和目录状态仍为 unknown。下一次必须先单独授权一次只读固定 `printf` 连接诊断，通过后再另行处理孤儿目录与写周期，不能直接重试 phase1。

下一次诊断的 SSH 配置已在本机通过 `ssh -G` 离线展开：BatchMode、严格主机密钥检查和 10 秒连接超时均生效，ProxyCommand、ProxyJump、LocalCommand 以及 local/remote/dynamic forward 均不存在。本次配置展开没有建立网络连接；它只证明下一次固定 `printf` 诊断不会继承隐式代理或转发，不能替代实际 transport 结果。

获得单次只读 transport 授权后，只启动一个 SSH 进程并固定执行 `printf transport_ok=true`。结果为退出码 0、stdout 唯一且精确匹配、`ssh_attempt_count=1`、`ssh_completed_count=1`，远端写入为 false。该证据排除持续性 SSH transport 故障，但不改变上一次唯一资产目录的 unknown 状态；依据本轮授权，本次没有检查目录、上传文件、连接数据库或启动后续周期。

DirectMail RAM 安全探针也已冻结。QueryTemplateByParam 与 DescTemplate 仅执行最小读取；SingleSendMail 不携带 AccountName、收件人、主题或正文，三个模板写 action 不携带 TemplateId、名称、主题或正文。正确 Allow 应返回 success/request 安全类别，正确 Deny 应返回 permission；若误授权，缺参请求只能返回 request 并使探针失败，不会创建、修改、删除模板或发送邮件。离线 permission/request/success/unknown 分类与六 action 字段形状共 10 个子用例通过，Go vet 与敏感扫描通过。真实最小权限探针已执行一次：两个模板读取 action 成功；SingleSendMail 的安全缺参请求返回 `rejected_other` 并立即停止，未发送邮件，三个模板写 action 未调用。阿里云官方文档仍把通用缺参列为 `MissingParameter`，因此当前不得猜测扩展白名单；需先用不可逆哈希、长度与 HTTP 状态族完成唯一候选匹配，再继续 RAM 矩阵。

2026-07-30 使用冻结的安全诊断二进制再次执行一次最小权限探针：QueryTemplateByParam 与 DescTemplate 继续成功，缺少全部邮件业务字段的 SingleSendMail 返回 HTTP 4xx、Code UTF-8 长度 18，安全摘要为 `48d419309078f725902c94f5ebfa1b5b194e7a481eb46055b6f9e51160ca064f`，与冻结候选 `MissingParameter`、`InvalidParameter` 均不匹配，分类保持 `unknown/rejected_other`。探针在该处立即停止，未发送邮件，CreateTemplate、ModifyTemplate、DeleteTemplate 均未调用。随后只读获取阿里云官方 DirectMail 公开错误码页面，对页面中的公开 Code 候选执行长度与 SHA-256 比对；18 字节候选没有命中该摘要。由于没有唯一官方候选，禁止猜测原始 Code、扩展安全白名单或继续 RAM Allow/Deny 矩阵；该门禁仍为未通过。

同一工作树随后重新执行仓库级敏感扫描：扫描器自测 4/4，通过读取 1015 个文本文件，结果为 `FAIL=0`、读取错误 0、`REVIEW=3`、`INFO=249`。三项 REVIEW 逐项复核后仍分别属于应用市场普通 `Code` 字段、手机号脱敏注释示例，以及由 `IsSafeNonProduction()` 与显式调试开关共同限制的验证码响应载体。本轮未发现已知真实 AccessKey、Secret、Token、OTP、完整邮箱或未脱敏手机号；该结果只关闭当前工作树静态字面量扫描，不替代测试环境运行时各数据面的同一时间窗扫描。

同日续跑本地门禁：管理端 `type-check`、`lint`、`build` 和 22 个契约用例通过，用户端 `type-check`、`lint`、`build` 和 15 个邮箱 OTP 契约用例通过；000056/000057 静态迁移契约、48 项无副作用权限矩阵以及安全输入脚本自检均通过。使用完整 Go 1.25.0 工具链并设置 `GOPROXY=off`、`GOSUMDB=off` 后，`go mod tidy -diff` 无输出，`go test ./... -count=1` 与 `go vet ./...` 均通过；先前出现的 `x/net`、`x/sys` 依赖差异已确认来自不完整 GOROOT，并非仓库依赖缺口。仓库复扫一度在未跟踪的 `.gstack/browse-audit.jsonl` 中发现 JWT 形态残留；确认其为普通、非符号链接且未被 Git 跟踪的浏览器 QA 临时产物后已精确移除，复扫覆盖 1134 个文本文件，结果 `FAIL=0`、读取错误 0、`REVIEW=4`、`INFO=271`。四项 REVIEW 分别为两个普通业务编码字段、手机号脱敏注释示例和受双门禁约束的验证码响应载体。该结果仍不替代测试环境运行时数据面的同一时间窗扫描。

2026-07-30 在补充 RAM 安全诊断事实并重新对齐冻结契约后，再次执行扫描器自测与仓库复扫：自测 4/4，通过读取 1140 个文本文件，结果 `FAIL=0`、读取错误 0、`REVIEW=4`、`INFO=271`。四项 REVIEW 的分类和文件位置与上一次复核一致，没有新增敏感输出面；该证据只覆盖当前工作树静态文本，测试服务器响应、日志、审计、数据库和 telemetry 仍须在最终同一时间窗独立扫描。
| QA/PM 签署 | 未通过 | 当前无 P0/P1 自动化失败，但仍有上述 SKIP/BLOCKED | 所有门禁关闭后由测试工程师出具报告，产品经理确认业务与文档，之后才能进入 Phase 5 |

推荐执行顺序：全新隔离库可逆周期 → 启用受控 Redis/数据库环境完成 6 个显式 SKIP 集成项 → 五场景真实重放/过期 → 模板测试发送与 unknown 注入 → RAM Deny → Redis 重启与数据库墓碑阻断 → 同一时间窗敏感扫描与真实角色三宽度 E2E → QA/PM 签署。Go 本地单元矩阵已补齐并通过，无需重复准备工具链；任一步失败都应停止在当前门禁，不得用 Python 参考模型、手工改库或扩大权限强行通过。

### 17.3 Phase 5 生产灰度与上线批准门禁

Phase 5 必须以已签署的 Phase 4 报告为前置条件，并完成：

- 生产变更单、责任人、维护窗口、备份恢复点和精确回滚清单审批。
- 在隔离环境完成与生产版本一致的 000055 发布和 down 回滚演练。
- 核对生产 Secret、RAM 最小权限、网络边界、Redis 安全、监控和告警，但报告只记录键名、别名和状态，不记录值。
- 按受控比例恢复流量，持续观察健康、ready、邮件 Adapter 计数、失败率、unknown 和锁所有权告警。
- 灰度期间验证注册、登录、找回密码、换绑邮箱和管理员认证的业务可用性，但不得在报告保存验证码或完整邮箱。
- QA 输出生产灰度测试结论，PM 确认业务体验与风险，运维确认部署和回滚可执行；三方全部书面通过后才批准全量。

Phase 4 或 Phase 5 任一项未完成都不得写“功能整体完成”“已上线”或“真实邮件链路通过”。

## 18. 部署与回滚

000055 修改验证码结构并创建邮件业务表，不允许滚动部署，也不允许新旧应用实例共存。

### 18.1 部署顺序

1. 停止邮箱、手机 OTP 发码，停止 OTP 校验、注册和登录流量，并确认请求为零。
2. 等待至少 10 分钟，使旧验证码过期。
3. 停止全部 auth/API 实例。
4. 备份数据库并在隔离库验证备份可恢复。
5. 只读核对 migration 版本、dirty 状态、旧验证码结构、历史验证码状态、权限和同名表冲突。
6. 执行 000055 up；先核对目标版本、dirty=0、五业务表、一 ownership 技术表、CHECK、外键、索引、五场景、四权限和管理员绑定，核验通过后才能继续。
7. 执行 000056 up；核对目标版本、dirty=0、bootstrap 专用权限与 admin 精确绑定、独立 ownership 表和空 receipt 表，核验通过后才能继续。
8. 执行 000057 up；核对 version=57/dirty=0、三列目标结构、receipt 行数不变、非零毫秒原值已完整备份且当前值只截到秒、非时间指纹不变，核验通过后才能部署应用。
9. 部署全部新版本应用实例。
10. 核验 `/api/health`、`/api/ready`、`/api/version`、schema 版本、邮件配置就绪、Redis 和内部 metrics 双门禁。
11. 按 Phase 4 或 Phase 5 的批准范围执行验证；所有门禁通过后才恢复或扩大流量。

`/api/ready` 只证明应用启动检查通过，不能单独证明邮件 Adapter、真实 Redis 锁、DirectMail、RAM、人工收件或 OTP 消费通过。

### 18.2 回滚顺序

1. 停止上述业务流量并确认请求为零。
2. 等待至少 10 分钟后停止全部实例。
3. 再次备份并验证可恢复。
4. 只读核对 000057/000056 是否执行、三列结构、成功 receipt 是否存在，以及两次基础 migration 创建对象的外部引用；存在未知状态或未知引用时必须失败关闭。
5. 若当前为完整 schema57，先执行 000057 down，按备份逐主键恢复原毫秒、核对非时间指纹与 schema56 列定义后删除专用备份。随后按以下唯一基础回滚矩阵执行：A）000056 未执行：按原 000055 down；B）000056 已执行且无成功 receipt：先执行 000056 down 并核验，再执行 000055 down；C）存在成功 receipt：应用回滚保留 schema56、receipt、模板镜像和 `admin_verify` 绑定，不执行 000056/000055 down。
6. C 类若业务明确要求回到 schema 55 之前，必须另立高风险变更单，先完成备份恢复验证、留存不可变审计证据并取得 QA、产品经理、运维联合批准；解除权限、角色、用户覆盖、分组、receipt、镜像和绑定等引用后，严格依次执行 000056 down、000055 down，禁止 migration `force`。
7. 仅 A/B 类或已完成上述高风险流程的 C 类，才核对 `code_hash` 等 000055 增量字段已删除，同时旧 `verification_codes.code VARCHAR(64) NULL` 仍保留。
8. 部署与目标 schema 匹配的旧版本应用并核验 health、版本和 schema。
9. 确认无新旧实例混跑后恢复流量。

`force` 例外只适用于 000055 自身发生 dirty、尚未执行 000056、且确认不存在任何 bootstrap receipt 的灾难恢复；它只修改版本元数据、不执行 SQL，并且仅在 000055 结构与数据已人工验证为完整 54 或完整 55 目标状态后由授权运维使用。该例外不适用于 000056，也不适用于 C 类回滚；C 类及其高风险例外全程禁止 `force`。

### 18.3 `admin_verify` 首次配置与回滚

首次配置只允许在 000056 已完成、普通 13 个邮件接口仍保持完整 MFA 的前提下执行：

1. 四键固定为 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`。enabled 只有配置键缺失时默认 false；字面 true/false（大小写不敏感）有效，显式空字符串或其他值必须启动失败；显式 false 时所有方法404，enabled=true 时其他三项任一缺失、空值或非法必须启动失败。Token 复用 `INTERNAL_API_TOKEN` 的客观安全校验基线实现但必须使用独立值：至少 32 字节、无首尾空白、拒绝冻结弱占位值、至少含 8 种不同原始字节，并且不得与已配置 `INTERNAL_API_TOKEN` 原始值相等。CIDR 独立显式配置后允许与平台既有 INTERNAL/TRUSTED 列表完全同值或重叠，同一 Nginx 代理网段可重复；禁止零前缀网段及任何读取、合并或回退。Bootstrap allowed 与 bootstrap trusted-proxy 之间存在规范化后完全相同的 CIDR 条目时必须启动失败，不同前缀仅部分重叠允许。两个列表分别规范化并求 CIDR 地址并集，任一列表通过多条非零前缀覆盖完整 IPv4 或 IPv6 地址族也必须启动失败，例如 `0.0.0.0/1,128.0.0.0/1`、`::/1,8000::/1`；两个列表不跨语义合并计算全地址族并集。覆盖检测的静态实现已改为候选祖先索引与无候选后代立即返回的有界剪枝，设计上避免展开完整地址树；配置与 CIDR 双轴静态复评 findings 已全部关闭，且当前工作树已通过 Go 1.25.12 全量测试与 vet，但本机仍无 Docker 容器验证，不得据此记为生产通过。配置通过安全渠道注入且不在命令、工单、日志或报告中记录值。
2. 使用具备 `email:template:bootstrap` 的管理员正常登录并完成手机 MFA。内部 Token 不能替代 JWT、账号状态或手机 MFA。
3. 变更单固定一个 DirectMail TemplateId 和一个本轮 Idempotency-Key；以 POST、单值安全 Header、`application/json` 和严格 `{provider_template_id}` 调用内部入口。`provider_template_id` 仅允许 1-64 字节 ASCII 十进制正整数；空值、全零、65 字节及以上、非数字、符号、小数、指数或任何空白均前置返回 `400/40000「请求参数错误」`，attempt 审计、Adapter 与数据库增量均为 0。不得携带邮箱、OTP、scene、变量映射或 MFA 字段。
4. 依据 [阿里云 DirectMail DescTemplate](https://help.aliyun.com/en/direct-mail/api-dm-2015-11-23-desctemplate) 核对详情真实且未废弃字段 `RequestId/CreateTime/TemplateSubject/TemplateStatus/TemplateName/TemplateText`；`QueryTemplateByParam` 列表字段 `TemplateId/TemplateName/TemplateStatus/CreateTime` 另行处理，不得混用。`ProviderTemplate.Name` 大小写精确等于 `molin_admin_verify_code_v1`、Status=`approved`、变量含 `Code/ExpireMinutes`。
5. 不同 key 可重复只读 Describe；bootstrap 并发控制仅在数据库写阶段执行，以事务 `SELECT ... FOR UPDATE` 锁定 admin_verify、复查 receipt、初始态 CAS 与 scope 唯一约束保证仅一人提交。`admin_id` 同时纳入既有 `EMAIL_IDEMPOTENCY_SECRET` 的 key HMAC 作用域和 request fingerprint，receipt 重放还必须满足 `completed_by` 为当前管理员。同一管理员、同一 key、同一 fingerprint 的并发首次请求即使都已完成 Describe，后取得行锁者仍返回原成功结果且 `idempotent=true`；跨管理员复用同 key 固定返回 `409/40900「管理员邮箱认证场景已完成首次配置」`，不得泄露原操作者。每次真实 Describe 只递增既有指标一次，不新增序列。
6. 核对 attempt 审计成功后才外呼；result 审计与镜像/绑定/receipt 同事务，且固定 `target_type=email_admin_verify_bootstrap_receipt`、`target_id=receipt` 内部十进制 ID，不得使用供应商 TemplateId、管理员 ID 或 scene 代替；result 失败必须全回滚。报告只记录内部 ID、状态和脱敏摘要。
7. 立即移除 enabled/token 并重启，确认入口恢复 404。随后才通过正常 admin_verify 邮箱发码与 verify-email 完成 MFA；供应商 accepted 不等于人工收件。

000056 使用独立 `migration_000056_permission_ownership` 一行记录专用权限及唯一 admin 绑定的预存/新增状态和最终 ID；不得修改 000055 ownership。admin 角色 0 行/多行、预存权限元数据冲突、partial-up/down 未知状态均失败关闭。回滚严格采用 A/B/C 矩阵：000056 未执行时走原 000055 down；000056 已执行且无成功 receipt 时先 down 000056、核验后再 down 000055；存在成功 receipt 时应用回滚保留 schema 55+56、receipt、模板镜像和绑定，不执行任一 down。确需回到 55 前只能另立高风险变更单，先完成备份恢复验证、留存不可变审计证据并由 QA/PM/运维联合批准，解除全部引用后依次 down 000056、down 000055，禁止 migration force。

身份边界额外固定：`ADMIN_VERIFY_EXPIRE_HOURS<0` 无论 bootstrap 是否启用均启动失败；`=0` 只豁免历史时间过期。手机 MFA 时间戳缺失、恰到过期边界或晚于当前数据库 UTC 时间均无效，未来时间即使 expireHours=0 也失败关闭，且无 attempt 审计、Adapter 或数据库副作用。仅通过动态权限覆盖得到 bootstrap 权限、但不直接关联 admin 角色的普通用户仍返回403。Bootstrap Token 缺失/空/重复/逗号多值/错误统一403；Authorization 标准401；Idempotency-Key异常400。

## 19. 相关权威资料

- 接口 SSOT：[`full-api-design.md`](./full-api-design.md)
- 前端接口 SSOT：[`frontend-api-reference.md`](./frontend-api-reference.md)
- 数据库 SSOT：[`database-schema-design.md`](./database-schema-design.md)
- Phase 1 历史快照：[`aliyun-directmail-email-template-phase1-design-review.md`](./aliyun-directmail-email-template-phase1-design-review.md)
- 测试计划：[`test-plan.md`](./test-plan.md)
- 管理后台任务：[`frontend-task-admin-console.md`](./frontend-task-admin-console.md)
- 用户控制台任务：[`frontend-task-user-console.md`](./frontend-task-user-console.md)
- 前端完成定义：[`frontend-definition-of-done.md`](./frontend-definition-of-done.md)
- 后端模块说明：[`../server/internal/modules/auth/README.md`](../server/internal/modules/auth/README.md)
- 基础设施说明：[`../infra/README.md`](../infra/README.md)
- 可执行测试资产：[`../tests/email/README.md`](../tests/email/README.md)
