# 墨灵 Chat Completions 服务端冒烟测试脚本设计

## 目标

提供一个适合 Windows PowerShell 的最小可重复测试脚本，验证现有 Project SK 是否能够调用 `molin/qwen-turbo`，并使用同一个 SK 按 `request_id` 查询执行与结算状态。

本脚本只验证既有 Project、平台 SK、模型授权、网关调用和账本查询，不负责注册用户、实名认证、充值、创建 Project 或签发 SK。

## 使用方式

脚本默认文件为 `scripts/test_molin_chat.ps1`，默认参数：

- Base URL：`http://8.130.9.163:3000`
- 模型：`molin/qwen-turbo`
- 提示词：`仅回复 OK`
- `max_tokens`：16
- 账本轮询：最多 10 次，每次间隔 1 秒

平台 SK 只允许通过当前进程的 `MOLIN_API_KEY` 环境变量传入。为了使用剪切板中的 SK，可先执行：

```powershell
$env:MOLIN_API_KEY = (Get-Clipboard).Trim()
.\scripts\test_molin_chat.ps1
Remove-Item Env:MOLIN_API_KEY
```

脚本不得读取或写入浏览器存储，不得把 SK 写入源码、配置文件、命令行参数、日志或测试制品。

## 数据流

1. 校验 `MOLIN_API_KEY` 非空，只输出脱敏前缀和长度，不输出完整值。
2. 为本次调用生成唯一 `Idempotency-Key`。
3. 向 `${BaseUrl}/v1/chat/completions` 发送非流式请求：
   - `Authorization: Bearer <SK>`
   - `Content-Type: application/json`
   - `Idempotency-Key: <唯一值>`
   - body 包含 `model/messages/max_tokens/stream=false`
4. 优先从 `X-Request-ID` 响应头提取账本 ID；若错误或 202 响应体含 `request_id`，则以响应体值补充。
5. 使用同一 Project SK 请求 `${BaseUrl}/v1/requests/{request_id}`。
6. 当账本达到终态时输出脱敏摘要：`request_id`、`execution_status`、`billing_status`、确认 Token 数和结算金额。

## 成功与失败判定

成功必须同时满足：

- Chat 接口返回 HTTP 200，或返回带 `request_id` 的 HTTP 202 待结算状态。
- 能提取非空 `request_id`。
- 状态查询返回 HTTP 200。
- 最终 `execution_status=succeeded`。
- 最终 `billing_status=settled`。

以下情况返回非零退出码并显示安全的中文诊断：

- 缺少 `MOLIN_API_KEY`。
- SK 无效、Project/SK 不可用或模型不在 allowlist。
- 商业流量总闸关闭、钱包不足、限流、上游异常或计费异常。
- 无法提取 `request_id`。
- 轮询超时后仍未结算。

错误输出只包含 HTTP 状态、平台稳定错误码、错误类型、中文消息和 `request_id`；禁止输出请求 Header、完整响应 Header、SK、内部地址或上游错误正文。

## 测试策略

先使用本地 Fake HTTP 服务完成自动化测试，再允许真实调用：

- 缺少环境变量时失败，且输出中不含密钥。
- Chat 200 时从 `X-Request-ID` 提取 ID 并查询账本。
- Chat 202 时从响应体提取 ID 并继续轮询。
- 账本从 `held/settlement_pending` 进入 `settled` 后成功退出。
- 401、403、402、429、503 等错误得到稳定中文诊断。
- Fake 服务记录的 Authorization 正确，但测试输出和生成文件中不出现完整 SK。

完成本地测试后，使用用户已授权的剪切板 SK 发起且仅发起一次真实低 Token 请求。真实验证只证明当前测试环境、当前 SK 和当前模型调用成功，不代表生产开放或长期稳定性验收。

## 交付范围

- PowerShell 冒烟测试脚本。
- 不接触真实 SK 的自动化测试。
- 中文使用说明，包含安全注入与清理环境变量示例。
- 一次经用户授权的测试环境真实低 Token 验证记录。

不包含 Project/SK 自动创建、密钥持久化、生产部署、循环压测、多模型测试或真实客户流量。
