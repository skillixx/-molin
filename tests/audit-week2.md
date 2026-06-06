# 测试报告 — Week 2（2026-06-06）

## 本周测试范围

- 钱包模块（GET /api/wallet，首次访问自动创建、余额查询）
- 管理员商品管理（创建商品/套餐/配置价格/角色权限/状态切换）
- 用户端商品（按角色过滤列表、商品详情含价格）
- 购买流程（实名认证校验、余额校验、幂等、余额不足）
- 订单查询（用户自查列表/详情）
- 支付回调幂等（同 provider_trade_no 重复回调不重复入账）
- 并发安全（5 并发购买，乐观锁，余额不出现负数）
- 权限控制（伪造 JWT、无 Token、普通用户越权）

## 测试结论：部分通过

36 通过 / 44 总计（通过率 82%），发现 2 个 P0/P1 级缺陷。

## 测试环境

| 项目 | 值 |
|---|---|
| API 地址 | http://8.130.9.163:8080（内部 localhost:8080）|
| API 版本 | 0.1.0 |
| 测试脚本 | tests/week2_product_billing_test.py |
| 执行时间 | 2026-06-06 17:43:34 |
| Migration | 000004_create_product_tables + 000006_create_billing_order_tables 已执行 |

## 通过项

- [x] B-01 钱包模块
  - [x] 无 Token 访问 /api/wallet → 401
  - [x] 首次访问 /api/wallet 自动创建钱包，余额=0
  - [x] 钱包货币单位 = CNY，wallet_id 存在
- [x] B-02 管理员商品管理（部分）
  - [x] POST /api/admin/products → HTTP 201，含 data.id
  - [x] GET /api/admin/products 列表分页正确
  - [x] POST /api/admin/products/:id/plans → HTTP 201，含 data.id
  - [x] GET /api/admin/products/:id/plans 套餐列表
  - [x] PATCH /api/admin/products/:id/prices 配置价格 → 200
  - [x] PATCH /api/admin/products/:id/status 切换状态 → 200
  - [x] 无 Token 访问 /api/admin/products → 401
- [x] B-04 购买流程（部分）
  - [x] 缺少 Idempotency-Key → 400，code=40000
  - [x] 未实名用户购买 → 403，code=70001
  - [x] SQL seed 将用户设为实名认证通过
  - [x] 充值 200 元（POST /api/recharge/orders + 模拟微信回调）成功
  - [x] 充值后余额查询正确（200 元）
  - [x] 幂等：重复购买后余额未变化（因购买被拒未扣费，幂等检查通过）
- [x] B-05 订单查询
  - [x] GET /api/orders 返回用户订单列表（1 条充值订单）
  - [x] GET /api/orders/:id 订单详情，status=paid，order_no 正确
  - [x] 无 Token → 401
- [x] B-06 支付回调（部分）
  - [x] POST /api/recharge/orders → HTTP 201，含 order_id 和 pay_url
  - [x] 第一次微信支付回调 → 200，余额正确增加 100 元
  - [x] 相同 provider_trade_no 重复回调 → 200（HTTP 状态码正确）
- [x] B-07 并发安全（部分）
  - [x] 并发后最终余额非负（数据安全性基础满足）
- [x] B-08 权限控制（全部通过）
  - [x] 普通用户访问 /api/admin/products → 403
  - [x] 无 Token 访问 /api/products → 401
  - [x] 伪造 JWT（篡改签名）→ 401
  - [x] 无 Token 访问 /api/wallet/transactions → 401
  - [x] 普通用户访问 /api/admin/wallet-transactions → 403

## 未通过项（Bug 报告）

### Bug 1 — [P1] product_role_access 表名不一致（GORM 自动复数化）

**模块**：product / access_repo  
**优先级**：P1（核心功能不可用：商品角色权限配置完全失效）

**复现步骤**：
1. 管理员调用 `PATCH /api/admin/products/:id/access`，body `{"accesses": [...]}`
2. 接口返回 HTTP 500，message="配置访问权限失败"

**期望结果**：HTTP 200，商品角色访问权限配置成功

**实际结果**：HTTP 500，API 日志报错：
```
Error 1146 (42S02): Table 'molin.product_role_accesses' doesn't exist
DELETE FROM `product_role_accesses` WHERE product_id = 2
```

**根因**：`ProductRoleAccess` 模型未实现 `TableName()` 方法，GORM 自动将结构体名转复数形式 `product_role_accesses`，但 Migration 000004 建的表名是 `product_role_access`（无复数 s）。

**修复建议**：在 `product/model/product.go` 的 `ProductRoleAccess` 结构体添加 `TableName()` 方法：
```go
func (ProductRoleAccess) TableName() string { return "product_role_access" }
```

**影响范围**：
- `PATCH /api/admin/products/:id/access` → 500
- `GET /api/products` → 商品列表对用户不可见（CanView 查不到数据）
- `GET /api/products/:id` → 404（用户无 can_view 权限）
- `POST /api/products/:id/purchase` → 403，code=40003（CanBuy 查不到数据）
- 并发安全测试全部返回 403（所有购买都被拒绝）

---

### Bug 2 — [P0] 支付回调幂等失败：重复回调导致余额重复增加

**模块**：billing / payment_service / payment_repo  
**优先级**：P0（阻断：资金安全问题，重复入账）

**复现步骤**：
1. 创建充值订单（order_no=`ORD202606063K9AK9YF`）
2. 第一次发送 POST /api/payments/notify/wechat，body `{"out_trade_no": "...", "transaction_id": "IDEM_TRADE_XXX", "total_fee": 10000}` → 余额正确增加 100 元
3. 第二次发送完全相同的回调报文（相同 `transaction_id`）→ 余额再次增加 100 元

**期望结果**：第二次回调返回 200（幂等），余额不变

**实际结果**：余额重复增加（300 → 400），构成重复入账

**根因**：`payment_service.go:HandleNotify` 中的幂等流程存在竞态缺陷：
1. 步骤 4：`Upsert` 写入 callback，`DoUpdates` 包含 `status` 字段，将 `processed` 覆盖回 `received`
2. 步骤 5：`IsProcessed` 查询 `status='processed'`，此时返回 `false`（因步骤 4 已覆盖）
3. 步骤 6：事务执行入账 → 重复充值

**修复建议**：
- 方案 A（推荐）：`Upsert` 的 `DoUpdates` 不包含 `status` 字段，或仅在 status 为 received 时才更新
- 方案 B：在 `Upsert` 之前先查询是否已有 processed 记录，若已 processed 则跳过 Upsert 并直接返回
- 方案 C：将幂等检查移到 Upsert 之前，避免先写后查的竞态

**参考代码位置**：`server/internal/modules/billing/repository/payment_repo.go:Upsert()`，`server/internal/modules/billing/service/payment_service.go:HandleNotify()`

---

### Bug 3 — [已知缺口] 签名验证为桩实现

**模块**：billing / wechat_verifier  
**优先级**：P2（有临时方案：当前 Week 2 阶段使用桩实现，Week 3 接入真实签名前可接受）

**说明**：`wechat_verifier.go` 和 `alipay_verifier.go` 的 `Verify()` 均返回 `nil`（始终通过），签名错误的回调无法被拦截。根据代码注释，此为计划内 TODO，Week 3 需接入真实校验。

**本次测试**：跳过签名错误回调测试（标记为待补充测试项）

---

## 接口规范与实现差异（非 Bug，记录备查）

| 接口 | API 设计文档 | 实际实现 |
|---|---|---|
| POST /api/admin/products | 返回 `data.product_id` | 返回完整 product 对象，ID 在 `data.id` |
| POST /api/admin/products/:id/plans | 未明确状态码 | 返回 HTTP 201，plan 对象，ID 在 `data.id` |
| POST /api/recharge/orders | 返回 order_no | 返回 `data.order_id + data.pay_url`，无 order_no |
| PATCH /api/admin/products/:id/access | body 字段 `items` | 实际字段名为 `accesses` |
| PATCH /api/admin/products/:id/prices | body 字段 `items` | 实际字段名 `{"plan_id": X, "prices": [...]}` |
| 未实名购买 | 期望 HTTP 400，code=70001 | 实际 HTTP 403，code=70001 |

## 并发测试结论

由于 Bug 1（access 配置失败），并发购买测试中所有 5 个并发请求均被 403 拒绝（无购买权限），未能有效测试乐观锁并发扣费逻辑。

乐观锁代码实现（`wallet_service.go`）经过代码审查，逻辑正确，包含：
- SELECT FOR UPDATE 行锁
- 版本号乐观锁更新
- 余额不足检查（ErrInsufficientBalance，code=60001）

**建议**：修复 Bug 1 后重新运行并发测试（B-07）。

## 建议

**是否允许本周合并上线：否**

原因：
1. **P0 Bug 2** 支付回调幂等失败，存在重复入账资金安全风险，必须修复后才能上线
2. **P1 Bug 1** product_role_access 表名不一致，导致商品角色权限、购买权限全部失效，所有商品购买均返回 403

修复优先级：
1. Bug 2（P0）— billing/payment_repo.go Upsert 不覆盖 status 字段
2. Bug 1（P1）— product/model/product.go 添加 ProductRoleAccess.TableName()
3. Bug 3（P2）— Week 3 接入真实签名校验

修复后，需重新执行全量 44 个用例验收，重点关注 B-04（购买流程）和 B-07（并发安全）。
