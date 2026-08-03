# 短信模板管理旧测试与手册清理清单

## 1. 文档目的

本清单用于闭环短信模板管理阶段 0 的 QA-08，记录仓库中仍使用旧验证码请求字段、旧公开高权限场景或旧响应语义的测试和手册。本文只定义后续清理目标，不修改历史测试结果，不代表任何清理项或新短信测试已经执行通过。

权威入口遵守 D-96：

| 场景 | 正确发码入口 | 正确请求体 |
|---|---|---|
| 公开手机注册、登录、重置密码 | `POST /api/auth/verification-codes/phone` | `{ "phone": "...", "scene": "register|login|reset_password" }` |
| 公开邮箱注册、登录、重置密码 | `POST /api/auth/verification-codes/email` | `{ "email": "...", "scene": "register|login|reset_password" }` |
| 换绑手机号 | `POST /api/me/verification-codes/phone` | `{ "phone": "新手机号" }`，需 Bearer Token |
| 换绑邮箱 | `POST /api/me/verification-codes/email` | `{ "email": "新邮箱" }`，需 Bearer Token |
| 管理员手机二次验证 | `POST /api/admin/auth/verification-codes/phone` | 无 body，需管理员 Token 和 `user:manage` |
| 管理员邮箱二次验证 | `POST /api/admin/auth/verification-codes/email` | 无 body，需管理员 Token 和 `user:manage` |

公开端点必须拒绝 `bind_phone`、`bind_email`、`admin_verify`；公开手机/邮箱发码字段分别为 `phone` 和 `email`，不再使用通用 `target`。

## 2. 逐项清理清单

| 编号 | 文件与位置 | 当前旧内容 | 目标修改 | 执行阶段 | 当前状态 |
|---|---|---|---|---|---|
| CLN-01 | `server/cmd/apitest/main.go:79-82` | 邮箱注册发码仍提交 `target` | 改为 `email`；保留公开 `scene=register` | 阶段 1 | 待清理 |
| CLN-02 | `tests/manual-test-guide-backend-apis.md:71-86` | 手机公开发码使用 `target`，并宣称允许 `bind_phone/admin_verify` | 改为 `phone`；公开场景只列 `register/login/reset_password`；增加高权限场景被拒的反向用例 | 阶段 1 | 待清理 |
| CLN-03 | `tests/manual-test-guide-backend-apis.md:102-117` | 邮箱公开发码使用 `target`，并引用同一旧场景集合 | 改为 `email`；公开场景只列 `register/login/reset_password` | 阶段 1 | 待清理 |
| CLN-04 | `tests/manual-test-guide-backend-apis.md:249-250` | 手机登录发码示例 body 使用 `target` | 改为 `{ "phone": "...", "scene": "login" }` | 阶段 1 | 待清理 |
| CLN-05 | `tests/manual-test-guide-backend-apis.md:436-447` | 换绑手机号调用公开端点并传 `scene=bind_phone` | 改用 `POST /api/me/verification-codes/phone`，Bearer Token，body 仅含新 `phone` | 阶段 1 | 待清理 |
| CLN-06 | `tests/manual-test-guide-backend-apis.md:466-474` | 换绑邮箱调用公开端点并使用 `target/scene=bind_email` | 改用 `POST /api/me/verification-codes/email`，Bearer Token，body 仅含新 `email` | 阶段 1 | 待清理 |
| CLN-07 | `tests/manual-test-guide-backend-apis.md:488-497` | 管理员手机发码调用公开端点并传手机号与 `scene=admin_verify` | 改用 `POST /api/admin/auth/verification-codes/phone`，管理员 Token + `user:manage`，无 body | 阶段 1 | 待清理 |
| CLN-08 | `tests/report-user-profile.md:57-68` | 历史结果只写 `scene=admin_verify` 且记录明文验证码 | 保留历史证据不可改写；在文件顶部标记为旧契约历史报告，后续报告改用专属 D-96 入口并禁止记录明文验证码 | 阶段 4 | 待清理 |
| CLN-09 | `tests/report-user-profile.md:82-91,147` | 历史结果只写 `scene=bind_phone/bind_email`，未说明专属入口 | 保留历史结果；增加旧契约标记，后续回归明确使用 `/api/me/verification-codes/{phone,email}` | 阶段 4 | 待清理 |
| CLN-10 | `docs/test-plan.md §3.1` 的修改手机、修改邮箱和管理员双认证用例 | 只描述消费验证码，未显式列出 D-96 发码入口 | 执行阶段 4 回归时按本文入口准备验证码，并同时验证公开端点拒绝高权限场景 | 阶段 4 | 待执行 |

密码重置接口中的 `target/target_type` 是 `POST /api/auth/password/reset` 的合法业务字段，不属于发码字段废弃范围；清理时不得机械替换。审计日志等其他领域的 `target_type/target_id` 同样不属于本次范围。

## 3. 新增测试资产计划

### 阶段 1

- 修正认证 API 冒烟工具和人工手册中的 `phone/email` 发码字段。
- 增加公开端点拒绝 `bind_phone/bind_email/admin_verify` 的 D-96 回归。
- 增加专属换绑与管理员发码入口的鉴权、请求体和邮箱回归测试。
- 增加手机 `pending/accepted/failed`、失败不可校验、邮箱 DirectMail 独立链路和 `SMS_ENABLED=false` 测试。

### 阶段 2

- 为 `GET/POST/PUT/PATCH /api/admin/sms/*` 九个接口建立正式契约测试。
- 覆盖四权限、管理员双重认证、D-95、nullability、同步原子性、并发改绑、测试发送幂等、白名单、限流、错误映射、审计和敏感信息。
- 使用 Mock 完成稳定的自动化用例；外部条件具备后另行记录阿里云 `Code=OK` 受理证据。

### 阶段 4

- 更新或替代旧 `report-user-profile` 流程，执行五个手机场景和全部邮箱路径回归。
- 使用受控白名单执行真实手机收件验证，分别记录 Mock、阿里云受理和真实收件结论。
- 旧历史报告只增加“旧契约、不得作为当前验收依据”标记，不改写当时实际结果。

## 4. 完成判定

本清单只有在以下条件全部满足后才能标记完成：

- 所有 CLN 项已按目标入口更新或完成历史报告标记。
- 新旧请求字段的正向和反向测试均已运行，并保存实际结果。
- 九个短信管理接口和四个权限均有自动化或可重复的接口测试。
- 测试报告没有把 Mock、阿里云受理或真实收件混写为同一种“发送成功”。
- 测试日志和报告不存在验证码、完整手机号、AccessKey 或请求签名原文。
