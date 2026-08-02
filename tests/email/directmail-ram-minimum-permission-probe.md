# DirectMail RAM 最小权限验收说明

## 1. 目的与结论

本说明用于验收测试环境专用 RAM 身份是否仅具备以下三个 DirectMail Action：

- `dm:QueryTemplateByParam`
- `dm:DescTemplate`
- `dm:SingleSendMail`

本轮废止“向 `SingleSendMail` 或模板写 API 发送缺少必填字段的请求，可依据参数错误证明 Allow/Deny”的旧假设。参数校验与权限校验的执行顺序不能作为稳定契约；`MissingParameter`、`InvalidParameter` 或其他请求类错误均不能证明某个 Action 已授权或被拒绝。

`SingleSendMail` 没有 `DryRun` 参数。合法请求成功即会触发真实发送，因此不得为补齐 RAM 证据重复发送邮件。当前仓库中的缺参真实探针及其 `request`/`permission` 分类不再作为 RAM 验收依据，真实模式暂停执行；离线分类测试即使通过，也不能关闭本门禁。

## 2. 官方规则

- `SingleSendMail` 的授权 Action 为 `dm:SingleSendMail`，资源范围为 `*`，无关联操作；请求没有 `DryRun`，成功调用属于真实发送。
- DirectMail 返回 HTTP 400 且 Code 为 `Forbidden` 时，表示调用者无该操作权限。
- RAM 通用拒绝码还可能表现为 `NoPermission`、`Forbidden.RAM`、`NotAuthorized` 等；策略求值时显式 Deny 优先于 Allow。
- 不能只凭 HTTP 状态或错误码完成归因。应使用对应 `RequestId` 在 OpenAPI Troubleshoot 中核对 `AccessDeniedDetail`、`AuthAction`，或使用 RAM 权限审计核对实际可能权限及最近 API 尝试。
- 日志和测试报告不得记录 AccessKey、Secret、Token、完整邮箱、供应商响应正文或其他敏感信息。`RequestId` 仅进入受控证据附件，公开报告只记录脱敏引用位置。

## 3. 无额外发信的最小 Allow 验收

必须先证明三份证据来自同一 RAM 身份。记录身份别名、策略版本、证据时间窗和部署版本，不记录凭证值。

| Action | 合格证据 | 判定边界 |
|---|---|---|
| `dm:QueryTemplateByParam` | 同一 RAM 身份既有的真实成功响应及受控 `RequestId` | 仅证明该 Action 在证据时点为 Allow |
| `dm:DescTemplate` | 同一 RAM 身份既有的真实成功响应及受控 `RequestId` | 仅证明该 Action 在证据时点为 Allow |
| `dm:SingleSendMail` | 同一 RAM 身份既有的真实调用已获供应商 `accepted`，并保留受控 `RequestId` 与消费记录 | `accepted` 只证明供应商接受调用和当时具备发送权限，不等于最终送达；不得因此再次发信 |

若无法证明历史 `accepted` 与两个读 API 使用同一 RAM 身份、同一有效策略版本或可解释的连续时间窗，则本项记为 `BLOCKED`，不能用新的发信请求补证。

## 4. 策略静态检查与权限审计

由运维通过安全渠道导出该 RAM 身份在证据时点的有效策略快照，测试人员只读核对：

1. Allow Action 归一化后必须恰好为上述三个 Action。
2. `dm:SingleSendMail` 的 Resource 必须为 `*`。
3. 不得存在 `dm:*`、`*` 或其他可扩张 DirectMail 权限的通配 Allow。
4. 不得 Allow `dm:CreateTemplate`、`dm:ModifyTemplate`、`dm:DeleteTemplate`。
5. 同时核对附加策略、用户组策略、角色信任链和显式 Deny；显式 Deny 优先。
6. 在 RAM 权限审计中按 API Action 检查实际可能权限及最近尝试，保存身份别名、策略版本、Action、时间窗和审计结论的脱敏证据。

只有“历史真实 Allow 证据 + 策略快照恰好三个 Action + 权限审计一致”同时满足，才可判定最小 Allow 通过。

## 5. 写操作与显式 Deny 的无副作用验收

`CreateTemplate`、`ModifyTemplate`、`DeleteTemplate` 以及各 Action 的显式 Deny，优先使用以下证据，不新增业务请求：

1. RAM 权限审计显示目标 Action 不在实际可能权限内，或被显式 Deny 命中。
2. 对既有失败请求的 `RequestId` 使用 OpenAPI Troubleshoot，确认 `AccessDeniedDetail` 与 `AuthAction` 指向预期 Action。
3. DirectMail 的 HTTP 400 `Forbidden`，或 RAM 的 `NoPermission`、`Forbidden.RAM`、`NotAuthorized`，仅在与上述 `RequestId` 诊断或权限审计相互印证时作为拒绝证据。

缺少必填字段的写请求不能证明拒绝，因为请求可能在权限校验之前失败。反过来，使用完整合法参数调用写 API，一旦策略配置错误就可能创建、修改或删除模板，也不能保证无副作用。

因此，在权限审计或既有 `RequestId` 诊断不足时，本项必须记为 `BLOCKED`。如仍要求进行真实 API 拒绝测试，必须另行取得明确授权，并先由阿里云能力或隔离环境证明该请求即使意外获准也不会产生副作用；无法证明时不得执行。`SingleSendMail` 没有 `DryRun`，不能以真实发送请求验证显式 Deny。

## 6. 验收记录

| 检查项 | 所需证据 | 当前判定规则 |
|---|---|---|
| 两个读 Action 为 Allow | 同一 RAM 身份的既有成功响应、受控 `RequestId` | 证据完整才 PASS |
| `SingleSendMail` 为 Allow | 同一 RAM 身份的既有真实 `accepted`、受控 `RequestId` | 不新增发信；证据完整才 PASS |
| Allow 集合恰好为三个 Action | 有效策略快照与权限审计 | 无通配、无模板写权限才 PASS |
| 三个模板写 Action 被拒绝 | 权限审计或既有 `RequestId` 诊断 | 禁止用缺参错误代替；不足则 BLOCKED |
| 显式 Deny 优先 | 权限审计或既有 `RequestId` 诊断 | 每个目标 Action 单独留证；不足则 BLOCKED |

旧输出 `RAM_PROBE PASS mode=minimum_allow ... send_signature_only=request ...` 和 `RAM_PROBE PASS mode=explicit_deny ... safe_request=true` 均为无效验收结论，不得写入新的测试报告。最终报告应分别列出每项 Action 的证据类型、证据时点、策略版本和 `PASS/BLOCKED/FAIL`，不得把部分证据扩展为 RAM 门禁通过。

## 7. 脱敏证据离线验收器

`directmail_ram_effective_evidence.py` 只读取运维通过安全渠道交付的脱敏 JSON 清单，不调用 RAM、DirectMail 或 OpenAPI。调用时必须同时提供清单绝对路径和独立冻结的 SHA-256：

```powershell
python tests/email/directmail_ram_effective_evidence.py `
  --manifest <脱敏清单绝对路径> `
  --manifest-sha256 <独立冻结的64位小写SHA-256>
python tests/email/directmail_ram_effective_evidence_contract.py
python -O tests/email/directmail_ram_effective_evidence_contract.py
```

清单固定绑定不超过 24 小时的 UTC 证据窗、不可逆身份别名摘要、策略版本摘要、部署 SHA、有效策略完整性、附加策略与用户组策略完整性、Deny 优先级、直接 RAM 用户或完整角色信任链、最近 API 尝试审计，以及六个固定 Action。两个读取 Action 必须是既有成功证据，`SingleSendMail` 只能使用历史 `accepted`，三个模板写 Action 必须分别由权限审计或既有 Troubleshoot 证明 `explicit_deny`。

验证器递归拒绝 AccessKey、Secret、Token、RequestId、邮箱、收件人、供应商 Message/response/body 和凭据字段；每份原始证据只允许以 SHA-256 引用。它不接受 `delivered` 代替历史 `accepted`，也不接受 live write request 作为模板写 Deny 证据。当前验证器 SHA-256 为 `6BFB43F8EA43D622BFCC46D661DDB53196D84DBC8ACF1AAC55656535D87D699E`，攻击契约 SHA-256 为 `1EE0573446C8868295D60C8399BF94E3006EFB7EBA2AAC59D0D2E26F6287BA5F`；normal/`-O` 均通过 15 个攻击模型。当前尚未收到真实脱敏清单，因此 RAM 有效权限门禁仍为 `external_evidence_required`，不能记为 PASS。

## 8. 阿里云官方资料

- [SingleSendMail API：授权信息、请求参数与返回参数](https://help.aliyun.com/zh/direct-mail/api-dm-2015-11-23-singlesendmail)
- [QueryTemplateByParam API](https://help.aliyun.com/zh/direct-mail/api-dm-2015-11-23-querytemplatebyparam)
- [DescTemplate API](https://help.aliyun.com/zh/direct-mail/api-dm-2015-11-23-desctemplate)
- [DirectMail 错误码](https://help.aliyun.com/zh/direct-mail/error-codes)
- [OpenAPI 错误诊断](https://help.aliyun.com/zh/openapi/user-guide/api-error-diagnosis)
- [RAM 权限审计](https://help.aliyun.com/zh/ram/overview-of-permissions-audit)
- [RAM 权限策略判定流程](https://help.aliyun.com/zh/ram/policy-evaluation-process)

以上资料用于解释权限证据，不构成外部调用授权。若官方页面后续调整，应以执行验收时的阿里云官方文档为准，并在报告中记录查阅日期。
