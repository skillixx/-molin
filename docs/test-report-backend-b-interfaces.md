# 后端乙接口功能验收测试报告（product / order / billing / finance_consumer）

- **测试对象**：已部署到测试服务器的后端乙全量接口
- **环境**：`http://8.130.9.163:8080`（main `4779eb2`，健康检查通过）
- **测试时间**：2026-06-16
- **测试脚本**：`tests/test_bug_abcd.py` / `tests/week2_product_billing_test.py` / `tests/test_pr128_backend_b_filters.py`

## 测试结论

**通过（88/88 PASS，0 FAIL，0 SKIP）**。BUG-A/B/C/D 四项缺陷修复均验证有效，购买闭环/幂等/并发安全/权限控制/过滤参数均符合接口设计规范。建议允许后端乙合并上线。

---

## 一、BUG-A/B/C/D 缺陷回归（test_bug_abcd.py，10/10 PASS）

| 缺陷 | 验证场景 | 结果 |
|---|---|---|
| BUG-B | PATCH /api/admin/products/999999 → HTTP 404, code=40400 | PASS |
| BUG-B | PATCH /api/admin/products/999999/status → HTTP 404, code=40400 | PASS |
| BUG-B | GET /api/admin/products/999999 → HTTP 404, code=40400 | PASS |
| BUG-C | 重复 product_code → HTTP 400, code=40000，消息"商品编码已存在" | PASS |
| BUG-C | 重复 plan_code → HTTP 400, code=40000，消息"套餐编码已存在" | PASS |
| BUG-D | PATCH /api/admin/products/{id}/prices 含 2 个套餐 → HTTP 200 | PASS |
| BUG-D | DB 验证两个套餐价格均已写入（原子性） | PASS |
| BUG-A | POST /api/products/{id}/purchase → HTTP 200，status=paid | PASS |
| BUG-A | 购买响应 idempotent=false，余额正确扣减 | PASS |
| BUG-A | 相同 Idempotency-Key 重复购买 → HTTP 200，idempotent=true，余额未变 | PASS |

---

## 二、后端乙全量接口（week2_product_billing_test.py，49/49 PASS）

### B-01 钱包

| 用例 | 结果 |
|---|---|
| 无 Token → 401 | PASS |
| 首次查询自动创建钱包，余额=0，currency=CNY | PASS |
| 响应含 wallet_id（D-008 修复） | PASS |

### B-02 管理员商品管理

| 用例 | 结果 |
|---|---|
| POST /api/admin/products → HTTP 201 | PASS |
| GET /api/admin/products（列表） | PASS |
| POST /api/admin/products/{id}/plans → HTTP 201 | PASS |
| PATCH /api/admin/products/{id}/access（`{"items":[...]}` D-011） | PASS |
| DB 验证访问权限写入（D-011 核心验证） | PASS |
| PATCH /api/admin/products/{id}/prices（`{"items":[{"product_plan_id":...}]}` D-009） | PASS |
| PATCH /api/admin/products/{id}/status → HTTP 200 | PASS |
| 无 Token → 401 | PASS |

### B-03 用户端商品

| 用例 | 结果 |
|---|---|
| GET /api/products（按角色过滤） | PASS |
| GET /api/products/{id}（含 plans/user_price） | PASS |
| GET /api/products/{id}/plans（套餐列表） | PASS |

### B-04 购买流程

| 用例 | 结果 |
|---|---|
| 缺 Idempotency-Key → 400/40000 | PASS |
| 未实名购买 → 400/70001 | PASS |
| 余额充足购买 → 200，status=paid | PASS |
| 幂等重复购买 → 200，idempotent=true | PASS |
| 余额不足 → 400/60001 | PASS |

### B-05 订单查询

| 用例 | 结果 |
|---|---|
| GET /api/orders（列表含已购订单） | PASS |
| GET /api/orders/{id}（详情 status=paid） | PASS |
| 无 Token → 401 | PASS |

### B-06 支付回调

| 用例 | 结果 |
|---|---|
| POST /api/recharge/orders → HTTP 201 | PASS |
| 无签名头回调 → HTTP 400/40000（RSA fail-closed 生效） | PASS |
| DB 充值后余额增加正确 | PASS |
| payment_callbacks 记录结构验证（禁止返回 notify_body） | PASS |

### B-07 并发安全

| 指标 | 值 |
|---|---|
| 并发线程数 | 5 |
| 商品单价 | 30 CNY |
| 初始余额 | 100 CNY |
| 购买成功次数 | 3 |
| 乐观锁冲突（409/50000） | 2 |
| 余额不足拒绝（60001） | 0 |
| 最终余额 | 10 CNY（100 - 3×30，精确吻合） |
| 负余额 | 无 |
| **结论** | **并发扣费安全，无超扣、无负余额** |

### B-08 权限控制

| 用例 | 结果 |
|---|---|
| 普通用户访问 admin 接口 → 403/40003 | PASS |
| 无 Token → 401/40001 | PASS |
| 伪造 JWT → 401/40001 | PASS |

---

## 三、订单与流水过滤（test_pr128_backend_b_filters.py，29/29 PASS）

| 模块 | 关键验证点 | 结果 |
|---|---|---|
| T-001 用户端订单过滤 | status/order_type/时间区间/联合过滤；返回数据全部满足过滤条件 | PASS |
| T-002 管理员订单过滤 | 全量/status/order_type/联合/空时间段 | PASS |
| T-003 用户钱包流水过滤 | type/direction/时间区间/联合过滤；新账号空列表不报 500 | PASS |
| T-004 管理员钱包流水过滤 | 全量/type/direction/联合/时间区间；无 Token → 401 | PASS |

---

## 四、已修复缺陷状态

| 缺陷编号 | 描述 | 修复 PR | 状态 |
|---|---|---|---|
| B-01（原 F1#1a） | 支付回调金额未校验，可超额充值 | #120 | ✅ 已修复 |
| B-02（原 F1#2） | /orders/{id}/pay 未限制 order_type | #120 | ✅ 已修复 |
| B-03（原 F2#3） | free_quota 未扣减，免费额内全额计费 | #121 | ✅ 已修复 |
| B-04 | notify_body 明文返回（安全红线） | #120 | ✅ 已修复 |
| B-05（原 F6） | 并发购买锁冲突返回裸 500 + 脏单 | #122 | ✅ 已修复（409/50000 + 脏单删除） |
| BUG-A | 购买事务：status 返回 pending（应 paid） | #136 | ✅ 已修复 |
| BUG-B | 商品/套餐不存在返回 200/500 | #136 | ✅ 已修复（404/40400） |
| BUG-C | 重复 code 透传 MySQL 1062 原文 | #136 | ✅ 已修复（400 友好提示） |
| BUG-D | 多套餐价格非原子写入 | #136 | ✅ 已修复（单事务） |
| D-008 | 钱包响应 `id` 字段应为 `wallet_id` | #135 | ✅ 已修复 |
| D-009 | 价格接口 body 结构不符文档 | #135 | ✅ 已修复 |
| D-011 | ReplaceAccess 缺 items 键静默删除所有规则 | #137 | ✅ 已修复（返回 400） |

---

## 五、上线建议

**建议上线**。所有 P0/P1 缺陷均已闭环，88 个测试用例全部通过。上线前仍需完成 `docs/backend-b-go-live-checklist.md` 第 2 节运维配置项（`NOTIFY_BODY_KEY`、`INTERNAL_API_TOKEN`、`WECHAT_PLATFORM_PUBLIC_KEY`、`ALIPAY_PUBLIC_KEY`、DB migrate 到 000025）。
