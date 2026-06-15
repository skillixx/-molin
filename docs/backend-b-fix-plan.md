# 后端乙缺陷修复任务单（senior-architect 审查）

> **来源**：2026-06-15 对后端乙（product / order / billing / finance_consumer）已合并代码的结构化审查
> **负责人**：后端工程师乙（`backend-b`）　|　**审查基线**：origin/main `fbd9708`
> **接口完整性结论**：product P1–P17 / order O1–O6 / billing B1–B8 / finance F1–F3 路由均已注册，**无缺失接口**；本任务单为 bug 与契约缺口修复。

---

## 执行次序总览

| 序 | 分支 | 修复项 | 优先级 | 依赖 |
|---|---|---|---|---|
| **F1** | `feature/backend-b-fix-txn-safeguard` | #1a 回调金额校验 + #2 O3 限制 order_type + **#1c payment-callbacks 移除明文 notify_body（B-04）** | 🔴 最高（资金/安全护栏） | 无 |
| **F2** | `feature/backend-b-fix-billing-correctness` | #3 free_quota 扣减 + #6 消费幂等返原结果 | 🟠 高（计费正确性） | 无 |
| **F3** | `feature/backend-b-fix-order-filters` | #4 订单列表补全过滤参数 | 🟡 中（契约补全） | 无 |
| **F4** | `feature/backend-b-fix-wallet-robustness` | #5 GetForUpdate 错误区分 NotFound | 🟢 低（健壮性） | 无 |
| **F5** | `feature/backend-b-payment-real-verify` | #1b 真实验签（微信 APIv3 / 支付宝 RSA2） | 🔴 上线前必须 | **外部**：支付渠道证书/密钥（运维配 env） |
| **F6** | `feature/backend-b-fix-concurrency-robustness` | **B-05 并发锁冲突返裸 500 + 遗留 failed 垃圾订单** | 🟡 中（健壮性，资金已安全） | 无 |

> 各序彼此独立、无代码依赖，可并行；建议优先级 **B-01/B-02(P0,F1) → B-03/B-04(P1,F2/F1) → F5 验签 → B-05(P2,F6) → 运维项 → 契约同步**。每序一个 feature 分支 + PR，独立验收。F5 因需外部凭据单独排期。
> QA 复测确认的新增发现（B-04/B-05/B-06、D-01/D-02）详见文末「附录」。

---

## F1 — 交易安全护栏（先做）

### #1a 支付回调金额校验
- **文件**：`server/internal/modules/billing/service/payment_service.go` → `HandleNotify`
- **问题**：当前按回调报文里的 `amount` 入账，未与 `order.Amount` 比对（设计 §4.4 要求「校验订单状态和金额」）。回调金额若大于订单金额会超额入账。
- **修复**：解析出 `amount` 后、入账前增加 `amount.Equal(order.Amount)` 校验；不符则**不入账**，将 `payment_callbacks.status` 记为 `ignored`（或记 warn 日志后返回），不调用 `rechargeTx`。
- **验收**：构造金额≠订单金额的回调 → 不入账、钱包余额不变、callback 记 ignored。

### #2 O3 支付限制 order_type
- **文件**：`server/internal/modules/order/service/pay_service.go` → `Pay`
- **问题**：pending 的**充值订单**也能被 `/api/orders/{id}/pay` 钱包扣款「支付」——扣钱却不入账，且把充值订单置 paid，导致后续真实回调因订单非 pending 被幂等跳过、永久无法入账。
- **修复**：加载订单、校验归属后，增加 `if order.OrderType != "product" { return ErrOrderNotPending }`（或新增更明确的哨兵错误 + handler 映射 40000）。仅允许产品订单走钱包支付。
- **验收**：对 recharge 订单调用 `/pay` 返回错误且**不扣款**；产品订单正常。

---

## F2 — 计费正确性

### #3 消费计费扣减 free_quota
- **文件**：`server/internal/modules/finance_consumer/service/consumer_service.go` → `Handle`
- **问题**：`amount = rule.PriceAmount × event.UsageAmount`，完全忽略 `rule.FreeQuota`。设计要求「扣除 free_quota 后再计费」，且 `billing_rule_service` 已校验 free_quota≥0，说明该字段本应生效 → 免费额度内用量被全额扣费。
- **修复**：`billable = max(0, UsageAmount − FreeQuota)`（FreeQuota 为 nil 视为 0）；`amount = PriceAmount × billable`；`billable ≤ 0`（全额在免费额度内）时**跳过扣费**，写一条金额为 0 的消费记录（保留幂等 + 用量留痕），返回 amount=0。
- **验收**：用量 ≤ free_quota 不扣费；用量 > free_quota 仅对超出部分计费。

### #6 消费幂等并发竞态
- **文件**：同 `consumer_service.go` → `Handle`
- **问题**：同 `idempotency_key` 并发时，两请求均过首次查重 → 第二个事务 `Create` 命中唯一键冲突 → 事务回滚**返错**（不重复扣费，安全，但返回 500 而非原结果）。
- **修复**：事务内 `Create` 返回唯一键冲突错误时，跳出事务后 `FindByIdempotencyKey` 重查并返回原记录的 `ToResult()`，使并发重复请求两次都返回相同成功结果。
- **验收**：同 key 并发只扣一次，且两次响应一致、均成功。

---

## F3 — 订单列表过滤补全（契约缺口 #4）

- **文件**：`server/internal/modules/order/handler/order_handler.go` + `server/internal/modules/order/repository/order_repo.go`
- **问题**：契约（`docs/backend-dev-plan-backend-b.md` §3.2）要求
  - O1 `GET /api/orders`：`order_type / status / created_from / created_to`——当前 `ListOrders` **无任何过滤**（仅分页）。
  - O5 `GET /api/admin/orders`：当前 `AdminListOrders` 已支持 `user_id/status/order_type`，**缺 `created_from/created_to`**。
- **修复**：handler 解析上述 query 参数（时间支持 `RFC3339` 与 `2006-01-02`）；`repository.ListByUser`/`AdminListAll` 增加对应 `WHERE` 条件（状态/类型等值、时间区间 `created_at BETWEEN`）。
- **验收**：契约 §3.2 全部 query 参数可过滤；与 `docs/frontend-api-reference.md` 七章一致。

---

## F4 — 钱包健壮性（#5）

- **文件**：`server/internal/modules/billing/service/wallet_service.go`（`Recharge`/`Unfreeze`）+ `server/internal/modules/billing/service/payment_service.go`（`rechargeTx`）
- **问题**：`Recharge`/`rechargeTx` 把 `GetForUpdate` 的**任意错误**都当「钱包不存在 → 去创建」，会掩盖真实 DB 错误；`Unfreeze` 直接透传 `gorm.ErrRecordNotFound` 致 500。
- **修复**：仅当 `errors.Is(err, gorm.ErrRecordNotFound)` 时走「创建钱包 / 业务错误」分支，其余错误如实上抛（对齐 `Deduct`/`deductOnce` 的处理）。`Unfreeze` 对无钱包返回业务错误（如「冻结金额不足」或「钱包不存在」）而非 500。
- **验收**：DB 异常不再被误判为「钱包不存在」；Unfreeze 对无钱包返回业务错误。

---

## F5 — 真实支付验签（单独排期，上线前必须）

### #1b 接入真实签名校验
- **文件**：`server/internal/modules/billing/service/wechat_verifier.go`、`alipay_verifier.go`
- **问题（高危）**：当前两个 verifier 是桩——只检查签名头/`sign` 字段**是否存在**，不做真实密码学验签（代码内 `TODO Week 3`）。回调端点 `POST /api/payments/notify/{provider}` 无需登录，叠加 #1a 修复前的「金额不校验」→ 构成「免费充值」风险。
- **修复**：
  - 微信 APIv3：构造 `timestamp\nnonce\nbody\n` 签名串，用微信平台公钥 RSA-SHA256 验 `Wechatpay-Signature`。
  - 支付宝 RSA2：按字母序拼接参数（排除 `sign`/`sign_type`），用支付宝公钥验 `sign`。
  - 验签失败返回 HTTP 400。
- **前置（运维）**：商户证书 / API v3 密钥 / 支付宝公钥经环境变量注入，**不入库**。
- **验收**：错误/伪造签名返回 400；仅合法签名回调入账。配合 F1 #1a 金额校验，彻底关闭「免费充值」风险。

---

## 通用约束（每序都必须遵守）

1. 每序独立 `feature/backend-b-*` 分支 + PR；`go build ./... && go vet ./...` 通过；**不自行合并 main**（走产品经理 review + 用户确认）。
2. 中文注释 / 中文 commit（`修复：...`）；**不动他人模块与前端**；只 `git add` 自己改的后端文件（工作树存在并发前端在制内容，勿误提交）。
3. 资金类改动（F1/F2/F5）必须保持事务 + 乐观锁不被破坏；流水只追加。
4. 涉及契约/字段的（F3）落地后需同步 `docs/frontend-api-reference.md`（该对接文档由 Claude/后端甲维护，PR 合并后统一更新）。
5. 新增/变更权限码（本任务单无）一律配 seed migration——红线。

---

## 审查中确认正确、无需改动的部分（避免误改）
乐观锁 `version+1`、扣费 `FOR UPDATE`+乐观锁+流水原子、O3 扣费与 `pending→paid` 同事务 + `RowsAffected` 守卫 + 3 次重试、回调幂等用事务外 `originalStatus` 快照（规避 MarkPaid 返回值陷阱）、`notify_body` AES-256-GCM 加密、计费规则 plan→商品级回退匹配、R4 参数校验、R6 freeze 权限收紧、扁平分页 + 空列表 `[]`、越权防护（pay/cancel 仅本人、F2 强制本人过滤）——均正确，勿动。

---

## 附录：QA 复测新增发现（2026-06-15，测试报告 `docs/test-report-backend-b-interfaces.md`）

测试工程师对部署到测试服的接口做了全面验收：R1/R2/R3/R4/R5 常规契约、并发资金安全（20 并发购买余额精确归 0、无超扣无负余额）、幂等均通过；**F1#1a、F1#2、F2#3 复测确认真实存在；F2#6 已正确修复**。在此基础上**新增以下发现**，纳入本任务单一并修复：

### 并入 F1（交易安全护栏）

**#1c admin payment-callbacks 明文回传 notify_body（B-04，P1，安全红线）**
- **文件**：`server/internal/modules/billing/handler/admin_billing_handler.go`（`ListPaymentCallbacks`，约 line 175-177 主动解密回传）
- **问题**：`GET /api/admin/payment-callbacks` 响应直接返回完整回调 JSON（含明文 `notify_body`），违反 B8「响应禁止返回明文 notify_body」红线；测试服未配 `NOTIFY_BODY_KEY` 时 DB 亦明文存储。
- **修复**：管理端列表**移除 notify_body 字段**（或仅返回脱敏摘要/元信息）；并由运维注入 `NOTIFY_BODY_KEY` 修复存储侧。
- **验收**：`/api/admin/payment-callbacks` 响应不含明文报文；配置 key 后 DB 密文存储。

### 新增 F6 — 并发健壮性（B-05，P2）

- **文件**：`server/internal/modules/product/service/purchase_service.go` + `order` 创建时序
- **问题**：高竞争下乐观锁 3 次重试耗尽 → 向客户端返回**裸 500**；且每次冲突已先建订单 → 扣费失败置 `failed`，遗留大量 `failed` 垃圾订单（瞬时锁冲突非真实业务失败）。**资金本身安全（无超扣/无负余额）**。
- **修复**：扩大重试上限 + 指数退避；或将「创建订单」移到「扣费成功之后」，避免锁冲突产生 failed 脏单；耗尽重试时返回明确业务码（如 409 + 重试提示）而非 500。
- **验收**：20 并发购买不产生 failed 垃圾订单、不返回裸 500；资金不变量仍成立。

### 运维项（B-06，P3 + B-04 存储侧）

- 测试/生产环境注入 `NOTIFY_BODY_KEY`（回调报文加密）与 `INTERNAL_ALLOWED_IPS`（内部上报 IP 白名单）；并确认反代是否透传真实源 IP（否则 `/api/internal/product-usage-events` 白名单形同虚设）。**属运维配置，非代码。**

### 契约/文档同步（D-01/D-02，非缺陷，归 Claude/后端甲）

- **D-01**：`PATCH /api/admin/products/{id}/prices` 实际 body 为顶层 `plan_id` + `items:[{role_id?,membership_level_id?,price_amount,currency}]`（item **无** `product_plan_id`，一次配一个套餐），与本文档 §3 P14 / `frontend-api-reference.md` §5.4 不一致——以代码为准更新对接文档（或反之修代码，二选一）。
- **D-02**：创建类接口（如 `POST /api/admin/products`、计费规则创建）返回 **HTTP 201** + 完整对象（字段 `id` 而非 `product_id`），前端需兼容 201。
- 旁注（属后端甲/iam）：注册流程不分配默认全局角色（库无 `user` 角色），新用户 0 角色 → 开箱无法购买，需后端甲确认是否预期。

> 修复优先级（含新增）：**B-01/B-02(P0) → B-03/B-04(P1) → F5 验签 → B-05(P2) → 运维 B-06 → 契约同步**。
