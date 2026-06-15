# 基础角色 seed 与首个 admin 用户落地说明

> 关联 migration：`000024_seed_base_roles.{up,down}.sql`
> 维护：后端甲（auth / iam）

## 1. 背景：基础角色缺口

项目此前没有任何 migration / bootstrap 负责写入「基础角色」，而 `000011`~`000023`
一系列权限 seed 全部假设 `admin` 角色已存在，绑定写法均为：

```sql
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON ... WHERE r.code='admin';
```

`admin` 不存在时，`SELECT` 命中 0 行，绑定静默成为 no-op。结果：
**全新数据库 `migrate up` 后 roles / role_permissions 均为空，系统中不存在任何
能通过 `RequirePerm` 鉴权的管理员，所有管理端接口返回 40003。**

`000024` 修复该缺口：写入 `admin` 角色，并把当前 `permissions` 表里的全部权限
CROSS JOIN 绑定到 `admin`（「治愈绑定」），使 `admin` 成为拥有全部权限的超级管理员。

## 2. 为什么基础角色集只有 admin

- **注册流程不依赖默认角色**：`auth_service.go` 的 `Register` 创建用户后直接发 token，
  不分配任何全局角色，因此无需 seed `user` / `member` 之类默认角色。
- **「组管理员 / 普通组员」不是全局角色**：它们是 `user_group_members.group_role`
  字段（取值 `admin` / `member`）表示的组内身份，与全局 `roles` 表正交，
  不应 seed 为全局角色。
- 数据范围设计文档中提到的 `region_admin`（区域管理员）属于后续阶段（数据范围落地），
  其角色与只读权限码应随该阶段单独建 seed migration，不在本 PR 范围。

## 3. 首个 admin 用户如何落地（不在本 migration 内建用户）

migration **绝不硬编码明文密码**（违反 CLAUDE.md 安全约定，且 bcrypt 哈希也不宜入库到版本库）。
`000024` 只负责「角色 + 权限绑定」，**不创建用户、不写 `user_roles`**。

首个 admin 用户推荐两种受控方式，按环境二选一：

### 方案 A（推荐）：注册首个用户后手工授予 admin 角色

1. 通过正常注册接口 `POST /api/auth/register` 创建第一个账号（拿到其 `user_id`）。
2. 由 DBA / 运维在受控环境直接执行一次性 SQL 授权：

   ```sql
   INSERT IGNORE INTO user_roles (user_id, role_id)
   SELECT :first_user_id, id FROM roles WHERE code = 'admin';
   ```

3. 该用户重新登录后即拥有全部权限。
   后续其他管理员可通过 IAM 管理端接口（`POST /api/admin/users/{id}/roles`）由该
   超级管理员授予角色，无需再手工改库。

优点：不在版本库 / migration 中留下任何凭据，授权动作可审计、可追溯到具体执行人。

### 方案 B：受控 bootstrap（密码哈希由环境变量注入）

若需自动化初始化（如 CI / 一键部署），后续可新增一个**独立**的 bootstrap CLI
（如 `cmd/seed-admin`），从环境变量读取首个管理员账号与**密码哈希**（不是明文），
在用户不存在时创建并绑定 admin 角色：

- 账号：`BOOTSTRAP_ADMIN_PHONE` / `BOOTSTRAP_ADMIN_EMAIL`
- 密码哈希：`BOOTSTRAP_ADMIN_PASSWORD_HASH`（bcrypt cost=12，由部署方离线生成后注入）
- 幂等：账号已存在则只补绑 admin 角色，不覆盖密码。

该 CLI 不属于本 PR，列为后续可选项；当前以方案 A 作为默认落地方式。

## 4. 对已有测试库的影响

测试库历史上已存在若干「孤本」角色（含 admin）。在已有 admin 的库上执行 `000024`：

- `INSERT IGNORE roles ... 'admin'`：admin 已存在 → 跳过，不改动既有角色。
- 治愈绑定 `CROSS JOIN`：会把当前 permissions 表里**尚未绑定到 admin 的权限**补绑上，
  即 **admin 将被补全为拥有全部权限**。这是预期且安全的修复（只增不减绑定）。

> 合并后需运维执行迁移（`migrate up` 到 000024）。本 PR 不自行执行 migrate、不改数据库。
