# 第一阶段 — 人工接口验收补充测试报告（必测安全/购买/回调场景查漏补缺）

**测试日期**：2026-06-07
**测试环境**：测试服务器 `8.130.9.163:8080`，MySQL `13306`
**测试脚本**：`tests/manual_gap_test.py`
**测试人员**：测试工程师（QA）
**测试目的**：在 `tests/test_backend_a.py`（33 用例）、`tests/stage1_e2e_test.py`（37 用例）、
`tests/bugfix_retest.py`（缺陷复测）三套既有自动化回归脚本已分别通过的基础上，
针对 `docs/qa-CLAUDE.md` "必测场景" 清单中**尚未被任何脚本显式覆盖的安全/购买/回调细分场景**
进行人工接口测试查漏补缺，重点关注：

1. 伪造 JWT（修改 payload 不更新签名 / alg=none 攻击）
2. 普通用户 Token 访问 `/api/admin/*`（与"无 Token → 401"区分，验证返回 403 而非 401）
3. 封禁用户后 Token 立即失效（验证 `docs/full-api-design.md` 文档化的 `PATCH /api/admin/users/:id/status` 链路）
4. 缺少 `Idempotency-Key` 头的购买请求
5. 相同 `Idempotency-Key` 重复提交购买（返回原订单、不重复扣费）
6. 支付回调签名错误（余额不变、订单状态不被篡改）

---

## 一、测试方法说明（自包含方案，未触碰任何已有账号/数据）

按照统一约定，全部测试身份均为**本脚本自行创建**：

- 管理员账号：自行注册全新邮箱账号（`qa_manual_admin_<ts>@molin.io`，密码 `Test1234!`），
  通过 `INSERT IGNORE INTO user_roles` 直接绑定已有 `admin` 角色（不覆盖、不修改任何已有角色定义），
  重新登录获取带权限 Token —— 与 `tests/stage1_e2e_test.py` 的 `setup_admin()` 完全一致的套路；
- 普通用户 / 待封禁账号 / 购买测试账号 / 回调测试账号：均为本脚本注册的全新账号，
  实名状态 / 角色绑定均通过 `UPDATE users SET real_name_status='verified'`、
  `INSERT IGNORE INTO user_roles` 等增量 SQL 完成，全程**无任何 DROP / 覆盖型操作**；
- 全程未尝试登录任何已存在的账号（包括任何形式的"猜测/已知凭证"）。

本轮新增数据：user_id=49~53，role_id=23，product_id=15/plan_id=15，订单 order_id=52/53。
均为 INSERT 新增，未修改/删除任何已有记录。

---

## 二、总体结论：**部分通过（28/29，96.6%），发现 1 个 P1 缺陷**

| 用例分组 | 用例数 | 通过 | 失败 |
|---|---:|---:|---:|
| A. 伪造 JWT（含 alg=none 攻击） | 3 | 3 | 0 |
| B. 普通用户访问 /api/admin/* → 403 | 5 | 5 | 0 |
| C. 封禁用户 Token 立即失效 | 2 | 1 | **1（P1）** |
| D/E. Idempotency-Key（缺失头 / 重复提交幂等） | 7 | 7 | 0 |
| F. 支付回调签名错误（余额/订单状态不变） | 4 | 4 | 0 |
| 准备阶段（账号/角色/商品/充值环境搭建） | 8 | 8 | 0 |
| **合计** | **29** | **28** | **1** |

---

## 三、通过项详情

### A. 伪造 JWT 安全测试 — 全部通过 ✅

```
PASS  伪造 JWT（篡改 user_id，签名未更新）访问受保护接口 → HTTP 401（code=40001）
PASS  篡改签名段的 Token → HTTP 401（code=40001）
PASS  alg=none 攻击 Token → HTTP 401（服务端正确拒绝未签名 Token，code=40001）
```

验证方式：取一个真实账号的合法 Access Token，base64url 解码 payload 段，
篡改 `user_id`（伪装成另一个身份）后重新编码，**保留原签名段不变**，
构造出"看似合法但签名与内容不匹配"的伪造 Token；另外构造 `alg: none` 攻击向量
（部分历史 JWT 库存在校验缺陷，将算法声明为 `none` 并清空签名段即可绕过验签）。
三种伪造手法均被服务端 `pkgjwt.Parse` 正确拒绝并返回 `401/40001`，
说明 JWT 签名校验逻辑健壮，未发现越权伪造身份漏洞。

### B. 普通用户访问 `/api/admin/*` — 全部通过 ✅（关键查漏点）

```
PASS  普通用户 Token 访问 /api/admin/roles → HTTP 403（code=40003）
PASS  普通用户 Token 访问 /api/admin/permissions → HTTP 403（code=40003）
PASS  普通用户 Token 访问 /api/admin/users/50/roles → HTTP 403（code=40003）
PASS  普通用户 Token 访问 /api/admin/identity-verifications → HTTP 403（code=40003）
PASS  对照：无 Token 访问 /api/admin/roles → HTTP 401（与普通用户 403 正确区分）
```

**说明本次查漏的必要性**：现有 `tests/test_backend_a.py` A-02 第 13 项用例命名为
"普通用户访问管理接口（期望 403）"，但其断言代码实际调用的是**无 Token** 的请求并断言 `401`
（详见 `tests/test_backend_a.py:368-369`），并未真正验证"已登录的普通用户 Token"访问管理接口
应返回 `403`（已登录但无权限）而非 `401`（未登录）这一关键的权限语义区分点。
本轮人工测试用一个全新注册、未绑定任何管理角色的普通用户 Token 实际发起请求，
确认服务端能正确区分"未登录"（401/40001）与"已登录但权限不足"（403/40003）两种语义，
弥补了既有自动化用例命名与断言不一致导致的覆盖盲区。

### D/E. Idempotency-Key 幂等测试 — 全部通过 ✅

```
PASS  缺少 Idempotency-Key 头 → HTTP 400（code=40000）
PASS  首次提交（带 Idempotency-Key）下单成功，order_id=52，HTTP 200
INFO  首次扣费后余额: 95（购买前 100，扣减 5.00，与商品定价一致）
PASS  相同 Idempotency-Key 重复提交 → 返回同一订单 order_id=52（幂等生效）
PASS  重复提交未导致二次扣费（余额保持 95，与首次扣费后一致）
INFO  DB 中该用户针对此商品的订单总数: 1（交叉验证未产生重复订单）
```

验证链路：注册全新账号 → SQL 标记实名通过 + 绑定可购买角色 → 充值 100 元到账 →
对同一商品分别测试"不带 Idempotency-Key"（应 400 拒绝）和
"带相同 Idempotency-Key 重复提交两次"（应返回同一 `order_id`，且余额、DB 订单数均不重复变化）。
两种场景均符合预期，购买接口的幂等保护机制工作正常。

### F. 支付回调签名错误 — 全部通过 ✅

```
PASS  缺少签名头的回调 → HTTP 400（code=40000）
PASS  签名错误回调未导致余额变化（0 → 0）
PASS  充值订单状态未被错误置为已支付（当前 status=pending）
```

验证链路：注册全新账号 → 创建充值订单（不触发正常回调）→ 直接发送一个**不带任何
`Wechatpay-*` 签名头**的伪造回调报文 → 确认返回 `400/40000`，且回调前后余额
（均为 0）、订单状态（仍为 `pending`，未被篡改为 `paid`）均无变化。
说明伪造回调无法绕过签名校验影响账户余额，符合安全预期。

---

## 四、未通过项详情（P1 — 待修复）

### 缺陷 #M1【P1】：`PATCH /api/admin/users/:id/status` 接口未实现，导致管理员无法封禁用户、"封禁用户 Token 立即失效"安全场景无法被任何官方渠道触发

**优先级**：P1（核心功能不可用 —— 安全闭环缺失，必测安全场景无法验证）

**复现步骤**：
1. 自助注册一个全新管理员账号并通过 SQL 绑定 `admin` 角色，重新登录获取带权限 Token；
2. 自助注册一个全新"待封禁"测试账号，确认其 Token 可正常访问 `GET /api/me`（基线，HTTP 200）；
3. 管理员调用 `docs/full-api-design.md` 第 590-601 行明确文档化的接口：
   ```
   PATCH /api/admin/users/{id}/status
   Body: {"status": "disabled", "reason": "..."}
   ```

**期望结果**：
返回 `200`，目标用户状态被置为 `disabled`，且其存量 Access Token 在不重新登录的情况下
**立即**（下一次请求即生效）被拒绝，返回 `401`（与 `docs/qa-CLAUDE.md` 必测场景
"封禁用户后 Token 立即失效 → 期望 401" 完全一致）。

**实际结果**：

```
HTTP 404，body={}
```

接口返回 `404 Not Found`，说明该路由**根本没有被注册**。进一步代码走查确认：

- `grep -rn "users/{id}/status\|users/:id/status\|UpdateUserStatus" server/internal/` **零命中**
  —— 服务端没有任何文件实现或注册过该路由 / handler；
- 但 `server/internal/modules/auth/service/auth_service.go` 内部**已经完整实现**了
  `BanUser` / `UnbanUser` / `IsUserBlocked` 三个方法（第 343-381 行）：
  - `BanUser`：将 `users.status` 置为 `disabled`，并写入 Redis 黑名单 `blocked:user:<id>`，
    TTL 与 Access Token 有效期一致；
  - `UnbanUser`：删除黑名单 key 并恢复 `status='active'`；
- `server/internal/middleware/auth.go` 的 `RequireAuth` 中间件也**已经对接**了
  `BanChecker.IsUserBlocked` 接口（第 37-41 行），命中黑名单时返回 `401/40101`
  （"账号已被封禁"），中间件层逻辑设计完整、注释清晰；
- 但是，纵观 `server/internal/modules/auth/route.go` 全部已注册路由
  （仅有 `POST /api/admin/auth/verify-phone`、`verify-email` 两个 admin 路由），
  **`BanUser`/`UnbanUser` 没有被任何 handler 调用、没有被暴露为任何 HTTP 接口**。

**影响分析**：

- `docs/full-api-design.md` 第 590-601 行已将"修改用户状态"列为正式 API 规范
  （`PATCH /api/admin/users/:id/status`，支持 `active`/`disabled`），但代码层面只完成了
  "下半截"（service + middleware），完全遗漏了"上半截"（handler + route 注册），
  导致这是一个**半成品功能**——内部机制健全但无法从产品/界面层触达；
- 实际后果：**当前系统中，管理员没有任何官方渠道可以封禁/解封一个用户**。
  无论是后台管理界面还是直接调 API，都无法触发 `BanUser`，
  `docs/qa-CLAUDE.md` 中明确列出的必测安全场景
  "封禁用户后 Token 立即失效 → 期望 401" **在当前系统中根本无法被复现验证**——
  不是"验证未通过"，而是"压根没有可供验证的入口"；
- 这是一个安全闭环缺口：风控/客服一旦需要紧急封禁恶意账号（如盗号、刷单、欺诈），
  现有系统**没有任何手段**可以做到，只能依赖直接操作数据库（绕过应用层、无审计记录、
  无法触发 Redis 黑名单从而无法让存量 Token 立即失效），属于运营层面的高风险缺口。

**期望结果**：补全 handler + 路由注册，将已有的 `AuthService.BanUser`/`UnbanUser`
暴露为 `PATCH /api/admin/users/:id/status` 接口（与文档定义一致），
打通"管理员操作 → DB 状态变更 → Redis 黑名单写入 → 中间件立即拦截存量 Token"全链路。

**日志/截图**：

```
请求：PATCH /api/admin/users/51/status
Body: {"status": "disabled", "reason": "QA 人工验收：验证封禁后 Token 立即失效"}
Authorization: Bearer <自建管理员 Token>

响应：HTTP 404
Body: {}
```

**环境**：测试服务器 `8.130.9.163:8080`（与本地 `server/internal/modules/auth/route.go`
代码核对一致，非环境配置问题，是代码层面遗漏）

---

## 五、与既有自动化测试结果对比

| 测试脚本 | 用例数 | 结果 | 备注 |
|---|---:|---|---|
| `tests/test_backend_a.py`（A-01/02/03） | 33 | 33/33 通过 | 认证/IAM/实名认证模块基础回归 |
| `tests/stage1_e2e_test.py`（第一阶段最终验收） | 37 | 37/37 通过 | 端到端全链路（注册→购买→资产），2 个历史 P1 缺陷均已修复闭环 |
| `tests/bugfix_retest.py`（缺陷复测） | — | 已通过 | 支付回调幂等 / 签名校验 / 实名状态码 等历史缺陷复测 |
| **本轮人工补充测试 `manual_gap_test.py`** | **29** | **28/29（96.6%）** | **新增覆盖：伪造JWT/alg=none攻击、普通用户403语义、Idempotency-Key缺失与重复提交、回调签名错误余额校验；发现 1 个 P1（封禁接口缺失）** |

**结论**：既有三套自动化脚本已覆盖的场景（注册登录、购买闭环、资产生成、会员定价、
回调幂等、签名头部存在性校验等）本轮均未发现新增回归，与既有"通过"结论一致。
本轮重点针对"必测场景清单"中**既有脚本未显式覆盖或断言名实不符**的细分点
（伪造 JWT 的具体攻击手法、403 与 401 的语义区分、Idempotency-Key 的缺失/重复行为、
回调签名错误时的资金安全性）进行补充验证，新发现 1 个此前从未被任何回归脚本触及的
**P1 级"功能缺失"型缺陷**（封禁接口未实现）——这恰好印证了人工接口测试对自动化回归
"按文档逐条核对官方 API 是否真正存在"这一查漏维度的必要性。

---

## 六、建议

1. **【P1，建议本周内修复】** 尽快补全 `PATCH /api/admin/users/:id/status` 接口
   （handler + 路由注册），打通既有 `BanUser`/`UnbanUser`/Redis 黑名单/中间件拦截的完整链路，
   并在修复后**立即重跑**本报告中"用例 C"以验证"封禁用户后 Token 立即失效 → 401"的端到端闭环；
2. 建议同时检查是否存在与"解封"对应的接口缺口（`UnbanUser` 同样未暴露），
   一并补全，避免出现"能封不能解"的二次缺陷；
3. 建议复核 `tests/test_backend_a.py` A-02 用例 13 的命名与断言不一致问题
   （命名为"普通用户访问管理接口（期望 403）"，实际断言"无 Token → 401"），
   修正用例命名或补充真正的"已登录普通用户 → 403"断言，避免后续维护者误判覆盖范围
   （本轮已通过人工测试补齐该断言空白，详见用例 B）；
4. 其余测试场景（伪造 JWT、Idempotency-Key、回调签名错误）均表现良好，无需整改。

**是否允许本周合并上线**：**否（P1 未修复）** —— 封禁接口缺失属于安全运营闭环缺口，
建议在修复并复测通过前不要将其视为"已具备生产环境运营能力"对外宣布，
但**不阻塞当前已验收通过的购买闭环等核心交易链路**继续推进。
