# 按量付费（postpaid）示例应用

一个最小可运行的第三方应用：演示「用户从平台进入 → 用功能 → 按量从钱包扣费」的完整对接。
业务功能是一个"文本转大写 + 字数统计"小工具，每用一次按量计费。

> 配套教程（逐步讲解）：[`docs/app/tutorial-postpaid-app.md`](../../docs/app/tutorial-postpaid-app.md)

## 你需要平台方提供什么

作为应用开发者，你**只需要**平台方给你这几样，就能独立开发：

| 项 | 用途 |
|---|---|
| `INTERNAL_API_TOKEN` | 调平台内部接口的密钥（高敏感，仅服务端用） |
| 你的服务器出口 IP | 平台方把它加进 `INTERNAL_ALLOWED_IPS` 白名单（同机/本机用 127.0.0.1 即可） |
| `usage_type` 约定 | 与平台计费规则一致的用量类型名（本例默认 `text_convert`） |
| 测试账号 | 一个有钱包余额、且已购买你这个应用的普通用户，用于联调 |

其余（应用怎么挂成商品、计费规则、价格）都由平台方在后台配置，你不用关心。

## 启动

```bash
cd examples/postpaid-app
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env        # 填入平台方给的 INTERNAL_API_TOKEN
uvicorn app:app --reload --port 9001
```

## 跑通流程

1. 平台方为你的应用配好 `access_url`（指向你的 `/enter`，如 `http://你的域名/enter`）。
2. 测试用户在平台「我的资产」点「进入应用」→ 平台带一次性票据跳到 `/enter?ticket=lt_xxx`。
3. 应用调 `verify` 换到 `user_id` → 建立会话 → 进入 `/workspace`。
4. 用户在工作台转换文本 → 应用调 `product-usage-events` 上报用量 → 平台扣钱包，返回实扣金额。

## 对接三件事在代码里的位置

| 步骤 | 文件:函数 |
|---|---|
| ① 身份：票据换 user_id | `app.py:enter` → `platform_client.py:verify_ticket` |
| ② 用前校验 | 本例无需额外校验（平台签票据前已校验持有 active 资产） |
| ③ 用时计费：上报用量 | `app.py:convert` → `platform_client.py:report_usage` |

## 错误处理要点

- 无匹配计费规则（`40000` + “未找到匹配的计费规则”）→ **静默跳过**，业务照常，不计费。
- 钱包余额不足（`60001`）→ 提示用户充值。
- 票据无效/过期/已用（`40003`）→ 让用户重新从平台进入，**不要重试同一张票据**。
