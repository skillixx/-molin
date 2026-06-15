# 后端甲交接说明：基础角色 seed 与 admin bootstrap

> **交接对象**：后端工程师甲（auth / iam）及运维
> **日期**：2026-06-15　|　**状态**：全部已合并 main 并应用到本地+测试库
> **关联 PR**：#113（migration 000024）、#115（seed-admin CLI）
> **关联文档**：`server/migrations/README-base-roles.md`（用法权威）

---

## 1. 背景：为什么要做这件事

权限 seed migration `000011`~`000023` 全部以 `WHERE r.code='admin'` 绑定权限到 admin 角色，但**项目里没有任何 migration / bootstrap / 脚本 seed 初始 admin 角色**，备份 `latest.sql` 也无 roles 数据。

后果：**全新数据库 `migrate up` 后 `roles` 表为空、`role_permissions` 为空，系统中不存在任何能通过 `RequirePerm` 鉴权的管理员**，所有管理端接口返回 40003，无法初始化。测试库现有角色是历史运行时/手工产生的「孤本」，不可复现。

本次交付补齐该缺口，并提供受控的首个 admin 用户落地方式。

---

## 2. 交付清单

### PR #113 — 基础角色 seed migration（commit `640eccf`）

| 文件 | 操作 | 要点 |
|---|---|---|
| `server/migrations/000024_seed_base_roles.up.sql` | 新增 | ① `INSERT IGNORE` 写入 `admin` 超级管理员角色；② `INSERT IGNORE ... SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.code='admin'`——把当前 permissions 表**全部权限**治愈绑定到 admin，修复历史 no-op 绑定。全部幂等，可重复执行 |
| `server/migrations/000024_seed_base_roles.down.sql` | 新增 | 解绑 admin 的 role_permissions；仅当 admin **未被 `user_roles` 引用**时才删角色（防悬空、防既有管理员失权），best-effort |
| `server/migrations/README-base-roles.md` | 新增 | 缺口背景、基础角色集设计、首个 admin 用户方案 A/B |

### PR #115 — admin bootstrap CLI（commit `4f91723`）

| 文件 | 操作 | 要点 |
|---|---|---|
| `server/cmd/seed-admin/main.go` | 新增 | 受控 CLI：从环境变量幂等创建首个 admin 用户并绑定 admin 角色 |
| `server/migrations/README-base-roles.md` | 更新 | §3 方案 B 改为「已实现」，补环境变量表 / bcrypt 哈希生成示例 / 用法 / 退出码 |

---

## 3. 设计要点与依据（交接重点）

### 3.1 为什么基础角色集只有 `admin`
- **注册流程不分配任何全局角色**：`auth_service.go` 的 `Register` 创建用户后直接发 token，故无需 seed `user`/`member` 默认角色。
- **「组管理员 / 普通组员」不是全局角色**：是 `user_group_members.group_role` 字段（`admin`/`member`），与全局 `roles` 表正交，不应 seed 为全局角色。
- `region_admin`（区域管理员）属后续「数据范围落地」阶段，其角色与只读权限码届时单独建 seed migration。

### 3.2 治愈绑定（CROSS JOIN）
`admin` 唯一，`r CROSS JOIN p WHERE r.code='admin'` 等价 admin × 全部权限；`INSERT IGNORE` 保证重复执行不冲突。**对已有库执行只增不减**：admin 已有的绑定跳过，未绑定的补全——使 admin 成为拥有全部权限的真正超管，安全。

### 3.3 首个 admin 用户为何不在 migration 内建
migration 绝不硬编码明文密码（违反 CLAUDE.md 安全约定），bcrypt 哈希也不宜入版本库。故 000024 只负责「角色 + 权限绑定」，用户落地走 CLI（方案 B）或手工授权（方案 A）。

### 3.4 seed-admin CLI 行为契约
- 读 `BOOTSTRAP_ADMIN_PHONE` / `BOOTSTRAP_ADMIN_EMAIL`（至少一个）+ `BOOTSTRAP_ADMIN_PASSWORD_HASH`（bcrypt，离线生成注入）。
- 校验：密码哈希须形似 bcrypt（`$2a$/$2b$/$2y$` 前缀 + 长度 60），不合法报错退出。
- **幂等**：用户不存在→创建并绑定 admin；用户已存在→**只补绑 admin 角色，不覆盖密码、不改既有字段**。
- 前置：admin 角色须由 000024 已 seed，否则报错提示「先 migrate up 到 000024」，不擅自建角色。
- 安全：只读 env 哈希、直接落 `users.password_hash`、不二次哈希；日志仅打印「已设置/未设置」，绝不打印手机号/邮箱/哈希。
- 退出码：成功 0 / 参数·环境·校验失败非 0（便于 CI 判断）。
- 复用：auth `UserRepository`（创建 + 唯一键兜底）、iam `UserRoleRepository.Assign`（绑定 + 冲突幂等）；账号规范化（email 小写+TrimSpace、phone TrimSpace）与注册流程一致。

---

## 4. 部署 / 运维操作（上线必做）

**顺序固定：先迁移 → 再（可选）跑 seed-admin → 再让新代码生效。**

```bash
# 1. 应用 migration 到 000024（治愈 admin 全权绑定）
./scripts/migrate.sh up        # golang-migrate，推进到最新（含 000023/000024）

# 2A. 方案 A（手工授权，过渡用）：注册首个用户后由 DBA 执行一次性 SQL
#   INSERT IGNORE INTO user_roles (user_id, role_id)
#   SELECT <first_user_id>, id FROM roles WHERE code='admin';

# 2B. 方案 B（自动化）：离线生成 bcrypt 哈希后注入环境变量运行 CLI
#   生成哈希（示例，任选其一，cost=12）：
#     htpasswd -bnBC 12 "" '你的密码' | tr -d ':\n' | sed 's/^\$2y/\$2b/'
#   运行：
export BOOTSTRAP_ADMIN_PHONE=13800000000
export BOOTSTRAP_ADMIN_EMAIL=admin@example.com
export BOOTSTRAP_ADMIN_PASSWORD_HASH='$2b$12$....'   # 由部署方离线生成注入
go run ./cmd/seed-admin          # 或编译后的二进制；幂等，可重复执行
```

> 权限按请求实时鉴权（DB 评估），治愈绑定/新管理员入库后无需重启 api。

---

## 5. 已验证状态

- 000024 已由运维 `migrate up` 应用到**本地开发库**与**测试服务器库**：两库均 v24，`admin` 角色存在，`admin` 已绑定全部 20 个权限（`perm_total == admin_bound`）。
- 测试 API 健康（`GET /api/health` 200）。
- seed-admin CLI 仅 `go build`/`go vet` 通过，**未对任何库实际运行**（避免污染），实际执行为部署动作。

---

## 6. 后续待办 / 注意

- 上线环境需按 §4 执行一次 seed-admin（或方案 A）创建首个真实管理员。
- **本地开发库历史漂移**（曾 `schema_migrations` 停 17、roles 表空）已由运维以测试库为权威源同步 roles/permissions/role_permissions 三表修复；若本地再次重建，直接全量 `migrate up` + 跑 seed-admin 即可得到干净状态，无需再从测试库抄。
- `region_admin` 及数据范围只读权限码留待「数据范围落地」阶段，单独建 seed migration（沿用 INSERT IGNORE + 绑定 admin 的模式）。
- 红线提醒：今后任何 `RequirePerm` 新权限码必须同时建 seed migration 并绑定 admin（历史多次因缺 seed 上线即 P1）。
</content>
