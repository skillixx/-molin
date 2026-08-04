# SMS 模块开发规范

## 模块职责

本模块负责阿里云中国大陆验证码短信的模板只读同步、五场景绑定、本地启停、安全测试发送、脱敏发送记录及阶段 1 OTP Dispatcher。邮箱验证码不属于本模块。

## 权威数据源

- `sms_templates`：阿里云模板只读快照唯一来源。
- `sms_scene_bindings`：`register/login/reset_password/bind_phone/admin_verify` 当前绑定唯一来源。
- `sms_send_logs`：脱敏提交终态与测试发送幂等墓碑。
- 禁止新增 `SMS_TEMPLATE_CODE_*` 环境变量或代码内固定模板编码。

## 安全规则

1. `SMS_ENABLED` 默认关闭，禁止静默回退 Mock 或固定验证码。
2. 真实发送只允许由 bootstrap 显式注入 Aliyun Sender；测试使用 Mock Sender。
3. 只有固定签名、验证码类型、变量集合精确为 `code`、审核通过且本地启用的模板可发送；含额外变量的模板必须在同步、启用、绑定和发送各层失败关闭。
4. 五场景必须分别使用独立模板；启用或换绑时先锁定模板行并拒绝其他启用场景复用，停用历史共用绑定必须保持可用以支持整改。
5. 测试发送要求 `sms:template:test`、管理员双重认证、测试模式、白名单、Redis 双维限流和数据库幂等抢占。
6. 相同管理员相同 key 改参数必须冲突；不同管理员不得串用结果；并发重放只能有一次供应商调用。
7. 响应、日志、审计和数据库普通字段禁止保存验证码、完整手机号、幂等键明文、AccessKey 或供应商原始响应。
8. `accepted` 仅表示供应商受理，不得命名为送达成功。
9. 管理写操作的请求审计必须在业务调用前失败关闭；业务副作用完成后的结果审计失败必须输出安全告警，但不得把已生效操作误报为 500。

## 代码结构

- `sender/`：官方 SDK 适配、模板只读 Provider、安全错误分类。
- `service/dispatcher.go`：五业务入口发送计划及 OTP 提交。
- `service/admin_service.go`：阶段 2 管理规则、幂等和限流。
- `repository/`：同步事务、乐观锁、幂等唯一约束和管理查询。
- `handler/`、`route.go`：九个管理 API、权限/MFA 和审计。

## 修改要求

- 代码注释、提交、PR、评审和文档使用中文。
- 接口字段或错误码变化时同步 `docs/full-api-design.md` 与 `docs/frontend-api-reference.md`。
- 数据库变化提供安全 up/down，并验证全新安装及 up/down/up。
- 写操作必须保留权限、参数、并发、审计和敏感信息测试。
- 修改 Dispatcher 后必须回归手机五入口、短信失败不可验证及完整邮件验证码测试。
- 未取得明确授权不得打开短信、使用真实凭据、调用 SendSms、部署或产生费用。
