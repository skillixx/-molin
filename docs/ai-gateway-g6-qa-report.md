# AI 网关 G6 独立 QA 报告

> QA 对象：PR #325，功能提交 `4b84b89`
>
> 环境：本地 Windows、GitHub Actions Linux、一次性隔离 MySQL 8、远程测试环境；未访问或变更生产环境。

## 1. 自动化结果

| 验收项 | 结果 |
|---|---|
| `go test -count=1 ./...` | PASS |
| `go vet ./...` / `go mod verify` | PASS |
| 用户端 `type-check` / `lint` / `build` | PASS |
| 管理端 `type-check` / `lint` / `build` | PASS |
| G6 Playwright | 10/10 PASS |
| 阶段 5 敏感扫描 | PASS，`findings=0` |
| PR #325 六项 CI | PASS |
| 隔离 MySQL 8 | PASS，schema `66:0`，容器已清理 |

## 2. 关键业务断言

- `provider` 对外映射为 `provider_confirmed`，`reconciled` 保持不变，未知内部值不泄漏。
- 人工核定后，账本、详情和总览使用同一组权威用量。
- 有效月预算为 105 元；有预算 Project 使用 21 元；无预算 Project 的 500 元消费不进入分子，最终 DTO 比例为 `20.00%`。
- 跨用户详情返回 404，重复申诉返回 409，申诉人与请求归属由组合外键保证。
- 关键词安全拒绝和 SK 吊销均在上游调用前完成。
- 模型市场、详情、Project/SK、账单及详情抽屉在 1440、768、375 三档无横向溢出。

## 3. 测试环境证据

- 真实 Bifrost 请求：`req_478e03928009d186`。
- 结算金额：`0.00000100 CNY`；Usage 与钱包差异均为 `0.00000000`。
- 最终 API 部署：change `g6-4b84b89`，SHA256 `685b067d674a17e53811d418d187f8b77f9c86240f5c2bc25cb771b744eee4e3`。
- 测试环境 schema：`66:0`；API ready 与 Bifrost 双节点健康。
- 测试用户、SK、Project、会话和浏览器 JWT 已回收；请求与财务事实保留。

## 4. QA 结论

- P0：0
- P1：0
- P2：0
- P3：0
- QA 结论：**PASS，满足 QA 合并门禁。**

残余风险：大体积前端 chunk 与 Vite 兼容性警告不阻断 G6；本阶段仅覆盖已发布文字模型，不包含生产开放和图片、音频、视频执行。
