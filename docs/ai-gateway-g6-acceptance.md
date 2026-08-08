# AI 网关 G6 验收记录

> 当前状态：**G6 阶段验收通过，最终独立代码评审、独立 QA、产品复验和 CI 均为 PASS，允许 PR #325 转 Ready 并合并。**
>
> 已有证据覆盖本地代码、自动化测试、隔离 MySQL 8、测试环境 Migration 000065、真实 Bifrost 请求、人民币金额/钱包对账、真实浏览器旅程和凭据回收。CI、最终独立评审、QA 与产品双签完成前，不得声明 G6 完成或允许生产开放。

## 1. 验收对象

| 项目 | 当前值 |
|---|---|
| 分支 | `feature/backend-d-ai-gateway-g6-customer-journey` |
| G5 验收提交 | `60b569f` |
| G6 实际开发基线 | `origin/main@c4126a6` |
| 最新功能提交 | `4b84b89` |
| 测试环境应用提交 | `4b84b89` |
| Migration | `000065_create_ai_gateway_g6_customer_journey`、`000066_enforce_ai_dispute_request_owner` |
| 部署范围 | 仅测试环境，禁止生产 |
| 多模态范围 | 仅已发布文字模型 |

## 2. 当前证据

| 门禁 | 状态 | 证据 |
|---|---|---|
| Token 网关定向 Go 测试 | PASS | `go test -count=1 ./internal/modules/token_gateway/... ./migrations` |
| 用户端类型检查 | PASS | `npm.cmd run type-check` |
| 管理端类型检查 | PASS | `npm.cmd run type-check` |
| 用户端 Playwright | PASS | 生产构建预览下 10 条通过：客户链路、Project/SK 一次性明文清理、文档状态、URL 前进后退、空/错误、账本三维状态/导出/申诉、详情失败重试、手机全宽筛选抽屉、键盘操作，以及模型市场/详情、Project/SK、账单/详情在 1440/768/375 三宽度无横向溢出 |
| 全量 Go/vet/mod verify | PASS | 本地 `go test -count=1 ./...`、`go vet ./...`、`go mod verify`；GitHub Actions Linux 执行 `go test -v -race -count=1 ./...` 通过 |
| 两端 lint/build | PASS | 用户端与管理端均通过 `type-check`、`lint`、`build`；用户端生产依赖审计为 0 个漏洞 |
| 可重复镜像构建 | PASS | `golang:1.25-alpine` 服务端、`node:24-alpine` 用户端和管理端镜像均完成本地 Docker 构建；Vite 5 开发依赖审计告警作为升级事项保留，不进入运行时镜像 |
| Migration 隔离 MySQL 8 验证 | PASS | 一次性 MySQL 8 容器完成 `64:0 -> 65:0 -> 66:0`、重复 up、事实保留 down、重新 up、历史文档初始化、文档健康/申诉约束、请求索引、跨用户申诉组合外键拒绝，并以真实 GORM 验证租户隔离、重复申诉和 SK 审计失败事务回滚 |
| 测试环境部署与真实浏览器 | PASS | 测试环境 schema `66:0`；桌面 `1440x1000` 与手机 `375x812` 真实页面通过，模型市场、人民币价格、快速入门、已撤销 SK、请求详情和申诉可追溯；两个宽度均无横向溢出 |
| 真实 Bifrost 与人民币对账 | PASS | Bifrost 负载入口 `127.0.0.1:18080`、两个节点健康；真实 request `req_478e03928009d186` 已结算 `0.00000100 CNY`，Usage 与钱包差异均为 `0.00000000`，价格版本 `v2` |
| 越权、吊销、不调用上游 | PASS | 跨用户请求详情返回 404；安全命中和 SK 吊销后均在网关前拒绝且未产生上游执行尝试；重复申诉返回 409 |
| 测试凭据回收 | PASS | 清理前测试集合为用户 2、活跃 SK 0、未归档 Project 1、请求事实 4；清理后为 `0|0|0|4`，会话撤销，本地和远程浏览器 JWT 已删除 |
| GitHub CI | PASS | PR #325 在验收证据提交 `4908a06` 上六项检查全部通过；最终状态文档提交后再次执行同一门禁 |
| 独立 QA | PASS | [独立 QA 报告](ai-gateway-g6-qa-report.md)：全量 Go、vet、mod verify、两端检查、10 条 Playwright、隔离 MySQL 8 和六项 CI 通过；P0/P1/P2/P3 均为 0 |
| 独立代码评审 | PASS | [独立评审记录](ai-gateway-g6-independent-review.md)：人工核定来源、预算 Project 集合和公开来源枚举三轮问题全部关闭；P0/P1/P2/P3 均为 0 |
| 产品复验 | PASS | 产品经理对 PR #325 精确 HEAD `4908a06` 最终签署：P0/P1/P2/P3 均为 0，允许转 Ready 并 Merge |

## 3. 测试环境真实 E2E 证据

| 项目 | 结果 |
|---|---|
| API 部署 | change `g6-4b84b89`，SHA256 `685b067d674a17e53811d418d187f8b77f9c86240f5c2bc25cb771b744eee4e3`，回滚目录 `/home/pc/molin/rollback/g6-4b84b89` |
| 完整前后端部署 | change `g6-75a3001-r1`，回滚目录 `/home/pc/molin/rollback/g6-75a3001-r1` |
| API 增量部署 | change `g6-9efb1bd`，回滚目录 `/home/pc/molin/rollback/g6-9efb1bd` |
| Migration 000066 | change `g6-4ef93de-m66`，`65:0 -> 66:0`；组合外键 1、组合索引 2 列、不一致事实 0；回滚备份 `/home/pc/molin/rollback/g6-4ef93de-m66`，SHA256 `38cd5bfc3d056b74c644368270650d0998a55c1c6d2a0ff17d0ef19445ec3457` |
| 模型与价格发布 | 模型发布版本 `v2`；旧价格窗口关闭后发布价格版本 `v2`，避免篡改历史快照 |
| 文档入口 | 模型介绍、快速入门、API 文档均以测试环境静态 URL 发布，健康状态为 `healthy`，三个入口返回 200 |
| 真实请求 | `req_478e03928009d186`，安全通过、执行成功、结算成功，16 输入 Token、1 输出 Token |
| 金额对账 | 请求结算、销售计量行、钱包消费流水和用户页面均为 `0.00000100 CNY`；Usage 差异和钱包差异均为 0 |
| 拒绝链路 | 关键词安全拒绝无上游尝试；吊销 SK 后拒绝且无上游尝试；跨用户详情 404；重复申诉 409 |
| 浏览器证据 | [桌面截图](evidence/g6/g6-real-desktop.png) SHA256 `3F750CDF2EA2B1F3CFEB53A7FA940C27BAF60499DBEC321816B5CA46E24399ED`；[手机截图](evidence/g6/g6-real-mobile.png) SHA256 `F75F97580E7AAB95BFD33B5D2C12643B1FC99776BEE7EC2B1328FB64A68A8B05`；真实页面断言抽屉背景 `rgb(14, 21, 33)`、请求 ID 文字 `rgb(248, 250, 252)`，两个视口均无横向溢出 |
| 清理与审计 | 测试身份、SK、Project、会话和浏览器 JWT 已回收；请求、计量、钱包、申诉事实保留；发布与清理均写审计日志 |
| 000066 部署后验证 | 跨用户申诉数据库探针被拒绝且残留 0；API `/api/health` 与 Bifrost `/health` 均返回 200 |

金额验收标准：请求 `settled_amount`、销售计量行合计、钱包 consume 流水和用户页面展示完全一致，差异为 `0.00000000 CNY`。

所有证据均来自测试环境。未部署生产，也未执行生产 Migration、流量切换或真实客户开放。

## 4. 阶段结论

G6 测试环境客户旅程、真实计费链路、Migration 000066、最终独立代码评审、独立 QA、产品复验和 CI 均已通过。PR #325 已满足转 Ready 与合并条件。

本验收只覆盖已发布文字模型和测试环境，不授权生产 Migration、生产部署、流量切换或真实客户开放；图片、音频、视频执行进入后续阶段。
