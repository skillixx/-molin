# 应用接入示例（examples）

两个**最小可运行**的第三方应用示例，配套教程，帮你快速熟悉「接入本平台并计费」的完整流程。
技术栈：Python + FastAPI（对接逻辑都收敛在各自的 `platform_client.py`，换语言照着改这一层即可）。

| 示例 | 计费方式 | 业务 | 教程 |
|---|---|---|---|
| [`postpaid-app/`](./postpaid-app/) | 按量付费（扣钱包） | 文本转换工具，每用一次按量扣费 | [tutorial-postpaid-app.md](../docs/app/tutorial-postpaid-app.md) |
| [`prepaid-app/`](./prepaid-app/) | 预付/扣积分（扣额度） | AI 文案生成，按实际消耗扣积分（预占→结算） | [tutorial-prepaid-app.md](../docs/app/tutorial-prepaid-app.md) |

## 两种方式怎么选

```
用一次收一次钱、单价固定、先用后扣钱包   → postpaid（按量）   看 postpaid-app
先买积分包、用时扣积分额度               → prepaid （预付）   看 prepaid-app
```

## 共同的对接骨架

两个示例都遵循同一套骨架，区别只在"用时计费"那一步：

```
① 身份：用户带 ?ticket= 进来 → verify 换 user_id（免登）
② 用前：postpaid 无需额外校验；prepaid 要先用 user-entitlements 解析 entitlement_id
③ 用时：postpaid → product-usage-events（扣钱包）
        prepaid  → reserve/settle（扣积分，防并发）或 entitlement-consume（一步扣）
```

## 本地体验（无需真平台）

目录下自带一个 **mock 平台**（`mock-platform/`，内存模拟平台内部接口 + 签发票据，仅演示用），
两种方式看效果：

```bash
# 方式一：浏览器交互（在你自己的机器上）
bash run_local.sh
# 然后打开 http://127.0.0.1:8080 → 点「进入应用」

# 方式二：进程内端到端验证（不占端口/不需浏览器，直接打印计费结果）
.venv/bin/python verify_demo.py postpaid
.venv/bin/python verify_demo.py prepaid
```

> mock 平台不是真平台；对接真平台时把示例 `.env` 的 `PLATFORM_BASE_URL`/`INTERNAL_API_TOKEN`
> 换成平台方给的真实值即可，示例应用代码无需改动。
>
> 注：示例 `app.py` 里的内联 HTML（`/workspace` 等）只是**演示脚手架**，不是平台前端页面，
> 仅为让示例能跑起来看到效果；正式产品 UI 请由前端实现。

## 你只需要平台方给这些就能独立开发

- `INTERNAL_API_TOKEN`（内部接口密钥）
- 把你的服务器出口 IP 加进平台 `INTERNAL_ALLOWED_IPS`（同机用 `127.0.0.1`）
- 一个测试账号（按量：有钱包余额并已购应用；预付：已购积分套餐）
- 按量付费还需一个 `usage_type` 约定

各示例目录下的 `README.md` 有详细启动步骤。
