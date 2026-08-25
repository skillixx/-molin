# IMG-GATE-00：图片网关立项冻结与人工最小问题包

> 当前阶段：`IMG-G0`
>
> 当前结论：`AUTO_PASS`
>
> 记录日期：2026-08-25
>
> 代码基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 当前分支：`codex/openrouter-image-poc-config`
>
> 本记录只冻结图片网关工程开发边界，不代表图片接口、人民币计费、测试环境、生产或商业验收已经完成。

## 1. 目标问题结论

图片 MVP 的 Provider 路径、首发模型、最小规格、接口边界、计费状态机、安全失败关闭、容量保护和工程 SLO 已经收敛为可实现合同。项目负责人已明确批准 IMG-G0～IMG-G8 只使用非商业测试价格夹具，并保持正式模型发布和真实钱包计费关闭；生成内容标识采用现行规则要求的失败关闭基线。因此后续工程开发不再依赖未决商业金额，IMG-G0 可以判定 `AUTO_PASS` 并进入 IMG-G1。

## 2. 已复核的 POC 事实

| 事实 | 当前证据 | 边界 |
|---|---|---|
| 图片路径 | 原生 OpenRouter Images API，不经过 Bifrost | Bifrost 继续只服务文字 Chat |
| 首发候选模型 | `bytedance-seed/seedream-5-0-lite` | 仅证明固定 POC 规格，不证明其他规格 |
| 固定端点 Provider | `seed`，禁用 fallback | 不代表生产路由已发布 |
| 固定 POC 规格 | `2K / 1:1 / n=1 / 零重试` | 不扩张为多张、多比例或图生图 |
| 真实 POC | HTTP 200，2048×2048 JPEG，177486 字节，84.031 秒 | 历史低敏摘要，不重复发起真实请求 |
| 成本信号 | 目录与实际 Usage 均为 0.035 USD，单次上限 0.04 USD | 不是墨灵人民币成本或销售价 |
| 一次性门禁 | ChangeId `IMG-OPENROUTER-POC-20260824-001` 已为 `consumed` | 禁止恢复旧配置后重放 |
| 回执边界 | 仓库外低敏回执；不含 Key、Prompt、响应正文或 Base64 | 本仓库不复制真实回执或凭据 |

2026-08-25 当前复核：

```text
python -I -W error::ResourceWarning infra/scripts/test_probe_openrouter_image_model_once.py
结果：Ran 9 tests，OK

python -I -W error::ResourceWarning infra/scripts/probe-openrouter-image-model-once.py --catalog-check
结果：CATALOG_CHECK=PASS，REAL_REQUEST_ATTEMPTED=NO，ZERO_RETRY=YES
```

目录检查是零费用只读验证，不构成新的图片生成。当前未读取、输出或复制任何真实 Key。

## 3. Codex 已冻结的工程决策

### 3.1 产品和模型边界

- MVP 只做文生图 `image.generate`，不做图生图、编辑、蒙版、variation、视频、音频或任意工作流。
- 首发工程实现以 POC 模型为唯一允许模型；更换或新增模型必须重新建立独立的目录、规格和成本证据。
- OpenRouter 采用原生 `OpenRouterImageAdapter`；Bifrost 不承载图片 Images API。
- MVP 不做跨 Provider fallback；结果未知不得自动重试 Provider。
- 正式图片业务开关默认关闭；没有发布价格、安全策略、健康路由和受限凭据时失败关闭。

### 3.2 首批允许规格

| 参数 | 冻结值 | 说明 |
|---|---|---|
| 张数 `n` | 仅 `1` | POC 只证明单张；多张必须以后续证据扩展 |
| 分辨率 | `2K` | 规范化为模型能力值，不接受任意宽高 |
| 宽高比 | `1:1` | 不接受任意比例 |
| 质量 | `standard` | 墨灵规范化值；Adapter 映射到 Provider 已验证默认能力 |
| 返回方式 | `url` | 只返回墨灵短效下载 URL，不向页面返回 Base64 |
| Provider 编码 | `provider_default` | 实际文件仍须为 PNG/JPEG/WebP 之一并通过完整解码；编码不作为首批销售价格维度 |
| Prompt | 非空，最多 4000 个 Unicode 字符且 UTF-8 不超过 16 KiB | 不进入普通日志、MySQL 或 RabbitMQ |

首批唯一价格 variant 规范化为：

```json
{"aspect_ratio":"1:1","delivery":"url","output_format":"provider_default","quality":"standard","resolution":"2K"}
```

该 JSON 只定义稳定选价键，不包含任何正式金额。

### 3.3 Quote、预占和结算

- Quote 有效期固定 5 分钟，只能消费一次；相同幂等请求只读取原绑定关系。
- `/v1/images/generations` 在同一事务内创建、消费内部 Quote 并预占；平台页面必须显式携带后端 Quote。
- 用户销售价只读取墨灵 CNY V2 不可变价格快照，禁止用 OpenRouter 动态美元价格直接扣钱包。
- 预占按请求允许的最大可交付图片数计算；当前 `n=1`。
- 只按已经存储、审核通过、结算成功且可交付的主图数量结算；缩略图、审核副本和派生图不计费。
- 部分成功公式保留用于未来扩张，但当前单张规格只会形成 0 张或 1 张可计费结果。
- 请求前拒绝、明确未发送、明确失败且无产物、输出安全拒绝均向用户收取 0 元并释放预占；Provider 实际成本作为平台成本事实保留。
- 存储失败或结果未知进入 `pending_reconcile`，不签发下载 URL，不自动再次调用 Provider。
- Provider 结果已经安全落入临时对象后，存储、审核和账本补偿允许按同一 request_id 重放；补偿不得重新生成图片。
- 所有金额使用 `DECIMAL(20,8)` 和 Decimal 字符串；报价向上舍入 8 位，结算仍只使用冻结快照。

### 3.4 安全和留存

- Prompt、图片 Base64、签名 URL、Provider 原始响应和 Key 不进入普通日志、MySQL 或 RabbitMQ。
- Provider URL 每次解析、重定向和实际连接前都重新执行 SSRF 检查；拒绝回环、私网、链路本地、metadata、多播及混合解析。
- 单图必须经过字节上限、魔数、MIME、格式、宽高、像素、完整解码和元数据清理；SVG、HTML及脚本型内容不得交付。
- 结果审核不可用时失败关闭；输出拒绝保留隔离元数据但不向用户交付。
- 成功主图工程留存 30 天；失败和取消临时对象 24 小时；隔离对象默认 30 天。争议或 legal hold 优先阻止删除。
- 下载 URL 有效期 15 分钟，必须在签发时重新校验用户/Project归属、资产可用状态和 `billing_status=settled`。

### 3.4.1 生成内容标识的强制工程基线

根据国家互联网信息办公室等四部门发布、2025-09-01 起施行的《人工智能生成合成内容标识办法》，图片生成服务应在图片适当位置添加显式标识，提供下载、复制或导出时应确保文件中仍含显式标识，并在文件元数据中加入生成合成属性、服务提供者名称或编码、内容编号等隐式标识。强制性国家标准 `GB 45438-2025《网络安全技术 人工智能生成合成内容标识方法》` 与其同日实施。

因此 MVP 不提供“无标识下载”模式，并冻结以下失败关闭规则：

- 工作台和画廊持续展示“AI生成”提示。
- 主图在可交付前写入可明显感知的“AI生成”显式标识。
- 下载文件写入隐式元数据，至少包含生成合成属性、墨灵服务提供者编码、不可逆内容编号和标识版本。
- 显式或隐式标识任一写入、复检失败时，资产不得进入 `available`，钱包不得结算，下载 URL 不得签发。
- 不允许用户、管理员或 Provider 参数关闭、删除或绕过标识；派生图和缩略图也必须保留相应标识语义。
- 用户协议必须说明标识方法、样式和禁止恶意删除、篡改、伪造、隐匿标识的责任。
- 标识与交付审计日志至少保留 6 个月；不记录 Prompt、图片正文、Base64、完整签名 URL 或密钥。
- 上线前由法务/合规负责人复核服务适用范围、算法备案、安全评估、应用上架材料及投诉申诉机制；这些属于 IMG-G10/生产准备，不阻塞 IMG-G1～IMG-G8 的关闭态工程开发。

官方依据：

- [人工智能生成合成内容标识办法](https://www.cac.gov.cn/2025-03/14/c_1743654684782215.htm)
- [互联网信息服务深度合成管理规定](https://www.cac.gov.cn/2022-12/11/c_1672221949354811.htm)
- [GB 45438-2025 国家标准信息](https://openstd.samr.gov.cn/bzgk/std/newGbInfo?hcno=F32EA2A561F1886CD8D606513512D547&refer=outter)

### 3.5 容量、并发、超时和 SLO

| 指标 | 工程冻结值 | 失败行为/验证 |
|---|---:|---|
| 单图最大响应字节 | 32 MiB | 有界读取超限即拒绝，不形成可用资产 |
| 单请求最大总响应字节 | 32 MiB | 当前 `n=1`；不得无界 `ReadAll` |
| 单图最大解码像素 | 5,308,416 | 解码前检查尺寸，覆盖图片炸弹测试 |
| Provider 请求超时 | 180 秒 | 超时按是否可能已发送区分明确失败与结果未知 |
| Provider 结果下载超时 | 30 秒 | 超时进入存储失败或待补偿，不重新生成 |
| 单用户同步并发 | 1 | Redis 原子租约；超限返回 429 |
| 单 Project 同步并发 | 2 | 防止单 Project 占满模型容量 |
| 单模型全局并发 | 4 | 可配置收紧，不能超过已发布上限 |
| 平台任务排队上限 | 1000 | 满载返回 429，不创建 Provider 调用 |
| 任务最长排队时间 | 300 秒 | 超时转失败或人工核对，不自动重试 Provider |
| 同步端点 P95/P99 目标 | 150 秒 / 180 秒 | Fake 与真实证据分开统计；单次 POC 不能证明分位数 |
| MinIO 容量预警/严重水位 | 70% / 85% | 告警只触发处置，不自动删除争议资产 |

归一化容量按每日日均 1000 个请求、单张最坏 32 MiB、30 天留存和 1.3 安全系数计算：

```text
主图最坏日新增 = 1000 × 1 × 32 MiB = 31.25 GiB
主图最坏留存 = 31.25 GiB × 30 = 937.5 GiB
安全余量后 = 937.5 GiB × 1.3 = 1218.75 GiB
```

缩略图、隔离副本、临时对象和日志必须另外计量；实际部署容量按批准的日请求量线性换算，不得把上式当作当前测试服务器已有容量证明。

## 4. 人工决定记录

2026-08-25，项目负责人明确确认：

```text
同意 IMG-G0～IMG-G8 使用非商业测试价格夹具，正式模型发布和真实钱包计费保持关闭。
```

后续阶段使用固定、显式标记为 `test_fixture` 的 CNY Decimal 金额执行报价、预占、结算、释放、退款和对账金样。测试夹具不得进入正式发布版本，不得开启真实钱包计费，也不得被表述为正式成本、销售价、最低毛利、税费或商业政策。正式价格必须在 IMG-G9 或独立商业 Goal 中重新取得人工决定和真实结算授权。

后续 IMG-G2、IMG-G3、IMG-G5 和 IMG-G6 的价格、资产、钱包、权限、幂等和安全敏感代码仍须按仓库规则提交独立人工审查包；本次问题包不预先替代那些代码审查。

## 5. 当前门禁报告

```text
GATE=IMG-G0
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=POC墓碑复核、文生图MVP、唯一规格、Quote/预占/结算、安全、留存、容量、并发、超时和SLO工程冻结
TEST_EVIDENCE=POC单测9/9通过；零费用目录检查PASS且REAL_REQUEST_ATTEMPTED=NO
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=未证明正式人民币价格、法务适用性/备案结论、图片业务实现、测试环境集成、真实人民币结算、生产或商业验收
HUMAN_QUESTIONS=NONE
```
