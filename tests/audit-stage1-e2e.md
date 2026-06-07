# 第一阶段（Week 1-4）整体验收 — 端到端全链路回归测试报告

**测试日期**：2026-06-07
**测试环境**：测试服务器 `8.130.9.163:8080`，MySQL `13306`
**测试脚本**：`tests/stage1_e2e_test.py`
**测试人员**：测试工程师（QA）
**对应需求**：`docs/development-execution-plan.md` 第 216-227 行「第一阶段测试用例」

---

## 一、测试目标与方法

本轮验收**不重复**各模块周度验收已覆盖的孤立接口测试，重点是用一个连贯脚本模拟真实用户的
完整业务旅程，打通 **注册 → 实名认证 → 充值/钱包 → 浏览应用商品 → 下单购买 → 支付回调/扣费
→ 生成资产 → 资产到期字段** 全链路，验证模块间协作的正确性。

测试数据通过 `INSERT`/`UPDATE` 方式新增（新注册账号 + SQL 播种角色绑定/会员关系/实名状态），
**未对已有数据做任何 DROP/覆盖操作**。

---

## 二、结论

**部分通过（35/37 用例通过，发现 2 个 P1 级缺陷）**

核心业务闭环（注册→实名→充值→购买→扣费→生成流水→生成资产）**全链路打通且功能正确**，
钱包扣费、订单状态流转、资产生成、权限即时生效等关键机制工作正常。

但发现 **2 个 P1 级缺陷**，需要后端工程师修复并重新验收后方可上线：

1. **【P1】全新用户首次购买触发 HTTP 500**（钱包记录懒创建链路缺陷，影响所有未访问过
   `/api/wallet`/未充值过的新用户首次购买）
2. **【P1/设计缺口】"非会员无法购买会员专属应用" 业务规则未实现**（系统当前不存在与
   `membership_level` 绑定的购买访问门槛，仅有会员价格隔离机制）

详见下文「四、缺陷清单」。

---

## 三、用例逐条验证结果

| # | 验收用例 | 结论 | 说明 |
|---|---|---|---|
| 1 | 用户可以使用邮箱注册 | ✅ 通过 | `POST /api/auth/register/email`，HTTP 201，返回 access_token/refresh_token |
| 2 | 用户可以使用手机号注册 | ✅ 通过 | `POST /api/auth/register/phone`，HTTP 201 |
| 3 | 用户可以使用邮箱登录 | ✅ 通过 | `POST /api/auth/login/email`，邮箱+密码登录返回 token |
| 4 | 用户可以使用手机号登录 | ✅ 通过 | `POST /api/auth/login/phone`，**注意：手机号登录使用验证码而非密码**（与邮箱登录机制不同，属设计如此，非缺陷；首轮因测试脚本误用密码登录而失败，已修正） |
| 5 | 用户注册后可以提交实名认证 | ✅ 通过 | `POST /api/identity/verifications` 提交成功，`GET /api/identity/verifications/me` 返回 `status=pending` |
| 6 | 未实名用户不能购买商品 | ✅ 通过 | `POST /api/products/{id}/purchase` → HTTP 400，`code=70001`，符合 `frontend-api-reference.md` 规范 |
| 7 | 重复邮箱不能注册两个用户 | ✅ 通过 | 二次注册返回 HTTP 409 |
| 8 | 重复手机号不能注册两个用户 | ✅ 通过 | 二次注册返回 HTTP 409 |
| 9 | 普通用户购买普通应用 | ✅ 通过 | 实名认证通过 + 余额充足 + 角色 can_buy=true → 购买成功，订单 `status=paid`，`amount=9.9` |
| 10 | VIP 用户购买会员价应用 | ✅ 通过 | 用户拥有有效 `user_memberships` 记录（level=qa_gold）时，按会员专属价 `6.00` 成交，而非默认价 `20.00`；`PricingService` 价格优先级（会员价 > 角色价 > 默认价）逻辑验证正确 |
| 11 | 非会员无法购买会员专属应用 | ❌ **不通过（P1，业务规则缺失）** | 同角色但无 `user_memberships` 记录的"非会员"账号成功以默认价 `20.00` 完成购买（HTTP 200），系统未对其进行任何拦截。详见缺陷 #2 |
| 12 | 用户余额不足无法购买 | ✅ 通过 | 余额为 0 时下单返回 HTTP 400，`code=60001` |
| 13 | 钱包扣费后生成流水 | ✅ 通过 | 购买后 `GET /api/wallet/transactions` 与 DB `wallet_transactions` 表均生成 `type=consume, direction=out, amount=9.9, balance_after=100` 记录，余额扣减金额与商品价格一致 |
| 14 | 订单支付成功后生成资产 | ✅ 通过 | 购买成功（订单 `status=paid`）后，`GET /api/my/assets` 返回对应商品资产记录，`status=active`，`expires_at` 按套餐 `duration_days=365` 正确计算（购买日 + 365 天） |
| 15 | 管理员修改用户角色后权限立即生效 | ✅ 通过 | 管理员通过 `POST /api/admin/users/{id}/roles` 为在线用户分配新角色后，**无需用户重新登录**，`GET /api/products` 商品列表立即按新角色的 `can_view` 规则更新可见性，权限实时生效 |
| 16 | 用户权限被禁用后无法访问应用 | ✅ 通过 | 管理员通过 `DELETE /api/admin/users/{id}/roles/{role_id}` 撤销角色后，目标商品立即从用户的 `GET /api/products` 列表中消失（`can_view` 权限被收回），验证了权限被禁用后访问立即受限 |

**额外链路验证（非清单内，作为全链路完整性补充）：**

| 验证点 | 结论 | 说明 |
|---|---|---|
| 充值 → 支付回调 → 余额到账 | ✅ 通过 | `POST /api/recharge/orders` 创建订单 + 模拟微信回调（携带 `Wechatpay-Signature/Timestamp/Nonce` 头）→ 余额从 0 增至 109.9，回调链路打通 |
| 资产到期字段 | ✅ 通过 | 资产 `expires_at` 字段正确写入（购买时间 + `duration_days`），到期机制数据结构完整 |
| 用户权益记录 `user_entitlements` | ℹ️ 信息 | 本次购买的套餐为 `one_time` 一次性商品，未生成配额型权益记录，属预期范围（按量/订阅类商品的权益消耗链路本轮未覆盖，建议后续专项测试） |

---

## 四、缺陷清单

### 缺陷 #1【P1】全新用户首次购买（钱包记录尚未创建）触发 HTTP 500

**优先级**：P1（核心功能不可用 —— 影响真实业务场景下相当比例的新用户首次购买）

**复现步骤**：
1. 注册一个全新账号（**全程不调用 `GET /api/wallet`，不发起任何充值**）
2. 通过管理员将其实名状态置为 `verified` 并分配具备 `can_buy` 权限的角色
3. 直接调用 `POST /api/products/{id}/purchase`（带合法 `Idempotency-Key`）

**期望结果**：HTTP 400，`code=60001`（余额不足）

**实际结果**：HTTP 500，`{"code":50000,"message":"record not found","data":null}`

**根因分析（代码走查）**：
- 钱包记录采用懒创建机制：仅 `WalletService.GetByUserID`（即 `GET /api/wallet` 接口）和
  充值到账路径会调用 `walletRepo.GetOrCreate`，在钱包不存在时自动 `INSERT` 一条余额为 0 的记录；
  用户**注册时不会创建钱包记录**。
- 但购买扣费路径 `WalletService.Deduct → deductOnce → walletRepo.GetForUpdate`
  （`server/internal/modules/billing/repository/wallet_repo.go:60`）直接执行
  `SELECT ... FOR UPDATE`，**对钱包不存在的场景没有兜底创建或错误转换**，
  原始 `gorm.ErrRecordNotFound`（`"record not found"`）被一路透传到
  `PurchaseService.Purchase` 并最终返回给 handler。
- `product_handler.go` 的 `errors.Is` 错误分支只识别
  `ErrRealNameRequired / ErrNoAccess / ErrInsufficientBalance / ErrNoPriceConfigured`，
  无法匹配原始的 `gorm.ErrRecordNotFound`，于是落入 `default` 分支返回
  `http.StatusInternalServerError, 50000, err.Error()`，将内部错误信息
  `"record not found"` 直接暴露给客户端。

**影响范围**：所有"从未访问过钱包页面、未充值过、注册后第一次就尝试购买"的新用户
（这是真实业务中很可能发生的场景，例如用户从应用市场详情页直接点击购买）都会遭遇
不可理解的 500 错误，而非清晰的"余额不足"提示，属于阻断性缺陷，建议作为 P1 修复。

**修复建议**：在 `WalletService.Deduct`/`deductOnce` 中改用 `GetOrCreate`（事务内，
配合行锁）替代 `GetForUpdate` 直接查询，或在 `GetForUpdate` 内对
`gorm.ErrRecordNotFound` 做转换，统一返回 `ErrInsufficientBalance`（钱包不存在
等价于余额为 0）。

**日志**：
```
2026/06/07 18:0x:xx [.../billing/repository/wallet_repo.go:63] record not found
HTTP 500 {"code":50000,"message":"record not found","data":null}
```

---

### 缺陷 #2【P1 / 设计缺口】"非会员无法购买会员专属应用" 业务规则未实现

**优先级**：P1（核心业务规则缺失，直接影响验收用例清单中的"会员体系"闭环）

**复现步骤**：
1. 创建一个商品，配置默认价 `20.00` + 会员专属价（`membership_level_id=qa_gold`）`6.00`
2. 创建账号 A：分配普通购买角色 + 创建有效 `user_memberships` 记录（VIP 会员）
3. 创建账号 B：分配相同的购买角色，但**不创建** `user_memberships` 记录（非会员）
4. 实名认证通过、余额充足后，分别用 A、B 两个账号下单购买该商品

**期望结果**（按验收用例清单业务预期）：账号 A 按会员价 `6.00` 成交；账号 B（非会员）
应被拦截，无法购买该"会员专属"商品

**实际结果**：
- 账号 A（VIP）：HTTP 200，`amount=6`，按会员价成交 —— **符合预期**
- 账号 B（非会员）：HTTP 200，`amount=20`，**按默认价正常成交，未受任何拦截**

**根因分析（代码走查）**：
- 购买访问控制 `PurchaseService.Purchase` 第 2 步仅校验
  `accessRepo.CanBuy(productID, roleIDs)`（即 `product_role_access.can_buy`），
  是**纯角色维度**的访问控制，与 `membership_level` 完全无关。
- 价格计算 `PricingService.GetPrice` 实现的是"会员价 > 角色价 > 默认价"的**定价优先级**，
  而非"访问门槛"——非会员用户查不到会员价时会自动回退到角色价/默认价，**仍然可以正常下单**。
- 通读 `server/internal/modules/membership/`、`product/`、`order/` 全部路由与服务代码，
  **未发现任何"会员专属商品"的访问控制实现**：
  - `docs/full-api-design.md` 中提及的 `product_membership_rules`（商品会员规则，
    包含 `rule_type` 字段，理论上可用于表达"仅会员可购买"语义）在路由层完全不存在
    （`grep` 全代码库仅在 `membership/CLAUDE.md` 设计文档中出现，未落地为表/接口/服务）
  - `POST /api/memberships/:id/purchase`（会员购买接口，设计文档 `full-api-design.md`
    第 5.2 节列出）也未在路由中注册，意味着**当前系统中根本不存在"购买会员"的标准入口**，
    `user_memberships` 表只能依赖 SQL 直接写入或未公开的内部机制产生

**结论**：该用例所依赖的"会员专属商品访问门槛"业务规则在当前代码中**完全未实现**，
不是 Bug（缺陷），而是**功能缺口（Gap）**——属于第一阶段验收范围内但尚未交付的能力。
鉴于验收用例清单明确将其列为必测项，建议按 P1 处理：要么在本阶段补齐实现后重新验收，
要么由产品经理明确决策将其调整为后续阶段交付，并同步更新验收范围文档。

**建议修复/补齐方向**：
1. 在 `product_role_access` 或新增 `product_membership_rules` 表中引入
   "会员专属"标记（如 `rule_type=member_only` 或 `require_membership_level_id`），
   在 `PurchaseService.Purchase` 的访问校验阶段加入会员等级判定；
2. 或在商品维度引入 `membership_required` 类字段，purchase 流程中对无有效
   `user_memberships` 的用户直接返回 `403/40003`（无购买权限）。

---

## 五、其他观察（非缺陷，供参考）

1. **手机号登录使用验证码而非密码**：`POST /api/auth/login/phone` 请求体为
   `{phone, code}`（验证码登录），与邮箱登录 `{email, password}`（密码登录）机制不同。
   这是合理的产品设计（手机号登录场景常用验证码），但 `docs/frontend-api-reference.md`
   未明确说明两种登录方式参数的差异，建议补充文档说明，避免前端/测试人员误用。

2. **支付回调签名校验需附带 `Wechatpay-Signature/Wechatpay-Timestamp/Wechatpay-Nonce`
   请求头**：`WechatVerifier.Verify` 当前仅校验三个头是否存在（真实 RSA-SHA256
   验签逻辑标记为 TODO，等待接入微信 APIv3 商户证书），不存在则返回 400/缺少签名字段。
   这是 Week 2 P1 安全修复引入的合理行为（符合"签名错误回调应返回 400"的安全要求），
   但意味着后续所有依赖"模拟支付回调"的测试脚本（包括本轮）都必须显式携带这三个
   请求头才能完成充值链路，建议在相关测试文档/脚本中统一注明，避免后续测试人员
   误判为充值链路故障。

3. **`POST /api/products/{id}/purchase` 响应未包含 `asset_id` 字段**：
   `docs/full-api-design.md` 第 4.4 节文档描述返回 data 包含 `order_id、order_no、
   status、asset_id`，但实际 `dto.PurchaseResult` 仅含
   `order_id/order_no/status/amount/idempotent`，无 `asset_id`（资产是异步生成，
   下单时尚不存在资产记录，文档与实现不一致）。建议更新文档去除 `asset_id` 字段
   描述，或在资产生成完成后通过订单详情/资产列表查询，避免误导前端预期同步获取
   `asset_id`。（P3，文档纠偏，不阻断）

---

## 六、测试覆盖小结

| 类别 | 用例数 | 通过 | 失败 |
|---|---|---|---|
| 注册/登录/重复校验（用例 1-4, 7-8） | 6 | 6 | 0 |
| 实名认证（用例 5） | 2 | 2 | 0 |
| 未实名拦截（用例 6） | 1 | 1 | 0 |
| 普通购买/余额不足/流水/资产（用例 9, 12, 13, 14） | 5 | 5 | 0 |
| 会员价/会员专属（用例 10, 11） | 2 | 1 | 1 |
| 角色权限即时生效（用例 15, 16） | 6 | 6 | 0 |
| 缺陷复现（钱包懒创建链路） | 1 | 0 | 1 |
| 旁路验证（资产到期字段、权益记录） | 2 | 2 | 0 |
| **合计** | **37** | **35** | **2** |

通过率：**94.6%**（核心 16 项验收用例中 15 项通过，1 项因业务规则未实现而不通过）

---

## 七、建议

1. **P1 缺陷 #1（钱包懒创建链路 500）和 #2（会员专属购买规则缺失）需要后端工程师
   确认并修复后，重新执行本端到端脚本进行复测**，确认两个缺陷修复且不引入新问题后
   方可允许整体合并/上线。
2. 缺陷 #2 本质是业务规则的实现缺口，建议产品经理参与决策：是在本阶段补齐，
   还是明确调整到后续阶段并同步更新验收范围文档（避免"验收用例清单与实际交付范围
   不一致"反复出现）。
3. 文档纠偏项（第五节）建议一并提交给文档维护者更新，降低后续联调和测试的沟通成本。

**是否允许本阶段合并上线：否**（存在 2 个 P1 未修复缺陷）
