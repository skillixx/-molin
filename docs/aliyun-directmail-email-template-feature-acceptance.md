# 阿里云 DirectMail 邮件模板与邮箱验证码功能及整体验收

> 文档性质：邮件模板管理、邮箱验证码发送和阶段验收的中文权威说明。
>
> 当前状态：Phase 1 契约评审、Phase 2 本地实现证据和 Phase 3 前端验收已经形成；Phase 4 已由 QA/PM 附负责人豁免通过，机器清单为 19 项关闭、0 项开放。四项负责人豁免仍未技术验证；Phase 5 与生产上线未批准。
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
| 管理端本地专项 | 邮件管理、安全预览、失效模板阻断、严格 MFA、刷新失败闭环 | 邮件专项契约测试 11/11、管理员认证验证 7/7、outbound 4/4，合计 22/22；`type-check`、`lint`、`build` 均通过。构建仅有既有 Vite chunk、dynamic-import 和模块类型 warning，无错误；安全预览、失效模板阻断、严格 MFA 与无循环 Token 策略的双轴 review findings 已全部关闭。该证据属于本地静态、契约和构建验证，不替代浏览器或真实环境 E2E |
| 用户端本地专项 | D-93/D-94、重复发码、持久错误态、触控区 | 邮箱 OTP 契约测试 15/15，`type-check`、`lint`、`build` 均通过；覆盖 D-93、D-94、重复发码前置互斥、持久错误态与 44px 触控区修复。该证据属于本地静态、契约和构建验证，不替代浏览器或真实邮件闭环 |
| 前端响应式与浏览器 | 1440、768、390、移动抽屉、五态、安全预览 | 双轴 review findings 已全部关闭；一次性本地 qa-only 浏览器组件已构建并通过离线自测。未认证管理端以及用户登录、注册、找回密码公开页均有 1440/768/390 证据；认证态邮件管理六页签、手机抽屉、平板收列、手机模板/日志卡片均已实测。加载中、正常和空数据三态通过；错误态与 view、view+manage、view+sync、view+test 四类页面降级已在本地回环构建、合成会话和受控 Mock 接口下通过，五个用例均无写请求。真实测试环境受控错误与四类真实角色会话仍待复验。模板安全预览 sandbox 阻断有效；原 ISSUE-001 已通过无业务脚本的最小对照页确认为 qa-only `addInitScript` 工具噪声，不属于产品缺陷。httpOnly Cookie 跨页面重载的静默刷新仍受当前架构限制，不得记为已关闭 |
| migration | 新库/旧库、up/down、CHECK、外键、索引、ownership、备份恢复 | 历史证据（已被后续证据取代）：当时测试主库已到 schema57/dirty0、应用重启后健康，但 000057 全新隔离库的完整 Down→Up 可逆周期尚未形成通过证据。后续真实周期已关闭 000057 技术可逆门禁，当前结论以 §17.2.1 的 000057 行及其后操作偏差记录为准；000055、000056 仍为部分通过，因此 migration 总矩阵仍未完成 |

`SKIP`、`BLOCKED` 或 `FAIL` 均不能计为通过。供应商 `accepted` 与人工确认收件必须分别记录。

## 17. 阶段状态与 Phase 4/5 门禁

### 17.1 当前阶段记录

| 阶段 | 当前结论 | 边界 |
|---|---|---|
| Phase 1：契约与设计 | 已由 QA/PM 书面通过 | `docs/aliyun-directmail-email-template-phase1-design-review.md` 是当时快照；其中“Phase 2 待验收”等表述反映当时状态，不覆盖本文最新阶段记录 |
| Phase 2：后端、本地数据库与自动化实现 | 当前仓库已具备 Go、migration、隔离 MySQL、离线测试和测试资产证据 | 不能据此声称真实 Redis、五场景 DirectMail、RAM 或完整 E2E 通过 |
| Phase 3：前端接入与产品验收 | 2026-07-23 QA 与 PM 通过，P0/P1/P2 为 0，允许进入 Phase 4 | 通过的是前端契约消费、交互和静态/Mock 浏览器证据，不是外部投递验收 |
| Phase 4：真实测试环境 | QA/PM 已附负责人豁免通过 | 13 项技术 PASS、4 项负责人豁免且未技术验证、QA/PM 两项附豁免签署；不代表生产环境验证 |
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
- 历史证据（已被后续证据取代）：远程测试 Redis 原语使用本轮 UUID 唯一前缀执行一次可审计验证，三条精确 key 创建前 `EXISTS=0` 为 3/3，分别验证 `SET NX PX` 互斥、错误/正确所有者续租与释放；finally 仅按已记录 key 精确 `DEL`，清理后 `EXISTS=0` 为 3/3。当时 `TestEmailRedisLeaseIntegration` 仍为 SKIP、Redis 重启与数据库墓碑阻断尚未执行。后续真实 Redis lease 已显式执行通过，当前结论以 §17.2.1、§17.2.1 后续复验和 Redis unknown 最新只读记录为准。
- 四类最小权限 RBAC 专项已在明确授权的测试环境完成：一次性执行器通过管理 API 创建并保留四个隔离角色和四个隔离账号，权限查询返回 29 项，四个账号完成双因素认证；`view`、`view+manage`、`view+sync`、`view+test` 固定无副作用矩阵 48/48 通过。认证使用短时调试回码，邮件适配器为显式非生产 Mock，因此证据等级仅为“测试环境调试 MFA + Mock 下的平台 RBAC 与接口门禁实测”，不证明供应商受理、真实收件或生产可用。执行器已恢复原环境并重启，恢复后调试回码与 bootstrap 均关闭，health、ready 均为 200，响应不再包含明文验证码；四个隔离账号会话及操作管理员会话共 5/5 吊销成功，操作管理员原访问与刷新凭据的独立复核均返回 401。角色和账号按验收要求保留；远端权限模式为 600 的输入材料已在获得单独授权后依次通过普通文件、非符号链接、当前用户属主和权限模式四项门禁精确删除，并经只读复核确认不存在。该证据不包含任何身份标识、联系方式、认证材料、验证码或随机路径。
- 本地新增的 Python 进程内参考模型矩阵 9/9 通过。五场景覆盖 success、reject、timeout，包含验证码状态可用性、成功后单次消费、重放拒绝、严格过期边界、冷却和 Adapter 调用次数；模板测试发送覆盖同 key accepted 重放、八并发单外呼、unknown 墓碑及新 key、600 秒冷却边界、白名单 active/revoked/missing 和前置失败零副作用。该矩阵完全离线，不访问测试服务器、数据库、外部 Redis、真实邮件或文件系统业务状态，不证明生产 Go 实现、真实并发、供应商受理或最终送达。随后补充生产 Go Phase 4 缺口测试：五场景逐一覆盖供应商拒绝、超时、accepted 后严格一次消费、重放拒绝和 `expires_at == now` 失效；模板测试发送覆盖 accepted 同 key 重放不二次外呼、并发同 key 仅一次外呼、白名单 active/revoked/missing、统一前置失败零外呼/零持久化/零审计，以及幂等键仅存摘要。Phase 4 顶层测试 8/8 通过且 0 SKIP，全量 Go 测试与 vet 均通过；新增测试不连接数据库或外部 Redis，因此真实业务数据效果、真实 Redis 并发和供应商外部结果仍须独立门禁。
- 同一时间窗本地敏感扫描覆盖 1003 个文本文件并包含当时前端构建产物，扫描器自测 4/4；结果 `FAIL=0`、读取错误为 0、`REVIEW=3`。三项 REVIEW 经人工只读复核，分别属于非验证码业务字段、合成脱敏示例和受统一响应门禁约束的内部回码载体，未发现已知真实秘密值。配置与 CIDR 双轴静态复评 findings 已全部关闭：`APP_ENV` 必须显式提供，调试开关只接受经 trim 后精确小写 `true`，安全环境权威集合包含 `testing` 而不包含 `staging`，CIDR 覆盖检测采用有界剪枝。随后从 Go 官方元数据选择 Go 1.25.12 Windows amd64 ZIP，在系统临时目录校验官方 SHA-256 后解压为一次性便携工具链；`go test ./... -count=1` 与 `go vet ./...` 均以退出码 0 完成。邮件相关顶层测试精确计数为 65 PASS、6 SKIP、0 FAIL；6 个 SKIP 是未启用真实 MySQL/Redis 门禁的集成项，不能计为通过。该结论仍不能替代容器验证、运行时日志、真实响应与部署产物的发布前复扫。
- 前端关卡 0 已与本机 `main` 对账：当前邮件分支 HEAD 为 `288599f`，本机 `main` 为 `608172e`，merge-base 为 `288599f`；`HEAD..main` 仅包含用户端布局、Agent 聊天和普通聊天三个非邮件文件，未发现邮件契约或页面 delta。由于本轮未执行 `fetch` 或更新远端引用，该证据只证明本机 `main` 对账，前端最终完成报告前仍须在获得 Git 只读联网授权后确认最新远端 `main`，不得将本机引用表述为最新远端状态。
- 管理员 QA 会话重新安全注入后再次只读复核真实测试页面：目标路由不再跳转登录，邮件模板概览显示 view/manage/sync/test 四权限均已授权，模板总数、审核通过和本地启用均为 5，未绑定场景为 0，浏览器控制台无错误；已保存 `admin-email-reinjected-session.png`。该证据只确认全权限管理员正常态，不替代四类最小权限真实角色会话或测试环境受控错误态。
- 获得四类真实 RBAC 页面复验授权后，测试工程师先执行只读前置门禁并安全停止：四个隔离账号和角色仍保留，但上一轮已授权删除的唯一 600 输入材料确认不存在，本机也没有四账号登录输入；现有执行器仅支持创建新资产，不能重新登录保留账号。本轮禁止重建账号、改库或扩权，因此未开启调试返回、未重启、未生成会话、未执行浏览器或任何远程写入。API 仍为唯一进程且 health/ready 均为 200。解除该门禁必须由账号所有者通过既有安全通道重新提供四个现有账号的登录输入，不能以新建账号替代。

### 17.2 Phase 4 真实测试环境门禁

以下项目必须具有技术 PASS 证据，或由项目负责人对明确列出的外部验证项作出书面豁免；豁免只能登记为 `waived_by_project_owner_not_verified`。全部项目得到处置且无 P0/P1/P2 未关闭缺陷后，才允许 QA 和 PM 依次书面签署 Phase 4：

- 在授权测试环境执行 000055，验证真实 MySQL 版本、结构、数据失效、五业务表、一技术表、五场景、四权限和精确 down。
- 在授权测试环境执行 000056，验证 bootstrap 专用权限、ownership、一次性 receipt、默认 404、内部 Token/CIDR、管理员 JWT、手机 MFA、并发唯一和成功后关闭入口；不得直接写 MFA 时间戳或数据库绑定替代该流程。
- 在授权隔离 MySQL 执行 000057 up/down，覆盖0/1/多条非零毫秒、完整备份与原值恢复、每个 DDL/DML/断言中断、缺行/孤儿/指纹篡改、重复执行和未知 partial；离线 SQL 摘要、故障注入与模型证据不能替代真实 MySQL 验证。
- 使用远程测试 Redis 验证 Go 服务的加锁、续租、所有权释放、外呼前复核、fencing、故障关闭和重启后的数据库阻断。
- 使用 Production Adapter 从 DirectMail 同步真实模板镜像，不直接向业务表导入供应商模板。
- 逐场景完成 register、login、reset_password、bind_email、admin_verify 的真实发送、人工收件、一次消费、重放和过期矩阵。本轮五业务流真实外发 E2E 由项目负责人明确豁免，未技术验证。
- 完成模板测试发送、同 key 幂等、并发、unknown 墓碑和冷却到期矩阵。本轮真实故障矩阵由项目负责人明确豁免，未技术验证。
- 使用最小权限 RAM 账号执行三个 Allow、三个显式 Deny以及创建/修改/删除模板的越权否定测试。
- 验证 Nginx 公开来源 Header、内部 metrics Token+IP 双闸、Prometheus 目标和告警规则。
- 对响应、日志、审计、数据库、metrics/telemetry 和前端执行全链路敏感扫描。
- 完成管理端 13 接口、用户端五业务流与移动/桌面页面的真实环境完整 E2E。
- 形成可复核测试报告，明确供应商 accepted 与用户确认收件两条证据，无任何凭据或 OTP。

### 17.2.1 Phase 4 当前证据与剩余执行顺序

下表只记录截至 2026-07-29 已取得的证据。`部分通过` 不得计为阶段通过，离线契约、代码存在、供应商 accepted 和人工收件也不能互相替代。

| 门禁 | 当前状态 | 已有证据 | 剩余动作与专项授权 |
|---|---|---|---|
| 000055 | 部分通过 | 测试主库已演进到更高版本，邮件五业务表、一技术表、五场景和四权限已被后续运行链路使用；新增静态契约在 normal/`-O` 两种模式均以 `checks=2161`、`mutations=16` 通过，当前 Up SHA 为 `7238522C...1FA3D`、Down SHA 为 `217B8FD...C26EE`，并显式拒绝 MySQL executable comment 与 optimizer hint。本轮 basic/partial 两套 runner 的 normal/`-O` 契约再次通过，攻击模型分别为 17/27；本地隔离包 SelfTest 与 normal/`-O` 打包契约也通过 | 本轮未执行真实 MySQL；测试 MySQL 容器内四个正式资产目录均不存在，`schema54-empty`、`schema54-legacy`、`schema55`、`schema56` 与两份基线 manifest 共六项受控输入尚未交付，因此当前不仅缺 migration 执行授权，也缺可执行输入。历史真实执行证据没有绑定执行时 SQL SHA，只能间接对应当前资产；仍需补齐与冻结 SQL 一致的独立结构、数据失效、partial 和精确 down 真实证据，不得在主库回滚 |
| 000056 Bootstrap | 部分通过 | 一次性 `admin_verify` 初始化成功，receipt、模板镜像、场景绑定、审计与入口关闭均已有证据；当前 Bootstrap 八种方法均 404；当前静态契约 normal/`-O` 均以 92 项通过，Up SHA 为 `BC900F...C735`、Down SHA 为 `F42A30...A5C2`。本轮 basic/partial 两套 runner 的 normal/`-O` 契约再次通过，攻击模型分别为 19/32；本地打包契约固定 `attack_cases=13`、`output_preservation_cases=4` | 本轮未执行真实 MySQL，且六项外部基线尚未交付；并发唯一、ownership 组合、完整失败矩阵和 43 个 partial 目标仍须取得授权后在隔离 MySQL 验证。基础 runner 的 partial 继续诚实保持 `not_implemented` |
| 000057 UTC 秒级时间 | 技术周期通过，操作偏差已登记 | 执行前测试主库为 schema57/dirty0、69 张 InnoDB 基础表，无 DDL 或锁活动；冻结脚本、Up、Down 摘要一致。真实隔离周期实际完成两次，两个新隔离库均最终为 schema57/dirty0、69 表、专用 backup 表存在、receipt 时间精度为 0、marker 权限为 600，稳定表摘要一致；测试主库仍为 schema57/dirty0，API health/ready 均为 200 | 技术上的 Down→Up→Down→Up 可逆门禁已取得真实证据，但专项授权仅允许执行一次，实际执行了两次。“授权一次、实际执行两次”已作为正式操作偏差登记；用户已确认两个新增隔离库及证据冻结保留至 Phase 4 验收结束，不清理、不复用、不再执行 |
| 真实 Redis | 部分通过 | 唯一前缀 Redis 原语和真实 `TestEmailRedisLeaseIntegration` 均已通过；历史 Redis unknown cleanup 精确删除 2 条日志、1 条白名单和 1 条模板已核验，identity diagnostic 与独立 postcheck-only 均已 PASS。2026-07-31 最新只读预检确认 API 单实例、health/ready 200、MySQL/Redis 容器各 1、Redis PING、Bootstrap 全方法 404，且 API 配置目标与唯一 `molin-redis` 容器的 `run_id` 完全相同 | Redis lease、历史 cleanup 与独立 postcheck 子门禁已关闭，cleanup 不得重跑；身份复核同时证明重启该唯一容器会影响当前 API，因此没有专项重启和数据库夹具写入授权时不得启动新 phase1。修复后二进制的全新 `phase1→重启→phase2` 仍未通过，数据库 unknown 墓碑阻断总门禁保持未通过 |
| Production Adapter 同步 | 通过 | 一次真实同步成功，五模板审核/变量/missing/快照与同步审计均已核验 | 不需要重复同步；后续 RAM Deny 矩阵应使用隔离窗口并验证失败不改快照 |
| 五场景真实 OTP | 部分通过 | 五场景均有真实 accepted、人工收件和一次成功业务消费；生产 Go 测试逐场景覆盖 success/reject/timeout、accepted 后单次消费、重放拒绝和严格过期，Phase 4 顶层测试 8/8、全量测试和 vet 均通过 | 新测试无数据库，仅证明 OTP 业务门一次性放行；真实建用户、建会话、修改密码、换绑邮箱和 MFA 时间戳仍以既有测试环境 E2E 证据为准，真实重放/过期故障矩阵仍需独立门禁 |
| 模板测试发送 | 部分通过 | 生产 Go 测试已覆盖 accepted 同 key 重放、并发同 key 单外呼、unknown 墓碑、外呼前丢锁、白名单三态、统一前置失败零副作用和幂等键摘要；新增测试 6 项均通过且 0 SKIP | 真实模板测试发送、真实 Redis 并发及供应商故障注入仍须单独授权；单元测试不得替代外部受理或最终送达 |
| RAM 最小权限与 Deny | `PARTIAL / BLOCKED_BY_AUTH` | 官方已确认 `SingleSendMail` 无 `DryRun`，缺参请求不能证明 Allow/Deny，旧缺参探针的权限结论作废。现有阿里云控制台截图已只读复核：专用 RAM 用户没有加入用户组，个人权限中只绑定一个直属自定义策略 `MolinDirectMailSingleSend`；策略当前版本 `v2` 的 Allow 集合精确为 `dm:SingleSendMail`、`dm:QueryTemplateByParam`、`dm:DescTemplate`，Resource 为 `*`，不存在 `dm:*`、全局 `*`、`dm:CreateTemplate`、`dm:ModifyTemplate` 或 `dm:DeleteTemplate`。2026-07-31 又以当前源码 SHA `987D8859...F953` 构建一次性探针，二进制 SHA `AAC7C92F...F4A0`；离线安全测试通过后，真实 `QueryTemplateByParam`、`DescTemplate` 两个只读 Action 均通过，未调用发信或模板写接口，源码包、二进制、构建目录和本地归档均已回收 | 当前读 Action 证据只关闭两项 Allow 现场复验，不能替代实际可能权限、角色信任链、最近 API 尝试或显式 Deny 的审计证据。既有真实 `accepted` 仍须与同一 RAM 身份和权限审计关联；Create/Modify/Delete 与显式 Deny 改由 RAM 权限审计或既有 `RequestId` 的 OpenAPI Troubleshoot 诊断证明。Chrome 权限审计因本机原生通信缺失暂未完成，当前仍未取得权限审计/RequestId 证据，最终门禁保持 `PARTIAL / BLOCKED_BY_AUTH`；补证无需新增真实邮件，也不得为补证构造有副作用的真实请求 |
| Nginx 与 Prometheus | 通过 | Token+IP 双闸、伪造来源拒绝、非 GET 405、回环监听、target UP、21 个封闭序列和三条健康告警规则均已实测 | 保留独立监控回滚点；无需重复触发 Adapter |
| 全链路敏感扫描 | 通过 | 2026-07-31 最终运行时扫描绑定当前 API 二进制、管理端/用户端部署树和同一捕获窗口；source projection 六面通过，collector 组装 157 个文件、4036542 字节，scanner 实际扫描 156 个文件、4033941 字节，六面 6/6、findings=0、window/deployment 均绑定。此前工作树静态扫描仍为 `FAIL=0`、`REVIEW=4`、`protected_env=0`、`read_errors=0` | 封闭证据包与六面安全投影保留；临时 Token、连接文件、MySQL 账号和进程环境快照已回收。管理员 Token 的服务端吊销未形成成功证据，账号持有人仍应从当前会话执行正常退出 |
| 真实 RBAC 与 E2E | 部分通过 | 测试环境固定无副作用权限矩阵 48/48 通过；四类真实角色会话的 1440/768/390 页面权限降级已完成，共形成 12 张截图。最终 `view+test` 专项 3/3 通过，替代账号 logout、禁用和操作管理员 logout 均为 1/1；原环境恢复，调试、Mock、Bootstrap 均关闭，API 单实例且 health/ready 为 200，输入与回滚文件不存在。测试服务器现有前端构建的受控 503 错误态也已在三宽度通过，业务 API 访问和写请求均为 0。管理端邮件专项 11/11、管理员认证验证 7/7、用户端邮箱 OTP 15/15，双端 `type-check`、`lint`、`build` 均通过 | 真实四角色三宽度及已部署前端受控错误态两个子门禁均已关闭，无需重复创建账号或修改测试 API。其余五业务流 E2E、后端运行时故障矩阵及 httpOnly Cookie 架构限制仍按既有门禁处理 |

当前 `main` 对账边界：邮件分支 HEAD 为 `87161414c553eddf3f900057488c3b0b7702838c`，merge-base 为 `288599f054eacbe334ea0e3a5734a75db7331a9f`；一次只读 `git ls-remote --refs origin refs/heads/main` 已精确确认远端 `main` 仍为同一 SHA，因此远端不存在邮件分支尚未对接的新增 delta。本地 `main=608172e1aa4532e12087afd90daa611ffccd4a73` 仅多一条尚未推送的聊天布局提交，只修改 `web/user-console/src/components/layout/UserLayout.vue`、`web/user-console/src/views/agent/AgentChatView.vue`、`web/user-console/src/views/chat/ChatView.vue` 三个非邮件路径；邮件分支相对 merge-base 未修改这三项，无路径或语义冲突。该只读核验未执行 fetch、merge、rebase、push 或任何工作树改写。

2026-07-30 的 000057 真实隔离周期证据：执行前只读门禁确认测试主库为 57/0、69 张 InnoDB 基础表、无 DDL 或锁活动，既有隔离库数量为 2；使用的冻结摘要为脚本 `D3A4B8...83A6`、Up `50DCD...CFC67C`、Down `EE05D...495BB`。同一技术周期实际成功运行两次，间隔约 55 秒；两个新隔离库都通过 Down→Up→Down→Up，最终均为 57/0、69 表、backup 表计数 1、receipt `DATETIME_PRECISION=0`、完成 marker 权限 600，稳定表摘要均为 `D41910...B237A`。第一次 dump 摘要为 `D6696C...B479E`；第二次 dump 摘要为 `9E1242...A6DBC`，并与对应操作员输出一致。后验只读对账显示隔离库数量由 2 增至 4、测试主库仍为 57/0、周期进程为 0、health/ready 均为 200。

上述证据只关闭 000057 的真实技术可逆周期门禁，不能掩盖操作控制偏差：本轮只授权一次执行，但实际执行两次，现已作为正式操作偏差登记。用户已确认两个新隔离库、dump、marker 和运行证据冻结保留至 Phase 4 验收结束，当前不清理、不复用、不再运行周期；它们不属于 Redis unknown 墓碑测试的资产、恢复点或 cleanup 目标，必须从该测试的全部目标清单和清理范围中排除，也不构成 Redis 只读准备的前置条件。Phase 4 结束后如需处置，必须建立独立变更单并获得明确破坏性操作授权；清理前先以只读方式从受保护证据解析两个精确目标，逐一核对 57/0、69 表、marker、dump 摘要和来源归属，禁止按前缀、通配符或模糊匹配删除。数据库与运行目录应分别列出精确目标并分别确认，清理后只读确认目标不存在，同时再次确认测试主库 57/0、API health/ready 200。文档不得保存完整随机库名、凭据或业务数据。

2026-07-29 替代账号补验更新：通过管理 API 创建并复用四个既有角色，四账号均在短时调试回码与 Mock 邮件适配器下完成手机、邮箱双 MFA。真实测试环境已完成 `view`、`view+manage`、`view+sync` 三角色的 1440/768/390 共 9 项只读页面降级并形成截图；`view+test` 在首个截图前被旧执行器统一归类为 `browser_acceptance_failed`，未重试，因此真实四角色门禁仍为部分通过。原环境逐字节恢复，调试与 Bootstrap 关闭，API 单实例且 health/ready 为 200；四替代账号 4/4 禁用，操作管理员 Access Token 为 401，600 输入和回滚快照已删除。旧执行器因先禁用再 logout，四账号显式 logout 被封禁中间件拒绝；`BanUser` 已关闭账号访问并尝试吊销 Refresh 会话，但缺四个旧 Access Token 的逐项 401 证据。执行器现已改为先 logout 再禁用并输出固定安全错误分类，离线验证通过，尚未获得再次真实执行授权。

同日单账号 `view+test` 复验获得授权后，执行器在只读角色列表门禁即返回 `role_list_failed`，说明本轮管理员 Access Token 无法满足当前管理 API 调用；执行未开启调试、未创建账号、未启动浏览器。环境保持调试与 Bootstrap 关闭，API health/ready 为 200，无回滚文件；无效 600 输入已删除。该失败不增加任何 `view+test` 验收证据。

管理员随后通过受控 10 分钟 Mock 调试窗口取得新的双 MFA 会话，窗口正常恢复原配置。第二次单账号 `view+test` 复验通过角色冻结、账号创建和 Mock 双 MFA，但浏览器精确失败为 `test_action_visibility_mismatch`。静态复核证明旧 QA 脚本在页面默认“概览”状态下先检查仅存在于“模板”页签的测试发送按钮，属于测试页签顺序缺陷；该结果既不能判为产品失败，也不能判为 `view+test` 通过。finally 已完成替代账号 logout 1/1、禁用 1/1、管理员 logout 1/1，环境与敏感文件清理通过。浏览器脚本已改为先进入“模板”，新增页签顺序契约后自测 8/8、远端哈希一致；修复后尚未再次真实执行。

最终复验更新：在再次获得单账号专项授权后，修复版 `--view-test-only` 状态机完整通过。唯一 `view+test` 角色冻结为 3 个权限项，替代账号通过 Mock 手机、邮箱双 MFA；1440、768、390 三宽度浏览器检查 3/3 通过。finally 完成替代账号 logout 1/1、禁用 1/1、操作管理员 logout 1/1，原环境恢复且调试、Mock、Bootstrap 均关闭。独立后验确认 API 单实例、health/ready 为 200、输入和回滚文件不存在；四角色共 12 张真实测试环境截图齐全。随后直接加载测试服务器现有前端构建，在浏览器内拦截全部 API 并对邮件概览构造固定 503/51003，三宽度的 Toast、持久错误条、重试入口和无横向溢出均通过，业务 API 访问和写请求为 0。真实 Redis Go lease 集成也已显式执行且未 SKIP，ownership fencing、续租和精确清理通过。至此“真实四角色三宽度权限降级”“已部署前端受控错误态”和“真实 Redis lease”三个子门禁均通过，但 Phase 4 总门禁仍需依据 Redis 重启/数据库墓碑、RAM Deny、最新 main 对账及测试/产品确认逐项判断。

Redis 重启/数据库墓碑门禁的分阶段 Go 测试资产已经冻结：使用真实 `EmailRepository`、真实 Redis 和计数 Mock Adapter。phase1 启动前要求指定状态文件不存在；phase1 创建本轮 unknown 墓碑并原子创建状态文件后，才要求该文件为当前用户独占的普通 600 文件。phase2 必须通过 Redis `run_id` 变化证明真实重启，再验证旧/新幂等键均由数据库墓碑阻断且 Adapter 增量为 0；phase2 固定保留测试数据和状态文件，不再隐式清理。独立 cleanup 阶段要求额外双重确认，且门禁位于数据库和 Redis 连接之前；cleanup 读取并验证既有 600 状态文件，但不比较 Redis `run_id`，只按状态文件记录的主键及单一锁键精确清理并验证不存在，禁止 `FLUSHDB`、`KEYS`、`SCAN` 或模式删除。测试强制 schema57/dirty0、测试环境、Mock Adapter和双重确认。000057 保留的两个隔离库及证据必须从 Redis unknown 的全部准备、恢复和 cleanup 目标中排除，不是本门禁的前置条件。资产冻结时的离线静态契约、敏感扫描、gofmt、禁用集成开关时的专项 Go 测试、全量 `go test ./... -count=1` 与 `go vet ./...` 均通过；后续真实周期证据及当前状态如下，cleanup 涉及数据库删除时必须另行确认。

本次 Redis unknown 远程准备只执行一次 SSH 只读对账，未重试，未执行数据库或 Redis 写入、服务重启、phase1、phase2 或 cleanup。已取得的分项证据为：API 单实例且 health/ready 均为 200；MySQL 与 Redis 容器均唯一；测试主库为 schema57/dirty0；MySQL UTC 墙钟偏差不超过 5 秒；既有状态文件为安全的普通 600 文件；两条归属已核验的 unknown 日志处于同一 scope；对应模板和白名单各 1 条；Redis 只读命令成功；两个 000057 证据目录的类型、属主、权限和非符号链接元数据门禁通过。这些分项结果不能替代最终聚合门禁。

该次对账最终在 `cycle_exclusion` 聚合处失败，没有输出最终固定摘要，因此无法确认两个 000057 隔离 schema 是否均存在并已从 Redis unknown 目标集合排除；状态文件的精确 phase、Redis `run_id` 是否变化、唯一锁 key 的 `EXISTS` 值和孤儿目录数量也未形成可信结论。进一步复核发现 PowerShell 经 stdin 传送的远端脚本文本带 UTF-8 BOM，导致远端首行 `set -u` 未生效；当前 runner 不可作为正式冻结证据。结论为 Redis unknown 总门禁未通过，本轮不得进入新的 phase1、Redis 重启或 phase2。后续只能在取得新授权后先修复 BOM 和只读聚合查询，再仅重跑一次只读对账；在最终固定摘要完整通过前禁止任何写入、重启或清理。

本地替代运行器与远端只读 payload 已按测试工程师冻结契约完成：Windows PowerShell 5.1 先把无 BOM payload 写入本轮唯一、ACL 受限临时文件，再通过模块限定 `Start-Process -RedirectStandardInput` 把文件句柄交给固定 SSH；stdout/stderr 使用独立临时文件，整体超时与 finally 精确清理已冻结。真实 Windows SelfTest 只有在子进程收到首字节 115、无 BOM、早期失败 stderr 为空且摘要固定时才通过。远端首行固定为 `set -Eeuo pipefail` 并立即以 `shopt -qo` 自检。`cycle_exclusion` 已取消嵌套 `sh -c`，从两个完成证据目录逐一验证 32 位十六进制目标、互异性、状态排除和 `information_schema.schemata` 存在性。两个隔离库由 root 创建且未授权应用账号，因此存在性改由容器内 root 严格只读通道核验：外层只传已验证 schema 名，容器内固定生成单条 COUNT SELECT，root 凭据不离开容器且不增加 GRANT。历史状态文件只接受 `/home/pc/molin-email-unknown-<32位小写十六进制>.state`，孤儿目录只纳入 `/home/pc/molin-runtime/email-unknown-<32位小写十六进制>`；health/ready 只接受 HTTP 200，000057 dump 在 schema 查询前显式拒绝符号链接。只读对账仅要求线上 API 的 `APP_ENV=test` 且当前 Adapter 已退出 Mock，摘要只输出固定 `live_adapter_mock=false`；历史 phase1/phase2 仍由独立测试进程显式注入 Mock。Mock 只代表隔离测试进程的 Adapter，持久化模板和发送日志 provider 固定为 `aliyun_directmail`。一次正式只读运行返回 34 字符失败摘要和 43 字符 stderr；34 字符可对应多个等长 stage，不能单凭长度定位。QA 随后确认旧运行器的默认 StandardInput StreamWriter 会注入 BOM，因此旧证据无效。失败正则现使用命名组与 `\\r?\\n?\\z` 绝对结束锚；仅当单行 stdout 完整匹配白名单、stderr 为空且分类为 `remote_gate_failed` 时，安全 JSON 才包含 stage，其他分类不带 stage。双 LF、双 CRLF、未知 stage、额外行和任意 stderr 均拒绝且不泄露 stage。远程固定证据已确认旧聚合 stage 为 `cycle_not_isolated`，但不能确定其中哪条断言失败；该 stage 已拆分为六个独立白名单 stage，并以 find 类型证明和数值 stat 元数据取代 `%F` 本地化文本。PowerShell SelfTest 18 项与 Python 离线契约 37 项通过；全部验证均为本地离线，没有启动 SSH、连接数据库或 Redis。该结果只说明替代资产已具备再次只读对账条件，Redis unknown 总门禁仍未通过，必须取得新的单次只读授权后才能远程复验。

最新正式远程只读门禁随后在严格一次、未重试的授权窗口中完整 PASS。固定脱敏摘要确认：API 1、health/ready true、`live_adapter_mock=false`；MySQL/Redis 各 1、schema 57/dirty 0、时钟通过；状态文件安全且 phase 为 `phase1_created`；primary/unexpected/scope/template/allowlist 分别为 1/1/2/1/1；Redis PING 通过、`run_id_changed=true`、`lock_exists=0`；orphan 为 0；000057 cycle evidence/valid/schema/excluded 均为 2；writes/restart/cleanup 均为 false。该摘要未包含任何敏感值。Redis unknown 只读准备现记为通过；下一步为需要新的人工授权的历史夹具精确 cleanup，清理范围不得包含任何 000057 隔离库、dump 或周期证据目录。

历史夹具精确 cleanup 首次编排在 metadata 摘要校验失败后，新专项授权下的正式流程再次停在 metadata，固定输出为 `classification=metadata_summary_invalid`、`stdout_length=0`、`cleanup_started=false`、`postcheck_started=false`，且未重试。因此两条隔离发送日志、一条测试白名单和一条测试模板仍未被删除，恢复点及两套 000057 隔离证据均未被触碰；两次 metadata 失败都不得记为 cleanup 或独立 postcheck 通过。随后仅在本地确认根因：Windows PowerShell 5.1 的 `Start-Process` 返回对象在读取 `ExitCode` 前未提前取得 `Handle`，导致 null 被强制转换为 0。运维仅修复 `run-email-unknown-history-authorized-flow.ps1`、`run-email-unknown-history-cleanup.ps1`、`run-email-unknown-history-postcheck.ps1` 三个脚本；对应 SelfTest 分别为 26、52、56 项通过，进程退出码 0 与 7 均被正确保留，`LocalPreflight` 9 文件通过、PowerShell AST 错误数为 0，测试工程师独立复审通过。同源风险随后扩展修复到 `run-email-unknown-recovery-gate.ps1`、`run-email-unknown-recovery-preflight-diagnostic.ps1`、`run-email-unknown-remote-readonly-gate.ps1`，对应 SelfTest 34/34/20 项、进程退出码 0/7 和 AST 0 错误均通过，测试工程师独立复审通过；对全部 `scripts/*.ps1` 的扫描识别 6 个候选，最终 `unsafe=0`。以上修复和验证没有启动 SSH、连接数据库、执行清理或执行 Git 写操作，只证明下一次编排资产的本地控制流已修正；真实 cleanup 仍未通过，下一次执行仍需新的专项授权，Phase 4 结论不变。

以下为早于本次只读对账的历史周期记录：当时曾在测试环境启动一次真实 Redis 重启周期。phase1 在建立并验证单事务 MySQL 恢复点后通过：schema57/dirty0、Mock Adapter 调用 1 次、发送日志收敛为 `failed/provider_outcome_unknown`，未调用 DirectMail。唯一 Redis 容器重启成功，PING 恢复且 `run_id` 已变化。首次 phase2 失败；只读安全投影确认原日志仍为 unknown 且同 scope 已有 2 行，但数据库计算的原日志年龄约为 8 小时。根因是该集成测试连接错误使用 `Loc: time.UTC`，而仓储 UTC 墙钟写入契约与生产全局 DSN 均固定为 `loc=Local`；上海进程下因此把测试时间再提前 8 小时，墓碑被立即判为超过十分钟冷却窗，新 key 进入计数 Mock Adapter 并形成第二条隔离日志。现有证据不指向生产连接路径缺陷。

测试资产已在本地修复：连接改为 `Loc: time.Local`，写入夹具前新增 MySQL UTC 墙钟偏差不超过 5 秒的门禁；phase2 在任何 Adapter 调用前精确核验原墓碑、至少 120 秒冷却余量和既存新-key日志，逐步输出真实旧 key、新 key 与调用计数；version 1 状态文件新增可选的意外日志精确主键并保持向后兼容，cleanup 对原日志与可选第二日志执行完整归属校验后才按主键删除。修复后状态兼容测试、静态契约、三文件敏感扫描、全量 Go 测试与 vet 均通过。修复后二进制随后在原失败现场执行 phase2：在任何 Adapter 调用前识别既存第二日志，固定输出 `adapter_calls=0` 与 `unexpected_log_recorded=true`，把第二日志精确主键写入 600 状态文件后安全 `BLOCKED`。当前两条隔离日志、白名单、模板、唯一 Redis 键、状态文件和数据库恢复点均保留；禁止直接重跑 phase1，必须先取得数据库精确清理授权。该子门禁仍为未通过。

获得一次专项授权后，远程周期在首个写门禁 `remote_asset_directory` 失败并立即停止：SSH 在创建本轮唯一资产目录时被远端关闭，退出码非 0。SCP 与维护控制器均未启动，因此数据库恢复点、phase1、Redis 重启和 phase2 均未执行，没有数据库状态文件或隔离测试数据；唯一资产目录是否已创建无法由本轮证据确认，记为 unknown。依据“任一门禁失败立即停止”要求，本轮没有追加 SSH 复核、自动清理或重试，不能增加墓碑门禁通过证据。

对上述早期 SSH 失败调用曾仅基于本地调用记录完成事故复盘，未再次联网。该早期调用使用固定 SSH 参数和单个远端命令参数，没有 TTY、stdin、here-string 或 BOM；目录后缀只含固定前缀、十六进制和短横线，并以单引号包裹。输出只有连接关闭，没有权限拒绝、shell 语法或 mkdir 错误，因此当时的安全分类为 `transport_or_session_closed`，更可能是连接/会话瞬断；精确退出码和目录状态仍为 unknown。该历史结论不适用于本次带 BOM 的只读对账 runner，也不能覆盖本次 `cycle_exclusion` 聚合失败。

下一次诊断的 SSH 配置已在本机通过 `ssh -G` 离线展开：BatchMode、严格主机密钥检查和 10 秒连接超时均生效，ProxyCommand、ProxyJump、LocalCommand 以及 local/remote/dynamic forward 均不存在。本次配置展开没有建立网络连接；它只证明下一次固定 `printf` 诊断不会继承隐式代理或转发，不能替代实际 transport 结果。

获得单次只读 transport 授权后，只启动一个 SSH 进程并固定执行 `printf transport_ok=true`。结果为退出码 0、stdout 唯一且精确匹配、`ssh_attempt_count=1`、`ssh_completed_count=1`，远端写入为 false。该证据排除持续性 SSH transport 故障，但不改变上一次唯一资产目录的 unknown 状态；依据本轮授权，本次没有检查目录、上传文件、连接数据库或启动后续周期。

DirectMail RAM 安全探针也已冻结。QueryTemplateByParam 与 DescTemplate 仅执行最小读取；SingleSendMail 不携带 AccountName、收件人、主题或正文，三个模板写 action 不携带 TemplateId、名称、主题或正文。正确 Allow 应返回 success/request 安全类别，正确 Deny 应返回 permission；若误授权，缺参请求只能返回 request 并使探针失败，不会创建、修改、删除模板或发送邮件。离线 permission/request/success/unknown 分类与六 action 字段形状共 10 个子用例通过，Go vet 与敏感扫描通过。真实最小权限探针已执行一次：两个模板读取 action 成功；SingleSendMail 的安全缺参请求返回 `rejected_other` 并立即停止，未发送邮件，三个模板写 action 未调用。阿里云官方文档仍把通用缺参列为 `MissingParameter`，因此当前不得猜测扩展白名单；需先用不可逆哈希、长度与 HTTP 状态族完成唯一候选匹配，再继续 RAM 矩阵。

2026-07-30 使用冻结的安全诊断二进制再次执行一次最小权限探针：QueryTemplateByParam 与 DescTemplate 继续成功，缺少全部邮件业务字段的 SingleSendMail 返回 HTTP 4xx、Code UTF-8 长度 18，安全摘要为 `48d419309078f725902c94f5ebfa1b5b194e7a481eb46055b6f9e51160ca064f`，与冻结候选 `MissingParameter`、`InvalidParameter` 均不匹配，分类保持 `unknown/rejected_other`。探针在该处立即停止，未发送邮件，CreateTemplate、ModifyTemplate、DeleteTemplate 均未调用。随后只读获取阿里云官方 DirectMail 公开错误码页面，对页面中的公开 Code 候选执行长度与 SHA-256 比对；18 字节候选没有命中该摘要。由于没有唯一官方候选，禁止猜测原始 Code、扩展安全白名单或继续 RAM Allow/Deny 矩阵；该门禁仍为未通过。

上述两段保留为历史执行事实，但其中“缺参响应可区分 Allow/Deny”及“必须先识别未知 Code 才能继续”的权限推论已被当前官方结论取代：`SingleSendMail` 无 `DryRun`，任何缺参请求都不能作为授权或拒绝证据。修复后的 `directmail_ram_probe_test.go` 真实路径只调用 QueryTemplateByParam、DescTemplate 两个 read action；SingleSendMail、CreateTemplate、ModifyTemplate、DeleteTemplate 等副作用 action 无法构造，旧 Deny 场景在 Adapter 前失败关闭。四个官方权限码仅精确匹配完整字符串，未知码不猜测；专项 `go test`、`go vet` 均 PASS，QA P1/P2=0。2026-07-31 最新一次当前源码真实探针已再次确认两个 read action 均成功，且没有发信、模板写入、数据库或 Redis 副作用；一次性资产已完整回收。当前证据链还需把既有真实 `accepted`、有效策略快照与尚待取得的 RAM 权限审计/既有 `RequestId` 诊断关联，因此最终 RAM 门禁仍为 `PARTIAL / BLOCKED_BY_AUTH`，无需也不得新增真实邮件补证。

同一工作树随后重新执行仓库级敏感扫描：扫描器自测 4/4，通过读取 1015 个文本文件，结果为 `FAIL=0`、读取错误 0、`REVIEW=3`、`INFO=249`。三项 REVIEW 逐项复核后仍分别属于应用市场普通 `Code` 字段、手机号脱敏注释示例，以及由 `IsSafeNonProduction()` 与显式调试开关共同限制的验证码响应载体。本轮未发现已知真实 AccessKey、Secret、Token、OTP、完整邮箱或未脱敏手机号；该结果只关闭当前工作树静态字面量扫描，不替代测试环境运行时各数据面的同一时间窗扫描。

同日续跑本地门禁：管理端 `type-check`、`lint`、`build` 和 22 个契约用例通过，用户端 `type-check`、`lint`、`build` 和 15 个邮箱 OTP 契约用例通过；000056/000057 静态迁移契约、48 项无副作用权限矩阵以及安全输入脚本自检均通过。使用完整 Go 1.25.0 工具链并设置 `GOPROXY=off`、`GOSUMDB=off` 后，`go mod tidy -diff` 无输出，`go test ./... -count=1` 与 `go vet ./...` 均通过；先前出现的 `x/net`、`x/sys` 依赖差异已确认来自不完整 GOROOT，并非仓库依赖缺口。仓库复扫一度在未跟踪的 `.gstack/browse-audit.jsonl` 中发现 JWT 形态残留；确认其为普通、非符号链接且未被 Git 跟踪的浏览器 QA 临时产物后已精确移除，复扫覆盖 1134 个文本文件，结果 `FAIL=0`、读取错误 0、`REVIEW=4`、`INFO=271`。四项 REVIEW 分别为两个普通业务编码字段、手机号脱敏注释示例和受双门禁约束的验证码响应载体。该结果仍不替代测试环境运行时数据面的同一时间窗扫描。

2026-07-30 在补充 RAM 安全诊断事实并重新对齐冻结契约后，再次执行扫描器自测与仓库复扫：自测 4/4，通过读取 1140 个文本文件，结果 `FAIL=0`、读取错误 0、`REVIEW=4`、`INFO=271`。四项 REVIEW 的分类和文件位置与上一次复核一致，没有新增敏感输出面；该证据只覆盖当前工作树静态文本，测试服务器响应、日志、审计、数据库和 telemetry 仍须在最终同一时间窗独立扫描。

同日最新一次本地静态扫描覆盖全工作树 1170 个文本文件；固定汇总为 `FAIL=0`、`REVIEW=4`、`INFO=280`、`protected_env=0`、`read_errors=0`。四项 REVIEW 与既有人工复核分类相同，固定为 `complete_phone_literal=1`、`debug_code_response_surface=3`，没有形成新的敏感输出结论。该结果仅更新本地工作树静态文本证据，不能替代尚未执行的运行时响应、应用日志、审计、数据库安全投影和 telemetry 同一时间窗扫描，因而不改变 Phase 4 仍未通过的判定。

本轮最新离线回归还确认：`email_unknown_remote_readonly_gate_contract.py` 已修复旧 `cases=18` 摘要兼容问题，normal 与 Python `-O` 两种模式均通过，固定为 `attack_cases=43`；配套 PowerShell SelfTest 通过，且固定声明 `external_access=false`、`database_access=false`、`redis_access=false`、`ssh_started=false`。000055 静态契约在两种模式下仍为 `checks=2161`、`mutations=16`，Up/Down SHA 不变；000056 两种模式仍为 92 项通过，Up/Down SHA 不变；000057 隔离周期契约通过并覆盖 12 类故障注入，本轮没有连接数据库或执行 migration。email unknown restart 攻击模型 23 项、isolated build 攻击模型 13 项、进程内邮件矩阵 9/9 以及敏感扫描 SelfTest 4/4 均通过。以上离线结果本身不构成历史夹具 cleanup 的通过证据；cleanup 已由后续新专项流程独立关闭。修复后二进制的新 Redis `phase1→重启→phase2` 周期、RAM 最小权限与 Deny、000055/000056 真实隔离 MySQL、运行时同一时间窗敏感扫描及最终 QA/PM 签署仍未关闭，Phase 4 结论保持未通过。

2026-07-31 运行时六表面首次正式捕获已完成前端部署树导出、API 捕获期五个只读 GET 与日志封闭恢复；日志预热固定摘要为 public/admin/internal 全部通过且请求数为 5。随后旧 source projection 在应用日志面以 `log_contract` 失败。只读结构诊断确认日志中没有敏感命中，失败来自 GORM 1.31 默认 Warn logger 输出的两组合法彩色慢查询三行块。准备器现已增加严格状态机：只接受固定 CRLF 前缀、Go 模块根 `molin/server/` 或等价部署根 `/home/pc/molin/server/`、窗口内首行及单条无锁只读 `SELECT`，并拒绝写 SQL、分号/注释、其他相对/绝对路径、残缺和乱序块；所有原始行仍先执行敏感扫描。修复后的 projection SHA 为 `071BB596...A5C45`，Windows 与 Linux normal/`-O` 契约均为 125 项通过，runtime log prime 在 Windows 为 48 项、Linux 为 52 项通过，运维资产契约为 43 项，两个启动器 SelfTest 分别为 8 项和 9 项。远端固定 companion 已更新并保留旧文件回滚副本；尚未使用新管理员会话重新执行完整 source projection、collector 与 scanner，因此运行时全链路敏感扫描仍为部分通过，不得据此签署 Phase 4。凭据回收随后逐个核验并删除五个状态均为 `restored`、窗口均已失效的精确捕获目录；这些目录中的进程环境快照可能包含测试 Secret，不能作为长期证据保留。删除后捕获根目录为空，`/home/pc/molin-runtime` 三层内没有 Token、connection、credential 或 secret 命名文件，MySQL `p4_` 临时只读账号计数为 0；API health/ready 仍为 200，`EMAIL_DEBUG_RETURN_CODE=false`。最终重跑必须使用新会话、新捕获 ID 和新临时只读账号，并在结束后执行同样的精确回收。

上述“尚未重跑”状态已被同日最终执行取代。最终捕获重新导出管理端 65 个、用户端 85 个部署文件，捕获期五个只读 GET 再次以 public/admin/internal 全部通过、requests=5 完成并恢复原 API。现场还暴露两个准备器兼容问题：GORM 首行使用 Go 模块路径 `molin/server/...`；MySQL 8.0.46 对 `REGEXP BINARY` 返回 `3995/ER_CHARACTER_SET_MISMATCH`，且 mysql 客户端会话字符集使中文占位符字面比较失真。最终实现只增加受限模块根白名单，把小写 64 位十六进制检查改为 `REGEXP_LIKE(...,'c')`，并用“历史邮箱已失效”的冻结 UTF-8 HEX 比较；聚合复核确认 768 行 email 验证码的 `target_value` 全为 NULL，747 行为同一历史占位符，21 行为带 `@` 的正常脱敏地址，没有为通过而放宽数据安全规则。最终 projection SHA 为 `2BC04F38...606AB`，本地 normal/`-O` source contract 均为 126 项通过，Linux normal 为 126 项通过；六面 source projection PASS，deployment SHA 为 `53490405...E2E54`。collector PASS，固定 manifest SHA 为 `237651DF...A67D8`、bundle ID 为 `F6366545...D75C1`；scanner 最终 PASS，`surfaces_passed=6`、`files_scanned=156`、`bytes_scanned=4033941`、`findings=0`、`window_bound=true`、`deployment_bound=true`，且 writes/restart/deploy/mail_sent 均为 false。凭据收尾确认 `p4_` 临时账号为 0，本轮 live/capture 目录不存在，历史 capture 根为空，远端三层内无 Token/connection/credential/secret 命名文件，本机剪贴板无 JWT，API health/ready 200 且 `EMAIL_DEBUG_RETURN_CODE=false`；封闭 bundle、六面安全投影和前端导出作为无凭据证据保留。注销请求在本地 HTTP 客户端构造阶段因 CRLF 校验失败，服务端是否收到请求不可证明；因此只记录凭据副本已回收，不声称管理员会话已吊销，账号持有人仍须正常退出。

本轮最终离线总回归 14/14 通过，在原 13 项基础上纳入并明确 `email_lock integration static`；auth 模块 `go test` 与 `go vet` 均通过。固定副作用边界为 `external_access=false`、`database_access=false`、`redis_access=false`、`migration_executed=false`。该批次发生的 metadata 失败、cleanup 未启动是历史执行记录，已被后续新专项流程的 metadata+cleanup 严格成功结果取代；独立 postcheck 仍失败且真实根因未知，新 Redis 周期仍未通过。000055/000056 runner 均保持默认关闭，本轮没有连接真实 MySQL，partial 基础 runner 均为 `not_implemented`。这些离线结果只关闭本地资产缺陷，不构成真实 MySQL、运行时同窗扫描或 Phase 4 QA/PM 签署依据。

000055/000056 的基础 runner 继续如实保留 `partial_fault_injection=not_implemented`，但已有相互独立的 partial 编排资产补充离线可执行性：000055 固定 Up 16 点、Down 15 点及两条无注入基线，共 33 个目标，partial runner SHA 为 `8B15531A...99589`、boundary SHA 为 `4B5E02DC...18585`，27 个攻击模型通过，QA 复核 P1/P2=0；000056 固定 Up 27 点、Down 14 点及两条无注入基线，共 43 个目标，partial runner SHA 为 `198B9693E6D65C09DA964425144A4A55D1DB7CF230F597F7CBA190815E3F1CEE`、boundary SHA 为 `7B9E3132B2A09D939FD81E908C889EE6EE41A69B5D680B52A081D5A0A9BA4A62`、contract SHA 为 `08069BA6C05B77B92648AC27C0257E6A7DE6697858476821B22DF6C157E3C70C`，`attack_cases=32`，终审 QA P1/P2=0，文档证据漂移已关闭。以上只说明独立资产具备可执行编排，不代表 partial 已由真实 MySQL 执行；本轮仍未连接真实 MySQL，两个 migration 的 partial 门禁均不得改记为已验收，Phase 4 继续保持未通过。

本地隔离矩阵打包资产已形成离线契约：manifest SHA 为 `231B37EA...D004`，PowerShell 打包器 SHA 为 `F1DBC34E...D5A3`，contract SHA 为 `CA2E2E6C...AB60`。manifest 固定 20 项，其中 14 项为仓库内部资产、6 项为外部基线占位；预期 tar 内容固定为 15 项。SelfTest、PowerShell AST、默认关闭、normal 与 Python `-O` 均通过，`attack_cases=13`、`output_preservation_cases=4`、`symlink_checks=true`，QA 复核 P1/P2=0。此前 P1“预存输出被删除”已通过 `CreateNew` 与 owned flags 修复：仅本轮创建并明确归属的输出才允许在失败路径处理，既有输出不得删除或覆盖。本轮未生成持久包、未上传、未外连、未访问数据库且未执行 migration；该资产只证明本地可重复打包契约，不是隔离 MySQL 执行证据，真实 MySQL 仍未执行，Phase 4 继续保持未通过。

在后续新专项流程之前，一次只读 recovery preflight diagnostic 的本地 SelfTest 为 `cases=34`，远程严格单次 SSH 成功且未重试。脱敏结果重新确认 `schema=57`、`dirty=0`、`migration_rows=1`，两条隔离发送日志、一条测试白名单和一条测试模板均存在，且全部字段归属与摘要匹配；固定副作用摘要为 `writes=false`、`backup=false`、`cleanup=false`、`restarts=false`、`retries=0`。这证明前次 `metadata_exit_nonzero` 没有证明历史夹具状态失效；该时点原 cleanup 授权已随失败执行终止且不得重试，cleanup 与 postcheck 尚未完成。当前状态以其后的新专项流程结果为准。

新专项流程前的 Redis unknown cleanup 历史收口为：修复前正式流程唯一一次执行在 `metadata_exit_nonzero` 安全停止，`cleanup_started=false`、`postcheck_started=false`，没有执行真实清理。随后仅执行一次远端只读诊断并 PASS，固定确认 `mysql_identity_count=1`、`mysql_compose_label_count=0`，其余 state、recovery、binary、cycle、snapshot 门禁全部通过。根因确定为正式 metadata 错误依赖测试 MySQL 容器的 Compose label，而当前唯一目标容器没有该 label；这不是夹具归属、恢复点或数据库状态失效。运维已把识别规则修复为以 `ID|Image|Name` 三元组唯一确定目标，修复后脚本 SHA 为 `A9DC...E073`；本地 PowerShell AST 错误数 0、SelfTest 33 项、LocalPreflight 9 项、两个 payload 的 `bash -n` 均通过，QA 复核 P1/P2=0。以上在该时点只关闭 metadata 编排资产缺陷，尚未执行真实 cleanup；当前状态以紧随其后的新专项流程结果为准。

上述记录已由后续新专项流程部分取代：metadata 与 cleanup 的严格成功摘要均通过，历史夹具两条日志、一条白名单和一条模板已精确删除，状态文件已移除，恢复点与两套 000057 周期资产保留，Redis 键未执行删除。cleanup 后独立只读 postcheck 已启动但失败，外层包装只保留 `stage=postcheck classification=postcheck_failed`，且没有重试。纯本地诊断确定分类传播缺陷位于外层 postcheck `catch`：它无条件覆盖了子 runner 已产生的白名单分类；最小修复改为仅接受退出码 2、子 stderr 为空、固定 JSON 结构以及固定分类/stage 白名单，其他输出继续失败关闭。授权流程 SelfTest 36/36、postcheck SelfTest 56/56、LocalPreflight 9/9、PowerShell AST、payload `bash -n` 和 `git diff --check` 均通过，本次诊断未联网、未连接数据库或 Redis。由于失败时原始 stdout/stderr 已清理，上次 postcheck 的真实失败根因仍无法确定；历史 cleanup 可记为通过，但独立 postcheck、全新 Redis unknown 周期及 Phase 4 仍未通过。

2026-07-31 用户再次授权后，postcheck-only 正式入口严格执行一次，但 Windows PowerShell 5.1 将空数组折叠，导致流程在 SSH 前以 `local_gate_failed` 失败；固定计数为 `metadata_ssh=0`、`postcheck=0`、`retries=0`，未访问远端，也未执行 metadata、cleanup 或真实 postcheck。该次授权已消费且不得复用。根因已通过共享 `Initialize-RunFiles` 修复，新 runner SHA 为 `9F524238...BE29`；本地自检 24/24、预检 3/3，QA 复核 P1/P2=0。以上只关闭本地入口缺陷，真实 postcheck 仍未执行，必须取得新的单次只读授权后才能再次执行 postcheck-only；历史 cleanup 继续保持通过且严禁再次执行。

随后取得的新授权下，第二次 postcheck-only 正式执行固定计数为 `metadata_ssh=1`、`postcheck_child=1`、`retries=0`，结果为 `postcheck_failed`，cleanup 未调用。纯本地复核确认根因为 PowerShell `-File` 将两个数组参数按 `hash1 hash2` 位置展开，触发 `PositionalParameterNotFound`；现已改为两个命名 scalar 参数。相关三个 SelfTest 分别为 56/56、25/25、37/37，preflight 分别为 3/3、9/9，QA 复核 P1/P2=0；postcheck-only 新 runner SHA 为 `5E69D2E6...5C85`。本次授权已消费且不得复用；真实 postcheck 虽已启动但仍未完成，必须取得新的单次只读授权后再执行，历史 cleanup 保持通过且严禁再次调用。

第三次 postcheck-only 在新授权下执行，固定计数为 `metadata_ssh=1`、`postcheck=1`、`retries=0`、`cleanup=0`，精确失败分类为 `recovery_gate`。静态差异确认 metadata 之前漏检 `/home/pc` 与 `/home/pc/molin` 父链的 owner、符号链接及 group/other writable 条件；现已把同一门禁前移对齐，未放宽任何安全条件。SelfTest 29/29、Preflight 3/3，QA 复核 P1/P2=0，新 runner SHA 为 `BE0217D5...17C9`。当前仍不知道远端具体哪一级父链不满足；下一步必须先取得新授权执行一次只读父链诊断，不得直接重试 postcheck。诊断如指向权限或属主修复，任何 `chmod`/`chown` 均须另行明确授权；历史 cleanup 保持通过且严禁再次调用。

后续修复 recovery trailer parser 后，identity diagnostic 严格执行一次并返回 `parser_pass=true`、`classification=pass`、`candidate_unique=true`、`file_identity=true`；随后独立 postcheck-only 严格执行一次并返回 `status=pass`、`stage=complete`、`metadata_ssh_attempts=1`、`postcheck_calls=1`、`retries=0`。正式 parser 新增 variable-width、2..8 个空格及数值范围严格白名单，未放宽身份或父链门禁；本地测试为 postcheck 58/58、postcheck-only 29/29、identity 70/70，QA P1=0、P2=0。历史 cleanup 精确删除 2 条日志、1 条白名单和 1 条模板的结果已核验，继续保持通过且不得重跑。该 PASS 只关闭 identity diagnostic 与独立 postcheck，不等于新 Redis `phase1→重启→phase2` 周期通过，也不改变 `accepted` 仅表示供应商明确受理、不等于人工收件或最终送达的语义；RAM 有效权限证据与最终 QA/PM 门禁仍未完成，Phase 4 不得记为完成。

最新前端复验固定在分支 `feature/aliyun-email-template-management`、HEAD `87161414...`。管理端 `type-check`、`lint`、`build` 均 PASS，契约测试为邮件 11、管理员 MFA 7、outbound 4，合计 22/22；用户端 `type-check`、`lint`、`build` 均 PASS，邮箱 OTP 契约测试 15/15。构建仅输出既有 Vite chunk、dynamic-import 和模块类型 warning，没有错误。随后通过只读 `git ls-remote --refs origin refs/heads/main` 精确确认远端 `main=288599f0...`，并与邮件分支 merge-base 完全一致，不存在已进入远端 main 但前端未对接的 delta；本地 `main=608172e...` 仅为未推送的聊天布局提交，其三个文件与邮件分支提交增量无路径或语义重叠。测试工程师据此书面确认当前邮件前端范围 DoD 关卡 0–3 全部通过，`P0=0`、`P1=0`、`P2=0`；产品经理随后正式签署关卡 4，通过措辞限定为“截至 commit `87161414...` 的邮件前端范围完成”。该签署复用 §17.2.1 已关闭的真实四角色三宽度、受控 503 三宽度、零写请求和安全预览证据，无需重复执行已关闭子门禁；它不代表 DirectMail Phase 4 总门禁、后端、Redis、RAM、真实邮件全链路或生产上线通过。

| QA/PM 签署 | 部分通过 | 当前邮件前端范围 DoD 关卡 0–4 已由 QA/PM 书面通过，P0/P1/P2 均为 0；Phase 4 总门禁仍有后端与外部环境缺口 | 其余门禁全部关闭后，测试工程师仍须出具 Phase 4 总报告，并由产品经理完成整体业务确认，之后才能进入 Phase 5 |

历史推荐顺序（已被后续证据部分取代）：保持两个 000057 新隔离库及证据冻结且排除在 Redis unknown 的全部目标之外 → 启用受控 Redis/数据库环境完成剩余显式 SKIP 集成项 → 五场景真实重放/过期 → 模板测试发送与 unknown 注入 → RAM Deny → Redis 重启与数据库墓碑阻断 → 同一时间窗敏感扫描与真实角色三宽度 E2E → QA/PM 签署。

2026-07-31 本次安全续跑的 QA/PM 状态审计为：RAM 两个真实只读 Action PASS，但有效权限、角色信任链、最近尝试、Create/Modify/Delete 和显式 Deny 仍缺权限审计或既有 RequestId 诊断；Redis 唯一身份 PASS，但新 phase1 数据库夹具写入和真实重启没有授权；000055/000056 的 basic/partial 和打包契约均 PASS，但四个正式资产目录不存在且六项受控基线未交付；Python 业务矩阵 normal/`-O` 9/9、当前 Go `TestPhase4*` 8/8 PASS，但没有形成真实重放、过期、并发、unknown、冷却或五业务流 E2E。最新静态敏感扫描覆盖 1083 个文本文件，结果 `FAIL=0`、`REVIEW=3`、`INFO=265`、`protected_env=0`、`read_errors=0`；三项 REVIEW 仍为既有手机号示例和受门禁约束的调试回码表面。临时 RAM/Go 构建源码、二进制、远端目录和本地归档全部回收。基于以上缺口，QA 不能出具 Phase 4 总通过报告，PM 也不能签署整体通过；状态必须保持未通过。

历史机器快照（已被 §17.2.6 取代）：为防止把局部 PASS 误扩展为整体通过，当时 `tests/email/phase4_remaining_gates.json` 固定为 13 个已关闭、6 个未关闭，000055/000056 尚待真实验收。同期 Redis unknown fresh cycle 已以冻结 ELF SHA/大小和冻结 payload 完整通过：手工原生 `scp.exe -O --` 上传成功并由 Linux 端复核；phase1 tombstone 创建且 Adapter 调用1次；唯一 `molin-redis` 重启后 run_id 改变；phase2 旧/新 key 均阻断且 Adapter 增量0；cleanup 后数据库行0、state和ELF不存在；finalize API health/ready 通过，payload与空 Stage 精确移除。Redis 门禁已标记 `must_not_repeat=true`。

历史最短剩余顺序（已全部处置，不得据此重跑）：当时计划依次完成 Redis fresh cycle、RAM 证据、000055/000056 隔离矩阵、真实外发矩阵和 QA/PM。最终 Redis 与 migration 已真实关闭，RAM 及三项真实外发相关门禁均明确豁免且未技术验证，QA/PM 已依次附负责人豁免签署。供应商 `accepted` 始终不等于人工收件或最终送达；后续动作属于 Phase 5，必须另行审批。

fresh cycle 的 `upload_binary` 失败后，已离线冻结专用纯只读诊断器。它只允许一次 SSH，并把现场分类为唯一空 Stage、固定名称的部分二进制或 SHA 匹配的完整二进制；同时复核 Stage inode、属主/权限、远端 SCP 工具和容量分类。首次授权启动因控制器缺少 UTF-8 BOM，被 PowerShell 5.1 将唯一 SSH 赋值语句并入中文注释，随后在 `$result` 门禁失败；AST 证明 SSH=0，远端现场未读取、未改变。修复后控制器使用 BOM，并由 PowerShell 5.1 AST SelfTest 固定正式分支恰有一次 SSH；normal/`-O` 21 个攻击模型通过。首次失败现场的正式只读诊断返回 `upload_failure_stage_empty`，随后独立空 Stage 清理资产按新授权成功执行一次精确 `rmdir`。最新 fresh-cycle 授权再次停在 `upload_binary`，需在新授权下重新运行纯只读诊断器，不能沿用旧结论或自动清理。

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

### 17.2.4 2026-07-31 全新 Redis unknown 周期授权执行记录

本次单次专项授权在远端源码快照 SHA-256 前置门禁处失败。失败原因是 PowerShell 到 SSH 的 `awk` 引号包装被截断；后续只读诊断确认已上传归档的 SHA-256 与本地一致，且远端测试二进制不存在、源码未解压。按照“任一安全门禁失败立即停止且不重试”的授权约束，本轮未执行远端构建、`phase1`、Redis 重启、`phase2`、夹具清理或 000055/000056 migration，也没有发送真实邮件。已按本轮记录的唯一 stage 精确删除远端暂存目录和本地归档，本次授权随失败执行消耗，不得复用。

离线修复补充了 `phase2_verified` 成功周期的独立 `cleanup_verified` 路径。该路径只接受完整冻结状态、三个正数精确主键和 `unexpected_send_log_id=0`，在事务内锁定并核验一条发送日志、一条白名单和一条模板，逐项要求精确删除一行；Redis 只执行精确 `EXISTS` 前后验，不执行删除。原历史双日志 `cleanup` 路径保持独立，仍拒绝成功周期状态。Go 服务测试、vet、原重启契约 normal/`-O` 均通过。

为下一次单次授权准备的 `email-unknown-fresh-cycle.payload.sh`、`run-email-unknown-fresh-cycle.ps1` 和 Python 契约固定执行 `preflight -> phase1 -> restart -> phase2 -> cleanup_verified -> finalize`，只允许重启唯一 `molin-redis`，不包含整库备份、主库 migration、Redis 删除、扫描或通配清理。legacy `scp -O` 上传连续两次在远端零字节落地前失败后，控制器仅把上传层替换为 OpenSSH 9 默认 SFTP，并更换为 SFTP 专用确认词；两次 SSH、两次传输、零重试及全部业务门禁保持不变。Bash、PowerShell SelfTest 及 normal/`-O` 36 个攻击变异已通过。以上仍只构成本地准备证据，不构成新 Redis 周期或 Phase 4 通过。

本轮最终全工作树敏感扫描覆盖 1223 个文本文件，固定结果为 `FAIL=0`、`REVIEW=2`、`INFO=290`、`protected_env=0`、`read_errors=0`。本轮移除了手机号脱敏注释中的完整号码字面量，并禁止预付费示例客户端把非 JSON 上游正文带入异常；剩余两项 REVIEW 为经 URL 校验的公开应用入口，以及仅在进程内短暂流转且由安全非生产环境双门禁控制响应的验证码载体。新增 Redis fresh-cycle、RAM 验收、migration 全隔离矩阵及 upload-failure 只读诊断资产未形成新的敏感命中。同期 `go test ./...` 与 `go vet ./...` 全部通过。该扫描仍只证明本地静态文本，不替代已经独立形成的运行时六表面证据，也不关闭其他 Phase 4 门禁。

同轮重新验证 000055/000056 隔离矩阵资产：打包器 SelfTest 固定为 20 项清单、4 个 runner、4 个 contract、2 个 partial boundary、4 个 migration 和 6 个外部基线占位，normal/`-O` 13 个打包攻击模型均通过；000055 完整与 partial 契约分别通过 17/27 个攻击模型，000056 完整与 partial 契约分别通过 20/32 个攻击模型。000056 basic 与 partial 现统一消费 schema55/schema56 两行 manifest，并逐项绑定两份文件 SHA，原同名 manifest 一行/两行互斥阻断已经关闭。全部摘要均固定声明 `database_access=false`、`migration_executed=false`，打包器同时声明 `package_created=false`。因此这里只证明本地编排资产完整，六个受控外部基线和真实 MySQL 8 完整/partial/down 执行仍未形成，必须在新的单次授权下使用独立临时容器完成。

六项外部基线使用默认关闭的生成入口 `generate-email-migration-baselines.sh`。它冻结 000001→000056 的 56 个 Up migration 集合 SHA，只接受本地已有、digest 与镜像 ID 双绑定的 MySQL 8 镜像，并在容器内实际核验 MySQL 主版本为 8；禁止 pull，临时容器固定 `network=none`、不发布端口、只读根文件系统。输出依次为 schema54 empty/legacy、schema55、schema56 与两份 manifest，发布使用预存空 700 目录和 noclobber，失败只清理本轮固定文件及精确容器 ID。当前生成器/契约 SHA 分别为 `FBAD0D66...EB6F0`、`E967BE59...6B011`，normal/`-O` 20 个攻击模型通过。当前 Windows 会话没有 Docker CLI，未执行 Docker/MySQL，也没有生成六项真实基线。migration 门禁保持 `baseline_generation_and_authorization_required`。

后续取得新的单次专项授权后，Redis 全新周期严格执行一次。远端唯一 stage 创建成功、二进制与 payload 上传 SHA-256 复核成功，`preflight` 固定确认 API ready、schema57、唯一 Redis 身份且无写入；随后在调用 `phase1` 时外层收到 `remote_gate_failed` 并立即停止，固定为 `retained=true`、`retries=0`。由于没有取得 `phase1` 成功摘要，流程未进入 Redis restart、phase2 或 cleanup_verified；000055/000056 migration 和 RAM 只读步骤也未启动。本轮没有真实邮件授权，测试进程仍固定 Mock Adapter。保留 stage 中是否存在部分夹具或状态文件当前未知；按照授权第 11 条，本轮未继续诊断或清理，单次授权已消耗。下一次授权必须先只读定位唯一合规 stage、冻结其状态并按精确主键恢复，不得直接开始新周期。

随后取得失败现场诊断与条件清理授权后，新的恢复入口仅启动一次只读 `preflight`，但外层收到通用 `remote_gate_failed` 并立即停止，`operation_id=unknown`、`retained=true`、`retries=0`。控制器没有取得 preflight PASS，因此未上传恢复二进制、未执行 chmod、数据库 cleanup、Redis 操作或 stage 删除。纯本地复核确认恢复 payload 的失败输出本身是固定脱敏 stage 分类，但 PowerShell 进程包装在非零退出码处无条件折叠了 stdout；后续修复只允许退出码 2、stderr 为空且整行命中固定 stage 白名单时传播 `remote_stage`，其他输出继续关闭。本轮授权已消耗，远端失败现场保持未诊断和未清理。

2026-08-01 在新的单次恢复授权下，修复后的 controller 再次只执行一次 recovery `preflight`，仍返回 `remote_stage=unknown` 并失败关闭。由于没有取得 `state_class=complete`、`phase=phase1_created` 及精确行数 PASS，未上传恢复二进制，未执行 `cleanup_phase1`、Redis 操作或 stage 删除；授权随本次执行消耗，不得自动重试。

本地源码审计确认首次 fresh-cycle phase1 的结构性原因是 controller nonce 与 Go phase1 脱绑定。修复后，Go phase1 在任何状态或夹具写入前强制读取并校验 `EMAIL_UNKNOWN_RESTART_NONCE`，fresh payload 显式绑定 controller nonce。后续又关闭 allowlist 表名、MySQL 默认配置、state symlink/硬链接和控制器哈希冻结问题；当前 payload SHA-256 为 `29EAA0B18959D9ABCCDCF10D3793AA6A0C8574B85028714AB7D6EB4E429DEF54`，Linux amd64 ELF SHA-256 为 `1179E29D9F43EFEA79F185E8D2319D015A627F69A48EF9ED7CE22E72BA6AD900`、大小 `25573597`。旧远端 Stage 已另行精确清理；当前未关闭的是新 fresh cycle 本身，Phase 4 继续未通过。

随后取得“保留 stage 只读诊断”单次授权。执行前新增独立纯只读载荷、单次 SSH 控制器和变异契约；载荷 SHA-256 为 `8A99E4B52C1B32413B2BAC59C4F3DAC169E37E482145538FB3AC62307644DDF5`，Bash 语法、PowerShell 离线 SelfTest、Python normal/`-O` 19 个攻击模型及副作用静态扫描均通过。正式命令只启动一次，未配置 SCP、远端临时文件、cleanup、数据库写入、Redis 删除/扫描/重启、migration、RAM 或真实发信；但控制器在 SSH 返回后的本地汇总阶段因严格模式下 `$result` 未赋值而退出，本地 `finally` 已删除 stdout/stderr 捕获文件，无法恢复或伪造远端摘要。授权随本次执行消耗且没有重试；原 stage 与夹具仍按失败关闭原则视为保留，`retained_stage_reconciled` 未通过。

本地静态审计另发现旧 recovery preflight 使用复数表名 `email_test_recipient_allowlists`，而模型和 000055 migration 的真实表名均为单数 `email_test_recipient_allowlist`；该错误可使 MySQL 命令在固定失败摘要前退出，是此前 `remote_stage=unknown` 的明确本地缺陷候选，但由于本次远端摘要不可恢复，不登记为远端已确认根因。独立只读诊断控制器已改为先固化捕获对象、完成精确本地临时文件清理后再返回，并新增缺失结果门禁与脱敏失败摘要回归；修复后 AST 错误数 0、SelfTest 及 normal/`-O` 19 个攻击模型通过。正式 recovery 资产也已同步修复为单数表名、`mysql --no-defaults` 和四个显式查询失败分类；固定失败摘要现在优先于 stderr 折叠，原始 stderr 仍不输出。其 payload、controller、contract SHA-256 分别为 `B22B4EFF...FB916`、`2777E041...A0DC`、`783F6A4C...D3D03`，Bash 语法、PowerShell AST/SelfTest 及 normal/`-O` 23 个攻击模型通过。最终仓库静态扫描覆盖 1096 个文本文件，结果 `FAIL=0`、`REVIEW=3`、`INFO=267`、`protected_env=0`、`read_errors=0`，三项 REVIEW 仍为既有一处测试手机号和两类受门禁约束的调试回码表面。再次读取远端 stage 仍必须取得新的单次只读授权。

第二次取得同范围“保留 stage 纯只读诊断”单次授权后，冻结载荷再次通过 SHA、AST、SelfTest、normal/`-O` 19 个攻击模型、单 SSH 计数和副作用扫描，随后正式命令只启动一次且未重试。SSH 返回后，控制器在构造本地捕获对象时因空 `PSObject` 被绑定为 null 而失败；`finally` 已删除本地 stdout/stderr 文件，因此仍没有可恢复的远端摘要。未调用 SCP、cleanup、数据库写入、Redis 删除/扫描/重启、migration、RAM 或真实发信；原 stage 与夹具继续按失败关闭视为保留，`retained_stage_reconciled` 仍未通过，本次授权已消耗。

纯本地动态复现确认该故障属于 Windows PowerShell 5.1 捕获对象构造，而不是远端业务门禁。控制器现通过初始化后直接构造带四字段的非空 `PSCustomObject`，并新增 factory null、返回类型、字段值和脱敏摘要动态回归；修复后的 controller/contract SHA-256 分别为 `45711793...8E13B`、`6FCC290D...65F3E`，AST/SelfTest 与 normal/`-O` 22 个攻击模型通过，未再次联网。再次读取远端 stage 仍需要新的单次只读授权。

继续执行完整本地子进程链路后确认，上述对象修复仍不足以覆盖 Windows PowerShell 5.1：无 BOM 的 UTF-8 控制器包含中文注释时会按系统代码页解析，部分换行被错误并入注释，导致等待、退出码读取或结果赋值不进入 AST；早期本地夹具首行 `cat >/dev/null` 还会消费同一 stdin 中后续的摘要和 `exit 2`。控制器现固定为 UTF-8 BOM，参数改为逐项无空白数组，stdin 使用本轮唯一受限临时文件交给 `Start-Process`，并在 `finally` 精确删除；SelfTest 使用真实 Git Bash 验证 `exit 2`、固定 stdout、空 stderr、白名单分类及临时目录无残留。最终 payload/controller/contract SHA-256 分别为 `8A99E4B52C1B32413B2BAC59C4F3DAC169E37E482145538FB3AC62307644DDF5`、`778F364910FD493C58E5BC5B7AEE3CC66CF5FD95C9FC525A5CD4F9E7F815A495`、`3852942071DEF7CAD26AB5611B3726DDEC736A78DE46C4AB0E1AF1154AA6637C`；Windows PowerShell 5.1 AST=0、完整 SelfTest、Bash 语法及 Python normal/`-O` 26 个攻击模型均通过。该修复与验证完全离线，不产生新的远端结论；两次正式授权仍已消费，原 stage 与夹具继续保留，Redis 状态保持 `recovery_diagnostic_reauthorization_required`。

同日继续完成剩余执行资产的纯离线复核：RAM 有效权限证据契约 normal/`-O` 各拒绝 15 个攻击模型；000055/000056 static、full、partial 与原隔离包契约继续通过。基线生成器新增容器内 MySQL 8 版本门禁后，normal/`-O` 20 个攻击模型通过，生成器 SHA 为 `FBAD0D66...EB6F0`。新增单次全隔离矩阵编排器，冻结 66 个源文件并固定两次 SSH、一次 SCP、两个顺序临时无网络 MySQL 8 容器和 94 个隔离目标；payload/controller/contract SHA 为 `4FC51DB9...FA401`、`1E002BAA...76A91`、`7769FD36...CE658`，normal/`-O` 32 个攻击模型、PS5 SelfTest 和默认关闭通过。当前 Windows 环境没有 Docker CLI，本轮没有联网、没有生成基线、没有执行 migration，也没有连接或修改测试主库；两项 migration 门禁继续保持 `baseline_generation_and_authorization_required`。RAM 解析器通过只证明证据结构可验，尚无云侧有效策略、信任链和近期审计清单，`ram_effective_permissions` 仍为 `external_evidence_required`。

第三次取得 Redis unknown 保留 stage 纯只读诊断单次授权后，执行前 payload/controller/contract 三项冻结 SHA、PS5 完整 SelfTest、Bash 语法及 normal/`-O` 26 个攻击模型全部通过。正式路径只执行一次 SSH，结果为 `diagnostic_complete`、`ssh_attempts=1`、`retries=0`、`stderr_length=0`、`writes=false`、`cleanup=false`、`restart=false`、`remote_artifact=false`。现场固定证明唯一 stage、3 个固定文件、文件身份和 SHA 均通过，state 为 `complete/phase1_created`；schema 为 57/dirty0，migration/operator/单数表计数均为 1、复数表为 0，template/allowlist/send_log/scope 均为精确 1，Redis PING 与唯一身份通过且派生精确 key `EXISTS=0`。但 `stage_nonce_match=false`，说明 state 及其完整业务夹具虽彼此一致，却未绑定当前 stage 名称；因此不得登记 `retained_stage_reconciled`，更不得直接 cleanup 或新建 phase1。本次授权已消费且没有第二次连接。

本地随即把旧 recovery 资产失败关闭：payload 读取 state 时必须同时接收 stage operation ID 并强制 nonce 等值；旧 controller 正式模式在确认词、SSH、SCP 和 cleanup 之前固定抛出 `recovery_asset_disabled_stage_nonce_mismatch`，SelfTest 仍保持离线。修复后的 payload/controller/contract SHA-256 为 `36853BAE63A78C3F4B9A869BCB6BFF9B184A6258A06F3DC7B5EDE62E8BAC6172`、`C83BA172655B232FB3B703BFABF39BFB84084F51E8046A0A77FB4410B89B243F`、`520643CAFF855FEF53C47FAFC81F0C8A96758BB87FCC9A124CC24BA315C6C774`；PS5 SelfTest、默认禁用、Bash 语法及 normal/`-O` 25 个攻击模型通过。后续必须取得明确接受 nonce mismatch 的专项恢复授权，且只能使用新建并独立验证的恢复资产。

旧资产禁用后已完成独立 nonce mismatch 精确恢复资产。新 payload 只接受唯一 `0700` stage、三个旧固定文件、严格 `0600` 完整 state、`phase1_created`、state nonce 与目录 nonce 明确不等、三条由 state nonce 派生且字段完整匹配的数据库夹具、唯一 scope、Redis 未重启及派生精确 key `EXISTS=0`；state 使用 `O_NOFOLLOW + fstat` 读取。正式顺序固定为一次只读 preflight、一次 SCP 上传冻结 ELF、一次 cleanup SSH，所有第二轮归属门禁均在首次 `chmod` 前完成。Go `cleanup_phase1` 才能事务内精确删除三条记录并移除 state，Redis 不执行删除，最后只删除三个固定二进制/payload 和空 stage。控制器不转发包含标识符的远端原始摘要，仅输出布尔值和 `ssh_attempts=2/scp_attempts=1/retries=0`；冻结 Linux amd64 ELF SHA-256 为 `1179E29D9F43EFEA79F185E8D2319D015A627F69A48EF9ED7CE22E72BA6AD900`、大小 `25573597` 字节。payload/controller/contract SHA-256 分别为 `12B57C09DDD14333ECA4B159D09DEA2E7BD9974170B9188BC437ACE3F2ACEC63`、`8461938874417E2D9D143D08C187C2AFFA388ACFB09D90F0BD399558CD5B02F6`、`3BA1C76D0D678DC0970343192621EE05F0F2D8F4FCAB3AAE1C636CDC05DC534B`；PS5 AST/SelfTest、Bash 语法、默认关闭、normal/`-O` 31 个攻击模型、目标 Go 测试和 ELF 身份检查均通过。最新全工作树静态扫描为 `files=1096`、`FAIL=0`、`REVIEW=3`、`INFO=268`、`protected_env=0`、`read_errors=0`，三项 REVIEW 仍为既有测试手机号和受门禁约束的调试回码表面。本轮完全离线，未连接远端、未上传、未清理；机器状态继续保持 `stage_nonce_mismatch_recovery_authorization_required`。

随后取得 nonce mismatch 精确恢复单次授权。执行前重新确认三项冻结 SHA、ELF SHA/大小/魔数、PS5 SelfTest 和 normal/`-O` 31 个攻击模型全部通过；正式控制器只启动一次。第一次 preflight SSH 的远端退出码为 0，但 stderr 非空，旧控制器因此在固定门禁处立即失败，脱敏摘要为 `stage=preflight`、`remote_stage=unknown`、`retained=true`、`retries=0`。控制流没有进入 SCP，因而没有上传、`chmod`、Go cleanup、数据库删除、Redis 操作或 stage 删除；本地临时 stdout/stderr 已在 `finally` 精确删除，原文不可恢复且未输出。授权随本次失败消耗，禁止重试。控制器随后仅做离线增强：失败摘要现报告实际 SSH/SCP 次数、退出码及 stdout/stderr 长度，并把该路径固定分类为 `stderr_nonempty`，仍不输出原文或任何标识符；新 controller/contract SHA-256 为 `A2121034FBB5A104AF9CE82A5353911AAAAD1DF47C608CFF1A06C7DCC5F3CCB7`、`FF5D605A8CEAEEDCAC6F3475C63E81E829CE2F16E9FDABDEF7BB387C1D5A8D20`，normal/`-O` 32 个攻击模型与 PS5 SelfTest 通过。机器状态更新为 `stage_nonce_mismatch_recovery_preflight_stderr_reauthorization_required`。

为避免下一次只读诊断意外进入上传，控制器又新增独立 `-PreflightOnly` 模式和不同确认词：该模式不要求恢复 ELF，仅允许一次 SSH，preflight 成功或失败后都不会进入 SCP/cleanup，摘要仍只含固定分类、退出码、stdout/stderr 长度、布尔值和次数。最终 payload/controller/contract SHA-256 为 `12B57C09DDD14333ECA4B159D09DEA2E7BD9974170B9188BC437ACE3F2ACEC63`、`59F927FD39E7923CDA352340020347053A922B739D4C9CD7AE65A1343FDA96EA`、`939122A14B0D12362132563148069A25F1DAB0F2744B667FD7D2CFD2D8D1251F`；PS5 SelfTest、preflight 默认关闭以及 normal/`-O` 34 个攻击模型通过。该改动完全离线，尚未获得新的诊断授权。

继续静态对比发现：此前成功的只读诊断载荷在入口执行 `exec 2>/dev/null`，失败的 recovery payload 未关闭远端 stderr，且 preflight 的精确 Redis `EXISTS` 是唯一未单独重定向 stderr 的容器调用。这是可复现的源码差异，但在未取得新只读证据前只登记为候选原因。恢复 payload 现统一禁止远端 stderr 进入传输层，并把 Redis run-id/EXISTS 命令失败显式折叠为 `redis_identity`/`redis_exact_exists`，没有放宽退出码、归属或精确 key 门禁。最终 payload/controller/contract SHA-256 为 `76430E008CEB5AF3E5457ECD6CF863FA2BADCC24D9908642C1B5C2C6CEA41D60`、`C0EEE96D3E4AB5C8D8D76BF38C3FAA89298AC1B57D33B0575F719C5F54169CEA`、`6BD2DAA67C50C6D4739714B37F9A9E47D93839EA8E65996D95FFDFEF27DB305F`；PS5 SelfTest、Bash 语法和 normal/`-O` 36 个攻击模型通过，未再次联网。

随后获得 `-PreflightOnly` 单次只读授权。正式执行严格使用一次 SSH、零 SCP、零重试，固定结果为 `preflight=true`、`state_class=complete`、`state_phase=phase1_created`、`stage_nonce_match=false`、`fixture_ownership=true`、`redis_identity=true`、`redis_key_exists=0`、`exit_code=0`、`stderr_length=0`、`retained=true`、`writes=false`。没有上传、`chmod`、cleanup、数据库写入、Redis 删除/扫描/重启或真实邮件。该结果确认 stderr 封闭修复有效并再次证明现场未漂移；只读授权已消费，精确 cleanup 仍需新的单次写授权。机器状态更新为 `stage_nonce_mismatch_recovery_cleanup_authorization_required`。

取得精确 cleanup 单次授权后，执行前再次通过 payload/controller/contract SHA、冻结 ELF SHA/大小/魔数、PS5 SelfTest 和 normal/`-O` 36 个攻击模型。正式路径的只读 preflight 通过，随后一次 SCP 成功；第二次 SSH 在 cleanup 的 `recovery_binary_identity` 固定门禁失败，摘要为 `exit_code=2`、`stdout_length=84`、`stderr_length=0`、`ssh_attempts=2`、`scp_attempts=1`、`retained=true`、`retries=0`。失败发生在任何 `chmod` 和 Go cleanup 之前，因此没有数据库删除、Redis 操作、state 或 stage 删除；上传 ELF 保留在四文件 Stage，授权已消费且没有重试。

为避免猜测上传文件模式，新增独立 `uploaded_preflight` 只读动作和 `-UploadedBinaryPreflightOnly` 专用控制器模式。它只接受四个固定文件，重新验证完整 state、夹具和 Redis 身份，并只输出 ELF 的 regular/symlink/owner 布尔值、`500/600/644/700/755/other` 模式分类及冻结 SHA 是否匹配；一次 SSH、零 SCP，任何结果都不能进入 cleanup。payload/controller/contract SHA-256 为 `B2DF03AEFFACE2343E2478295F7328C250C9F16E925103EDBA24DA094CB41F1D`、`9F50446CBAB9B2DC983F0516F14D989C3CDC7519C958C62B3322734C1DE7C65A`、`18890BF17789FC4A3A799DE282916ED8603A26453C82C6CEC9117A345D9E6D39`；PS5 SelfTest、默认关闭和 normal/`-O` 38 个攻击模型通过，尚未获得诊断授权。

随后获得并严格执行一次 `-UploadedBinaryPreflightOnly`：`binary_regular=true`、`binary_symlink=false`、`binary_owner=true`、`binary_hash_match=true`，权限模式分类为 `other`；完整 state、夹具、Redis 身份和精确 key 不存在再次通过，`exit_code=0`、stderr0、一次 SSH、零 SCP、零写入。该结果证明上传内容和归属没有问题，原身份门禁失败仅剩模式白名单不兼容，但具体原始模式仍未输出也不猜测。

恢复资产的 `-ResumeUploadedCleanup` 已按单次授权执行成功：第一次 SSH 的 uploaded preflight 证明四文件 Stage、完整 state、夹具归属、Redis 身份及 ELF 普通文件/非链接/属主/SHA；第二次 SSH 将无特殊位权限归一为 `0500` 并复核，随后执行一次 Go `cleanup_phase1`。三个精确主键删除后计数均为 0，state、三个固定测试资产和空 Stage 已删除，API health/ready 通过。最终回执为 `preflight=true binary_hash_match=true cleanup=true retained=false ssh_attempts=2 scp_attempts=0 retries=0`；该单次授权已经消耗。

下一轮全新 Redis 周期资产已完成 SFTP 上传层修复，尚未使用修复版执行文件上传。payload 仍固定单数表 `email_test_recipient_allowlist`、MySQL `--no-defaults`、state `O_NOFOLLOW`/`fstat`/单硬链接和 `0600` 门禁；冻结 Linux amd64 ELF 身份及唯一 Redis 重启边界不变。旧 legacy controller/contract SHA `AC4D207F...A647`、`37F1A2D6...F0B4` 保留为两次上传失败的历史证据；当前 payload/controller/contract SHA-256 分别为 `29EAA0B18959D9ABCCDCF10D3793AA6A0C8574B85028714AB7D6EB4E429DEF54`、`D756D4451D31FCF63CB61A56F37705E1A3E2096EA5A8F03F662993730EC99FB9`、`AE3EA5761A4E20988D2B76A6AD4CF29FDAF02915A0B224769CAB88391F29443F`。修复版 normal/`-O` 36 个攻击模型和 SelfTest 通过。项目负责人手工建立 SFTP 会话并成功取得远端工作目录，证明 SFTP 子系统可用；该会话未执行 `put`，不替代上传或 fresh cycle 证据。当前仍需先精确清理已证明为空的 Stage，再取得新的全新周期单次授权。

RAM 有效权限补充了不联网的脱敏证据验收入口 `directmail_ram_effective_evidence.py`。它要求同一身份、策略版本、部署 SHA、24 小时内证据窗、有效策略及全部附加/用户组来源、Deny 优先级、直接 RAM 用户或完整角色信任链、最近尝试审计和六个固定 Action 全部闭合；两个读 Action 只能是既有成功，`SingleSendMail` 只能引用历史 `accepted`，Create/Modify/Delete 必须分别由权限审计或既有 Troubleshoot 证明 `explicit_deny`。验证器拒绝 AccessKey、Secret、Token、RequestId、邮箱和供应商原文，normal/`-O` 15 个攻击模型通过。当前没有真实脱敏 RAM 清单，因此只完成验收通道，不关闭 `ram_effective_permissions`。

### 17.2.5 2026-08-02 migration 最终矩阵当前状态

当时状态：Redis unknown fresh cycle 已通过手工 legacy `scp.exe -O` 上传链路完成真实 `phase1 -> Redis 唯一重启 -> phase2 -> cleanup_verified`，数据库夹具和 Stage 均已精确清理；该证据标记为 `must_not_repeat=true`，后续不得再次重启 Redis。RAM 有效权限和五场景真实重放/过期已按项目负责人决定登记为 `waived_by_project_owner_not_verified`。当时真实邮件故障矩阵、五业务流 E2E、QA 报告和 PM 签署尚未关闭；前两项后来由 §17.2.6 的新负责人决定豁免，仍不得表述为技术验证通过。

000055/000056 已在独立 MySQL 8 容器真实生成六项基线，基线生成门禁不再是当前阻点。矩阵编排已证明临时容器使用 `network=none`、只读根文件系统和受控 tmpfs，四套资产使用 `Type=bind`、精确 Source/Destination 且 `RW=false`；主库 `molin-mysql` 未被访问或修改。真实执行目前停在 000055 的 schema55 Down，旧 runner 只报告 `schema55_down_sql/other`。本地修复现保持同一 MySQL 会话，在 24 条 Down 语句前插入固定只读标记，并把 CHECK、唯一键和外键错误归入 `constraint` 白名单；000055 runner 契约 normal/`python -O` 均通过 30 个攻击模型，完整 migration 契约集合 16 项以两种 Python 模式共执行 32 次通过，8 个 Bash 语法、4 个 PowerShell SelfTest 和 `git diff --check` 通过。

最新单次恢复及最终矩阵授权执行因正式命令遗漏 `-RecoverKnownFailure`，第一条 SSH 进入普通新建模式并在 `retained_stage_present` 只读门禁停止：`ssh_attempts=1`、`scp_attempts=0`、`retries=0`、stderr0。没有删除旧 Stage、创建新 Stage、上传文件、启动 Docker/MySQL、执行 migration、访问数据库或 Redis，也没有发送邮件。该授权已消费，远端失败现场保持不变。控制器已新增恢复专用确认词，恢复开关与普通矩阵确认词不再共用；下一次必须取得新的单次授权后显式使用 `-RecoverKnownFailure`，不得把本轮失败当作重试许可。

随后取得新的单次恢复授权并显式使用 `-RecoverKnownFailure` 与恢复专用确认词。正式流程严格使用两次 SSH、一次 legacy `scp.exe -O`、零重试：第一阶段完成已证明旧失败 Stage 的精确恢复并创建新 Stage，第二阶段上传当前冻结包并启动矩阵。最终在 `full_matrix/matrix55_execution` 失败关闭，`exit_code=2`、`stdout_length=147`、stderr0，新 Stage 按门禁保留。本轮没有 Redis、真实邮件、RAM、生产环境或主库操作，也没有追加第三次 SSH。语句级 runner 已把精确 `down_statement_01..24` 写入保留输出，但当前授权传输预算已耗尽；在新的单次纯只读授权读取白名单阶段前，不猜测具体失败语句，不清理 Stage，也不重跑矩阵。

后续单次纯只读诊断已严格使用一次 SSH、零 SCP、零写入、零 Docker/数据库/Redis 访问和零重试，精确结果为 `matrix55_failure=schema55_down_statement_05`、`matrix55_case=schema55`、`matrix55_target_created=true`、`matrix55_error=constraint`。该语句是 000055 Down 对四条权限中文元数据的精确计数断言。根因已定位为基线生成、`mysqldump` 导出/恢复和四套矩阵 runner 均未显式固定 MySQL 客户端字符集，可能使中文值在不同客户端链路发生不一致转码；业务 migration SQL 未作放宽或改写。

本地工具现统一为所有 `mysql` 与 `mysqldump` 调用显式指定 `--default-character-set=utf8mb4`，并补充删除字符集参数的攻击用例。000055/000056 full/partial、基线生成器、完整编排、手动远端、保留现场诊断和精确清理契约在普通及 `python -O` 模式全部通过；4 个 PowerShell SelfTest 和 9 个 Bash 语法门禁通过。清理器只新增接受已证明的 `schema55_down_statement_05/schema55/true/constraint` 组合。当前远端 Stage 仍原样保留，000055/000056 尚未技术通过；下一步需要新的单次授权执行精确恢复和最终隔离矩阵。

取得该单次授权后，正式流程严格使用 SSH 2、legacy `scp.exe -O` 1、零重试，并成功越过原 `schema55_down_statement_05`。本轮停止于 `full_matrix/matrix55_summary`，`exit_code=2`、stdout 长度 145、stderr0，Stage 保留且临时容器已按精确 ID 清理。源码对账确认 matrix55 runner 成功时先输出 7 条脱敏目标进度，再输出 13 条固定成功摘要；旧远端包装器却把整个 stdout 与仅 13 条摘要直接比较，因此成功执行必然被误判为 summary 不匹配。

修复后的包装器严格校验 7 条 case 的顺序、schema 版本、唯一 64 位目标哈希，再校验固定终止摘要；同一机制覆盖 partial55、matrix56 和 partial56，避免后续重复误判。完整编排契约现以 normal/`python -O` 通过 81 个静态攻击模型和 7 个真实 Bash stdout 场景；只读诊断与精确清理分别通过 16 和 22 个攻击模型。当前授权已经消费，远端 Stage 仍未读取；下一步只能先取得一次纯只读授权，确认分类为 `matrix55_success_summary_contract_mismatch_retained`，不能直接清理或重跑。

随后取得并严格执行一次纯只读授权：SSH 1、SCP 0、零重试，返回 `classification=matrix55_success_summary_contract_mismatch_retained`。Stage 顶层 6 项、source manifest、六项基线、四套资产、五个输出、基线摘要和两项基础 stderr 全部通过；matrix55 精确结果为 `summary_contract_mismatch/none/false/none`，证明 7 个有序唯一目标及完整成功摘要均正确。全程 `writes=false`、`database_access=false`、`docker_access=false`，现场保持不变。000055 尚不能关闭，因为 partial55、matrix56 和 partial56 仍需使用修复后的包装器取得同一轮最终成功回执。

取得后续精确恢复及最终矩阵授权后，控制器严格使用 SSH 2、legacy `scp.exe -O` 1、零重试；旧成功 Stage 被精确清理，新 Stage 和冻结包建立完成，matrix55 已被修复后的有序输出解析器接受。流程随后在 `full_matrix/partial55_execution` 失败关闭，stderr0、Stage 保留、临时容器不保留。该结果说明当前阻点已进入 000055 partial runner 内部，不能再归因于 matrix55 摘要。授权已消费，下一步必须先以纯只读方式读取 `partial55_failure/case/target_created/error`，禁止直接重跑。

随后单次纯只读诊断严格使用 SSH 1、SCP 0、零重试，确认 `partial55_failure=environment_precheck`、`partial55_case=none`、`partial55_target_created=false`、`partial55_error=invalid`；Stage 顶层 6 项、source manifest、六项基线、四套资产和当前 7 个输出均通过，matrix55 仍为完整成功证据。该结果证明 partial55 在创建任何目标库或执行 migration 前失败；现有 stderr 不是固定三行 MySQL 错误信封，因此不能将其解释为数据库端口或 SQL 失败。只读诊断资产已进一步增加固定 stderr 分类和 partial55 七项资产的名称、模式、属主及 SHA 绑定，payload SHA-256 为 `DAB28DABFCDC3AD35DC5CDE9D8933F418BCECFE97FB7958DC0D0B844F9E86C53`；Bash 语法、PowerShell SelfTest、Python normal/`-O` 30 个攻击模型及 8 个运行时解析场景通过。当前只允许再取得一次纯只读授权确认 `partial55_stderr_class` 与 `partial55_assets_verified`，禁止清理或重跑。

新增白名单只读分类已按授权严格执行一次，结果为 `classification=partial55_environment_precheck_classified_retained`、`partial55_stderr_class=other`、`partial55_assets_verified=true`；SSH 1、SCP 0，且未访问 Docker、数据库或 Redis。通过的 full runner 与失败的 partial runner 的环境预检静态差分显示，partial 唯一新增系统工具为 `/usr/bin/wc`；MySQL 官方镜像并未把该绝对路径列为镜像契约。000055 与 000056 partial runner 已移除该额外依赖，改用已通过共同预检的 awk 精确核验两行 baseline manifest，不放宽清单数量。新 runner SHA 分别为 `E9EC4C1F7EE742FDB918D9720C5A94AFCDC1772B03FEBA62C0A7C27983EBA9C9` 和 `1BDAF1453073F48098EE131FEB7B5711C8749987025A7C37C9C0AE544A43BFB1`；normal/`python -O` 分别通过 32/37 个攻击模型，完整编排 82 个攻击模型、PowerShell SelfTest、20 项隔离包 SelfTest 与 `git diff --check` 通过。远端 Stage 继续保留，授权已消费；下一步需要新的精确恢复及最终矩阵授权。

精确恢复清理器现只额外接受当前已证明的 7 输出现场：matrix55 必须为完整成功摘要，partial55 必须严格为四行 `environment_precheck/none/false`，stderr 必须保持 `other` 白名单分类且大小不超过 4096 字节，归属临时容器必须不存在。cleanup payload SHA 为 `5B0191E372FDAB3611879077FAA49D4FA64C49E81C94E44B25C47558AC910547`；normal/`python -O` 26 个攻击模型、13 个运行时解析场景及 PowerShell SelfTest 通过。完整控制器使用新的恢复确认词 `I_CONFIRM_EMAIL_MIGRATION_PARTIAL55_WC_RECOVERY_MATRIX_ONCE`，未取得新授权前不会执行。

上述 `wc` 根因判断已被后续真实执行推翻。获授权的单次恢复严格使用 SSH 2 次、`scp.exe -O` 1 次、零重试，旧 Stage 清理和新包执行均已进入正式路径，但仍以 `classification=partial55_execution`、退出码 2 失败；新 Stage 保留，临时容器已由失败路径精确移除。因此移除 `/usr/bin/wc` 仅保留为可移植性加固，不能再登记为根因修复。000055/000056 partial runner 已把原预检拆分为 `environment_identity`、`environment_hash_inputs`、`environment_tools`、`asset_directory_identity`、`asset_hashes`、`baseline_manifest_shape`、`boundary_manifest_shape` 七个有序阶段；远端包装器只接受固定四行失败摘要并映射到脱敏 `partial55_*` 分类，其他输出统一关闭为 `partial55_execution_unclassified`。当前仍未通过真实矩阵，状态为 `partial55_precheck_instrumented_recovery_authorization_required`。

随后取得精确观测恢复授权并严格执行一次，传输预算为 SSH 2、`scp.exe -O` 1、零重试。旧现场通过精确门禁后被清理，新矩阵返回 `classification=partial55_boundary_manifest_shape`、退出码 2；新 Stage 保留、临时容器已精确移除。该结果证明前六项预检全部通过，失败位于 31 条 boundary manifest 的语法检查，且尚未创建 partial55 目标库。纯本地复现确认两套 partial runner 的 awk 把相邻 action 写成 `... {bad++} seen[$2]++ ...`，真实 awk 报语法错误；现已改为独立 `{seen[$2]++}` action，并将正式 awk 程序加入运行时契约。000055/000056 partial 契约 normal/`python -O` 分别通过 34/39 个攻击模型。恢复清理器只新增接受 `boundary_manifest_shape/none/false` 且 stderr 为空的当前现场；完整编排继续保持失败关闭。当前状态为 `partial55_boundary_awk_fix_recovery_authorization_required`，尚未取得修复后的真实矩阵证据。

修复后的恢复授权随后只进入第一次 SSH，清理器以 `classification=partial55_stderr_pair`、退出码 2 停止；SCP 0、零重试，旧 Stage 原样保留，未创建容器或执行 migration。该门禁证明 Stage 内固定 `partial55.stderr` 并非空文件，也纠正了此前把外层 SSH `stderr_length=0` 误当作 Stage 内 stderr 为空的判断。纯只读诊断器已增加 `awk_syntax` 白名单，只按 awk/mawk/gawk 固定错误外壳分类而不输出原文；payload SHA 为 `A9E03452...B4B4D`，normal/`python -O` 32 个攻击模型、8 个 partial55 运行时解析场景和 PowerShell SelfTest 通过。当前状态为 `partial55_boundary_manifest_stderr_classification_authorization_required`。

单次纯只读分类随后通过：唯一 Stage、source manifest、六项基线、四套资产和七项输出均完整，matrix55 保持完整成功摘要；partial55 精确为 `boundary_manifest_shape/none/false`，Stage 内 stderr 白名单分类为 `awk_syntax`，资产 SHA 身份通过。执行使用 SSH 1、零重试，未访问 Docker、数据库或 Redis，现场保持不变。恢复清理器现只在 stderr 大小 1..4096 且每一行均符合 awk/mawk/gawk 固定语法错误外壳时接受该现场；混合、空或其他内容全部失败关闭。当前状态为 `partial55_boundary_awk_stderr_fix_recovery_authorization_required`。

最终精确恢复及隔离矩阵已按新授权一次通过：固定预算为 SSH 2、`scp.exe -O` 1、零重试；旧 `awk_syntax` Stage 经精确门禁清理，新 Stage 上传后在两个顺序、独立、`network=none`、只读根文件系统和受控 tmpfs 的 MySQL 8 临时容器中重新生成六项基线，并完成 000055/000056 full、partial、down 全矩阵。固定回执为 `baseline_generation=true`、`full55=true`、`partial55=true`、`full56=true`、`partial56=true`、`targets=94`、`temporary_containers_removed=true`、`stage_retained=false`、`main_database_modified=false`。因此 000055/000056 migration 技术门禁均关闭并标记 `must_not_repeat=true`；该证据不扩展为真实邮件、RAM 或 QA/PM 通过。

最终审计又关闭了恢复控制器的输出契约错位：当前清理器返回 `matrix_outputs=2`，控制器原先仍只接受历史值 1，会在旧 Stage 已精确删除且新 Stage 已创建后误判失败。控制器现严格接受 2，并新增降回 1 的攻击用例；完整编排契约增至 82 个攻击模型。该修复完全离线，未连接远端。

### 17.2.6 2026-08-02 项目负责人外发豁免与签署状态

项目负责人明确确认，本轮不执行 `template_send_real_fault_matrix` 和 `five_business_flow_e2e` 的真实外发验收，二者统一登记为 `waived_by_project_owner_not_verified`。该决定不证明真实模板测试发送的重放、并发、unknown、冷却和白名单矩阵通过，也不证明 register、login、reset_password、bind_email、admin_verify 五业务流的完整真实外发 E2E 通过。

机器清单现为 19 项关闭、0 项开放。13 项具有技术 PASS 证据；RAM 有效权限、五场景真实重放/过期、真实模板测试发送故障矩阵、五业务流真实外发 E2E 共 4 项为负责人豁免且未技术验证；`qa_phase4_report` 与 `pm_phase4_signoff` 均按书面结论关闭为附负责人豁免通过。P0/P1/P2 未关闭缺陷均为 0。

最终报告位于 `tests/email/directmail-phase4-qa-report.md`。QA 已确认机器清单、四项豁免边界、零缺陷计数和 `accepted` 语义；PM 已确认业务范围、QA 结论、四项未验证风险和 Phase 5 边界。Phase 4 总状态为 `passed_with_project_owner_waivers`。该结论不代表生产环境验证，也不批准 Phase 5、生产灰度或生产上线。

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
