# IMG-G4：Fake ImageGateway 与有界图片安全处理

> 当前阶段：`IMG-G4`
>
> 当前状态：`AUTO_PASS`
>
> 基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 分支：`codex/openrouter-image-poc-config`
>
> 本阶段仅运行Fake Provider、Fake审核和Fake ObjectStore，不连接真实Provider、外部图片URL、MinIO或钱包。

## 1. 功能说明

IMG-G4 在不产生费用的情况下跑通图片生成、URL/Base64读取、完整解码、双标识、临时存储、审核、结果区/隔离区存储和主图/缩略图归一化，并对恶意输入失败关闭。

使用角色：

- 后端开发：后续结算和HTTP层只能依赖 `ImageGateway` 深模块。
- 测试/安全人员：验证输入、图片、URL、审核、存储和结果未知故障。
- 用户与管理员：本阶段没有页面或公开接口。

页面入口：无。

接口清单：无新增HTTP接口。

## 2. 核心文件

| 文件 | 作用 |
|---|---|
| `image/provider.go` | Provider Adapter稳定接口与故障分类 |
| `image/fake_provider.go` | 成功、部分成功、失败、超时、断连、损坏和结果未知Fake |
| `image/moderation.go` | Prompt/图片审核接口与Fake审核 |
| `image/processor.go` | Base64/URL有界处理、完整解码、元数据清理、双标识和缩略图 |
| `image/safe_fetcher.go` | HTTPS白名单、SSRF、DNS重绑定和重定向防护 |
| `image/gateway.go` | Fake生成、临时存储、审核、结果/隔离存储和故障归一化 |
| `image/*_test.go` | 安全、故障、格式、并发和交付边界测试 |

## 3. ImageGateway闭环

```text
Prompt前审
  → FakeImageAdapter.Generate（严格一次）
  → URL/Base64有界读取
  → 签名/MIME/尺寸/像素/完整解码
  → 重编码PNG、清理原元数据、写双标识
  → ai-upload-temp临时对象
  → 图片后审
  → 通过：ai-result主图+缩略图
  → 拒绝：ai-quarantine且不可计费
```

本阶段产物只处于 `temporary/quarantined`，不会绕过 IMG-G5 钱包结算进入可交付 `available`。

## 4. Fake Provider故障模型

支持：

```text
success / partial / failed / timeout / disconnected / unknown / corrupt
```

规则：

- 每次 Gateway 请求最多调用Adapter一次。
- `unknown` 不自动重试、不fallback，返回结果未知。
- 部分成功只统计实际安全处理成功的主图。
- 返回图片数超过请求数、重复result index和越界index失败关闭。
- Prompt拒绝发生在Provider前，Adapter调用数为0。
- 输出审核拒绝保留隔离对象，用户可交付数和可计费数为0。
- 审核服务不可用返回 `moderation_unavailable`。
- 结果区存储失败返回 `asset_storage_failed`，不得交付或重调Provider。
- 客户端在前审阶段取消返回 `client_disconnected`。

## 5. 图片处理边界

冻结限制来自 IMG-G0，可由构造参数进一步收紧：

- 源响应和归一化文件分别设置字节上限。
- Base64使用流式decoder和 `max+1` reader，不执行无界解码。
- 先读取config校验宽高和像素乘积，再完整解码。
- 宽高、像素和1:1比例容差均失败关闭。
- 只允许PNG/JPEG/WebP；SVG、HTML、GIF和未知签名拒绝。
- Provider声明、data URL、HTTP Content-Type和魔数不一致时拒绝。
- PNG/JPEG使用标准库，WebP使用Go官方补充模块 `golang.org/x/image/webp v0.41.0`。
- 所有输入完整解码为像素后统一重编码PNG，清除EXIF、GPS、XMP和原始文本块。
- 主图和缩略图重新写入可感知的 `AI GENERATED` 标识。
- PNG写入批准的隐式metadata：生成属性、Molin服务编码、内容编号和标识版本。
- 标识写入或复检失败时不形成产物。

## 6. URL与SSRF防护

SafeHTTPFetcher要求：

- 仅HTTPS。
- Host必须命中精确白名单，禁止尾点、userinfo、fragment和非443端口。
- 不使用系统/环境代理。
- DNS结果必须全部为允许的公网单播地址；混合公网+私网也整体拒绝。
- 拒绝loopback、RFC1918、CGNAT、link-local、metadata、多播、文档网段、benchmark网段和IPv6私网/文档地址。
- URL预检、每次重定向和实际拨号分别重新解析。
- 拨号直接使用本次已验证IP而不是重新交给系统按hostname连接，防止DNS rebinding。
- 最多3次重定向，每个目标重新执行scheme/host/DNS门禁。
- HTTP状态、Content-Length和无Content-Length正文都执行有界校验。
- 测试使用Fake resolver/dialer/transport，不访问任何真实外部URL。

## 7. Fake ObjectStore使用

- 原始归一化产物先写 `ai-upload-temp`。
- 审核通过后写 `ai-result`，审核拒绝写 `ai-quarantine`。
- 结果区主图成功后，缩略图失败不阻断主图；缩略图不是计费产物。
- 主图结果区写入失败不返回资产；临时对象留待IMG-G5补偿/IMG-G7清理策略处理。
- Fake存储有界、同键幂等，不访问真实MinIO。

## 8. 测试矩阵

- PNG、JPEG、WebP完整解码并统一重编码。
- 原始metadata清理，双标识存在且PNG可重新完整解码。
- MIME冲突、data URL冲突、损坏文件、SVG、HTML、GIF拒绝。
- 最大字节、最大像素、宽高、比例和超长内容编号拒绝。
- URL有界读取、私网、metadata、文档网段、混合DNS、重定向和DNS重绑定拒绝。
- Fake成功、部分成功、明确失败、超时、断连、结果未知、损坏结果。
- Prompt拒绝、图片拒绝、Prompt审核失败、图片审核失败。
- 临时存储/结果存储失败，Provider调用严格为1或0。
- Fake ObjectStore 100并发幂等。

## 9. 依赖与证据边界

- `golang.org/x/image v0.41.0` 与项目Go 1.25、`x/text v0.37.0`兼容，`go mod verify`和`go mod tidy -diff`通过。
- 本阶段不证明真实Provider、真实URL、MinIO、钱包、Outbox、HTTP、前端、测试服务器、生产或商业验收。
- Fake成功不能冒充真实图片生成、真实安全审核或真实对象存储通过。

## 10. 机器审查与最小人工审查包

### 10.1 机器已经验证

- Fake成功、部分成功、明确失败、超时、断连、结果未知和损坏结果全部通过，Adapter每请求最多调用一次。
- Prompt拒绝发生在Provider前，输出拒绝进入隔离且可交付/可计费为0，审核不可用失败关闭。
- PNG/JPEG/WebP完整解码后统一重编码PNG，原始metadata清除，显式和隐式标识写入并复检。
- Base64、URL、Content-Length和无Content-Length响应全部使用有界reader。
- MIME/魔数冲突、SVG、HTML、GIF、损坏文件、超字节、超宽高、超像素、比例和内容编号拒绝。
- SSRF覆盖loopback、私网、CGNAT、link-local、metadata、文档/benchmark网段、IPv6私网、混合DNS、重定向和DNS rebinding。
- DNS重绑定测试证明第二次解析变为私网时不会进入拨号；实际拨号只使用本次验证IP。
- Host白名单只接受规范ASCII DNS名称，拒绝Unicode同形字符、IP字面量、下划线、空标签、尾点和非法首尾连字符。
- 结果区存储失败不交付、不重调Provider；缩略图失败不增加计费且不阻断安全主图。
- Prompt、URL、Base64、Provider引用、图片bytes和对象key均从内部JSON序列化排除，图片包无日志输出调用。
- Go全量、vet、Linux race、`go mod verify`、`go mod tidy -diff`、diff和敏感扫描通过。
- 全部测试不访问真实外部URL、Provider、MinIO或钱包。

### 10.2 人工审查结论

2026-08-25，项目负责人明确批准：

```text
批准 IMG-G4 的PNG/JPEG/WebP先限额再完整解码、统一重编码清理元数据、显式/隐式双标识和审核失败关闭合同；
批准HTTPS精确Host白名单、每次重定向与拨号前重新解析、全部DNS结果必须为公网且拨号使用已验证IP，
以及Provider结果未知零重试、输出拒绝隔离、审核和存储失败关闭合同。
该批准不授权真实URL、Provider、MinIO、钱包、HTTP、测试服务器或远程Git操作。
```

人工确认只批准 IMG-G4 Fake与安全处理合同，不授权真实URL、Provider、MinIO、钱包、HTTP、测试服务器、Git提交或远程操作。

## 11. IMG-G4 门禁报告

```text
GATE=IMG-G4
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=ImageGateway、FakeImageAdapter、Fake审核、Fake ObjectStore、PNG/JPEG/WebP有界处理、双标识、SSRF/DNS重绑定/重定向防护、成功/部分/失败/超时/断连/unknown故障模型及中文文档
TEST_EVIDENCE=图片定向Go PASS；全量Go PASS；go vet PASS；Linux race五包PASS；依赖verify/tidy PASS；安全/MIME/像素/metadata/SSRF/DNS/重定向/Fake故障/序列化测试PASS；diff与敏感扫描PASS
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=NO
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=未实现钱包/Outbox/补偿、HTTP、真实MinIO/Provider/审核、前端、测试环境、生产或商业验收
HUMAN_QUESTIONS=NONE
```
