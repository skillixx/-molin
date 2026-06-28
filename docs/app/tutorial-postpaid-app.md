# 教程：从零接入一个「按量付费」应用（postpaid）

> 目标读者：第三方应用开发者。读完你能独立写出一个接入平台、按使用量从用户钱包扣费的应用。
> 配套可运行代码：[`examples/postpaid-app/`](../../examples/postpaid-app/)。
> 前置阅读（可选）：[开发者对接指南](./developer-integration-guide.md)、[字段级契约](./billing-integration-spec.md)。

---

## 1. 我们要做什么

做一个「文本转换」小工具：用户输入文本，应用转成大写并统计字数。**每转换一次，按量从用户钱包扣一次钱**。

选 postpaid（后付/按量）的判断依据：**用一次收一次钱、单价固定、先用后扣钱包**。
（如果是"先买积分包、用时扣积分"，那是 prepaid，见 [预付教程](./tutorial-prepaid-app.md)。）

---

## 2. 先分清边界：平台做什么，你做什么

```
平台做的：卖你的应用、收钱、发资产凭证、按规则扣钱包、记消费流水、对账
你做的：  应用功能本身  +  在"用户用了一次"时调一下计费接口
```

你**几乎不写扣费逻辑**——扣钱、防并发、免费额度都是平台做。你只负责两件交互：
认人（用户是谁）、报量（用了多少）。

---

## 3. 你需要平台方提供什么（只要这些就能独立开发）

| 平台方下发 | 说明 |
|---|---|
| `INTERNAL_API_TOKEN` | 调内部接口的共享密钥，放服务端环境变量 |
| 已加白名单 | 平台方把你服务器出口 IP 加入 `INTERNAL_ALLOWED_IPS`（同机/本机用 `127.0.0.1`） |
| `usage_type` 约定 | 与平台计费规则一致的用量类型名，本例用 `text_convert` |
| 测试账号 | 一个有钱包余额、已购买你应用的普通用户 |

> 拿到这四样，你就能完全离线地把应用写好、联调通。其余（怎么把应用挂成商品、计价多少）是平台方的事。

---

## 4. 端到端流程

```
用户在平台「我的资产」点「进入应用」
        │  平台签发一次性票据 lt_xxx，浏览器跳转
        ▼
你的应用  GET /enter?ticket=lt_xxx
        │  ① 调 verify 换 user_id（免登）
        ▼
        进入工作台 /workspace
        │  用户点"转换"
        ▼
你的应用  做业务（转大写） + ③ 调 product-usage-events 上报用量
        │
        ▼
        平台按规则扣钱包，返回实扣金额
```

---

## 5. 代码逐步讲解

项目结构（[`examples/postpaid-app/`](../../examples/postpaid-app/)）：

```
postpaid-app/
├─ config.py          # 集中读环境变量（平台地址、密钥、usage_type）
├─ platform_client.py # 把"调平台接口"收敛在这一层（verify / report_usage）
├─ app.py             # FastAPI：/enter（认人）、/workspace（页面）、/api/convert（业务+计费）
├─ requirements.txt
├─ .env.example
└─ README.md
```

### 5.1 身份：票据换 user_id（`app.py:enter`）

用户带 `?ticket=lt_xxx` 进来，你调 `verify` 换身份。票据**一次性、60s 过期、防重放**：

```python
claims = platform.verify_ticket(ticket)        # POST /api/internal/app-launch/verify
# → {user_id, app_id, product_id}
_set_session(resp, {"user_id": claims["user_id"], "product_id": claims["product_id"]})
```

- 票据无效/过期/已用 → 平台返回 `40003`，让用户**重新从平台进入**，不要重试同一张票据。
- 拿到 `user_id` 后用你**自己的会话**（这里是签名 Cookie）维持登录，**不要**在 URL 里塞平台 JWT。

### 5.2 用前校验：本例不需要额外查

平台**签发票据前已经校验过**"用户对该应用持有 active 资产"。所以拿到有效票据，就说明用户有权用，
postpaid 不必再查一次资产。（要更严谨可加 `GET /api/my/assets`，但那需要用户 JWT，本流程没有。）

### 5.3 用时计费：上报用量（`app.py:convert` → `platform_client.report_usage`）

业务完成后上报一次用量，平台据计费规则扣钱包：

```python
billing = platform.report_usage(
    event_id=event_id,                          # 你生成的 UUID，对账用
    user_id=sess["user_id"],
    product_id=sess["product_id"],
    usage_amount="1",                           # 本次用量；这里每转换一次算 1
    idempotency_key=f"{event_id}:{config.USAGE_TYPE}",   # 全局唯一，重复上报不二次扣
    occurred_at=datetime.now(timezone.utc).isoformat(),
)
# billing["amount"] 是本次实扣金额
```

请求体里的 `usage_type`（在 `platform_client` 内固定为 `config.USAGE_TYPE`）**必须与平台计费规则一字不差**。

### 5.4 错误分类（`platform_client.PlatformError`）

| 平台返回 | 含义 | 你该做什么 |
|---|---|---|
| `40000` + “未找到匹配的计费规则” | 该商品没配这类计费 | **静默跳过**，业务照常，不计费、不重试 |
| `60001` | 钱包余额不足 | 提示用户充值 |
| `40003` | 鉴权失败（token/IP） | 查 `INTERNAL_API_TOKEN`、IP 白名单 |

> ⚠️ `40000` 既是参数错误也是"无规则"，**靠 message 串区分**（代码里 `is_no_billing_rule`）。

---

## 6. 本地运行

```bash
cd examples/postpaid-app
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env          # 填入平台方给的 INTERNAL_API_TOKEN
uvicorn app:app --reload --port 9001
```

---

## 7. 平台方侧要配什么（你作为平台方/管理员时）

让这个应用真正"能被买、能扣费"，平台方需要在管理后台依次：

1. **建应用**：`POST /api/admin/apps`，拿到 `applications.id`；设 `access_url` 指向你的 `/enter`，`status=active`。
2. **挂成商品**：建 `product_type=application`、`business_ref_id=应用id` 的商品，配套餐/价格/`can_buy`，`status=active`。
3. **配计费规则**：为 `(product_id, usage_type=text_convert)` 配 `product_billing_rules`（单价、可选免费额度）。
4. **内部凭证**：生成 `INTERNAL_API_TOKEN`（`openssl rand -hex 32`）配进平台 env；把开发者 IP 加进 `INTERNAL_ALLOWED_IPS`。
5. **测试账号**：给一个有钱包余额、已购买该应用的普通用户。

> 详细配置见 [应用怎么挂成商品（设计）](./billing-integration-design.md)、[平台方接入任务清单](./platform-integration-tasks.md)、[应用管理指南](../app-management-guide.md)。

---

## 8. 联调自测清单

```
□ 测试账号在市场能看到并购买该应用
□ 购买后从「我的资产」点「进入应用」，能带 ticket 跳到 /enter
□ /enter 能换出 user_id 并进入 /workspace
□ 转换文本后，返回里有 billed_amount，且平台消费流水 +1 条
□ 重复同一 idempotency_key 上报，金额不二次扣（幂等）
□ 钱包余额耗尽后，转换返回"余额不足"提示
□ 未配计费规则时，业务照常、不计费、不报错
```

---

## 9. 一句话总结

> 认人（票据换 user_id）→ 做业务 → 报量（product-usage-events，带幂等键）。三步做完，按量计费就通了。
