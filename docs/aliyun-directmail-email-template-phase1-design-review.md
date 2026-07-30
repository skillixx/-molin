# 阿里云 DirectMail 邮件模板管理 Phase 1 契约与设计评审

## 1. 文档状态与交付边界

| 项目 | 结论 |
|---|---|
| 阶段 | Phase 1 基线及邮箱验证码登录、内部 metrics、公开邮件来源 IP delta 均通过；Phase 2 实现待验收 |
| 状态 | QA 与产品经理已书面通过全部 Phase 1 契约与设计；结论仅关闭契约与设计门禁，不代表任何 Go、Nginx、限流器、DirectMail、环境或端到端实现通过 |
| 基线 | `feature/aliyun-email-template-management` @ `288599f` |
| 已有产出 | API 契约、锁原语、前端对接任务与测试准入清单；现有 Go、migration、MySQL 和真实发送材料仅作为 Phase 2 待复验输入 |
| 尚未验收 | Go 集成、真实 Redis 7、五场景完整 DirectMail 链路、RAM 否定矩阵、Vue 前端、完整 E2E、部署与生产流量恢复 |

阶段定义与顺序固定如下，禁止用后续阶段的实现或环境证据倒置前一阶段门禁：

| 阶段 | 范围 | 出口 |
|---|---|---|
| Phase 0 | 外部前置证据：企业资质、发信地址、五模板审核状态、变量和 RAM 账号准备情况 | 证据来源、日期、责任人可追溯；仅证明外部条件具备，不证明平台实现正确 |
| Phase 1 | 协议与设计：接口、错误码/文案、MFA、Redis 锁、限流、审计、敏感扫描、RAM 否定矩阵和五场景验收步骤 | QA 在本文件“15. Phase 1 书面门禁记录”签署“可测”，产品经理在同处签署“业务确认”；两项均通过才允许宣称 Phase 1 通过并进入 Phase 2 |
| Phase 2 | 实现与环境：Go 集成、migration、真实 MySQL/Redis、DirectMail 五场景、RAM 否定、前端与 E2E | 按 `docs/test-plan.md §3.1.1` 留存可复核证据；QA 与产品经理再次书面验收后才可发布 |

本轮 Phase 1 只确认协议与锁原语设计，**不验收 Go 集成**。下列 Go、migration、MySQL 与真实发送材料全部归入
Phase 2“待复验输入”，不能用于跳过 Phase 1 书面门禁，也不能据此宣称 Phase 2 或功能整体完成。

### 1.1 Phase 0 外部证据

| 场景 | 阿里云模板名称 | TemplateId | 审核与变量证据 |
|---|---|---:|---|
| 注册 | `molin_register_code_v1` | `437227` | 审核通过，包含 `Code`、`ExpireMinutes` |
| 登录 | `molin_login_code_v1` | `437228` | 审核通过，包含 `Code`、`ExpireMinutes` |
| 找回密码 | `molin_reset_password_code_v1` | `437229` | 审核通过，包含 `Code`、`ExpireMinutes` |
| 换绑邮箱 | `molin_bind_email_code_v1` | `437230` | 审核通过，包含 `Code`、`ExpireMinutes` |
| 管理员二次验证 | `molin_admin_verify_code_v1` | `437231` | 审核通过，包含 `Code`、`ExpireMinutes` |

专用 RAM 身份查询模板和读取详情已通过；`register` 模板的 `SingleSendMail` 真实调用已被阿里云接受，
用户于 2026-07-22 确认测试邮件已收到。这里将“供应商已受理发送请求”和“用户确认已收到”作为两条独立证据记录，
不把 `accepted` 字段扩展解释为通用投递回执；其余四个场景仍需在后续全链路阶段分别验证。

### 1.2 Phase 2 待复验输入：真实 MySQL 核心迁移材料

2026-07-22 使用官方 MySQL Community Server 8.4.10 Windows 便携包，在仅监听本机高位端口的独立临时实例完成验证；
下载包 MD5 与 Oracle 下载页公布值一致。验证结果如下：

- 原始 `git HEAD` 空库从 `000001` 连续迁移时在 000020 因重复创建 `idx_audit_operator_id` 实测 `ERROR 1061`，因此原始 HEAD 的完整 1→55 尚未通过。
- 54 版本库预置历史邮箱/手机验证码后执行 up：历史验证码全部失效；邮箱明文清除并写不可关联摘要；手机号兼容字段保留。
- 执行 down：五张业务表和 ownership 技术表清理；旧 `code VARCHAR(64) NULL` 保留；八个新验证码字段移除；历史记录保留。
- 混合预存权限状态：ownership 的 `permission_created`、`admin_binding_created` 与预置一致；down 仅删除本迁移创建项，预存权限和绑定保留。
- 权限元数据冲突：up 在创建邮件业务表和 ownership 表之前失败关闭，冲突记录不被覆盖。
- 非 admin 角色、用户覆盖或分组权限引用本迁移创建权限时：down 均失败关闭，五张业务表与 ownership 保留；清除引用后可正常回滚。
- schema 实测口径为：五张业务表加 `verification_codes` 共 33 个 CHECK，ownership 技术表另有 2 个，总计 35 个；外键共 7 个；五张业务表按 `(table_name,index_name)` 计 26 个索引，连同 `verification_codes` 与 ownership 共 35 个索引。合法模板、绑定、白名单和 accepted 日志 CRUD 均通过。
- 大写 SHA、非法外键、删除已绑定模板、accepted 日志缺少 RequestId 四类非法写入均被数据库拒绝。
- 54 状态库经 `mysqldump --single-transaction` 备份并恢复到新隔离库后，000055 up 成功，五张业务表、四条 ownership、五个场景完整。
- 55 状态库同样备份并恢复到新隔离库后，000055 down 成功，业务表与 ownership 表均为 0，旧 `code VARCHAR(64) NULL` 保留。
- 000055 partial-up 16/16、partial-down 15/15 全部通过，共 31 个故障注入：所有注入均 `exit=1`，`information_schema` 与 ownership 状态和断点一致；另有无注入 up/down 各 1 次，均 `exit=0` 并恢复到目标结构。
- 000020 最小兼容修复已获产品经理批准、实施并通过真实复验：真实 `golang-migrate` + MySQL 8.4.10 空库 1→55 为 version55/dirty0 且三索引正确；19→20→19→20 均 dirty0，v19 仅保留 operator/created、v20 增加 module_action；同一 v20 库继续到 55 成功且未重放 000020；55→54→55 均 dirty0，五邮件表 0→5、ownership 4；测试服务器 MySQL 8.0.46 只读审计为 version54/dirty0 且三索引正确。全程未使用 force，当前 up/down SHA256 分别为 `C91CB6A30CE6577C3CC88BE18CEADFC03406435172A03D61D39A7014EB8AB9A8`、`921521A7863E2FE7DC95A067267198C2E690537367D9A729C73F11D3FD81070C`，与 ADR 修改后值一致。决策边界见 `docs/adr/000020-audit-index-migration-compatibility.md`。

使用完整隔离 Go 1.25.0 与临时 modfile 对当前最终工作树重跑 `go test -count=1 ./...`、`go build ./...`、
`go vet ./...` 均通过；仓库 `go.mod`、`go.sum` 未修改。

上述结果证明 000055 存量路径的代表性 up/down 前备份可恢复，且 31 个 partial 故障注入断点已覆盖。原始 `git HEAD` 000020 新库实测 `ERROR 1061`；其最小兼容修复已获产品经理批准、实施并通过上述真实复验。Redis 基础设施 P1、其余四场景、RAM 否定矩阵及完整 E2E 尚未验收，因此 Phase 2 仍未通过阶段门禁。

## 2. 业务目标与固定范围

平台统一管理以下五个邮件 OTP 场景：

| scene | 中文名称 | 发码入口 |
|---|---|---|
| `register` | 注册 | 公开邮箱发码端点 |
| `login` | 邮箱验证码登录 | 公开邮箱发码端点 |
| `reset_password` | 找回密码 | 公开邮箱发码端点 |
| `bind_email` | 换绑邮箱 | 登录态专属端点 |
| `admin_verify` | 管理员邮箱双重认证 | 管理员专属端点 |

场景为封闭枚举，不支持后台新增。环境验收时，`register` 作为首个真实发送试点；
`login`、`reset_password`、`bind_email`、`admin_verify` 四场景的真实发送仍待后续逐项验收，不因首个场景成功而自动视为通过。

模板变量只有两项稳定业务字段：

```text
code             -> Code
expire_minutes   -> ExpireMinutes
```

映射大小写固定，管理端只读，不允许运营自行修改。

## 3. 官方能力核对与采用边界

本设计按阿里云 DirectMail 官方 API 的当前能力收口：

- `QueryTemplateByParam`：分页读取模板列表，提供 `TemplateId`、名称、审核状态和审核意见。
- `DescTemplate`：按 `TemplateId` 读取主题、正文、名称、状态等详情。
- `SingleSendMail`：正式 OTP 与后台白名单测试统一使用的单封邮件发送接口；服务端从本地冻结镜像 `TemplateText` 固定渲染 `Code` 与 `ExpireMinutes` 后提交镜像 `Subject + HtmlBody`，不得提交 `Template.TemplateId/Template.TemplateData`。`TemplateId` 仅用于本地绑定、日志和追踪；固定 `ClickTrace=0`，发件人别名默认“墨灵”并允许安全配置覆盖。主题必须是有效 UTF-8、非空且不超过 100 个 Unicode 字符，渲染正文必须是有效 UTF-8、非空且按 UTF-8 字节不超过 80 KiB。
- DirectMail 模板状态映射：`0→draft`、`1→pending`、`2→approved`、`3→rejected`；`missing` 是平台在一次完整同步后推导的本地状态。
- RPC 固定使用 JSON：`QueryTemplateByParam` 列表只读取 `data.template[]` 的 `TemplateId/TemplateName/TemplateStatus/CreateTime`；`DescTemplate` 详情只以真实且未废弃的 `RequestId/CreateTime/TemplateSubject/TemplateStatus/TemplateName/TemplateText` 为准。两组字段不得混用，也不得把废弃字段作为详情依据。未知状态不得降级为 draft，必须失败关闭且本次镜像不变。

官方参考：

- <https://help.aliyun.com/zh/direct-mail/api-dm-2015-11-23-querytemplatebyparam>
- <https://help.aliyun.com/zh/direct-mail/api-dm-2015-11-23-desctemplate>
- <https://help.aliyun.com/zh/direct-mail/api-dm-2015-11-23-singlesendmail>

当前范围不设计从墨灵后台调用 `CreateTemplate`、`ModifyTemplate`、`DeleteTemplate`。供应商模板仍由阿里云控制台管理，
墨灵只做只读镜像、固定场景绑定、原子同步、白名单测试和发送日志，减少越权修改供应商资源的风险。

## 4. 模块边界与依赖方向

```text
auth 验证码业务
  -> EmailOTPSender 稳定接口
      -> ProductionDirectMailAdapter
          -> 场景绑定/模板镜像
          -> DirectMail SingleSendMail
      -> MockEmailAdapter（仅显式非生产）

admin email 管理接口
  -> 模板同步服务 / 绑定服务 / 白名单服务 / 测试发送服务
      -> DirectMail QueryTemplateByParam / DescTemplate / SingleSendMail
```

auth 只依赖语义稳定的发送接口，建议签名：

```text
SendOTP(ctx, business_request_no, scene, recipient, code, expire_minutes) -> acceptance result
```

接口入参不出现 `TemplateId` 或阿里云 SDK 类型。EmailService 按 scene 解析绑定与冻结模板镜像，在本地将 `Code`、`ExpireMinutes`
渲染为最终 `HtmlBody`；Production Adapter 只提交 `Subject + HtmlBody`，不接收或组装 `TemplateId/TemplateData`。business_request_no 由服务端生成/复用，现有客户端不新增 Idempotency-Key Header。
这样更换模板或未来更换供应商时，注册/登录/找回密码逻辑无需修改。

## 5. 模板同步设计

### 5.1 原子同步

一次同步必须完成以下步骤：

1. 创建 `running` 同步记录并占有单实例同步锁；
2. 拉取 `QueryTemplateByParam` 全部分页；
3. 对每个模板调用 `DescTemplate`，规范化状态、变量和内容摘要；
4. 全部远端读取成功后开启数据库事务；
5. upsert 本次全部模板；
6. 把“上次存在、本次完整结果未出现”的模板标为 `missing`；
7. 写入计数和 `succeeded`，提交事务；
8. 任一远端读取或数据库写入失败，模板镜像与 missing 标记不变，仅同步记录记为 `failed`。

禁止边拉一页边提交一页；否则中途失败会把未拉到的合法模板误标 missing。

### 5.2 幂等与并发

- `POST /api/admin/email/templates/sync` 必须传 `Idempotency-Key`。
- scope 固定为跨管理员全局 `admin-email-template-sync:aliyun_directmail`，指纹固定为规范化 method+path+provider；
  唯一约束为 scope+key_hash，不按管理员拆分。
- 数据库只保存 key 的 SHA-256 与规范化请求指纹，不保存调用方原 key。
- 同 key + 同指纹返回第一次结果，`idempotent=true`。
- 同 key + 不同指纹返回 `409/40900`。
- 已有同步 running 时，不同 key 也返回 `409/40900`，避免两个任务并发写镜像。
- failed 结果不会自动用同 key 重跑；管理员明确发起新一次同步时生成新 key，保留失败审计轨迹。
- running 超过5分钟仅为陈旧候选；必须先取得同一sync lease，原执行者仍续租时不得收敛。同步事务首尾锁定/校验run仍为running，最终RowsAffected非1则回滚镜像。

Redis 是发布必需依赖，不是可选缓存。同步、正式 OTP 和测试发送在进入数据库幂等占位或供应商调用前，必须取得
Redis 分布式锁。只有未取得锁，或外呼开始前发生续租/所有权校验失败时，才失败关闭并统一返回
`503/51003「邮件发送服务未就绪」`，Adapter 调用次数增量为 0。外呼开始后的续租/所有权失败不得返回 503，
也不得断言 Adapter 增量为 0，必须按明确响应 fencing 或未知结果持久化阻断规则收敛。

锁原语冻结如下：

- 加锁使用单条 `SET key token NX PX ttl`；`token` 为每次竞争生成的高熵随机所有权标识，只保存在进程内，不写日志、响应或审计。
- 同步锁 key 固定 `lock:email:template-sync:aliyun_directmail`，初始 TTL 30 秒；OTP/测试发送锁 key 使用 scope 的 HMAC 摘要，初始 TTL 15 秒，key 中不得出现完整邮箱、用户标识明文或幂等原 key。
- 持锁任务每隔不超过 TTL 的三分之一续租；续租必须用 Lua 原子比较 value=token 后 `PEXPIRE`，不得无条件延长其他执行者的锁。
- 释放必须用 Lua 原子比较 value=token 后 `DEL`；非所有者、已过期或 token 不匹配时不得删除，并产生安全告警。
- 进入数据库事务/供应商外呼前再次确认锁所有权；同步在提交镜像前还必须用 run 状态与锁 token 做 fencing。外呼前丢锁立即停止且不调用 Adapter；外呼期间丢锁按下方唯一收敛规则处理。
- Redis 故障恢复后不自动重发邮件。调用方必须先查询既有幂等/unknown failed 记录；只有不存在未过期阻断记录时，人工新业务动作/新 key 才可重试。

本轮只验收上述锁协议与原语设计，不代表现有 Go 服务已正确接入、续租、fencing 或故障关闭；这些属于 Phase 2 集成验收。

## 6. 场景绑定与乐观锁

五场景在 migration 中预置为 disabled。管理员只能选择
`approved && local_enabled && !missing && variables_complete` 的镜像模板，并提交当前 `version`：

```text
UPDATE email_scene_bindings
SET template_id=?, enabled=?, version=version+1, updated_by=?, updated_at=NOW()
WHERE scene=? AND version=?
```

影响行数为 0 时返回 `409/40900`，要求客户端刷新。即使历史绑定仍指向模板，只要模板后续变为 rejected 或 missing，
本地停用或缺少 Code/ExpireMinutes，发送前置检查也会阻断，不能只看绑定的 enabled 字段。绑定、启用模板、模板测试、
正式发送四个入口均重新校验两个变量，缺任一项固定返回 `422/51001`。
其中 status 端点启用必须变量完整；停用也读取并记录变量完整性，但为保证故障模板可立即关停，不因缺变量拒绝。

管理端增加 `GET /api/admin/email/summary`，固定返回 template_total、approved_count、local_enabled_count、
unbound_scene_count、submitted_today_count、failed_today_count、last_synced_at。今日按 Asia/Shanghai 自然日
`[00:00,次日00:00)`；submitted 只统计 accepted+failed，数据库内部 pending 不计，failed 只统计 failed；last_synced_at 取最近 succeeded.completed_at 且可 null；
`PATCH /api/admin/email/templates/{id}/status` 以 local_enabled+version 乐观锁启停，供应商同步不得覆盖 local_enabled。
模板详情 HTML 是不可信内容，只允许在 iframe srcdoc + 空 sandbox 中预览，禁止 scripts、forms、top navigation、popups、
same-origin，并用 CSP 阻断外部网络；不得直接注入管理后台主文档。

## 7. 邮件 OTP 受理状态

当前范围只判断 DirectMail 同步受理结果，不建立最终投递状态机。数据库内部状态为三态，但管理端公开状态固定为后两态：

```text
internal: pending | accepted | failed
public: accepted | failed
```

业务流程：

1. 生成验证码并先持久化，显式写 `verification_codes.send_status=pending`；后台测试发送则在调用供应商前先写 pending 日志幂等占位；
2. 调用 Production Adapter；
3. 只有 DirectMail 明确返回成功，才原子置 send_status=accepted、写 accepted_at，并写 accepted 发送日志及阿里云 RequestId；
4. 明确失败/拒绝与响应未知/超时都置 send_status=failed；前者记录归一化安全失败原因，后者严格写 `provider_outcome_unknown` 并启用持久化阻断规则；
5. 验证码校验必须同时满足：send_status=accepted、未使用、未过期、scene/target/code 匹配。

因此，内部 pending 以及失败、拒绝、超时对应的验证码永远不可用于认证。超时结果即使供应商最终可能受理，也必须失败关闭；
结果明确且冷却结束后可用新业务动作发码，结果未知时必须遵守持久化墓碑到期规则。`accepted` 仅表示供应商同步受理，不等于最终送达；所有面向管理员和用户的成功文案统一为“供应商已受理发送请求”。
当前范围明确不接入投递回执 Webhook，不跟踪最终送达、打开率或点击率。

正式 OTP 幂等由服务端在冷却窗口内生成或复用 business_request_no。五个 scope 固定为：register/login/reset_password
使用各自公开入口+target_hmac；bind_email 使用 user_id+target_hmac；admin_verify 使用 admin_id+target_hmac。
key_hash 由 business_request_no+scope 使用独立密钥 HMAC 派生，指纹包含 endpoint、scene、target_hmac、purpose、
expire_minutes、template_id、binding_version。同 scope+指纹重放返回相同业务结果，expires_in 按原 expires_at 递减，
failed 重放返回原安全错误，pending 并发重放返回 `409/40900「邮件正在发送，请稍后重试」`；三者都不重置有效期、
不再次发信。scope 必须先获取 Redis 分布式锁，再原子创建或复用 verification_codes 幂等字段；数据库条件更新仅用于 fencing 和唯一收敛，不能替代 Redis 锁；同业务请求号
对应不同指纹返回 `409/40900`。冷却结束后的明确新发码才生成新业务请求号。

所有公开、换绑和管理员发码首次成功响应统一为 `data={sent:true,expires_in:600}`，仅幂等重放递减。生产永不返回 code；既有非生产调试
扩展只在 APP_ENV 被显式设置、经 trim+小写后精确属于 local/development/dev/test/testing，且调试开关经 trim 后精确等于小写 `true` 时可额外返回 data.code。APP_ENV 缺失、空白、staging、未知值或 production 均失败关闭；调试开关的大写、混合大小写、数字及其他宽松布尔别名均不接受。该字段不属于稳定前端契约，也不能进入日志、审计、持久化或 telemetry。

## 8. 正式发送与测试发送

### 8.1 正式 OTP

- 从 scene 绑定解析当前 `TemplateId` 并读取对应冻结镜像 `TemplateText`，仅在本地将 `Code` 与 `ExpireMinutes` 渲染为最终 `HtmlBody`；`SingleSendMail` 只提交镜像 `Subject + HtmlBody`，不得提交 `Template.*` 参数。
- `TemplateId` 只记录到场景绑定和发送日志用于本地追踪，禁止代码、环境变量或调用方硬编码。
- 本地渲染值只由服务端生成：`Code` 来自本次验证码，`ExpireMinutes` 固定为 `10`。主题必须是有效 UTF-8、非空且不超过 100 个 Unicode 字符；渲染正文必须是有效 UTF-8、非空且按 UTF-8 字节不超过 80 KiB。
- `bind_email` 收件人只能由登录态当前换绑流程中的目标邮箱产生；`admin_verify` 只能使用当前管理员已绑定邮箱，管理员发码端点无 email Body。服务层发现伪造或越权目标固定返回 `403/40003「无权向该邮箱发送验证码」`；管理员端点黑盒请求额外 `email` 字段固定返回 `400/40000「请求参数错误」`。

### 8.2 后台模板测试发送

- 端点固定为 `POST /api/admin/email/templates/{id}/test-send`，通过平台模板镜像 ID 解析 DirectMail `TemplateId`，并使用 `SingleSendMail`。
- Body 传固定五场景之一与测试邮箱；场景只用于选择固定 `Code`/`ExpireMinutes` 变量语义，不要求模板已绑定该场景。
- 收件人必须是单个裸邮箱地址，trim 后完整地址统一小写并命中测试邮箱白名单的 HMAC；拒绝多地址、显示名与换行注入，白名单库不存完整邮箱。
- 未命中 active 白名单固定返回 `400/40000`，不得返回权限错误。
- 测试码由服务端生成且不写入 `verification_codes`，不具备任何认证用途。
- 必须传 Idempotency-Key。明确受理才返回 HTTP 200/status=accepted；供应商明确失败/拒绝先写 failed 与安全原因并返回通用 502/51002；响应未知/超时按 `provider_outcome_unknown` 专用 502/51002 文案及持久化阻断规则处理。失败绝不返回 HTTP 200/status=failed。
- 同 key 重放 accepted 返回同一 200 且 idempotent=true；重放 failed 返回同一安全 502/51002 错误信封；两者均不再次调用供应商。
  同 key 不同模板/场景/邮箱返回 409/40900。成功响应只返回脱敏邮箱、发送日志 ID、业务请求号和 accepted，不返回测试码。
- test-send 在供应商调用前先持久化 pending 幂等占位；崩溃或并发重放命中 pending 时返回 409/40900，绝不重复调用供应商。
- pending 必须按外呼结果和 fencing 规则及时收敛，禁止以“超过5分钟”作为常态终结机制；响应未知时按下方墓碑规则阻断所有新旧 key。所有管理写动作先写attempt审计，结果审计失败只告警、不反转成功响应。

test-send 的锁 scope 固定为：

```text
admin-email-template-test:admin:{admin_id}:template:{platform_template_id}:scene:{scene}:recipient:{recipient_hmac}
```

邮箱必须先 trim、统一小写并完成单裸地址校验，再计算 `recipient_hmac`。Redis key 只使用该 scope 的 HMAC 摘要，
不得包含完整 scope、完整邮箱或 `Idempotency-Key`。`Idempotency-Key` 只参与结果幂等记录，不进入锁 scope；因此同一管理员、
平台模板、场景、收件人即使使用不同 key 也竞争同一把锁，任一维度不同则不竞争。

外呼期间丢锁时，发送日志不得长期停留 pending，且不得新增“墓碑表”：

- 供应商明确 accepted 时，以 `WHERE id=? AND status='pending'` 唯一收敛为 accepted；明确 rejected 时以同一条件唯一收敛为 failed。accepted 只能依据明确 accepted 响应。
- 响应未知或超时时，复用已持久化的 `email_send_logs` pending 行，在同一数据库事务中条件更新为 failed、`failure_reason=provider_outcome_unknown` 并保留 `idempotency_scope`。`purpose=otp` 保留日志及 `verification_codes.expires_at`，且同事务把对应验证码置 failed；`purpose=test` 的 `email_send_logs.expires_at` 必须保持 NULL，墓碑处理不得修改该列。该 unknown failed 行就是持久化冷却墓碑。
- 墓碑查询与响应统一使用派生值 `cooldown_until`：OTP 为原验证码/发送日志 `expires_at`，test 为 `email_send_logs.submitted_at + 10分钟`，与测试码固定 10 分钟一致；`cooldown_until` 是服务层派生口径，不新增数据库列。
- 每次新外呼必须先取得 Redis 锁，再按同 scope 和派生 `cooldown_until` 查询仍在冷却期的 pending 或 `provider_outcome_unknown` failed 行；命中即阻断，所以 Redis 重启或锁 key 丢失也不能绕过。
- 原未知请求返回 `502/51002「供应商响应未知，请在验证码过期后重试」`；同一旧 Idempotency-Key 重放原 502/51002 且 `idempotent=true`。墓碑期内新 key 返回 `409/40900「邮件发送结果确认中，请在验证码过期后重试」`，Adapter 调用增量为 0。
- `cooldown_until` 到期后仅新 key 可重新发送；旧 key 仍重放原失败。条件更新影响行数不是 1 时，不得覆盖已有终态，读取已有终态后返回对应结果并告警。

## 9. 所有发送前置条件

每次正式或测试发送必须依次检查：

1. scene 属于固定五场景，且当前端点有权使用该 scene；
2. 生产环境选择 Production Adapter；Mock 只能显式非生产启用；
3. DirectMail AccessKey、Region、发信地址等配置完整且来自安全环境配置；
4. 正式 OTP 的场景绑定存在且 enabled；模板测试的路径模板存在；
5. 模板 approved、local_enabled、非 missing；
6. 正式 OTP 的 TemplateId 从绑定解析，模板测试的 TemplateId 从路径镜像 ID 解析；变量必须完整包含 Code+ExpireMinutes；
7. 收件人合法，正式发送绑定当前流程目标，测试发送命中白名单；
8. 验证码 IP 与账号维度限流及场景前置校验通过；公开场景账号键为规范化邮箱 HMAC，换绑为 user_id，管理员验证为 admin_id；任一维度超限统一返回 `429/42900「请求频率超限」`；
9. 模板同步/测试的 Idempotency-Key，或正式 OTP 的服务端业务请求号、scope、指纹不冲突；
10. 正式 OTP 验证码已先落库且 send_status=pending；
11. 日志与审计脱敏策略可用；
12. 供应商明确受理后才允许写 accepted；其他结果写 failed。缺变量固定 `422/51001`，上游/RAM 失败
`502/51002`，生产 Adapter/配置未就绪 `503/51003`。

任一条件不满足均失败关闭，不降级成“生成一个本地可用验证码但不发邮件”。

### 9.1 发送前置失败契约

以下 HTTP/code/message 是 `docs/full-api-design.md` 的冻结契约，所有管理端与正式 OTP 入口一致执行：

| 条件 | HTTP/code | message | Adapter 调用次数增量 |
|---|---|---|---:|
| 路径模板或其他邮件资源不存在 | `404/40400` | `邮件资源不存在` | 0 |
| 场景无绑定 | `409/40900` | `邮件场景未绑定模板` | 0 |
| 场景绑定 `enabled=false` | `409/40900` | `邮件场景已停用` | 0 |
| 模板 `local_enabled=false` | `409/40900` | `邮件模板已停用` | 0 |
| 模板 `draft` | `409/40900` | `邮件模板尚未提交审核` | 0 |
| 模板 `pending` | `409/40900` | `邮件模板正在审核` | 0 |
| 模板 `rejected` | `409/40900` | `邮件模板审核未通过` | 0 |
| 模板 `missing=true` | `409/40900` | `邮件模板在供应商侧不存在` | 0 |
| 缺少 `Code` 或 `ExpireMinutes` | `422/51001` | `邮件模板变量不完整` | 0 |
| 未取得 Redis 锁、外呼前丢锁，或生产 Adapter/必要配置未就绪 | `503/51003` | `邮件发送服务未就绪` | 0 |

管理员邮件接口还必须同时满足对应的四个邮件权限之一以及手机、邮箱两项管理员认证均在有效期内。缺权限或未完成
双重认证均返回 `403/40003`；其中未完成双重认证的文案固定为 `请先完成管理员双重认证`。资源不存在统一使用
`404/40400`，禁止使用 `40004` 或为邮件模块另造 404 错误码。

## 10. 权限、审计与敏感信息

### 10.1 平台权限

| 权限码 | 最小能力 |
|---|---|
| `email:template:view` | 查看概览、模板、绑定、同步、白名单、发送日志 |
| `email:template:manage` | 模板本地启停、修改绑定、维护白名单 |
| `email:template:sync` | 执行供应商模板同步 |
| `email:template:test` | 执行白名单测试发送 |

后续 migration 必须 seed 四个权限码并赋予平台管理员角色；权限变更后按 IAM 规范立即失效缓存。

### 10.2 DirectMail RAM 最小权限

运行账号只授予实际使用的 action：

- 模板同步：`dm:QueryTemplateByParam`、`dm:DescTemplate`；
- 正式 OTP 与后台模板测试：`dm:SingleSendMail`。

RAM Allow 严格只有上述三个 action。QA 必须分别显式 Deny 每个允许 action，断言平台统一返回
`502/51002「邮件上游调用失败」`，并验证同步原子回滚、验证码不可用、模板测试不误报成功，以及响应/日志脱敏。
另用最小权限账号直接探测 `dm:CreateTemplate`、`dm:ModifyTemplate`、`dm:DeleteTemplate`，三者都必须被拒绝，且应用运行轨迹不得调用它们。
平台 RBAC 拒绝仍使用 `403/40003`，不得与供应商 RAM 拒绝混淆。

### 10.3 审计与日志

允许记录：操作人 ID、平台模板/绑定/同步/发送日志 ID、scene、脱敏邮箱、版本、业务请求号、公开 accepted/failed、
安全失败原因、阿里云 RequestId、request_id。

禁止记录：验证码、完整邮箱、AccessKey、签名、完整 TemplateData、供应商原始响应、模板变量实际值。
合法发码/白名单/测试请求在请求处理内存中可以短暂包含 email/code；敏感扫描范围固定为响应、日志、审计、持久化和
telemetry，不把合法请求入参本身误报为泄露。

建议审计 action：

```text
email.template.sync
email.scene.binding.update
email.test_allowlist.add
email.test_allowlist.revoke
email.template.test_send
```

可观测口径冻结如下：

- 每次供应商 Adapter 调用递增 `email_adapter_calls_total{operation,scene,result}`；`operation` 仅允许 query_templates、describe_template、send_mail，`result` 仅允许 accepted/failed/timeout，禁止把邮箱、模板正文、RequestId 或错误原文放入 label。
- 前置条件拒绝必须保证对应 `send_mail` 调用计数增量为 0；幂等重放前后计数不变。五场景真实验收分别记录调用前后计数快照，证明每个首次动作恰好调用一次。
- 审计 attempt 写入失败时动作不执行；result 写入失败、锁续租/所有权异常、同步失败、清理任务失败、敏感扫描命中时产生 `warning` 级结构化告警。连续 5 分钟内同一 scene 的 failed 比例超过 20% 且样本不少于 10，或任一 Redis 锁所有权异常，升级为 `critical`。
- 告警最小字段固定为 request_id、action、scene、内部对象 ID、result、error_code、occurred_at；不得包含完整邮箱、OTP、AccessKey、TemplateData、锁 token 或供应商原始响应。
- 敏感扫描落点固定为：HTTP 响应抓包、应用 stdout/stderr 与集中日志、`audit_logs`、邮件五业务表及 `verification_codes`、指标 label/trace span/event、前端 console/埋点/持久缓存。扫描规则覆盖完整邮箱、6 位 OTP 形态、AccessKey 形态、`TemplateData` 和供应商原始响应字段名；合法请求体仅在受控内存中短暂存在，不落盘作为扫描样本。

## 11. 数据设计与保留

数据表、字段、索引与 up/down 顺序的 SSOT 为 `docs/database-schema-design.md §3.1.1`，核心表：

- `email_provider_templates`
- `email_scene_bindings`
- `email_template_sync_runs`
- `email_test_recipient_allowlist`
- `email_send_logs`

000055 必须承认真实基线：`verification_codes` 保留原 `code VARCHAR(64) NULL`，新增 `code_hash CHAR(64)`、
`send_status`、`business_request_no` 与 `accepted_at`，禁止重命名或删除旧 `code`。全停机窗口等待 10 分钟后，历史 email
验证码全部置 failed、过期、已使用，写入不可关联随机占位 hash 与统一 masked 占位并清空 `target_value`；占位不用应用
HMAC 密钥。仅新 email 写真实 HMAC；历史 phone 同样置 failed、`accepted_at=NULL`、过期且已使用，仅迁移后的新 phone 显式 accepted。down 只删除 `code_hash` 及 000055 新增对象，
继续保留原 `code VARCHAR(64) NULL`，不得恢复有缺陷的 VARCHAR(16)，也不得静默截断 hash。

DirectMail 业务表固定为五张；`migration_000055_permission_ownership` 是 migration-only 技术表，不是第六张业务表。
up 以四行记录四权限及 admin 绑定的预存 ownership，补缺项并回填最终 ID 后强断言；down 仅按 created 标志精确清理，
发现未知角色、用户覆盖或分组引用即 fail-closed，写后断言通过才删除技术表。真实 MySQL 门禁必须验证五业务表+一技术表，
并验证 down 后两类表均按预期清理、预存权限与预存 admin 绑定不受影响。

MySQL DDL 会隐式提交，000055 不能依赖事务整体回滚。执行前必须通过 `information_schema` 检查 schema 版本、表、列、索引、
约束和 seed 基线；每个 DDL 阶段后再次核对，并按已完成阶段恢复或人工清理。发布固定为：停止邮箱/手机 OTP 发码、OTP 校验、
注册、登录流量；等待 10 分钟；停止全部 auth/API 实例；备份并验证可恢复；执行 000055 up 并完整核验；再执行 000056 up
并完整核验；部署全部新版本应用实例；核验 health、ready、应用版本、schema 版本与配置；恢复流量。回滚固定按 A/B/C
矩阵：000056 未执行时走原 000055 down；000056 已执行且无成功 receipt 时先 down 000056、核验后再 down 000055；存在成功
receipt 时应用回滚保留 schema 55+56、receipt、模板镜像和绑定，不执行任一 down。确需回到 55 前必须另立高风险变更，先完成
备份恢复验证、不可变审计留证和 QA/产品经理/运维联合批准，解除全部引用后依次 down 000056、down 000055，禁止 force。
禁止滚动部署，禁止新旧应用共存。

所有状态/scene/purpose/布尔值通过 MySQL 8 CHECK 约束。模板增加 local_enabled、variables_complete、missing_since；
missing 清理使用 missing+missing_since 索引。白名单 revoked 满 30 天后按 status+revoked_at 索引物理删除整行，审计保留
脱敏操作事实。同步幂等唯一键使用全局 scope+key_hash；发送日志中 `purpose=otp` 的 expires_at 非空、`purpose=test` 的 expires_at 必须为空；阿里云 RequestId 在 failed 时可空，
failure_reason 在 accepted 时为空。同步记录 running/succeeded/failed 的 completed/error 字段可空性在 API 与 DB 约束一致。

模板 missing 后至少保留 180 天；同步记录与发送日志保留 180 天；撤销白名单 30 天后物理删除。
仍被绑定或历史发送日志引用的模板不得物理删除。

## 12. API 与前端对接摘要

权威字段见：

- `docs/full-api-design.md §2.1、§3.19`
- `docs/frontend-api-reference.md §五之二`
- `docs/frontend-task-admin-console.md §5.1`

建议的管理端端点全部位于 `/api/admin/email/*`：模板列表/详情、五场景绑定、模板同步与同步记录、
`GET /api/admin/email/summary`、`PATCH /api/admin/email/templates/{id}/status`、测试邮箱白名单、
`POST /api/admin/email/templates/{id}/test-send`、`GET /api/admin/email/send-logs`。
所有列表严格 D-95；写入冲突统一 `409/40900`；同步和测试发送必须使用 Idempotency-Key。
白名单 POST 成功固定 HTTP 201 返回 active 脱敏对象，DELETE 成功固定 HTTP 200 返回 revoked 脱敏对象；
非白名单测试固定返回 `400/40000`。

## 13. Phase 1 契约自检与 Phase 2 实现状态

当前交付自检：

- [x] 五固定场景及变量映射在后端、前端、测试文档一致。
- [x] 模板同步原子、幂等、missing 语义明确。
- [x] version 乐观锁与 40900 处理明确。
- [x] 测试邮箱白名单只存 HMAC/脱敏值。
- [x] verification_codes 真实基线、64 位 hash、send_status 与邮箱/手机 up/down 兼容策略明确。
- [x] summary、local_enabled 乐观锁、概览统计和隔离 HTML 预览契约明确。
- [x] Code+ExpireMinutes 在绑定/启用/测试/正式发送四处强制校验，错误码冻结为 51001。
- [x] 五入口正式 OTP 服务端幂等与统一 `{sent,expires_in}` 响应明确，生产不返回 code。
- [x] 非白名单 40000、邮件专属 51001/51002/51003、白名单成功响应与字段可空性明确。
- [x] 枚举 CHECK、missing_since/清理索引、白名单物理清理和同步全局 scope 明确。
- [x] accepted 仅表示供应商受理，展示文案固定为“供应商已受理发送请求”；OTP fail-closed 规则及当前范围不跟踪送达/打开/点击的边界明确。
- [x] Production/Mock Adapter 边界与 TemplateId 动态解析明确。
- [x] 四个平台权限码、审计脱敏、RAM 否定测试明确。
- [x] 表、字段、索引、唯一约束、状态、保留期与 000055 migration up/down 已实现。
- [x] Redis 锁的 TTL、续租、token 所有权释放、fencing 与故障失败关闭协议已冻结。
- [ ] Go 服务对上述 Redis 锁协议的集成尚未验收，现有测试、构建或 vet 结果不得替代该项。

Phase 1 出口必须先取得：

1. QA 对契约可测性与否定用例的书面通过；
2. 产品经理对五场景、管理端范围、错误文案和真实发送验收顺序的书面确认；
3. 两份结论写入本文件“15. Phase 1 书面门禁记录”，包含评审人、日期、结论和阻断项；口头确认、聊天截图或 Phase 2 实现证据不能替代。

Phase 2 环境验收还必须由运维通过安全渠道确认 DirectMail Region、发信地址、RAM 最小权限与测试白名单配置可用，
并执行五场景矩阵；不得把 Mock、OpenAPI Explorer 或供应商 accepted 当作最终送达验收。

## 14. Phase 2 待复验历史输入（不代表本轮通过）

- 历史记录包含 MySQL 8.4.10 新库/旧库迁移、ownership、备份恢复及故障注入结果；本轮未重跑，全部待 Phase 2 复验。
- 历史记录包含 CHECK、外键、索引、合法/非法写入结果；本轮未核验，不作为当前验收结论。
- 历史记录包含 000020 兼容修复和 `golang-migrate` 往返材料；本轮未核验，不证明 000055 当前路径可发布。
- 历史记录包含 `/api/admin/email/*`、Adapter、Go test/build/vet 材料；本轮未验证 Redis 锁集成、冷却墓碑或端到端行为。
- 历史记录包含 DirectMail 模板读取、`register` accepted 和用户收件确认；本轮未复验，且不能代表其余四场景或通用投递回执。
- 当前范围不接入或验证投递回执 Webhook，不跟踪最终送达、打开率或点击率。
- `register` 的供应商 accepted 与用户确认收到是两条独立证据，不等于通用投递回执；另外四场景真实发送仍待逐项验收。
- 真实 Redis 7、其余四场景、RAM 否定矩阵、Vue 前端、完整接口 E2E、浏览器测试与生产环境验证尚未完成；不得据此宣称功能整体完成。

## 15. Phase 1 书面门禁记录

此处是 Phase 1 唯一书面记录位置；禁止把本门禁结论扩展解释为 Phase 2 运行、环境或端到端验收通过。

| 角色 | 评审范围 | 评审人 | 日期 | 结论 | 阻断项/证据链接 |
|---|---|---|---|---|---|
| QA | 契约可测性、五场景矩阵、错误码/文案、Redis 锁、限流、敏感扫描、RAM 否定矩阵 | 测试工程师（QA） | 2026-07-23 | 通过 | Phase 1 契约与数据设计具备可执行验收口径；未执行 Go 集成、真实 Redis、DirectMail 五场景、前端或 E2E，以上均留待 Phase 2 验收 |
| 产品经理 | Phase 0/1/2 边界、五场景业务、MFA、错误文案、accepted 展示文案、Phase 1 出口 | 产品经理（PM） | 2026-07-23 | 通过 | 确认 Phase 1 业务边界与出口；本结论不批准上线，不代表任何 Phase 2 运行或环境能力通过 |

两行结论均为“通过”且无未关闭 Phase 1 阻断项。当前结论：**Phase 1 契约与数据设计评审通过；Phase 2 待验收**。

## 16. 邮箱验证码登录 Phase 1 delta 门禁（QA/PM 已签署）

本节是基线评审通过后新增的最小契约 delta，不改变 §15 对原 Phase 1 基线的历史结论。QA 和产品经理已完成书面签署，delta 已并入 Phase 1 通过基线；本结论仅代表契约与设计通过，未验证 Go、数据库、Redis、DirectMail、Vue 或 E2E，也不代表接口已实现或 Phase 2 验收通过。

### 16.1 冻结契约

- 新增 `POST /api/auth/login/email/code`；既有 `POST /api/auth/login/email` 继续作为邮箱密码登录，不改变路径、Body 或行为。
- 新端点 Body 严格只允许 `email`、`code`。缺失、空值、类型错误或出现 `scene`、`password` 等额外字段，固定返回 `400/40000「请求参数错误」`。
- 只消费 `scene=login` 且 `send_status=accepted`、未使用、未过期、邮箱与验证码匹配的记录。验证与条件更新 `used_at` 必须在同一事务中原子完成，并发提交同一码只能一次成功。
- scene 不匹配、pending/failed、已使用、已过期、邮箱或验证码不匹配及并发消费失败，统一返回 `400/40000「验证码错误或已过期」`，不得泄露失败原因。
- 邮箱未注册返回 `404/40404`；账号禁用返回 `403/40003`，两者均不得消费验证码或创建会话。
- 邮箱密码与邮箱验证码登录复用 D-16 失败保护：同一规范化邮箱累计失败 5 次后锁定 15 分钟，锁定期返回 `423/42901「登录失败次数过多，请15分钟后重试」`；锁定请求不得消费验证码或创建会话，任一邮箱登录方式成功均清除失败计数。
- `scene=login` 邮箱发码同时执行 `10 次/分钟/IP` 与 `10 次/分钟/规范化邮箱 HMAC`，任一维度超限返回 `429/42900`；限流、日志和审计不得保存完整邮箱或验证码。
- 登录成功复用既有 `LoginResp`，创建新会话且不吊销其他有效会话。
- 普通邮箱验证码登录不得设置或刷新管理员手机/邮箱双重认证状态；取得的普通登录 Token 不能绕过管理员双重认证门禁。
- 数据层复用现有 `verification_codes` 的 `scene=login` 与既有会话表，不新增 schema 或 migration。

### 16.2 delta 书面签署记录

| 角色 | 评审范围 | 评审人 | 日期 | 结论 | 阻断项/证据链接 |
|---|---|---|---|---|---|
| QA | 严格 Body、OTP 资格与原子消费、D-16 跨方式累计、双维度限流、会话与 MFA 否定用例 | 测试工程师（QA） | 2026-07-23 | 通过 | delta 契约具备可执行测试口径且无未关闭阻断项；本结论未执行或批准 Go、数据库、Redis、DirectMail、Vue、E2E 与部署验收，以上全部留待 Phase 2 |
| 产品经理 | 密码登录保留、错误码/文案、锁定规则、会话共存与管理员双重认证边界 | 产品经理（PM） | 2026-07-23 | 通过 | 确认 delta 业务契约与边界并同意并入 Phase 1 基线；本结论不代表接口已实现、不批准上线，也不代表任何 Phase 2 代码、运行或环境能力通过 |

当前 delta 结论：**Phase 1 契约与设计评审通过；Phase 2 实现待验收，不得宣称代码或功能实现通过。**

## 17. 内部邮件 Adapter 指标 Phase 1 metrics delta 门禁（QA/PM 已签署）

本节冻结 `GET /api/internal/metrics` 的最小可观测性契约，不改变 §15 基线及 §16 邮箱验证码登录 delta 的既有签署结论。QA 与产品经理已完成书面签署，metrics delta 已并入 Phase 1 通过基线；本结论仅代表契约与设计通过，未验证 Go、反向代理、监控系统或 E2E，不代表端点已实现或 Phase 2 验收通过。

### 17.1 端点与安全契约

- 只允许 `GET /api/internal/metrics`；其他方法（包括 `HEAD`）固定返回 405，并带 `Allow: GET`。
- 成功固定返回 HTTP 200、Prometheus text exposition format 0.0.4，响应头包含 `Content-Type: text/plain; version=0.0.4; charset=utf-8`、`Cache-Control: no-store`、`X-Content-Type-Options: nosniff`。
- 应用必须同时校验 Token 与来源 IP 两道安全闸；任一失败统一返回 `403/40003「无权限」`，不得透露失败原因。
- `INTERNAL_API_TOKEN` 使用不 trim 的原始 UTF-8 值：不得含首尾空白且至少 32 字节，并大小写不敏感拒绝空值、`REPLACE_WITH_INTERNAL_API_TOKEN`、`CHANGE_ME`、`CHANGEME`、`DEFAULT`、`SECRET`、`TEST`。部署建议用 CSPRNG 生成至少 32 个随机字节后编码为 base64 或 hex。请求头按原始字节常量时间比较，Token 禁止进入任何日志、审计、指标或错误响应。
- `INTERNAL_ALLOWED_IPS` 与新增 `INTERNAL_TRUSTED_PROXY_IPS` 均只接受逗号分隔的精确 IP 或 CIDR，每项 trim 后解析；空项、非法项或整个列表空对 metrics 均失败关闭。
- 来源真相先解析 `RemoteAddr`。只有 `RemoteAddr` 命中 trusted proxy 时，才要求并信任代理覆盖的恰好一个合法 `X-Real-IP` 单值；缺失、空值、多值或非法均返回 403。非 trusted proxy 或直连始终只使用 `RemoteAddr`，任何来源 Header 都不能改变结果；应用永远不读取 `X-Forwarded-For`。
- 配置错误不得降级为匿名访问、本机默认、可信代理豁免或只执行单闸。反向代理只能从专用监控网络暴露路径，必须删除 XFF/Forwarded 并覆盖而非追加单值 `X-Real-IP`；网络隔离不能替代应用层双闸。

### 17.2 唯一指标族与封闭标签

- 只输出 `email_adapter_calls_total{operation,scene,result}`，不得输出其他指标族。
- `operation=query_templates|describe_template` 只允许 `scene=template_sync`；`operation=send_mail` 只允许 `scene=register|login|reset_password|bind_email|admin_verify`。
- `result` 只允许 `accepted|failed|timeout`。合法 operation/scene 配对与三个 result 组成 21 个封闭时间序列，进程启动即全部以 0 输出。
- 计数器在同一进程生命周期内单调递增；进程重启允许归零，不提供跨重启持久化保证。
- label 禁止包含邮箱、邮箱 HMAC、OTP、用户/管理员 ID、请求 ID、业务请求号、供应商 RequestId、TemplateId、错误原文、IP、Token 或其他高基数/敏感值。

### 17.3 metrics delta 书面签署记录

| 角色 | 评审范围 | 评审人 | 日期 | 结论 | 阻断项/证据链接 |
|---|---|---|---|---|---|
| QA | 方法、响应格式、Token 客观强度与常量时间比较、IP/CIDR 双列表、可信代理来源算法、封闭 21 序列、敏感扫描 | 测试工程师（QA） | 2026-07-23 | 通过 | 两项阻断修订后契约具备完整可执行测试口径且无未关闭阻断项；本结论未执行或批准 Go、反向代理、监控系统、E2E 与部署验收，以上全部留待 Phase 2 |
| 产品经理 | 内部端点边界、唯一指标族、统一错误语义、配置失败关闭、监控网络与应用双闸责任 | 产品经理（PM） | 2026-07-23 | 通过 | 确认 metrics delta 业务契约与安全边界并同意并入 Phase 1 基线；本结论不代表端点已实现、不批准上线，也不代表任何 Phase 2 代码、运行或环境能力通过 |

当前 metrics delta 结论：**Phase 1 契约与设计评审通过；Phase 2 实现待验收，不得宣称代码、指标接入或功能实现通过。**

## 18. 公开邮件发码来源 IP Phase 1 delta 门禁（QA/PM 已签署）

本节冻结公开邮件发码的来源 IP 真相，不改变 §15、§16、§17 已签署结论。新增全局 `TRUSTED_PROXY_IPS`，与 metrics 专用 `INTERNAL_TRUSTED_PROXY_IPS` 完全分离。QA 与产品经理已完成书面签署，该 delta 已并入 Phase 1 通过基线；本结论仅代表契约与设计通过，未验证 Go、Nginx、限流器、DirectMail 或 E2E。

### 18.1 冻结契约

- `TRUSTED_PROXY_IPS` 为空是合法直连模式，只使用 `RemoteAddr`。非空时只接受逗号分隔的精确 IP 或 CIDR，每项 trim 后严格解析；空项、非法 IP/CIDR 或 IPv6 zone 使应用启动失败或 ready 不通过。
- 非 trusted 连接只使用 `RemoteAddr`，忽略 `X-Real-IP`、XFF 与 Forwarded。trusted 连接必须有代理覆盖的恰好一个合法单值 `X-Real-IP`；缺失、空值、非法、逗号多值或重复 Header 固定返回 `403/40003「无权限」`，不泄露原因。
- trusted Header 拒绝发生在计数与服务入口之前：不增加 IP/账号限流计数，不写验证码或发送记录，不取发送锁，不入邮件服务，Adapter 增量为 0。
- 应用永不使用 XFF 做安全判定。运行时来源解析器不可用固定返回 `503/51003「邮件发送服务未就绪」`，同样无上述副作用。
- Nginx 必须覆盖 `X-Real-IP=$remote_addr`，删除 XFF/Forwarded，并从显式列入 `TRUSTED_PROXY_IPS` 的地址连接应用；不得用网络位置、回环地址或 `INTERNAL_TRUSTED_PROXY_IPS` 替代本规则。
- 浏览器及前端不得发送或信任来源 Header。

### 18.2 delta 书面签署记录

| 角色 | 评审范围 | 评审人 | 日期 | 结论 | 阻断项/证据链接 |
|---|---|---|---|---|---|
| QA | 配置严格解析、RemoteAddr 真相、trusted 单值 X-Real-IP、403/503 副作用、Nginx Header、十项矩阵 | 测试工程师（QA） | 2026-07-23 | 通过 | 契约具备完整可执行测试口径且无未关闭阻断项；本结论未执行或批准 Go、Nginx、限流器、DirectMail、E2E 与部署验收，以上全部留待 Phase 2 |
| 产品经理 | 全局/内部代理配置分离、公开发码 IP 限流语义、失败关闭、前端与反代责任边界 | 产品经理（PM） | 2026-07-23 | 通过 | 确认公开邮件来源 IP delta 的业务契约与安全边界并同意并入 Phase 1 基线；本结论不代表功能已实现、不批准上线，也不代表任何 Phase 2 代码、运行或环境能力通过 |

当前公开邮件来源 IP delta 结论：**Phase 1 契约与设计评审通过；Phase 2 实现待验收，不得宣称代码、反向代理或功能实现通过。**

## 19. `admin_verify` 首次配置 bootstrap delta 契约冻结

### 19.1 决策与边界

产品最终选择一次性内部后端入口 `POST /api/internal/email/bootstrap/admin-verify`，解决“普通邮件管理要求完整 MFA，而邮箱 MFA 发码又依赖已配置 `admin_verify`”的首次配置闭环。该入口只配置投递通道，不修改管理员 MFA 时间戳、不签发 Token、不发送邮件，也不允许配置其他四个场景。

四个配置键固定为 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`，不能从 metrics 或公开邮件安全配置读取、合并或回退。Token 校验复用 `INTERNAL_API_TOKEN` 的客观基线和常量时间比较实现，但配置值必须独立：至少 32 字节、无首尾空白、拒绝冻结弱占位值、至少含 8 种不同原始字节；若与已配置 `INTERNAL_API_TOKEN` 原始值相等则启动失败。Bootstrap CIDR 与平台既有 INTERNAL/TRUSTED 列表允许在各自显式配置后完全同值或重叠，同一 Nginx 可信代理网段可合理重复；但 IPv4/IPv6 零前缀网段及任何隐式回退均启动失败。Bootstrap allowed 与 bootstrap trusted-proxy 之间存在规范化后完全相同的 CIDR 条目时启动失败，不同前缀仅部分重叠允许。两个列表还必须各自规范化并求 CIDR 地址并集，任一列表由多条非零前缀覆盖完整 IPv4 或 IPv6 地址族也启动失败，例如 `0.0.0.0/1,128.0.0.0/1`、`::/1,8000::/1`；两个列表之间不跨语义合并计算全地址族并集。enabled=false 时路由全方法 404；enabled=true 时其他三项任一缺失或非法均使应用启动失败。运维请求必须同时具备独立 Token、批准来源、管理员 JWT、有效手机 MFA 和专用 `email:template:bootstrap` 权限。仅允许 POST、严格单值安全 Header、`application/json` 和只含 `provider_template_id` 的 Body，并强制合规 `Idempotency-Key`；不接受 scene、邮箱、验证码、变量映射、enabled 或 MFA 字段。`provider_template_id` 仅接受 1-64 字节 ASCII 十进制正整数；空值、全零、65 字节及以上、非数字、符号、小数、指数或任何空白均前置返回 `400/40000「请求参数错误」`，attempt 审计、Adapter 与数据库增量均为 0。

服务只对精确 TemplateId 调用 Adapter `DescribeTemplate`（对应供应商 DescTemplate）。官方响应字段 `TemplateName/TemplateStatus/TemplateText` 分别映射 Adapter Name/Status/变量，其中 JSON `TemplateName` 精确映射 `ProviderTemplate.Name`；官方定义见 [阿里云 DirectMail DescTemplate](https://help.aliyun.com/en/direct-mail/api-dm-2015-11-23-desctemplate)。Name 大小写精确等于 `molin_admin_verify_code_v1`，同时要求 Status=`approved` 与 `Code`/`ExpireMinutes` 完整。每次真实 Describe 都复用 `operation=describe_template,scene=template_sync` 指标并只增一次，不新增序列。

不同 key 可以并发完成只读 Describe；bootstrap 并发控制只在写阶段执行：单个事务 `SELECT ... FOR UPDATE` 锁定 admin_verify 行并复查 receipt，以 NULL/false/version1 CAS 和 scope 唯一约束决定唯一胜者。`admin_id` 必须同时纳入既有 `EMAIL_IDEMPOTENCY_SECRET` 的 key HMAC 作用域和 request fingerprint；只有 key hash、fingerprint 与 `completed_by` 均匹配当前管理员才可重放。即使同一管理员、同一 key、同一 fingerprint 的并发首次请求都已完成 Describe，后取得行锁者也必须返回原成功结果且 `idempotent=true`；跨管理员复用同 key 固定返回 `409/40900「管理员邮箱认证场景已完成首次配置」`，不得泄露原操作者。attempt 审计必须先成功才可外呼；镜像启用、绑定、receipt 与 result 审计同事务，result 固定以 `target_type=email_admin_verify_bootstrap_receipt`、`target_id=receipt` 内部十进制 ID 关联成功凭据，result 失败全回滚。普通 13 接口完全不变。

四键为 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`。enabled 只有配置键缺失时默认 false；字面 true/false（大小写不敏感）有效，显式空字符串或其他值必须使应用启动失败；显式 false 时全方法404，true 时其他三项任一缺失或非法启动失败。Bootstrap Token 五类请求异常统一403，Authorization标准401，Idempotency-Key异常400。`ADMIN_VERIFY_EXPIRE_HOURS<0` 无论 bootstrap 是否启用均启动失败；`=0` 只豁免历史时间过期，手机 MFA 时间戳在未来仍失败关闭且无审计、Adapter 或数据库副作用；非 admin 普通用户即使通过动态覆盖获权仍403。

成功后必须移除 enable/token 并重启确认 404；管理员仍按现有手机→邮箱验证码流程完成完整 MFA。普通 13 个 `/api/admin/email/*` 的契约、四个既有权限和完整 MFA 要求不变。邮件权限不足的冻结文案为 `403/40003「无权限」`，采用邮件专用权限包装器对齐，不全局修改其他模块历史使用的 `「无操作权限」`。

### 19.2 数据、回滚与验收状态

000056 新增专用权限及 admin 精确绑定、独立 `migration_000056_permission_ownership` 和 `email_admin_verify_bootstrap_receipts`。admin code 必须恰好一行；预存权限只有 name/resource/action 精确一致才可复用，ownership 以与 000055 同结构的一行记录预存/新增状态及最终 ID。receipt 字段、约束、partial-up/down 和未知引用断言以数据库 SSOT 为准。部署必须在 000055 完整核验后执行并核验 000056。回滚统一采用 A/B/C 矩阵：000056 未执行时走原 000055 down；000056 已执行且无成功 receipt 时依次 down 000056、down 000055；存在成功 receipt 时保留 schema 55+56、receipt、模板镜像和绑定，不执行任一 down。确需回到 55 前只能走备份恢复验证、不可变审计和 QA/PM/运维联合批准的高风险变更，解除引用后依次 down 000056、down 000055，禁止 force。

本节仅冻结产品选择后的 delta 契约与设计，不代表 Go、000056 SQL、反向代理、DirectMail、真实数据库/Redis 或 E2E 已实现或通过。实现完成后必须按 `docs/test-plan.md` 的 bootstrap 矩阵由 QA 执行，并由产品经理确认一次性、最小权限、无前端入口和回滚边界。
