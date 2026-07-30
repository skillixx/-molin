# DirectMail RAM 最小权限安全探针

## 用途

该探针用于验证测试环境专用 RAM 身份仅具备以下最小权限：

- `dm:QueryTemplateByParam`
- `dm:DescTemplate`
- `dm:SingleSendMail`

并确认 `CreateTemplate`、`ModifyTemplate`、`DeleteTemplate` 被拒绝。

探针默认只执行完全离线自测。真实 RAM 检查必须由测试负责人单独授权，并同时开启两个显式门禁。不得把凭据、完整邮箱、验证码或供应商原始响应写入命令、日志或报告。

## 安全请求设计

- 读取模板列表使用合法的最小分页参数。
- 查询详情只使用测试负责人指定且已审核通过的 `TemplateId`。
- `SingleSendMail` 只发送带签名但缺失全部业务必填参数的 RPC，不包含收件人、主题、正文或完整发信字段。正确的 Allow 策略应返回安全归类后的 `request`。
- 三个模板写动作同样不携带任何业务必填参数。正确的最小权限策略应先返回安全归类后的 `permission`；如果错误地获得写权限，供应商最多只能返回 `request`，探针随即失败，不会创建、修改或删除模板。
- 显式 Deny 验证每次只检查一个 action。无论 action 是否为读取、发送或模板写入，探针都期待 `permission`。
- 探针不自动重试，不打印供应商 Code、Message、响应正文、RequestId、请求字段值或凭据。
- 如果供应商返回未进入现有安全白名单的 Code，测试专用观测器只输出该 Code 的 SHA-256、UTF-8 字节长度和 HTTP 状态族。它不会保存或输出原始 Code。
- 观测器只把摘要与源码内冻结的官方通用请求参数错误候选集合（`MissingParameter`、`InvalidParameter`）离线比较。仅当摘要唯一匹配时才输出规范化候选名；没有匹配或匹配不唯一时固定输出 `unknown`，不得自行推测字段后缀或扩充集合。

## 默认离线验证

```powershell
$go = (Get-Command go -ErrorAction Stop).Source
$env:GOENV = 'off'
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
& $go test ./internal/modules/auth/service -run '^TestDirectMailRAMProbe' -count=1 -v
```

命令只接受当前受控终端 `PATH` 中已经安装并完成来源校验的 Go；找不到时固定记为 `BLOCKED`。不得把某次执行产生的临时目录、随机工具路径或个人工作站路径写入仓库文档，也不得联网自动下载工具链。

未开启真实门禁时，真实探针必须显示固定的 `RAM_PROBE SKIP gate=double_confirmation`。离线分类和请求字段门禁必须通过。

## 真实测试门禁

真实执行必须在独立安全进程中注入以下环境变量，不得从聊天、命令历史或普通文本文件复制凭据：

- `RUN_DIRECTMAIL_RAM_PROBE=1`
- `DIRECTMAIL_RAM_PROBE_CONFIRM=I_CONFIRM_SAFE_SIGNED_MISSING_PARAMETER_PROBE`
- `APP_ENV=test`
- `EMAIL_ADAPTER=production`
- `DIRECTMAIL_ENDPOINT=https://dm.aliyuncs.com/`
- `DIRECTMAIL_REGION=cn-hangzhou`
- `DIRECTMAIL_RAM_PROBE_TEMPLATE_ID`：已审核通过的测试模板 ID
- 现有 DirectMail 凭据、发信地址和别名环境变量

最小 Allow 基线执行时，`DIRECTMAIL_RAM_PROBE_DENY_ACTION` 必须为空。显式 Deny 检查时，每次只设置下列一个固定 action 并单独运行：

- `QueryTemplateByParam`
- `DescTemplate`
- `SingleSendMail`
- `CreateTemplate`
- `ModifyTemplate`
- `DeleteTemplate`

真实执行属于外部权限验证，必须单独取得授权。本文件不构成执行授权。

## 固定结果语义

- 最小 Allow 通过：`RAM_PROBE PASS mode=minimum_allow reads=true send_signature_only=request writes_denied=3`
- 单 action Deny 通过：`RAM_PROBE PASS mode=explicit_deny permission=true safe_request=true`
- 失败：仅输出 `RAM_PROBE FAIL stage=<固定阶段> category=<安全类别>`

未知 Code 的附加安全字段固定为：

```text
code_sha256=<64位小写十六进制摘要> code_length=<UTF-8字节数> http_class=<固定状态族> candidate=<冻结候选名或unknown>
```

这些字段不能用于证明供应商错误语义；只有 `candidate` 唯一命中时，才可作为下一次扩充生产安全白名单的人工评审线索。禁止依据摘要自动修改生产错误码分类。

`request` 表示供应商已验证签名权限后拒绝缺参请求，不表示邮件已发送；`permission` 表示 RAM 策略拒绝；`unknown` 表示无法安全判断，必须失败并停止。
