# AI 网关 G6 验收记录

> 当前状态：**测试环境 E2E、CI 和独立 QA 已通过，产品整改复验中，尚未通过阶段门禁。**
>
> 已有证据覆盖本地代码、自动化测试、隔离 MySQL 8、测试环境 Migration 000065、真实 Bifrost 请求、人民币金额/钱包对账、真实浏览器旅程和凭据回收。CI、最终独立评审、QA 与产品双签完成前，不得声明 G6 完成或允许生产开放。

## 1. 验收对象

| 项目 | 当前值 |
|---|---|
| 分支 | `feature/backend-d-ai-gateway-g6-customer-journey` |
| G5 验收提交 | `60b569f` |
| G6 实际开发基线 | `origin/main@c4126a6` |
| 当前分支提交 | `9c325b6` |
| 测试环境应用提交 | `9efb1bd`（后续 `b80af84` 仅调整 CI 隔离探活脚本） |
| Migration | `000065_create_ai_gateway_g6_customer_journey` |
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
| Migration 隔离 MySQL 8 验证 | PASS | 一次性 MySQL 8 容器完成 `64:0 -> 65:0`、重复 up、事实保留 down、重新 up、历史文档初始化为 unknown、文档健康/申诉约束、请求索引，并以真实 GORM 验证跨用户隔离、重复申诉和 SK 审计失败事务回滚；`b80af84` 增加连续三次就绪判定和失败诊断，本地复验通过 |
| 测试环境部署与真实浏览器 | PASS | 测试环境 schema `65:0`；桌面 `1440x1000` 与手机 `375x812` 真实页面通过，模型市场、人民币价格、快速入门、已撤销 SK、请求详情和申诉可追溯；两个宽度均无横向溢出 |
| 真实 Bifrost 与人民币对账 | PASS | Bifrost 负载入口 `127.0.0.1:18080`、两个节点健康；真实 request `req_478e03928009d186` 已结算 `0.00000100 CNY`，Usage 与钱包差异均为 `0.00000000`，价格版本 `v2` |
| 越权、吊销、不调用上游 | PASS | 跨用户请求详情返回 404；安全命中和 SK 吊销后均在网关前拒绝且未产生上游执行尝试；重复申诉返回 409 |
| 测试凭据回收 | PASS | 清理前测试集合为用户 2、活跃 SK 0、未归档 Project 1、请求事实 4；清理后为 `0|0|0|4`，会话撤销，本地和远程浏览器 JWT 已删除 |
| GitHub CI | PENDING | 原 PR #324 在 `b80af84` 六项检查全绿；因分支命名规范迁移至替代 PR #325，等待最终提交 `9c325b6` 的 CI 复验 |
| 独立 QA | PASS | 独立测试工程师复跑全量 Go、vet、mod verify、两端 type-check/lint/build、10 条 G6 Playwright 和隔离 MySQL 8；P0/P1/P2/P3 均为 0 |
| 独立评审与产品 | PENDING | 最终代码评审进行中；产品首轮提出证据固化、响应式覆盖和详情失败状态问题，均已整改，等待复验 |

## 3. 测试环境真实 E2E 证据

| 项目 | 结果 |
|---|---|
| API 部署 | `9efb1bd`，SHA256 `a9f73033687875e1e3aa6fe0a413f88eadc000ab4cc1e8545fb57a324fcf48bd` |
| 完整前后端部署 | change `g6-75a3001-r1`，回滚目录 `/home/pc/molin/rollback/g6-75a3001-r1` |
| API 增量部署 | change `g6-9efb1bd`，回滚目录 `/home/pc/molin/rollback/g6-9efb1bd` |
| 模型与价格发布 | 模型发布版本 `v2`；旧价格窗口关闭后发布价格版本 `v2`，避免篡改历史快照 |
| 文档入口 | 模型介绍、快速入门、API 文档均以测试环境静态 URL 发布，健康状态为 `healthy`，三个入口返回 200 |
| 真实请求 | `req_478e03928009d186`，安全通过、执行成功、结算成功，16 输入 Token、1 输出 Token |
| 金额对账 | 请求结算、销售计量行、钱包消费流水和用户页面均为 `0.00000100 CNY`；Usage 差异和钱包差异均为 0 |
| 拒绝链路 | 关键词安全拒绝无上游尝试；吊销 SK 后拒绝且无上游尝试；跨用户详情 404；重复申诉 409 |
| 浏览器证据 | [桌面截图](evidence/g6/g6-real-desktop.png) SHA256 `AC124FF6B9CE2711E9DCDC88FB23431AFAA5575010136BED04DCAFCE1D0099D4`；[手机截图](evidence/g6/g6-real-mobile.png) SHA256 `00CB70AD4F2F9293DAE9D2FE2C4100E3E12EE4292D40111C628C2446432D1109` |
| 清理与审计 | 测试身份、SK、Project、会话和浏览器 JWT 已回收；请求、计量、钱包、申诉事实保留；发布与清理均写审计日志 |

金额验收标准：请求 `settled_amount`、销售计量行合计、钱包 consume 流水和用户页面展示完全一致，差异为 `0.00000000 CNY`。

所有证据均来自测试环境。未部署生产，也未执行生产 Migration、流量切换或真实客户开放。

## 4. 阶段结论

G6 测试环境客户旅程、真实计费链路和独立 QA 已经通过，但替代 PR #325 仍为 Draft，阶段尚未完成。只有新 PR CI 全绿、最终独立评审无 P0/P1且产品复验 PASS 后，才允许将 PR 转为 Ready、合并并更新本节为最终通过。
