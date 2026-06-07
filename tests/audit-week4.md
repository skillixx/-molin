# Week 4 验收测试报告 — 应用 CRUD 模块（app）

**测试日期**：2026-06-07
**被测 commit**：`86580c8`（合并：Week 4 应用 CRUD 模块）
**测试环境**：测试服务器 `8.130.9.163:8080`，MySQL `13306`
**测试人员**：测试工程师（QA）

---

## 一、结论

**部分通过（存在 P1 阻塞项，需修复后重新验收管理端 CRUD）**

- 用户端接口 `GET /api/marketplace/apps/{id}`：**全部通过**，可见性边界、信息不泄露、数据完整性均符合预期。
- 管理端接口（`/api/admin/apps`、`/api/admin/app-adapters`）：**权限网关验证通过**（401/403 行为正确），
  但**完整 CRUD 功能验收被阻塞** —— `app:manage` 权限码尚未在 `permissions` 表中配置，也未绑定到任何角色，
  导致系统中**没有任何账号能够通过权限校验**，管理端接口始终返回 403/40003，无法继续验收创建/更新/列表/详情等核心功能。
  这与 PM review 时发现的 P3 遗留问题一致（详见下文 Issue #1，建议将优先级上调为 **P1 阻塞项**）。
- 通过代码走查（route.go / service / repository / handler）核对了创建/更新/唯一性/枚举校验逻辑，
  实现与 `server/internal/modules/app/CLAUDE.md` 规范一致，**预期补充权限配置后可顺利通过**，
  但仍需实测验证（尤其是并发创建时的唯一性竞态，见 Issue #2）。

---

## 二、测试数据准备

由于测试服务器上不存在管理员测试账号 `admin@molin.io`（登录返回 40001），且 Week 3 遗留的
`qa_admin_w3@molin.io` 密码未留存，本轮重新通过 `/api/auth/register/email` 注册了两个新账号：

| 账号 | user_id | 用途 |
|---|---|---|
| `qa_admin_w4@molin.io` | 7 | 拟用于管理端 app:manage 权限验证（**因权限码缺失，未能完成角色/权限绑定**）|
| `qa_user_w4@molin.io`  | 8 | 普通用户，用于用户端可见性边界测试、403 校验 |

**种子文件**（已提交至 `tests/seed/`）：

- `tests/seed/init-week4-app-perms.sql` — 拟补充 `app:manage` 权限码并绑定 admin 角色
  （**本轮验收未实际执行该 SQL** —— 涉及修改共享测试库的权限/角色绑定关系，超出 QA 单方面操作范围，
  应由后端开发者或 PM 在确认方案后执行，详见 Issue #1）
- `tests/seed/init-week4-users.sql` — 拟将 user_id=7 绑定 admin 角色（同上，未执行）
- `tests/seed/init-week4-apps.sql` — **已执行**，插入 4 条不同 status 的应用记录（id 101~104，
  code 前缀 `qa-app-*`），用于验证用户端可见性边界（不涉及权限表，属于 QA 允许范围内的纯业务数据 seed）

验证当前数据库状态（验收时刻）：

```sql
-- permissions 表中无 app:manage
SELECT code FROM permissions WHERE code='app:manage';   -- 空结果

-- user_id=7（qa_admin_w4）尚未绑定任何角色
SELECT * FROM user_roles WHERE user_id=7;                -- 空结果
```

---

## 三、测试结果详情

### 3.1 用户端接口：`GET /api/marketplace/apps/{id}`

| 用例 | 输入 | 期望 | 实际 | 结果 |
|---|---|---|---|---|
| 未登录访问 | 无 Token | 401 | `HTTP 401 {"code":40001,"message":"未登录"}` | 通过 |
| 非法 ID 格式 | `/api/marketplace/apps/abc` | 400 | `HTTP 400 {"code":40000,"message":"应用 ID 无效"}` | 通过 |
| 不存在的 ID | `/api/marketplace/apps/9999` | 404，统一提示 | `HTTP 404 {"code":40400,"message":"应用不存在或未上架"}` | 通过 |
| status=draft（id=102） | 已登录普通用户 | 404，统一提示 | `HTTP 404 {"code":40400,"message":"应用不存在或未上架"}` | 通过 |
| status=inactive（id=103） | 已登录普通用户 | 404，统一提示 | `HTTP 404 {"code":40400,"message":"应用不存在或未上架"}` | 通过 |
| status=archived（id=104） | 已登录普通用户 | 404，统一提示 | `HTTP 404 {"code":40400,"message":"应用不存在或未上架"}` | 通过 |
| status=active（id=101） | 已登录普通用户 | 200，返回完整详情 | `HTTP 200`，详情字段完整（见下） | 通过 |

**关键验证 —— 可见性边界（重点验收项 4）：**

`draft`、`inactive`、`archived` 三种非 active 状态，HTTP 状态码、错误码（40400）、错误消息
（`"应用不存在或未上架"`）**完全一致**，与"不存在"的响应**无法区分**，符合"不能泄露真实状态"的安全要求。
代码走查（`app_service.go: GetAppDetail`）确认逻辑：`gorm.ErrRecordNotFound` 与 `status != active`
两条分支返回**同一条错误消息字符串**，handler 层统一包装为 `40400`，未出现状态分支泄露。

**数据完整性验证（重点验收项 6）：**

为 id=101 应用补充 `icon_url`、`callback_url`、`adapter_config_json` 后再次查询，返回：

```json
{
  "id": 101,
  "code": "qa-app-active",
  "name": "QA测试应用-已上架",
  "type": "netdisk",
  "description": "...",
  "icon_url": "https://cdn.molin.io/icons/qa-app.png",
  "callback_url": "https://qa-app.example.com/callback",
  "adapter_config_json": "{\"region\": \"cn-hangzhou\", \"max_storage_gb\": 100}",
  "status": "active",
  "created_at": "...", "updated_at": "..."
}
```

字段完整、值正确，JSON 序列化无异常（中文字段正常显示，adapter_config_json 原样透传）。**通过**。

### 3.2 管理端接口：权限网关验证

| 用例 | 账号 | 期望 | 实际 | 结果 |
|---|---|---|---|---|
| 无 Token 访问 `GET /api/admin/apps` | 匿名 | 401 | `HTTP 401 {"code":40001,"message":"未登录"}` | 通过 |
| 已登录但无 `app:manage` 权限 | `qa_user_w4`（无角色） | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |
| 已登录但无 `app:manage` 权限（管理员候选账号，因权限码缺失实际也无权限） | `qa_admin_w4` | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过（侧面证实：当前系统中无任何账号拥有该权限）|

`RequireAuth` + `RequirePerm("app:manage")` 中间件链行为完全符合预期（重点验收项 1 中的网关部分**通过**）。

**但** —— 由于 `app:manage` 权限码未配置到 `permissions` 表，也未绑定到任何角色（包括内置 `admin` 角色），
**当前系统中不存在任何账号能够通过该权限校验**。这意味着：

- `POST /api/admin/apps`（创建应用）
- `PATCH /api/admin/apps/{id}`（更新/上下架）
- `GET /api/admin/apps`、`GET /api/admin/apps/{id}`（列表/详情）
- `POST /api/admin/app-adapters`、`PATCH /api/admin/app-adapters/{id}`、`GET /api/admin/app-adapters`

以上**全部管理端接口的功能验收均无法实际执行**（均会先在权限校验阶段被拦截返回 403），
包括重点验收项 2（唯一性校验）、项 3（status 枚举校验）、项 6（数据完整性）的管理端部分。

### 3.3 应用与商品边界（重点验收项 5）

```sql
-- products 表 schema 未受 app 模块 migration 影响（无新增字段、无外键变更）
SHOW CREATE TABLE products;
-- 确认仍为 Week 2 设计：product_type / product_code / business_ref_id 等字段不变

-- 当前 products 表中 product_type='application' 的记录
SELECT id, product_type, business_ref_id, status, name FROM products WHERE product_type='application';
-- 结果：1 条（id=2 "QA应用配额商品"，business_ref_id=NULL，Week 3 遗留数据，非本模块产生）
```

- `applications`/`application_adapters` 为全新表，migration 未对 `products`/`product_plans` 做任何 DDL 变更；
- 代码走查确认 `app_service.go`/`adapter_service.go` 均只操作 `applications`/`application_adapters` 表，
  未引入对 `products`/`product_plans`/`product_prices`/`product_role_access` 的任何读写；
- `route.go` 暴露的 `NewService` 仅供 `provision` 模块的 `AppProvisioner` 复用查询能力，未发现越权写操作。

**结论**：应用模块与商品模块边界清晰，符合 CLAUDE.md 中"创建应用本身不应影响 products/product_plans"的约定。**通过**。

### 3.4 代码走查补充验证（因管理端接口被 403 阻塞，以下项目仅通过代码审查核实，未实测）

- **唯一性校验**（项 2）：`CreateApp`/`RegisterAdapter` 在插入前先 `FindByCode`/`FindByAppCode` 查重，
  若已存在返回 `"应用 code 已存在"`/`"该 app_code 已注册适配器"`（400）。逻辑正确，
  但属于"先查后插"模式，存在并发竞态窗口（见 Issue #2）。数据库层 `UNIQUE KEY` 约束
  （`uk_applications_code`、`uk_app_adapters_app_code`）可兜底防止脏数据产生，但并发时
  第二个请求会从 GORM 收到原始 MySQL duplicate-entry 错误并被包装为 `"创建应用失败: ..."`/
  `"注册适配器失败: ..."`，可能透传数据库层错误信息给前端（与 Week 3 audit 中发现的
  "DB 错误透传" P3 问题同类，建议归并处理）。
- **status 枚举校验**（项 3）：`UpdateApp` 校验 `validAppStatuses = {draft, active, inactive, archived}`，
  `UpdateAdapter` 校验 `validAdapterStatuses = {active, inactive}` 及 `validAdapterTypes = {internal, external}`，
  非法取值返回 400 + 明确提示。逻辑与规范完全一致。
- **路由与中间件**（项 1）：`route.go` 中管理端 7 个接口均正确包装
  `RequireAuth(jwtSecret, banChecker, RequirePerm(iamSvc, "app:manage", handler))`，与规范一致。

---

## 四、发现的问题

### Issue #1【阻塞项】[app][P1] `app:manage` 权限码未配置，导致管理端全部接口无法验收

**优先级**：P1（核心功能不可用 —— 阻断管理端 CRUD 全部 7 个接口的功能验收）

> 备注：PM review 时已将此问题标记为 P3 遗留问题；经本轮验收实测确认，该问题导致**当前系统中
> 不存在任何账号能够调用管理端接口**（包括内置 admin 角色），属于阻断性缺陷，建议将优先级
> 由 P3 上调为 **P1**，需在下次部署/发布前修复，否则管理端应用管理功能完全不可用。

**复现步骤**：
1. 使用任意已登录账号（含拥有 `admin` 角色的账号）访问 `GET /api/admin/apps`
2. 查询 `permissions` 表：`SELECT * FROM permissions WHERE code='app:manage'` → 空结果
3. 查询 `role_permissions` 表关联 admin 角色的权限列表 → 不含 `app:manage`

**期望结果**：应在 `permissions` 表 seed 中补充 `app:manage` 权限码并绑定到 `admin` 角色
（参考 `server/internal/modules/app/CLAUDE.md` 第 106~107 行的要求："若该权限码不存在需在
permissions 表 seed 中补充并告知后端 A"）。

**实际结果**：`permissions` 表中无 `app:manage` 记录，`role_permissions` 中无对应绑定，
任何账号访问管理端接口均返回 `HTTP 403 {"code":40003,"message":"无操作权限"}`。

**日志/截图**：
```
$ curl -H "Authorization: Bearer <qa_admin_w4 token>" http://8.130.9.163:8080/api/admin/apps
HTTP 403
{"code":40003,"message":"无操作权限","data":null}

$ mysql ... -e "SELECT code FROM permissions WHERE code='app:manage';"
（空结果）
```

**环境**：测试服务器（8.130.9.163:8080）

**建议修复方式**：在权限种子 SQL（如 `server/migrations/` 或后端 seed 脚本）中补充：
```sql
INSERT IGNORE INTO permissions (code, name, resource, action) VALUES
  ('app:manage', '应用管理', 'app', 'manage');
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON p.code='app:manage'
WHERE r.code = 'admin';
```
（已在 `tests/seed/init-week4-app-perms.sql` 中准备好等价 SQL，供开发者参考执行；
 QA 未直接对共享测试库执行权限/角色绑定变更，需由负责模块的后端开发者确认并落地。）

---

### Issue #2 [app][P3] 唯一性校验存在"先查后插"竞态窗口，且并发冲突时可能透传 DB 原始错误

**优先级**：P3（体验问题，不阻断功能；数据库唯一键约束可兜底防脏数据）

**复现步骤**（基于代码走查推断，需在 Issue #1 修复后实测验证）：
1. 两个并发请求同时携带相同 `code` 调用 `POST /api/admin/apps`
2. 两者几乎同时执行 `FindByCode` 查重，均未发现已存在记录，均进入 `Create` 分支
3. 数据库 `UNIQUE KEY uk_applications_code` 拦截第二条插入，GORM 返回原始 MySQL duplicate-entry 错误
4. `service.CreateApp` 将其包装为 `"创建应用失败: %w"`，可能将原始 DB 错误信息透传到 HTTP 响应

**期望结果**：应识别 DB 唯一键冲突错误（如 MySQL error 1062），转换为统一的
`"应用 code 已存在"` 友好提示，避免向前端泄露数据库层错误细节。

**实际结果**（代码走查推断）：`fmt.Errorf("创建应用失败: %w", err)` 直接包装底层错误，
`AdminCreateApp` handler 将 `err.Error()` 原样作为 message 返回（400），可能包含
MySQL 原始错误文本（如 `Error 1062: Duplicate entry 'xxx' for key 'uk_applications_code'`）。

**日志/截图**：（因 Issue #1 阻塞，未能实测获取真实响应内容；建议修复 Issue #1 后补测）

**环境**：测试服务器（代码走查 + 推断，未实测验证）

**说明**：该问题与 Week 3 audit（`tests/audit-week3.md`）中报告的"DB 错误透传"P3 问题为同一类型，
建议归并到统一的错误处理改进任务中一并修复（如封装 `IsDuplicateKeyError` 帮助函数）。

---

## 五、按重点验收项汇总

| # | 验收项 | 结论 |
|---|---|---|
| 1 | 权限校验（401/403/40003） | **通过**（网关行为正确；但因 Issue #1，无账号可实际通过权限校验，管理端功能本身未验证）|
| 2 | 唯一性校验（code/app_code） | **未能实测**（被 Issue #1 阻塞）；代码走查显示逻辑正确，但有 Issue #2 提到的竞态/错误透传风险 |
| 3 | status 枚举校验 | **未能实测**（被 Issue #1 阻塞）；代码走查显示 `validAppStatuses`/`validAdapterStatuses`/`validAdapterTypes` 校验逻辑与规范完全一致 |
| 4 | 用户端可见性边界（draft/inactive/archived 统一提示） | **通过**（实测验证：4 种状态下响应码、错误码、错误消息完全一致，无信息泄露）|
| 5 | 应用与商品边界 | **通过**（products/product_plans 表结构未受影响，代码未发现越权读写）|
| 6 | 数据完整性（创建/更新后查询） | 用户端 **通过**（字段完整正确）；管理端 **未能实测**（被 Issue #1 阻塞）|

---

## 六、是否允许合并/上线

**不建议在当前状态下作为"管理端应用管理功能"正式上线** —— Issue #1 为 P1 阻塞项，
会导致管理后台完全无法使用应用管理功能（任何账号访问均 403）。

**建议**：
1. 由负责该模块的后端开发者（后端 C）补充 `app:manage` 权限码 seed 并绑定到 `admin` 角色
   （可直接复用 `tests/seed/init-week4-app-perms.sql` 中的 SQL），完成后告知 QA 重新执行管理端验收；
2. 用户端接口（`GET /api/marketplace/apps/{id}`）功能已验证通过，**可以单独放行**；
3. Issue #2（DB 错误透传）可与 Week 3 同类问题一并排期修复，不阻塞本次发布。

---

## 附：测试数据清单

| 表 | 说明 |
|---|---|
| `users` id=7 `qa_admin_w4@molin.io` | 拟用于管理端验收的管理员候选账号（暂未绑定角色，待 Issue #1 修复后补充绑定）|
| `users` id=8 `qa_user_w4@molin.io` | 普通用户账号，已用于用户端边界/403 测试 |
| `applications` id=101~104 | 4 条不同 status 的测试应用记录（`qa-app-active/draft/inactive/archived`），用于验证用户端可见性边界，建议保留供回归测试复用 |

种子文件位置：
- `tests/seed/init-week4-apps.sql`（已执行，可重复执行）
- `tests/seed/init-week4-app-perms.sql`（待开发者确认后执行，修复 Issue #1）
- `tests/seed/init-week4-users.sql`（待 Issue #1 修复后，将 user_id=7 绑定 admin 角色时一并执行）
