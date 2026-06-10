---
name: feedback-permission-code-seeding
description: 反复出现的 P1 根因：RequirePerm 声明了权限码但 permissions 表从未 seed，导致功能对所有人 403
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 25f47551-2bee-4efa-8a8b-d6653fbe1e67
---

每次新增路由并用 `RequirePerm("xxx:yyy")` 声明权限码时，必须同时创建对应的 seed migration，否则会造成"代码可运行但功能对所有人（包括 admin）返回 403"的 P1 缺陷。

**Why:** 该项目已三次重复出现完全相同的根因（`app:manage` → `user:manage` → `product:view`/`order:list`），每次都是"路由层声明了权限码，但 permissions 表里从未插入该记录，role_permissions 也无法绑定，系统中没有任何账号能通过 RequirePerm 校验"。表面看是种子数据问题，实质是"代码声明与数据库状态脱节"。

**How to apply:**
1. 每次写 `RequirePerm("xxx:yyy")` 时，立即检查是否已有对应的 seed migration（`grep -r "xxx:yyy" server/migrations/`）
2. 如果没有，参照 `000011_seed_app_manage_permission.up.sql` 的 INSERT IGNORE 幂等写法创建 migration
3. down.sql 必须先删 role_permissions 绑定，再删 permissions 记录（避免外键冲突）
4. 建议后续在 CI 中加"grep RequirePerm 提取权限码 vs migrations seed 数据交叉核对"脚本，从根本上消除这类问题

已修复的三个案例：migration `000011`（app:manage）、`000012`（user:manage）、`000013`（product:view/order:list）

[[design-decisions]]
