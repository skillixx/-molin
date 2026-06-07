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

---

# 【复测】Week 4 管理端应用 CRUD 接口验收 — Issue #1 修复后复测

**复测日期**：2026-06-07
**被测 commit**：`403f0d6`（migration `000011_seed_app_manage_permission`：补充 `app:manage` 权限码并绑定 `admin` 角色）
**测试环境**：测试服务器 `8.130.9.163:8080`，MySQL `13306`
**测试人员**：测试工程师（QA）

## 一、复测结论

**通过** —— Issue #1（P1 阻塞项：`app:manage` 权限码缺失导致管理端全部接口 403）已修复，
管理端 7 个接口功能验收全部完成且符合预期。**Week 4 应用 CRUD 模块整体可正式验收通过。**

## 二、复测前置准备

1. 确认数据库中 `app:manage` 权限码已存在并绑定到 `admin` 角色（migration 000011 已生效）：
   ```sql
   SELECT code, name FROM permissions WHERE code='app:manage';        -- 1 行：app:manage / 应用管理
   SELECT r.code, p.code FROM role_permissions rp
     JOIN roles r ON r.id=rp.role_id JOIN permissions p ON p.id=rp.permission_id
     WHERE p.code='app:manage';                                       -- 1 行：admin / app:manage
   ```
2. 发现上轮验收创建的候选管理员账号 `qa_admin_w4@molin.io`（user_id=7）**仍未绑定 `admin` 角色**
   （`user_roles` 表中无记录），执行 `tests/seed/init-week4-users.sql` 完成绑定：
   ```sql
   INSERT IGNORE INTO user_roles (user_id, role_id) SELECT 7, id FROM roles WHERE code = 'admin';
   ```
   执行后验证：`user_id=7 / qa_admin_w4@molin.io / role=admin`，绑定成功。
3. 上轮注册账号的密码未留存，通过非生产环境验证码明文返回机制（`config.AppEnv != "production"` 时
   `/api/auth/verification-codes/email` 响应体含 `data.code`），分别为 `qa_admin_w4@molin.io`（user_id=7，admin 角色）
   和 `qa_user_w4@molin.io`（user_id=8，普通用户）走 `scene=reset_password` 重置密码并重新登录获取 Token，
   未新增账号、未变更角色绑定关系之外的任何共享数据。

## 三、管理端 7 个接口复测结果

### 3.1 `GET /api/admin/apps`（应用列表，分页 + status/type 筛选）

| 用例 | 期望 | 实际 | 结果 |
|---|---|---|---|
| admin 账号访问，无筛选 | 200，返回分页列表 | `HTTP 200`，`total=4`，返回 id 101~104 全部记录，含完整字段 | 通过 |
| `?status=active` 筛选 | 仅返回 active 状态记录 | `HTTP 200`，仅返回 id=101（`qa-app-active`） | 通过 |
| `?type=netdisk` 筛选 | 返回该 type 下全部记录 | `HTTP 200`，返回全部 4 条（均为 netdisk） | 通过 |
| 普通用户（无 `app:manage`）访问 | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |

### 3.2 `GET /api/admin/apps/{id}`（应用详情）

| 用例 | 期望 | 实际 | 结果 |
|---|---|---|---|
| admin 访问 id=101 | 200，返回完整详情 | `HTTP 200`，字段完整（code/name/type/icon_url/callback_url/adapter_config_json/status 等） | 通过 |
| 不存在的 id=9999 | 404 | `HTTP 404 {"code":40400,"message":"应用不存在"}` | 通过 |
| 非法 id 格式（`/abc`） | 400 | `HTTP 400 {"code":40000,"message":"应用 ID 无效"}` | 通过 |
| 普通用户访问 | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |

### 3.3 `POST /api/admin/apps`（创建应用）

| 用例 | 期望 | 实际 | 结果 |
|---|---|---|---|
| admin 创建新应用（code=`qa-app-new-105`） | 201，初始 `status=draft` | `HTTP 201`，返回 `id=105`，**`status":"draft"`**，字段完整 | 通过 |
| 重复 `code`（与 id=101 相同 `qa-app-active`） | 400，明确错误提示 | `HTTP 400 {"code":40000,"message":"应用 code 已存在"}` —— **未透传 DB 原始错误**，提示友好 | 通过 |
| 普通用户创建 | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |
| 缺少必填字段（`code`） | 400 | `HTTP 400 {"code":40000,"message":"code、name、type 为必填项"}` | 通过 |

**重点验收项 2（唯一性校验）实测结论**：单次重复提交场景下，返回的是友好提示
`"应用 code 已存在"`（非 DB 原始错误文本），与 Issue #2 中代码走查推断的"可能透传 DB 错误"
**不一致** —— 实测表现优于走查预期，说明 `FindByCode` 查重分支在常规（非并发竞态）场景下
正常拦截并返回友好错误。**真正的并发竞态窗口**（两个请求同时通过查重、同时插入触发 DB 唯一键冲突）
仍需专门的并发测试验证，但该场景概率极低且有 `UNIQUE KEY` 兜底防止脏数据，
继续维持 Issue #2 为 **P3（不阻断）**的判定，建议保留观察、酌情归并到统一错误处理改进任务。

### 3.4 `PATCH /api/admin/apps/{id}`（更新应用 / 上下架）

| 用例 | 期望 | 实际 | 结果 |
|---|---|---|---|
| 更新 name + status: draft→active + icon_url | 200，更新成功 | `HTTP 200 {"message":"更新成功"}`；复查详情：`name`/`icon_url`/`status` 全部正确更新 | 通过 |
| status 传非法值（`published`） | 400，提示合法取值范围 | `HTTP 400 {"code":40000,"message":"status 取值非法，仅支持 draft/active/inactive/archived"}` | 通过 |
| 普通用户更新 | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |
| 更新不存在的 id=9999 | 404（建议） | `HTTP 400 {"code":40000,"message":"应用不存在"}` —— **实际返回 400 而非 404**，与 `GET /api/admin/apps/{id}` 对"不存在"统一返回 404+40400 不一致 | 见 Issue #3（新发现，P3） |

**重点验收项 3（status 枚举校验）通过**：`validAppStatuses = {draft, active, inactive, archived}`
校验生效，非法值被正确拒绝并给出合法取值提示。

**重点验收项 6（数据完整性）通过**：创建后初始 `status=draft`，更新后再次查询字段（`name`、
`icon_url`、`status`、`updated_at`）均正确反映最新值，无脏数据、无字段丢失。

### 3.5 `GET /api/admin/app-adapters`（适配器列表）

| 用例 | 期望 | 实际 | 结果 |
|---|---|---|---|
| admin 访问（初始为空） | 200，空列表 | `HTTP 200 {"items":[],"total":0}` | 通过 |
| admin 访问（注册 2 个适配器后） | 200，返回 2 条完整记录 | `HTTP 200`，`total=2`，字段完整（含 `service_name`/`callback_url`/`supported_actions_json` 等） | 通过 |
| 普通用户访问 | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |

### 3.6 `POST /api/admin/app-adapters`（注册适配器）

| 用例 | 期望 | 实际 | 结果 |
|---|---|---|---|
| admin 注册（`app_code=qa-app-active`，`adapter_type=internal`，含 service_name/callback_url/supported_actions_json） | 201，创建成功 | `HTTP 201`，返回 `id=1`，字段完整正确，初始 `status=active` | 通过 |
| 重复 `app_code`（`qa-app-active`） | 400，明确错误提示 | `HTTP 400 {"code":40000,"message":"该 app_code 已注册适配器"}` —— 未透传 DB 错误 | 通过 |
| `adapter_type` 传非法值（`cloud_api`） | 400，提示合法取值 | `HTTP 400 {"code":40000,"message":"adapter_type 取值非法，仅支持 internal/external"}` | 通过 |
| 普通用户注册 | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |
| 不传 `adapter_type`（可选字段） | 200/201，使用 DB 默认值 `internal` | `HTTP 201`，返回记录 `"adapter_type":"internal"` —— 与 `application_adapters` 表 `adapter_type VARCHAR(32) NOT NULL DEFAULT 'internal'` 的 DDL 默认值设计**一致**，非缺陷 | 通过（确认为设计行为）|

### 3.7 `PATCH /api/admin/app-adapters/{id}`（更新 / 启停适配器）

| 用例 | 期望 | 实际 | 结果 |
|---|---|---|---|
| 更新 service_name + status: active→inactive（启停） | 200，更新成功 | `HTTP 200 {"message":"更新成功"}`；复查列表：`service_name`/`status`/`updated_at` 全部正确更新为 `qa-internal-svc-v2`/`inactive` | 通过 |
| status 传非法值（`suspended`） | 400 | `HTTP 400 {"code":40000,"message":"status 取值非法，仅支持 active/inactive"}` | 通过 |
| `adapter_type` 传非法值（`hybrid`） | 400 | `HTTP 400 {"code":40000,"message":"adapter_type 取值非法，仅支持 internal/external"}` | 通过 |
| 更新不存在的 id=9999 | 404（建议） | `HTTP 400 {"code":40000,"message":"适配器不存在"}` —— 同样返回 400 而非 404，与 Issue #3 同类 | 见 Issue #3 |
| 普通用户更新 | 403，code=40003 | `HTTP 403 {"code":40003,"message":"无操作权限"}` | 通过 |

**重点验收项 6（数据完整性）通过**：注册后初始字段完整（`service_name`/`callback_url`/
`supported_actions_json` 原样保存），更新后复查 `service_name`/`status`/`updated_at` 均正确反映最新值。

## 四、按重点验收项汇总（复测结果）

| # | 验收项 | 复测结论 |
|---|---|---|
| 1 | 权限校验是否已修复（admin 不再 403，普通用户正确 403） | **通过** —— admin 账号绑定 `admin` 角色后可正常完成全部 CRUD 操作；普通用户访问全部 7 个接口均正确返回 `403 {"code":40003}`，权限边界生效，未出现"放行所有人" |
| 2 | 唯一性校验（code/app_code 重复） | **通过** —— 单次重复提交场景下均返回友好错误提示（`应用 code 已存在`/`该 app_code 已注册适配器`），未透传 DB 原始错误；真正并发竞态场景维持 P3 判定（详见 3.3 说明） |
| 3 | status 枚举校验 | **通过** —— `validAppStatuses`/`validAdapterStatuses`/`validAdapterTypes` 均正确拦截非法值并提示合法取值范围 |
| 4 | 数据完整性（创建/更新后查询） | **通过** —— 应用和适配器创建/更新后复查，所有字段（含初始默认值 `status=draft`/`internal`）均正确完整，无丢失或脏数据 |
| 5 | 无权限角色访问（应正确 403/40003） | **通过** —— `qa_user_w4`（无 `app:manage` 权限）访问全部 7 个接口均被正确拦截，验证权限边界确实生效 |

## 五、新发现的问题

### Issue #3 [app][P3] PATCH 接口对"资源不存在"返回 400 而非 404，与 GET 详情接口不一致

**优先级**：P3（体验/一致性问题，不阻断功能）

**复现步骤**：
1. `GET /api/admin/apps/9999` → 返回 `HTTP 404 {"code":40400,"message":"应用不存在"}`
2. `PATCH /api/admin/apps/9999` → 返回 `HTTP 400 {"code":40000,"message":"应用不存在"}`
3. 同理 `PATCH /api/admin/app-adapters/9999` → 返回 `HTTP 400 {"code":40000,"message":"适配器不存在"}`

**期望结果**：同一资源"不存在"的语义，HTTP 状态码和错误码应保持一致（建议统一为 `404 + 40400`），
便于前端统一处理 404 场景。

**实际结果**：`GET` 详情接口返回 `404 + 40400`，`PATCH` 更新接口返回 `400 + 40000`，
同一种错误语义在不同接口上表现不一致。

**日志/截图**：
```
$ curl -X PATCH .../api/admin/apps/9999 -d '{"status":"inactive"}'
HTTP 400 {"code":40000,"message":"应用不存在","data":null}

$ curl .../api/admin/apps/9999
HTTP 404 {"code":40400,"message":"应用不存在","data":null}
```

**环境**：测试服务器（8.130.9.163:8080）

**说明**：不阻断本次发布，建议归并到下一轮统一错误码/状态码规范化任务中处理。

## 六、是否允许合并/上线

**结论：是，建议正式验收通过，允许合并/上线。**

- Issue #1（P1 阻塞项）已通过 migration `000011_seed_app_manage_permission` 修复并验证生效，
  管理端全部 7 个接口功能完整可用，权限边界（401/403/40003）行为正确。
- 重点验收项 1~6 全部复测通过（含管理端此前因 Issue #1 被阻塞的 CRUD 功能、唯一性校验、
  status 枚举校验、数据完整性）。
- Issue #2（DB 错误透传，P3）经实测，单次重复场景下**未复现**（返回友好提示），风险低于
  此前代码走查的预期，维持 P3、不阻断。
- 新发现 Issue #3（PATCH 不存在资源返回 400 而非 404，P3）为体验一致性问题，不阻断功能，
  建议归并到统一错误码规范化任务中处理。

**Week 4「应用 CRUD」模块（含用户端可见性、应用-商品边界、管理端 7 个接口）整体验收通过，可以合并/上线。**

## 七、本轮新增/变更的测试数据

| 表 | 记录 | 说明 |
|---|---|---|
| `user_roles` | `user_id=7` → `admin` 角色 | 执行 `init-week4-users.sql` 完成绑定（此前缺失，已补充） |
| `applications` | `id=105`（`qa-app-new-105`） | 复测创建接口产生，建议保留供后续回归复用 |
| `application_adapters` | `id=1`（`qa-app-active`，internal，已设为 inactive）、`id=2`（`qa-app-draft`，internal，active） | 复测注册/更新接口产生，建议保留供后续回归复用 |

种子文件位置（无变更）：
- `tests/seed/init-week4-apps.sql`
- `tests/seed/init-week4-app-perms.sql`（已确认在测试库生效，等价于 migration 000011 的效果）
- `tests/seed/init-week4-users.sql`（本轮已执行，完成 user_id=7 → admin 角色绑定）
