# 教程：从零接入一个「预付/扣积分」应用（prepaid）

> 目标读者：第三方应用开发者。读完你能独立写出一个接入平台、按使用扣积分额度的应用。
> 配套可运行代码：[`examples/prepaid-app/`](../../examples/prepaid-app/)。
> 前置阅读（可选）：[开发者对接指南](./developer-integration-guide.md)、[字段级契约](./billing-integration-spec.md)。

---

## 1. 我们要做什么

做一个「AI 文案生成」小工具：用户给一个主题，应用生成一段文案。**每次生成按实际消耗扣积分**。

选 prepaid（预付/扣积分）的判断依据：**用户先买积分包，使用时扣积分额度、不实时扣钱包**。
本例还演示一个进阶点：生成的成本**事前不确定**（像 LLM 出多少 token 不一定），所以用
「**预占 → 结算**」防并发透支——先按上限占住积分，生成完按实际多退少补。

---

## 2. 边界与 postpaid 的关键差异

```
平台做的：卖积分套餐、开通额度凭证(user_entitlements)、原子扣减额度、防并发透支
你做的：  应用功能 + 定位用户的积分权益 + 用时扣额度（预占/结算）
```

与按量付费最大的不同有两点：

1. **不用 `usage_type`**：积分消耗数由你调用时直接指定（`actual_cost`），不靠平台规则算。
2. **多一步"定位权益"**：扣积分要 `entitlement_id`，而 SSO 票据只给 `user_id/product_id`，得先解析。

---

## 3. 你需要平台方提供什么

| 平台方下发 | 说明 |
|---|---|
| `INTERNAL_API_TOKEN` | 调内部接口的共享密钥 |
| 已加白名单 | 你服务器出口 IP 加入 `INTERNAL_ALLOWED_IPS`（同机用 `127.0.0.1`） |
| 测试账号 | 一个**已购买积分套餐**的普通用户（这样才有 entitlement 可扣） |

> 注意没有 `usage_type`——预付不需要。

---

## 4. 端到端流程（注意比 postpaid 多了"定位权益"和"预占/结算"）

```
用户点「进入应用」→ 平台签票据 → 跳转 /enter?ticket=lt_xxx
        │
        ▼
① verify 换 {user_id, product_id}            （票据没有 entitlement_id！）
        │
        ▼
② user-entitlements 按 user_id+product_id 解析出可用权益 entitlement_id
        │
        ▼
       进入 /workspace，用户点"生成"
        │
        ▼
③ reserve(预估上限) 预占积分  ──额度不足──► 拒绝(60005)
        │ 成功拿 hold_id
        ▼
       执行生成业务，得到实际消耗 actual_cost
        │
        ├─ 成功 → ④ settle(hold_id, actual_cost)   多退少补，计入 quota_used
        └─ 失败 → ④ release(hold_id)                回滚预占，不扣积分
```

---

## 5. 代码逐步讲解

项目结构（[`examples/prepaid-app/`](../../examples/prepaid-app/)）：

```
prepaid-app/
├─ config.py          # 环境变量（平台地址、密钥、预占上限）
├─ platform_client.py # verify / resolve_entitlement / reserve / settle / release
├─ app.py             # /enter（认人+定位权益）、/workspace、/api/generate（预占→生成→结算）
├─ requirements.txt
├─ .env.example
└─ README.md
```

### 5.1 身份 + 定位权益（`app.py:enter`）

```python
claims = platform.verify_ticket(ticket)               # → {user_id, app_id, product_id}
ent = platform.resolve_entitlement(                   # 票据没有 entitlement_id，按商品解析
    claims["user_id"], claims["product_id"]
)
if ent is None:
    return "你还没有可用的积分额度，请先购买积分套餐"
_set_session(resp, {
    "user_id": claims["user_id"],
    "product_id": claims["product_id"],
    "entitlement_id": ent["entitlement_id"],          # 存进会话，后续扣额度要用
})
```

**为什么需要 `resolve_entitlement` 这一步**：扣积分的接口（reserve/settle/consume）都要 `entitlement_id`，
但 SSO 票据只给 `user_id/product_id`，应用又没有用户 JWT 去调 `/api/my/entitlements`。所以平台提供了内部接口：

```
GET /api/internal/user-entitlements?user_id={uid}&product_id={pid}   （X-Internal-Token）
→ data.entitlements: [{entitlement_id, quota_total, quota_used, remaining, status, expires_at, usable}]
```

`resolve_entitlement` 取第一个 `usable=true` 的权益。

### 5.2 预占积分（`app.py:generate` → `platform_client.reserve`）

生成前先按预估上限占住积分。额度不足平台直接拒（`60005`），靠平台行锁防并发透支：

```python
hold = platform.reserve(
    entitlement_id, user_id, config.RESERVE_ESTIMATE,  # 预占上限，如 10
    idempotency_key=f"{req_id}:reserve",
)
hold_id = hold["hold_id"]
```

> ⚠️ **不要自己"查余额→if 够→再扣"**：并发下会超扣。够不够交给 `reserve` 的原子判定，查余额只为体验。

### 5.3 生成 + 结算/释放（多退少补）

```python
try:
    text = _generate_copy(topic)                       # 你的业务
    actual_cost = min(config.RESERVE_ESTIMATE, max(1, len(text)//10))
except Exception:
    platform.release(hold_id, idempotency_key=f"{req_id}:release")   # 失败回滚，不扣
    raise

settled = platform.settle(                             # 成功结算，按实际消耗
    hold_id, actual_cost, idempotency_key=f"{req_id}:settle",
)
# settled["quota_used"] 累计已用；settled["available"] 结算后可用余额
```

`actual ≤ 预占额` 计入 `quota_used`，差额自动归还。每个内部调用都带**全局唯一幂等键**
（这里用 `请求UUID:动作`），重复调用不重复扣。

### 5.4 什么时候不用预占、直接一步扣

如果用量**事前已知**（如"修改一次固定扣 2 积分"），不必预占，直接：

```
POST /api/internal/entitlement-consume   body: {entitlement_id, user_id, amount, idempotency_key}
```

预占/结算只为"事前不知道实际用量"的贵动作（生成、转发 LLM）准备。

---

## 6. 本地运行

```bash
cd examples/prepaid-app
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env          # 填入平台方给的 INTERNAL_API_TOKEN
uvicorn app:app --reload --port 9002
```

---

## 7. 平台方侧要配什么

1. **建应用 + 挂商品**：同按量教程第 7 节，但商品套餐要在 `quota_json` 里声明积分额度：
   `{"entitlement_type":"...","quota_total":1000,"quota_unit":"credits","valid_days":365}`。
2. **购买开通额度**：用户购买该积分套餐后，平台开通生成 `user_entitlements`（带 `quota_total`）——这才有 `entitlement_id` 可解析。
3. **内部凭证 + 白名单**：`INTERNAL_API_TOKEN` 配进 env，开发者 IP 加白名单。
4. **测试账号**：给一个**已购买积分套餐**的用户。

> 详见 [应用怎么挂成商品（设计）](./billing-integration-design.md)、[平台方接入任务清单](./platform-integration-tasks.md)。

---

## 8. 联调自测清单

```
□ 测试账号已购买积分套餐，GET /api/my/entitlements 能看到额度
□ 从「我的资产」进入应用，/enter 能解析出 entitlement_id 并进工作台
□ 生成一次：返回 actual_cost、quota_used 增加、available 减少
□ 额度耗尽后，生成返回"积分不足"（60005）
□ 重复同一 idempotency_key，不重复扣（幂等）
□ 业务故意抛错时，预占被 release 回滚，quota_used 不变
```

---

## 9. 一句话总结

> 认人 → 定位权益（user-entitlements 拿 entitlement_id）→ 预占(reserve) → 生成 → 结算(settle)/释放(release)。
> 贵动作用预占防超扣，幂等键防重复扣，余额判定交给平台原子扣减。
