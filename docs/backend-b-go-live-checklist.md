# 后端乙上线检查单（交易与计费域）

> 适用：product / order / billing / finance_consumer
> 前置：fix-plan F1~F6 全部修复并合并 main，两轮 QA 回归通过（见 `docs/regression-backend-b-fixes.md`、`docs/final-regression-f3-p3.md`），测试库已迁移至 000025。
> 制定：2026-06-15

## 1. 代码与回归状态（已完成 ✅）

| 项 | 状态 |
|---|---|
| F1 资金安全护栏（回调金额校验 / O3 order_type / 移除明文 notify_body 回传） | ✅ #120 |
| F2 free_quota 计费 + B-06 内部接口共享密钥鉴权 | ✅ #121 |
| F6 并发健壮性（重试退避 + 409 + 删瞬时脏单） | ✅ #122 |
| F5 真实支付验签（微信 APIv3 / 支付宝 RSA2，fail-closed） | ✅ #123 |
| F4 钱包健壮性 + B-03 幂等 txid（migration 000025） | ✅ #125 |
| F3 订单列表过滤补全 | ✅ #126 |
| 两轮 QA 回归（B-01~B-06 + F5 + F3 + F4 + B-03） | ✅ 全闭环 |
| BUG-A 购买事务完整性（创建+扣费+MarkPaid 同事务，status 直接返回 paid） | ✅ #136 |
| BUG-B 商品/套餐不存在返回 404/40400（原返回 200 或 500） | ✅ #136 |
| BUG-C 重复 product_code/plan_code 返回 400 友好提示（屏蔽 MySQL 1062 原文） | ✅ #136 |
| BUG-D 多套餐价格覆盖写入改为单事务原子操作 | ✅ #136 |
| D-008 钱包响应字段 `id` → `wallet_id` | ✅ #135 |
| D-009 价格接口 body 结构对齐：`{"items":[{"product_plan_id":...,...}]}` | ✅ #135 |
| D-011 ReplaceAccess nil 保护：缺失 `items` 键返回 400（防静默删除） | ✅ #137 |
| 全量回归（test_bug_abcd + week2_product_billing + test_pr128_filters，88/88 PASS） | ✅ 2026-06-16 |

## 2. 上线前必办（运维 / 配置，**未完成 ⬜**）

### 2.1 生产环境变量（均 fail-closed，不配则相关功能全拒）
- [ ] `NOTIFY_BODY_KEY`：32 字节，支付回调报文 AES-256-GCM 加密密钥。
- [ ] `INTERNAL_API_TOKEN`：内部上报接口共享密钥；调用方（计费上报来源）须带请求头 `X-Internal-Token`。
- [ ] `WECHAT_PLATFORM_PUBLIC_KEY`：**微信支付平台证书公钥**（PEM 文本或文件路径）。⚠️ 测试用的是 test-only 自签密钥，生产必须换**真实平台公钥**。
- [ ] `ALIPAY_PUBLIC_KEY`：**支付宝公钥**（PEM 文本或文件路径），同上须换真实公钥。

### 2.2 数据库迁移
- [ ] 生产库执行 `migrate up` 至 **000025**（含 wallet:manage 000023、base-roles 000024、consumption wallet_transaction_id 000025）。
- [ ] 顺序：**先迁移，后上线新代码**（wallet:manage 等权限码、列必须先在库）。

### 2.3 网关 / 网络（防内部接口被绕过）
- [ ] `/api/internal/*`（尤其 `product-usage-events`，直接扣款）**不对公网暴露**（仅内网/服务网格可达）。
- [ ] 反向代理**剥离客户端伪造的 `X-Real-IP` / `X-Forwarded-For`**，只注入可信源 IP（QA 实测：边缘注入 `X-Real-IP=127.0.0.1` 会使 IP 白名单对公网放行；目前靠 `INTERNAL_API_TOKEN` 共享密钥兜底，生产应双重收紧）。
- [ ] 确认 `INTERNAL_ALLOWED_IPS` 配置与真实内网拓扑一致。

### 2.4 首个管理员
- [ ] 按 `server/migrations/README-base-roles.md`：方案 A（注册后 DBA 授 admin 角色）或方案 B（`cmd/seed-admin` + `BOOTSTRAP_ADMIN_PASSWORD_HASH` 注入）创建首个 admin 用户。

## 3. 真实支付渠道联调（上线前 / 灰度）
- [ ] 用**真实**渠道密钥 + 真实回调报文跑通：验签通过 → 金额校验（callback amount == order amount）→ 幂等入账 → 订单 paid → 钱包流水。微信金额单位为分需换算、支付宝为元。
- [ ] 回调失败/伪造报文：验签失败返回 HTTP 400；金额不符 callback 记 `ignored` 不入账。

## 4. 已知非阻断残留（P3，可上线后处理）
- 测试库历史明文回调记录（id 1/2，前轮未配 NOTIFY_BODY_KEY 时产生）——生产全新库无此。
- （其余 P3 均已在 #125 修复：unfreeze 文案、幂等 txid。）

## 5. 回滚预案
- 各 migration 均有 `.down.sql`；代码按 PR 粒度可 revert。
- 钱包/订单资金不变量在所有改动中保持（FOR UPDATE + 乐观锁 + 流水只追加），回滚不影响已入账数据。
</content>
