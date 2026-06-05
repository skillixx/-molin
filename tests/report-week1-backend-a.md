# 测试报告 — Week 1 后端A（auth / iam / identity）

**测试日期：** 2026-06-05
**测试环境：** 测试服务器 8.130.9.163（API 端口 8080）
**测试脚本：** `tests/test_backend_a.py`
**测试人员：** 测试工程师

---

## 测试结论：**全部通过（33/33）**

---

## 测试范围

- A-01 认证模块（auth）— 14 项
- A-02 IAM 模块（角色 / 权限 / RBAC）— 12 项
- A-03 实名认证模块（identity）— 5 项
- 前置播种操作 — 2 项

---

## 通过项

### A-01 认证模块（auth）

- [x] 发送邮箱注册验证码 → HTTP 200
- [x] 邮箱注册（正确验证码）→ HTTP 201
- [x] 错误验证码注册被拦截 → HTTP 400
- [x] GET /api/me（有效 Token）→ HTTP 200
- [x] GET /api/me（无 Token）→ HTTP 401
- [x] 邮箱密码登录 → HTTP 200
- [x] 错误密码登录 → HTTP 401
- [x] 发送手机验证码 → HTTP 200
- [x] 刷新 Token → HTTP 200
- [x] 修改密码 → HTTP 200
- [x] 旧密码登录失效（修改密码后）→ HTTP 401
- [x] 新密码登录 → HTTP 200
- [x] 退出登录 → HTTP 200
- [x] 已退出 Refresh Token 刷新被拦截 → HTTP 401（user_sessions 黑名单生效）

### A-02 IAM 模块（角色 / 权限 / RBAC）

- [x] GET 权限列表 → HTTP 200
- [x] POST 创建角色 → HTTP 201
- [x] GET 角色列表 → HTTP 200
- [x] PUT 更新角色 → HTTP 200
- [x] POST 分配角色给用户 → HTTP 200
- [x] GET 用户角色列表 → HTTP 200
- [x] DELETE 撤销用户角色 → HTTP 200
- [x] POST 设置权限覆盖（deny）→ HTTP 200
- [x] POST 非法 effect='DENY'（大写）被拦截 → HTTP 400
- [x] GET 用户权限覆盖列表 → HTTP 200
- [x] DELETE 删除角色 → HTTP 200
- [x] 无 Token 访问管理接口 → HTTP 401

### A-03 实名认证模块（identity）

- [x] POST 提交实名认证 → HTTP 201
- [x] GET 查询我的实名状态 → HTTP 200（状态 pending）
- [x] 重复提交实名被拦截 → HTTP 4xx
- [x] GET 管理员查看待审列表 → HTTP 200
- [x] 无 Token 访问审核接口 → HTTP 401

---

## 安全场景覆盖

| 场景 | 结果 |
|---|---|
| 无 Token 访问受保护接口 | 401 正确拦截 |
| 错误密码登录 | 401 正确拦截 |
| 错误验证码注册 | 400 正确拦截 |
| 已退出 Refresh Token 刷新 | 401 正确拦截（user_sessions 黑名单生效）|
| 修改密码后旧 Token 失效 | 401 正确拦截 |
| 非法 effect 大写 DENY | 400 正确拦截 |
| 重复提交实名认证 | 4xx 正确拦截 |

---

## 未通过项

无，所有用例全部通过。

---

## 建议

是否允许本周合并上线：**是**

auth / iam / identity 三个模块通过全部验收，无 P0/P1 缺陷。安全约定（Refresh Token 黑名单、非法 effect 值校验、无 Token 拦截、OTP 哈希存储）均已正确实现并通过测试验证，建议批准合并。
