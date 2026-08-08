# AI 网关 G6 用户端客户旅程开发说明

> 状态：测试环境真实 E2E、Migration 000066、独立 QA 与真实浏览器可读性复验已通过，最新 CI、独立评审和产品整改复验中。G5 验收提交为 `60b569f`，G6 实际开发基线为 `origin/main@c4126a6`；最终状态以 `docs/ai-gateway-g6-acceptance.md` 为准。

## 1. 阶段目标

G6 只交付已发布文字模型的用户端商业闭环：模型发现、价格理解、Project/SK 管理、快速接入、请求账本、用量与费用解释、导出和账单申诉。图片、音频、视频执行、多模态异步任务、对象存储生命周期和生产放量不在本阶段。

## 2. 当前能力审计

| 能力 | 当前事实 | G6 处理 |
|---|---|---|
| 模型目录 | `/api/token/models` 仅按 `token_models.status=active` 返回精简字段；未强制发布版本、活动人民币价格和健康路由 | 新增 G6 用户目录查询；公开内容读取不可变 `ai_model_release_versions.snapshot_json`，运行态再聚合活动价格与健康路由，草稿不泄漏 |
| 模型详情 | 无用户端详情和完整价格接口 | 新增按公开模型代码查询的详情接口，返回销售价、计量单位、文档状态和发布信息，不返回成本与内部路由 |
| Project | 已有本人隔离的创建、列表、详情、更新接口 | 复用并补充创建、名称/时区编辑与归档页面；归档使用既有 `status=archived` 状态变更 |
| Project SK | 已有签发、列表、轮换、吊销和显式模型 allowlist；明文只返回一次 | 复用并补充 Project 维度 UI；保留 HMAC 存储与一次展示边界 |
| 实名门禁 | 正式请求编排会校验实名；Project/SK 管理接口尚未统一前置门禁 | G6 对 Project/SK 写操作增加实名校验，未实名不能获得可调用 SK |
| 用量记录 | `/api/token/usage` 读取旧 `token_usage_logs`，不能完整解释 G3 价格快照、钱包预占和结算 | 新增以 `ai_requests` 为事实源的用户账本、总览和详情接口；旧接口保留兼容 |
| 价格与结算 | `ai_price_versions`、`ai_price_skus`、`ai_requests.price_snapshot_json`、`ai_request_wallet_links` 已存在 | 只输出销售价格和用户财务事实；禁止输出 `cost_unit_price`、上游成本和原始快照 |
| 导出 | 无 AI 请求账本 CSV 导出 | 新增本人范围、时间范围和条数上限的 CSV 导出，执行公式注入防护 |
| 账单申诉 | 只有内容安全申诉，无账单申诉事实 | Migration `000065` 新增 `ai_billing_disputes`，按 `request_id + user_id` 幂等受理并可追踪；`000066` 用组合外键强制申诉用户与请求归属一致 |
| 用户导航 | `/api-keys`、`/token/usage`、`/consumption` 入口割裂 | 新增 `/ai/models`、`/ai/models/:modelCode`、`/ai/api-keys`、`/ai/usage`，旧 Token 路由重定向 |

## 3. 后端设计

新增 G6 用户查询仓库、服务、Handler 和 DTO，职责如下：

- 目录聚合：已发布模型、当前活动人民币价格、销售 SKU 和可用路由。
- 请求账本：强制 `user_id` 条件，支持 Project、SK、模型、状态和时间筛选。
- 请求详情：公开模型、三维状态、计量行、销售金额、价格版本和钱包流水 ID。
- 用量总览：今日与本月请求量、输入/输出 Token、结算费用和预算使用情况；人工核定后优先展示 `reconciled` 用量，预算百分比按各 Project 固化月周期汇总。
- CSV 导出：最大时间跨度和最大行数限制；单元格以 `= + - @` 开头时增加安全前缀。
- 账单申诉：仅允许请求所有者创建；检测到 API Key、Bearer Token、JWT 等密钥样式时拒绝入库，审计只保存长度、哈希或受控枚举，不保存用户筛选原文。
- 文档健康：Migration 000065 保存三个外部网页状态；地址变化重置为 unknown，发布要求 API 文档和快速入门 healthy，用户端按发布快照状态决定是否可打开。

所有列表保持 `{items,page,page_size,total}` 扁平分页。Decimal 金额以 JSON 字符串传输。

## 4. 前端设计

- `/ai/models`：文字模型搜索、厂商/能力/上下文筛选、排序和可恢复 URL Query。
- `/ai/models/:modelCode`：公开信息、完整人民币销售价、失败收费规则、静态文档入口和创建 SK 导航。
- `/ai/api-keys`：Project 与 SK 同页管理，覆盖创建、归档、签发、轮换、吊销和一次明文展示。
- `/ai/usage`：总览、请求账本、详情、CSV 导出和账单申诉。
- 旧 `/api-keys`、`/token/usage` 重定向到新入口；通用商品 `/consumption` 保留，避免与 AI 请求账本混淆。

页面使用现有 Vue3、TypeScript、Element Plus 和设计变量，验证 1440、768、375 三种宽度，并覆盖加载、空、错误、无权限、模型下架、SK 停用和待对账状态。

## 5. 安全边界

1. 用户查询必须以 JWT 中的 `user_id` 为唯一租户边界，查询参数不能覆盖。
2. 不返回完整 SK、Key Hash、提示词、响应正文、上游模型、渠道地址、上游密钥或成本单价。
3. 模型详情只从已发布配置和当前活动销售价格构建，不读取草稿作为用户事实。
4. 导出和申诉写审计；日志只记录用户 ID、request_id、筛选摘要和结果数量。
5. 文档链接只使用 G5 已发布的 HTTP/HTTPS URL；前端使用 `noopener,noreferrer` 打开外部网页。
6. 测试环境真实请求必须使用一次性临时 SK 和幂等键，验收后立即吊销并保留账本事实。

## 6. 验收证据

- Go 单元、集成、竞态和静态检查通过。
- 用户端 type-check、lint、build 和 Playwright 通过。
- 真实测试后端浏览器旅程覆盖模型详情、Project/SK、一次真实请求、账本详情、导出和申诉。
- 真实 Bifrost 请求的 Usage、价格快照、销售金额、钱包结算和用户页面完全一致。
- 越权、吊销 SK、未实名、CSV 注入和成本字段泄漏测试通过。
- 独立 QA 与产品验收均为 P0=0、P1=0 后才允许阶段收口。
