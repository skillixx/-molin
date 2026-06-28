# 本地 mock 平台（仅演示用）

> ⚠️ 这**不是真平台**。真平台是 `server/` 下的 Go 实现。本目录用内存复刻了示例应用会调到的
> 几个平台内部接口 + 模拟「我的资产 → 进入应用 → 签发一次性票据」，让两个示例应用**代码原样不改**
> 就能在本地离线跑通完整链路（认人 → 计费/扣额度），方便你快速"看效果"。

## 它实现了什么

| 接口 | 行为（内存模拟） |
|---|---|
| `GET /` | 模拟"我的资产"页，两个「进入应用」按钮 |
| `GET /launch?app=postpaid\|prepaid` | 签发一次性票据并 302 跳转到应用 `/enter` |
| `POST /api/internal/app-launch/verify` | 校验并消费票据（一次性），返回 `{user_id, app_id, product_id}` |
| `POST /api/internal/product-usage-events` | 按量扣内存钱包（初始 5.00 元，单价 1.00/次），余额不足 `60001` |
| `GET /api/internal/user-entitlements` | 返回测试用户的积分权益（含 `entitlement_id`/余额/usable） |
| `POST /api/internal/entitlement-reserve\|settle\|release` | 预占/结算/释放积分（多退少补） |
| `GET /state` | 查看实时钱包/积分状态（JSON） |

测试用户固定 `user_id=1001`；内部密钥固定 `demo-internal-token-123`（与示例 `.env` 一致）。

## 跑起来

见上层 [`examples/README.md` 的「本地体验」](../README.md)。最快的方式：

```bash
# 进程内端到端验证（不依赖浏览器/端口，直接打印计费结果）
.venv/bin/python verify_demo.py postpaid
.venv/bin/python verify_demo.py prepaid
```
