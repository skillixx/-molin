# 阿里云短信验证码阶段 1 功能说明

## 1. 功能定位

阶段 1 为墨灵手机号验证码建立安全的数据底座和关闭态发送链路。它让系统具备“数据库场景绑定 → 验证码 pending → Sender 提交 → sent/failed → 脱敏日志”的内部能力，但默认保持 `SMS_ENABLED=false`，不代表真实业务短信已经上线。

## 2. 使用角色

- 终端用户：通过现有注册、登录、重置密码和换绑手机号入口申请验证码。
- 管理员：通过现有管理员手机双重认证入口申请验证码。
- 运维：维护短信开关、阿里云凭证、固定签名、独立手机号 HMAC 密钥和测试白名单。
- 测试工程师：使用数据库 fixture 和 Mock 验证五场景、状态机、错误映射和敏感信息边界。

阶段 1 不提供短信模板管理页面、管理 API、模板同步、场景绑定管理或管理员测试发送。

## 3. 五个手机号场景

| 场景 | 入口 | 用途 |
|---|---|---|
| `register` | `POST /api/auth/verification-codes/phone` | 用户注册 |
| `login` | `POST /api/auth/verification-codes/phone` | 手机验证码登录 |
| `reset_password` | `POST /api/auth/verification-codes/phone` | 找回密码 |
| `bind_phone` | `POST /api/me/verification-codes/phone` | 登录用户换绑手机号 |
| `admin_verify` | `POST /api/admin/auth/verification-codes/phone` | 管理员手机双重认证 |

每个场景只读取自己的数据库绑定。模板编码、正文和审核状态不得来自 `SMS_TEMPLATE_CODE_*` 环境变量。

## 4. 核心业务规则

1. 短信默认关闭；关闭时手机发码返回 `503/50300`，不生成验证码、不调用供应商。
2. 配置不完整、测试白名单为空、手机号不在白名单或场景无有效绑定时统一 fail-closed。
3. 手机验证码先以 `pending` 保存；供应商受理后变为 `sent`，拒绝、超时或网络错误后变为 `failed`。
4. 只有 `sent`、未过期、未使用且哈希匹配的手机验证码可以被原子消费。
5. 手机验证码明文只在进程内短暂传给 Sender，任何环境的 HTTP 响应、日志和数据库都不返回或保存明文。
6. 邮箱验证码保持 DirectMail 独立流程，状态统一为 `accepted/failed`，不调用短信 Sender。
7. `accepted` 只表示供应商受理，不表示运营商送达或用户收件。

## 5. 响应与错误

- 手机发码成功：HTTP 200，统一响应 `{sent,expires_in,business_request_id,submit_status}`；不返回验证码或供应商原始响应。
- 功能关闭、配置缺失或场景未绑定：HTTP 503，业务码 `50300`。
- 供应商拒绝、超时或网络异常：HTTP 502，业务码 `50200`。
- 手机号响应永不包含 `code`、AccessKey、完整手机号或供应商原始响应。

## 6. 当前边界

截至阶段 1，真实发送开关保持关闭，仅允许使用 Mock 证明内部状态流转。真实阿里云同步、受理、白名单收件、管理后台和生产灰度均须等待阶段 2 及其独立验收。
