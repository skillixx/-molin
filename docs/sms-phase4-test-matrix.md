# 阿里云短信验证码阶段 4 测试矩阵

## 1. 审计基线与证据口径

- 审计基线：`origin/main@2c2fcbebb940cb559ade68dbf8ebf96e9880412a`。
- 阶段 3 合并提交：`e7f29d51a95d8903351e139db28d4d18b873de04`，已确认是当前基线祖先。
- 审计环境：Windows 本地独立工作树，`SMS_ENABLED=false`，未注入阿里云凭证，未连接测试服，未发送真实短信。
- 数据库版本基线：代码最新 migration 为 `000063`；短信结构由 `000058`、`000059` 提供。本轮 4A 尚未执行隔离 MySQL。
- 证据等级依次记录为：代码存在、单元测试、Mock、隔离数据库、HTTP E2E、阿里云受理、真实收件、验证码消费、业务最终状态。未实际执行的等级不得标记通过。

## 2. 五场景业务矩阵

| 场景 | 页面入口 | 发码 API | 消费 API | 身份与权限 | 模板场景 | 关键表 | 成功状态变化 | 主要失败码 | 4A 已有证据 | 4A 缺失证据 |
|---|---|---|---|---|---|---|---|---|---|---|
| `register` | 用户端 `/register` | `POST /api/auth/verification-codes/phone`，body `{phone,scene:register}` | `POST /api/auth/register` | 公开入口；注册同时要求手机和邮箱验证码 | `register` | `verification_codes`、`sms_scene_bindings`、`sms_templates`、`sms_send_logs`、`users`、`sessions` | 手机 OTP 原子消费；邮箱 OTP 消费；创建用户与会话 | `40000`、`40900`、`42900`、`50200`、`50300` | 路由/DTO/Service/Dispatcher 存在；五模板 Mock 选择通过；关闭态 HTTP 历史通过；阶段 2 阿里云受理和真实收件 | 隔离 MySQL 下真实业务入口消费、重放、并发、用户和会话最终状态 |
| `login` | 用户端 `/login` 手机号登录 | `POST /api/auth/verification-codes/phone`，body `{phone,scene:login}` | `POST /api/auth/login/phone` | 公开入口；账号需存在且启用 | `login` | 上述短信四表、`users`、`sessions`、`user_login_logs` | OTP 原子消费；创建会话；登录账号脱敏落库 | `40000`、`40404`、`423/42901`、`42900`、`50200`、`50300` | 登录错误计数代码存在；Sender Mock 与模板选择通过；阶段 2 阿里云受理和真实收件 | 隔离 MySQL/Redis CI 执行状态 |
| `reset_password` | 用户端 `/reset-password` | `POST /api/auth/verification-codes/phone`，body `{phone,scene:reset_password}` | `POST /api/auth/password/reset` | 公开入口；目标账号需存在 | `reset_password` | 上述短信四表、`users`、`sessions` | OTP 原子消费；密码哈希更新；全部会话撤销 | `40000`、`40100`、`42900`、`50200`、`50300` | 服务和页面存在；Sender Mock 与模板选择通过；阶段 2 阿里云受理和真实收件 | 密码变化、旧会话吊销、错误/过期/重放/并发 HTTP E2E |
| `bind_phone` | 用户端 `/profile` 手机号换绑 | `POST /api/me/verification-codes/phone`，body `{phone}` | `PATCH /api/me/phone` | Bearer Token；按用户限流 | `bind_phone` | 上述短信四表、`users`、`audit_logs` | OTP 原子消费；手机号更新并验证；旧手机 MFA 时间清空 | `40000`、`40001`、`40900`、`42900`、`50200`、`50300` | D-96 路由和原子联系方式更新代码存在；阶段 2 真实换绑/MFA 历史证据 | 当前提交隔离 MySQL/HTTP E2E、并发换绑、审计脱敏 |
| `admin_verify` | 管理后台 `/verify` | `POST /api/admin/auth/verification-codes/phone`，空 body | `POST /api/admin/auth/verify-phone` | Bearer Token + `user:manage`；本入口用于建立手机 MFA | `admin_verify` | 上述短信四表、`users`、`audit_logs` | OTP 原子消费；`admin_phone_verified_at` 更新；脱敏审计 | `40000`、`40001`、`40003`、`42900`、`50200`、`50300` | D-96 权限路由、MFA 服务、前端契约测试和阶段 2 真实收件存在 | 隔离 MySQL/Redis CI 执行状态 |

## 3. 横向安全与可靠性矩阵

| 编号 | 验证项 | 当前实现/证据 | 4A 结论 | 阶段 4 动作 |
|---|---|---|---|---|
| P4-01 | 验证码只存哈希 | `VerificationService` 使用 SHA-256，模型只持久化 `code_hash` | 代码与单测证据存在 | 增加隔离数据库敏感数据断言 |
| P4-02 | `pending → accepted/failed` | 手机发送先建 pending，供应商结果后收敛 | Mock 单测通过 | 增加发送日志失败及状态一致性集成测试 |
| P4-03 | 单次、过期与并发消费 | 单条条件 UPDATE 限定 accepted、未使用、未过期 | SQL/仓储单测存在 | 增加真实 MySQL 并发消费测试及 `-race` |
| P4-04 | 最大错误次数 | 首轮审计发现缺口；当前已改为校验前 Redis 原子取得资格，手机号+场景十分钟最多五次，并发不能越界 | **已修复，真实 Redis CI 待执行** | 单元测试已通过；16 路 Redis 并发用例等待 CI |
| P4-05 | 发码频率限制 | 已补手机号 HMAC+场景 60 秒原子冷却；公开入口 IP 桶改用可信来源解析器，禁止 XFF 伪造拆桶 | **已修复，真实 Redis HTTP CI 待执行** | 单元测试已通过；重复发码和伪造头 10/11 边界等待 CI |
| P4-06 | 普通 OTP 重复发码契约 | 公开/认证态 OTP 不接受客户端幂等键；每次获准发送由服务端生成唯一 `business_request_no`，同手机号+场景 60 秒内重复请求返回 429 | **契约已明确；HTTP 429 CI 用例待执行** | 管理端测试发送的 `Idempotency-Key` 另由既有幂等/并发测试覆盖，不把两类语义混写 |
| P4-07 | 五场景独立模板 | `Dispatcher.Prepare` 读取数据库绑定；阶段 2 已有五个独立模板 | Mock 与历史部署证据存在 | 当前提交隔离数据库重新核验五个模板 ID 均不同 |
| P4-08 | 模板异常与供应商错误 | 未绑定、未审核、停用、变量不符失败关闭；Sender 归类超时/限流/签名/模板/账户/网络 | 七类表驱动单元测试已通过 | 隔离 HTTP/CI 状态待执行 |
| P4-09 | `SMS_ENABLED=false` | Dispatcher 在准备阶段拒绝且不建 OTP、不调用 Sender | 单元测试和阶段 1 HTTP 历史证据存在 | 当前提交 HTTP E2E 复验 |
| P4-10 | 邮箱与短信隔离 | 手机和邮箱显式分支 | 单元测试存在 | 全量邮箱回归并检查短信 Sender 调用增量为 0 |
| P4-11 | 敏感信息 | 发送日志保存脱敏手机号和 HMAC；响应不含 OTP | 局部单测存在 | 对响应、数据库、日志、审计、前端构建产物执行扫描 |
| P4-12 | 管理 API 权限/MFA | 九接口、四权限、手机+邮箱 MFA | 阶段 2/3 自动化和部署历史证据存在 | 当前提交 HTTP 权限矩阵及浏览器复验 |
| P4-13 | 前端重复点击/倒计时 | 主要页面有 sending 锁和 60 秒倒计时 | 代码及局部契约测试 | 补四业务页面行为测试与浏览器验证 |
| P4-14 | 响应式与五态 | 管理后台阶段 3 已验证；用户端各认证页面具备移动样式 | 历史 Mock 证据 | 当前提交 1440/1024/768/390 浏览器复验 |
| P4-15 | 环境隔离 | 示例配置默认关闭；阶段 2 窗口已恢复关闭 | 历史证据 | 本地静态核验；真实测试服状态需单独只读授权/凭证 |

## 4. 三十项强制覆盖映射

下表把 Goal 的 30 项要求映射到可执行行为证据。`本地通过` 表示相关单元/Mock/契约测试已执行；`CI 待执行`
表示用例已编译，但必须等 PR 的 MySQL 8、Redis 7、Linux race 结果，不能提前标记通过。

| # | 强制验证项 | 主要行为证据 | 当前状态 |
|---:|---|---|---|
| 1 | 五场景正常流程 | `TestPhoneCodeFiveScenesUseIndependentDatabaseBindings`；`TestSMSPhase4FiveSceneBusinessE2E` | Mock 本地通过；MySQL/HTTP CI 待执行 |
| 2 | 错误验证码 | 五场景 HTTP E2E 每个消费入口先提交错误码 | CI 待执行 |
| 3 | 验证码过期 | `TestVerificationExpiryBoundaryIsStrict`、仓储 accepted/expiry 条件更新 | 本地通过 |
| 4 | 验证码重复使用 | 五场景 HTTP E2E 成功后逐场景重放 | CI 待执行 |
| 5 | 最大错误次数 | `TestPhase4PhoneOTPBlocksAfterFiveWrongAttemptsAndClearsOnSuccess`、`TestRedisOTPGuardIntegration` | 单测通过；Redis CI 待执行 |
| 6 | 发码频率限制 | `TestPhase4PhoneSendRateLimitRejectsBeforeOTPAndProvider`、重复发码 HTTP 429 | 单测通过；HTTP CI 待执行 |
| 7 | 单号码、单 IP、单场景限流 | HMAC+scene OTP Guard；可信来源解析器；两条公开路由来源失败前置测试；轮换 XFF/X-Real-IP 的 `assertPhase4IPRateLimit` 真实 Redis 十次/十一次边界 | 单测通过；Redis CI 待执行 |
| 8 | 并发发码 | `TestRedisOTPGuardIntegration` 16 路并发只放行一次 | CI 待执行 |
| 9 | 并发消费同一验证码 | `assertPhase4ConcurrentConsumption` 16 路 MySQL 并发恰好一次成功 | CI 待执行 |
| 10 | 同一业务请求幂等 | 普通 OTP 由服务端生成唯一业务号并以 60 秒冷却拒绝重复；管理测试发送由 `TestAdminTestSendConcurrentReplayCallsProviderOnce` 验证同一幂等键 | 本地通过；普通 OTP HTTP 429 CI 待执行 |
| 11 | 模板未绑定 | `TestPrepareRejectsEveryUnavailableBindingState/场景未绑定` | 本地通过 |
| 12 | 模板未审核 | `TestPrepareRejectsEveryUnavailableBindingState/模板未审核` | 本地通过 |
| 13 | 模板本地停用 | `TestPrepareRejectsEveryUnavailableBindingState/模板本地停用` | 本地通过 |
| 14 | 场景停用 | `TestPrepareRejectsEveryUnavailableBindingState/场景已停用` | 本地通过 |
| 15 | 模板变量不匹配 | `TestPrepareRejectsTemplateWithExtraVariable`、`TestUpsertAdminSceneBindingRejectsTemplateWithExtraVariable` | 本地通过 |
| 16 | 五个启用场景误用同一模板 | `TestSMSAdminSceneConcurrentBindingRejectsSharedTemplate`、仓储唯一占用测试、E2E 五个不同 template_id | 本地通过；数据库 CI 待执行 |
| 17 | 阿里云超时 | `TestAliyunSenderClassifiesSDKTransportErrors`、手机发送失败不可消费表驱动用例 | 本地通过 |
| 18 | 阿里云限流 | `TestAliyunSenderClassifiesAllPhase4ProviderFailures/供应商限流` | 本地通过 |
| 19 | 模板错误 | 同上 `/模板错误`，并验证 OTP 收敛为 failed | 本地通过 |
| 20 | 签名错误 | 同上 `/签名错误`，并验证 OTP 收敛为 failed | 本地通过 |
| 21 | 账户异常 | 同上 `/账户余额异常`，并验证 OTP 收敛为 failed | 本地通过 |
| 22 | 网络中断 | Sender SDK 网络分类和手机发送失败不可消费用例 | 本地通过 |
| 23 | 普通 403 不误导到 MFA | `admin-verification-contract.test.mjs` 对普通权限 403 返回 false | 本地通过 |
| 24 | 403/40031 正确进入 MFA | `TestAllSMSAdminRoutesRejectAuthPermissionAndMFABypass` 与前端冻结三元组契约 | 本地通过 |
| 25 | `SMS_ENABLED=false` 不调用供应商 | `TestDisabledSMSDoesNotCreateCodeOrCallSender`、`TestAdminTestSendFailsClosedWhenSMSDisabled` | 本地通过 |
| 26 | 邮箱链路不被短信 Sender 接管 | `TestEmailCodeRemainsIndependentFromSMSDispatcher` 及邮件全量回归 | 本地通过 |
| 27 | 发送失败后手机验证码不可用 | `TestPhoneCodeFailureRemainsUnusable` 七类失败表驱动断言 | 本地通过 |
| 28 | 响应、日志、审计、前端无敏感信息 | 哈希/脱敏仓储断言、`TestProviderErrorDoesNotExposeRawMessage`、登录日志专项测试、差异扫描 | 本地通过；数据库 CI 复核待执行 |
| 29 | 测试/生产配置隔离 | 两个 `vite-proxy-isolation` 测试；示例配置默认关闭；本轮不注入阿里云凭证 | 本地通过 |
| 30 | 五场景分别读取独立模板 | Dispatcher 五场景测试与 MySQL `COUNT(DISTINCT template_id)=5` | Mock 本地通过；MySQL CI 待执行 |

第 10 项的契约边界必须保留：公开及登录态业务发码 API 不接收客户端 `business_request_id` 或幂等键，不能把
服务端生成的追踪号伪装成客户端幂等能力；重复请求由手机号 HMAC+场景冷却返回 429。只有管理端测试发送 API
接受 `Idempotency-Key`，并以数据库唯一约束保证并发幂等。

## 5. 阶段 4A 结论

本矩阵第 2 节和首轮缺口判断冻结记录 4A 审计基线；第 3 节“当前实现/结论”已随修复更新。手机号+场景冷却、
并发错误次数硬边界、管理员手机四字段受理校验和管理端环境隔离均已补齐本地测试。五场景 HTTP 用例现覆盖错误码、
成功、重放、未登录、缺权限及重复发码 429，但真实 MySQL/Redis 和 Linux race 仍待 CI，不能提前写成通过。
普通业务发码的 `business_request_id` 由服务端生成并用于追踪，不是客户端幂等键；管理端测试发送的
`Idempotency-Key` 幂等由既有并发测试覆盖，两类契约不得混写。
