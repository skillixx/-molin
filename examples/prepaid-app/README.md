# 预付/扣积分（prepaid）示例应用

一个最小可运行的第三方应用：演示「用户进入 → 用功能 → 扣积分额度」的完整对接。
业务功能是一个"AI 文案生成"小工具，每次生成按实际消耗扣积分，用「预占→结算」防并发透支。

> 配套教程（逐步讲解）：[`docs/app/tutorial-prepaid-app.md`](../../docs/app/tutorial-prepaid-app.md)

## 你需要平台方提供什么

| 项 | 用途 |
|---|---|
| `INTERNAL_API_TOKEN` | 调平台内部接口的密钥（高敏感，仅服务端用） |
| 你的服务器出口 IP | 平台方加进 `INTERNAL_ALLOWED_IPS` 白名单（同机/本机用 127.0.0.1） |
| 测试账号 | 一个**已购买积分套餐**的普通用户（这样才有 entitlement 可扣），用于联调 |

注意：预付和按量不同，**不需要** `usage_type`——积分消耗数由你调用时直接指定（`actual_cost`）。

## 启动

```bash
cd examples/prepaid-app
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env        # 填入平台方给的 INTERNAL_API_TOKEN
uvicorn app:app --reload --port 9002
```

## 跑通流程

1. 平台方为你的应用配好 `access_url`（指向你的 `/enter`）。
2. 测试用户在「我的资产」点「进入应用」→ 带票据跳到 `/enter?ticket=lt_xxx`。
3. 应用 `verify` 换 `user_id` → **`user-entitlements` 按 user_id+product_id 解析出 entitlement_id** → 建会话。
4. 用户生成文案：`reserve` 预占 → 生成 → `settle` 按实际消耗结算（失败 `release` 回滚）。

## 为什么需要 user-entitlements 这一步

SSO 票据只换得 `{user_id, app_id, product_id}`，**没有 `entitlement_id`**，而扣积分接口都要 `entitlement_id`。
应用又拿不到用户 JWT（无法调 `/api/my/entitlements`）。所以平台提供了内部接口
`GET /api/internal/user-entitlements?user_id=&product_id=`，让你用 `user_id+product_id` 解析出该用户在本商品下的可用权益。

## 对接四步在代码里的位置

| 步骤 | 文件:函数 |
|---|---|
| ① 身份：票据换 user_id | `app.py:enter` → `platform_client.py:verify_ticket` |
| ② 定位权益 entitlement_id | `app.py:enter` → `platform_client.py:resolve_entitlement` |
| ③ 预占积分 | `app.py:generate` → `platform_client.py:reserve` |
| ④ 结算/释放 | `app.py:generate` → `platform_client.py:settle` / `release` |

## 何时用「一步扣减」而非「预占→结算」

- 用量**事前已知**（如"修改一次固定扣 2 积分"）→ 直接 `entitlement-consume` 一步扣，无需预占。
- 用量**事前不定**（如生成、转发 LLM，按 max_tokens 预占、按实际结算）→ 用本例的 `reserve`→`settle`。

## 错误处理要点

- 额度不足 / 权益不可用（`60005`）→ 提示用户充值。
- 不要自己"查余额→if 够→再扣"：并发会超扣。**够不够交给 reserve/consume 的原子扣减判定**，查余额只为体验。
- 业务执行失败务必 `release` 回滚预占，否则积分被占住不释放。
