# 阿里云短信验证码阶段 4 开发说明

## 1. 范围与基线

- 分支：`codex/aliyun-sms-phase4-e2e-acceptance`
- 独立工作树：`D:\molingproject\molin-sms-phase4`
- 开发基线：`origin/main@2c2fcbebb940cb559ade68dbf8ebf96e9880412a`
- 阶段 3 合并提交：`e7f29d51a95d8903351e139db28d4d18b873de04`，已确认是基线祖先。
- 数据库结构：复用 `000058`、`000059`；本阶段不新增 migration。
- 运行门禁：`SMS_ENABLED=false` 为默认值。本阶段未部署、未连接阿里云、未修改白名单、未发送真实短信。

该 `codex/` 分支名由本次用户 Goal 明确指定，作为仓库通用 feature 分支示例的任务级例外；不得擅自改名或
迁移到其他分支。推送和创建 PR 仍需用户单独明确授权。

阶段 4 只处理五个手机验证码场景的全链路验收和验收中发现的缺陷，不进入阶段 5。

## 2. 修复内容

### 2.1 手机号与场景发码门禁

新增 `RedisOTPGuard`，按 `手机号 HMAC + scene` 建立 60 秒冷却。计数通过 Lua 原子执行
`INCR + PEXPIRE`，并发发码只能有一个请求通过。Redis 不可用、客户端为空或 HMAC 密钥不足时失败关闭，
不得继续生成验证码或调用短信 Sender。

完整手机号不进入 Redis key；key 只包含场景和 HMAC 摘要。

### 2.2 验证码最大错误次数

同一手机号和场景在十分钟内最多记录五次错误校验。达到上限后继续返回统一的“验证码错误或已过期”，
避免向调用方泄漏账号锁定状态。验证码成功消费后清除对应失败计数。

验证码消费仍由 MySQL 单条原子 `UPDATE` 完成；并发消费同一个验证码只能恰好一次成功。

### 2.3 错误契约

- 发码冷却超限：`429/42900`。
- 发码前 Redis 门禁不可用：`503/50300`。
- 消费前 Redis 门禁不可用：失败关闭并统一返回 `400/40000` 验证码错误。
- 阿里云超时、限流、签名、模板、账户、网络及普通拒绝：内部保留安全分类，对业务统一返回
  `502/50200`，对应验证码转为 `failed` 且不能消费。

### 2.4 公开入口可信来源 IP

手机公开发码和密码重置不再使用可被客户端伪造的 `X-Forwarded-For` 生成 Redis 桶。两者复用全局
`TRUSTED_PROXY_IPS` 解析器：直连或非可信代理只使用 `RemoteAddr`；可信代理只接受恰好一个合法
`X-Real-IP`。来源异常在限流与业务处理前失败关闭，轮换 XFF/X-Real-IP 不能拆分直连 IP 桶。

### 2.5 管理后台环境隔离

管理后台 Vite 代理不再硬编码共享测试服务器。默认目标改为 `http://127.0.0.1:8080`；只有开发者在
不提交的 `.env.local` 中显式配置 `VITE_API_PROXY_TARGET` 才能选择远端环境，并限制协议为 HTTP/HTTPS。

### 2.6 独立审查修复

- 管理员手机发码拆分手机/邮箱响应语义，手机必须通过 `sent/expires_in/business_request_id/submit_status`
  四字段校验后才显示成功和启动倒计时。
- 错误次数从“先查询、失败后递增”改为数据库校验前 Redis 原子取得尝试资格，16 路并发最多五路进入校验。
- 五场景 CI E2E 增加错误码、业务 HTTP 重放、未登录、缺权限和重复发码 429；失败日志只输出布尔、长度和状态。
- 换绑、管理员手机 MFA、重置密码写入脱敏 auth 审计；手机号/邮箱登录账号在写入登录日志前即脱敏，
  CI 断言数据库不存在完整手机号。
- 用户控制台补充本地代理隔离防回归测试。
- QA 收尾复核发现手机 IP 桶曾信任可伪造 XFF；已改为可信来源解析器，并补来源异常零副作用及真实 Redis
  轮换伪造头十次/十一次边界测试。

## 3. 关键代码

| 文件 | 作用 |
|---|---|
| `server/internal/modules/sms/service/otp_guard.go` | Redis 原子发码冷却与错误次数门禁 |
| `server/internal/modules/auth/service/verification_service.go` | 在验证码生成前限流，在消费前执行错误次数保护 |
| `server/internal/bootstrap/app.go` | 短信启用时装配 Redis 门禁 |
| `server/internal/modules/auth/handler/auth_handler.go` | 将服务层限流映射为 `429/42900` |
| `server/internal/middleware/ratelimit.go` | 公开验证码入口使用可信来源 IP，禁止 XFF 伪造拆分限流桶 |
| `server/migrations/sms_phase4_e2e_mysql_test.go` | CI 隔离 MySQL/Redis 五场景 HTTP+Mock E2E |
| `.github/workflows/ci.yml` | 在 MySQL/Redis/race 之外显式执行两端短信、MFA、邮件回归与代理隔离契约测试 |
| `web/admin-console/vite.config.ts` | 管理后台开发环境隔离 |

## 4. 五场景状态变化

| 场景 | 成功消费后的核心状态 |
|---|---|
| `register` | 新建用户，手机和邮箱均验证，返回登录令牌 |
| `login` | 创建登录会话并返回访问令牌 |
| `reset_password` | 更新密码哈希并吊销全部有效刷新会话 |
| `bind_phone` | 更新手机号并置 `phone_verified=true`，清空旧手机管理员 MFA |
| `admin_verify` | 写入当前管理员手机 MFA 时间戳 |

所有手机验证码只保存 SHA-256 哈希；发送日志只保存脱敏手机号和 HMAC。每个场景读取自己的启用绑定，
五个启用场景不得复用同一个模板。

## 5. 回滚

本阶段没有 migration。应用回滚时回退阶段 4 代码即可；`SMS_ENABLED` 保持 `false` 时不会产生新短信提交。
若 Redis 门禁导致异常，应先确认 Redis 和 HMAC 配置，不得通过删除门禁或返回明文验证码规避问题。
