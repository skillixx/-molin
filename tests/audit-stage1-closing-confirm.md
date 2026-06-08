# 第一阶段收尾确认 — `user:manage` 权限根因修复最终验证

**测试时间**：2026-06-08
**测试环境**：测试服务器 `8.130.9.163:8080`（API + MySQL 13306）
**关联 commit**：`d5115c8`（migration `000012_seed_user_manage_permission`，已部署并执行）

## 验证目标

确认一个被分配了系统中**真实 `admin` 角色**（`role_id=1`，`roles.code='admin'`）的全新账号，
**无需任何手动权限播种**，即可"开箱即用"调用封禁接口 `PATCH /api/admin/users/:id/status`。

## 前置确认：admin 角色权限绑定现状

```sql
-- role_permissions 中 role_id=1 已含 user:manage（permission_id=12, 绑定记录 id=13）
SELECT id, role_id, permission_id FROM role_permissions WHERE role_id = 1;
-- ... 共 12 条，含 id=13 -> permission_id=12 (user:manage)

SELECT p.id, p.code FROM permissions p
JOIN role_permissions rp ON rp.permission_id = p.id
WHERE rp.role_id = 1 AND p.code = 'user:manage';
-- id=12, code='user:manage'  ✓ 已正式绑定到 admin 角色
```

确认 migration `000012_seed_user_manage_permission` 已生效，`admin` 角色天然拥有 `user:manage` 权限，无需额外播种。

## 测试步骤与结果

### 1. 注册三个全新测试账号（通过正常注册流程，邮箱验证码由 dev 模式接口直接返回）

| 账号 | user_id | 用途 |
|---|---|---|
| closing-admin-1780887611@molin.io | 63 | 待绑定真实 admin 角色，作为操作者 |
| closing-target-1780887611@molin.io | 64 | 被封禁对象 |
| closing-normal-1780887611@molin.io | 65 | 反向验证用的普通账号（不绑定 admin） |

### 2. 唯一的 SQL 操作：将 user 63 直接绑定到系统已存在的真实 admin 角色

```sql
INSERT INTO user_roles (user_id, role_id) VALUES (63, 1);
-- 结果：id=50, user_id=63, role_id=1, created_at=2026-06-08 03:01:10
```

**未做任何其它操作**：未新建角色、未新建权限码、未手动 INSERT permissions / role_permissions 记录。
验证后复查 `role_permissions WHERE role_id=1` 仍为原有 12 条记录，无任何新增。

### 3. 用 admin 角色账号（user 63）调用封禁接口

```
PATCH /api/admin/users/64/status   {"status":"disabled","reason":"closing-confirm-test"}
→ HTTP 200  {"code":0,"message":"ok","data":"updated"}
```

封禁接口直接调用成功，**无任何 403/40003 权限拦截**——admin 角色"天然"具备该权限。

### 4. 验证封禁立即生效

```
GET /api/me（user 64 旧 access_token）
→ HTTP 401  {"code":40101,"message":"账号已被封禁"}

POST /api/auth/refresh（user 64 旧 refresh_token）
→ HTTP 401  {"code":40001,"message":"凭证无效或已过期"}
```

被封禁用户的 access token 与 refresh token 均立即失效，符合预期。

### 5. 解封并验证恢复正常

```
PATCH /api/admin/users/64/status   {"status":"active","reason":"closing-confirm-unban"}
→ HTTP 200  {"code":0,"message":"ok","data":"updated"}

POST /api/auth/login/email（user 64 重新登录）→ HTTP 200，成功获取新 token
GET /api/me（新 token）→ HTTP 200  {"status":"active", ...}
```

解封后用户可正常重新登录，状态恢复 `active`。

### 6. 反向验证：非 admin 账号调用接口被正确拦截

```
PATCH /api/admin/users/64/status（user 65，普通账号，无 admin 角色）
→ HTTP 403  {"code":40003,"message":"无操作权限"}

PATCH /api/admin/users/64/status（无 Token）
→ HTTP 401  {"code":40001,"message":"未登录"}
```

权限边界正确：未绑定 admin 角色的账号仍被 403 拦截，无 token 请求被 401 拦截。

## 测试用例汇总

| # | 用例 | 期望 | 实际 | 结论 |
|---|---|---|---|---|
| 1 | 真实 admin 角色账号调用封禁接口（无手动播种权限码）| 200 | 200 | 通过 |
| 2 | 被封禁用户旧 access_token 访问 | 401 / 40101 | 401 / 40101 | 通过 |
| 3 | 被封禁用户旧 refresh_token 刷新 | 401 | 401 / 40001 | 通过 |
| 4 | 解封后用户恢复正常登录与访问 | 200 / status=active | 200 / status=active | 通过 |
| 5 | 非 admin 账号调用封禁接口 | 403 / 40003 | 403 / 40003 | 通过 |
| 6 | 无 Token 调用接口 | 401 | 401 / 40001 | 通过 |

**全部 6 项验证通过，0 缺陷。**

## 数据库改动核查（确保未污染权限体系）

```sql
-- user_roles：仅本次测试新增 1 条记录（user 63 -> role 1），无其它变更
SELECT * FROM user_roles WHERE user_id IN (63,64,65);
-- id=50, user_id=63, role_id=1

-- role_permissions（role_id=1）：与测试前完全一致，共 12 条，未新增/未修改
-- 含 id=13 -> permission_id=12 (user:manage)，由 migration 000012 正式播种
```

本次测试**未新建角色、未新建权限码、未手动插入 permissions/role_permissions 记录**，
唯一写操作是将一个全新账号绑定到系统已存在的真实 `admin` 角色，完全符合本次验证目的的约束要求。

## 最终结论

### 1. admin 角色账号是否无需任何手动权限播种即可直接使用封禁接口

**通过**。一个全新创建、仅通过 `user_roles` 表直接绑定到系统真实 `admin` 角色（role_id=1）的账号，
"开箱即用"即可成功调用 `PATCH /api/admin/users/:id/status` 接口完成封禁/解封操作，
全程未进行任何手动权限码播种或自定义角色/权限记录插入。
这证明 migration `000012_seed_user_manage_permission`（commit `d5115c8`）已彻底修复此前
"`user:manage` 权限码未绑定到 admin 角色"的根因问题。

### 2. 第一阶段是否可以正式画上句号

**是**。本轮验证补齐了此前因"自行播种权限码导致测试结果失真"而遗留的最后一个疑点——
现已用真实、未经任何额外播种的 admin 角色账号端到端验证通过封禁/解封全链路，
且反向权限边界（非 admin 拦截、无 token 拦截）同样正确。
结合此前 33/33 后端 A 验收用例全部通过、`app:manage` 同模式问题也已闭环修复，
**第一阶段所有已知问题（含本次权限码根因修复）均已闭环，可以正式画上句号**。

## 测试数据清理说明

本次测试在测试服务器数据库中新增了 3 个用户账号（id=63/64/65）及 1 条 `user_roles` 绑定记录（id=50），
均为常规账号注册流程产生的数据，未触碰 `permissions`/`role_permissions` 表，
不影响生产权限体系，无需特殊清理（与历次测试数据处理方式一致）。
