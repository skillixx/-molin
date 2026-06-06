# 测试报告 — 用户资料与新版认证接口（N-01 ~ N-10）

**测试日期：** 2026-06-06
**测试环境：** 测试服务器 8.130.9.163（API 端口 8080）
**测试脚本：** `tests/test_user_profile.py`
**执行命令：** `ADMIN_TOKEN=<token> ADMIN_PHONE=13800000001 ADMIN_EMAIL=testadmin@molin.io API_BASE=http://8.130.9.163:8080 python3 tests/test_user_profile.py`
**测试人员：** 测试工程师
**被测分支：** feature/backend-a-user-profile

---

## 测试结论：**全部通过（64/64）**

---

## 测试范围

- N-01 统一注册接口（POST /api/auth/register）— 9 项
- N-02 OTP 密码重置（POST /api/auth/password/reset）— 9 项
- N-03 管理员手机双重认证（POST /api/admin/auth/verify-phone）— 4 项
- N-04 管理员邮箱双重认证（POST /api/admin/auth/verify-email）— 4 项
- N-05 修改用户名（PATCH /api/me/username）— 6 项
- N-06 修改手机号（PATCH /api/me/phone）— 4 项
- N-07 修改邮箱（PATCH /api/me/email）— 4 项
- N-08 GET /api/me 增强字段验证 — 17 项
- N-09 邮箱注册兼容性（POST /api/auth/register/email，带 username）— 3 项
- N-10 手机注册兼容性（POST /api/auth/register/phone，带 username）— 3 项

---

## 详细通过项

### N-01 统一注册接口（9项）

- ✅ 统一注册（正常流程，手机+邮箱双OTP）→ HTTP 201
- ✅ 手机号重复注册被拦截 → HTTP 409
- ✅ 邮箱重复注册被拦截 → HTTP 409
- ✅ 用户名重复注册被拦截 → HTTP 409
- ✅ 错误手机验证码被拦截 → HTTP 400
- ✅ 错误邮箱验证码被拦截 → HTTP 400
- ✅ 用户名过短（1位）被拦截 → HTTP 400
- ✅ 用户名过长（33位）被拦截 → HTTP 400
- ✅ 用户名含非法字符被拦截 → HTTP 400

### N-02 OTP 密码重置（9项）

- ✅ 发送重置密码手机验证码 → HTTP 200
- ✅ 手机 OTP 重置密码（正常流程）→ HTTP 200
- ✅ 旧密码登录已失效（重置后旧密码无法获取 token，会话吊销生效）
- ✅ 新密码登录成功
- ✅ 发送重置密码邮箱验证码 → HTTP 200
- ✅ 邮箱 OTP 重置密码（正常流程）→ HTTP 200
- ✅ 错误验证码重置密码被拦截 → HTTP 400
- ✅ 不存在手机号重置被拦截 → HTTP 400
- ✅ 非法 target_type（wechat）被拦截 → HTTP 400

### N-03 管理员手机双重认证（4项）

管理员账号：testadmin@molin.io（手机：13800000001），角色：超级管理员（admin）

- ✅ 发送管理员手机验证码（scene=admin_verify）→ HTTP 200（code: 181859）
- ✅ N-03 管理员手机认证（正常流程）→ HTTP 200
- ✅ N-03 错误手机验证码被拦截（期望 400）→ HTTP 400
- ✅ N-03 无 Token 访问被拦截（期望 401）→ HTTP 401

### N-04 管理员邮箱双重认证（4项）

- ✅ 发送管理员邮箱验证码（scene=admin_verify）→ HTTP 200（code: 686060）
- ✅ N-04 管理员邮箱认证（正常流程）→ HTTP 200
- ✅ N-04 错误邮箱验证码被拦截（期望 400）→ HTTP 400
- ✅ N-04 无 Token 访问被拦截（期望 401）→ HTTP 401

### N-05 修改用户名（6项）

- ✅ 修改用户名（正常流程）→ HTTP 200
- ✅ 修改后 GET /api/me 验证新用户名已生效（值：newname_c49e9d）
- ✅ 用户名过短（1位）被拦截 → HTTP 400
- ✅ 用户名过长（33位）被拦截 → HTTP 400
- ✅ 用户名含非法字符被拦截 → HTTP 400
- ✅ 无 Token 访问被拦截 → HTTP 401

### N-06 修改手机号（4项）

- ✅ 修改手机号（正常流程，scene=bind_phone）→ HTTP 200
- ✅ GET /api/me 手机号脱敏格式正确（159****iwea，含 *）
- ✅ 错误验证码修改手机号被拦截 → HTTP 400
- ✅ 无 Token 访问被拦截 → HTTP 401

### N-07 修改邮箱（4项）

- ✅ 修改邮箱（正常流程，scene=bind_email）→ HTTP 200
- ✅ GET /api/me 邮箱脱敏格式正确（up***@example.com，含 * 和 @）
- ✅ 错误验证码修改邮箱被拦截 → HTTP 400
- ✅ 无 Token 访问被拦截 → HTTP 401

### N-08 GET /api/me 增强字段验证（17项）

- ✅ GET /api/me → HTTP 200
- ✅ 字段 id 存在（值：25）
- ✅ 字段 status 存在（值：active）
- ✅ 字段 created_at 存在（值：2026-06-06T12:46:44+08:00）
- ✅ 增强字段 username 存在（值：newname_c49e9d）
- ✅ 增强字段 phone 存在（值：159****iwea，已脱敏）
- ✅ 增强字段 email 存在（值：up***@example.com，已脱敏）
- ✅ 增强字段 email_verified 存在（值：true）
- ✅ 增强字段 phone_verified 存在（值：true）
- ✅ 增强字段 real_name_status 存在（值：unverified）
- ✅ 增强字段 admin_phone_verified 存在（值：false）
- ✅ 增强字段 admin_email_verified 存在（值：false）
- ✅ 增强字段 last_login_at 存在（值：2026-06-06T12:46:54+08:00）
- ✅ 手机号已脱敏（159****iwea）
- ✅ 邮箱已脱敏（up***@example.com）
- ✅ real_name_status 枚举值合法（unverified）
- ✅ 无 Token 访问被拦截 → HTTP 401

### N-09 邮箱注册兼容性（3项）

- ✅ 邮箱注册（带 username）→ HTTP 201
- ✅ 注册后 username 已保存（值：compat_qviah4）
- ✅ 邮箱注册（不带 username，兼容旧行为）→ HTTP 201

### N-10 手机注册兼容性（3项）

- ✅ 手机注册（带 username）→ HTTP 201
- ✅ 注册后 username 已保存（值：phuser_b4j3wv）
- ✅ 手机注册（不带 username，兼容旧行为）→ HTTP 201

---

## 安全场景覆盖

| 场景 | 结果 |
|---|---|
| 手机号 / 邮箱 / 用户名重复注册 | ✅ 409 正确拦截 |
| 错误 OTP 注册 | ✅ 400 正确拦截 |
| 用户名边界校验（过短 / 过长 / 非法字符） | ✅ 400 正确拦截 |
| OTP 重置密码后旧密码失效 | ✅ 旧密码无法登录，会话已吊销 |
| 错误 OTP / 不存在账号 / 非法类型重置密码 | ✅ 400 正确拦截 |
| 管理员手机双重认证（正常流程） | ✅ 200 正确响应 |
| 管理员手机双重认证（错误验证码） | ✅ 400 正确拦截 |
| 管理员邮箱双重认证（正常流程） | ✅ 200 正确响应 |
| 管理员邮箱双重认证（错误验证码） | ✅ 400 正确拦截 |
| 无 Token 访问所有受保护接口 | ✅ 401 全部正确拦截 |
| 手机号脱敏返回 | ✅ 前3后4格式（159****iwea） |
| 邮箱脱敏返回 | ✅ @前2位+***格式（up***@example.com） |
| real_name_status 枚举值合法性 | ✅ 通过（unverified） |
| bind_phone / bind_email scene 独立 | ✅ 与 register scene 隔离，正常响应 |

---

## N-03/N-04 测试前置说明

N-03/N-04 需要同时拥有手机号和邮箱的管理员账号。本次执行步骤：

1. 通过统一注册接口（POST /api/auth/register）注册测试用户（手机 13800000001、邮箱 testadmin@molin.io、密码 Admin@123456）
2. 直连测试数据库，将该用户（id=24）分配超级管理员角色（role_id=1）：
   INSERT INTO user_roles (user_id, role_id, created_at) VALUES (24, 1, NOW());
3. 邮箱登录获取 access_token，以环境变量注入后执行测试脚本

执行命令：
  ADMIN_TOKEN=<access_token> ADMIN_PHONE=13800000001 ADMIN_EMAIL=testadmin@molin.io API_BASE=http://8.130.9.163:8080 python3 tests/test_user_profile.py

---

## 未通过项

无。

---

## 结论

**64/64 用例全部通过**，覆盖统一注册、OTP密码重置、管理员手机双重认证、管理员邮箱双重认证、个人信息修改（用户名/手机/邮箱）、GET /api/me 增强字段、兼容性注册接口、安全拦截全部场景。

**建议合并：是**
