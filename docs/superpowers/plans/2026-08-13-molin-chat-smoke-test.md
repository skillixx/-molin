# 墨灵 Chat Completions 冒烟测试脚本 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 创建一个不会泄漏平台 SK 的 PowerShell 脚本，调用墨灵 Chat Completions 并按 `request_id` 验证最终结算账本。

**Architecture:** `scripts/test_molin_chat.ps1` 负责参数校验、HTTP 调用、响应解析、账本轮询和退出码；`scripts/tests/test_molin_chat.ps1` 启动本机 Fake HTTP 服务验证请求与脱敏行为。真实调用只在 Fake 测试通过且用户已授权后执行一次。

**Tech Stack:** PowerShell 7/Windows PowerShell 5.1 兼容语法、`System.Net.Http.HttpClient`、本机 `HttpListener` Fake 服务、墨灵 OpenAI 兼容 `/v1` 接口。

## Global Constraints

- 默认 Base URL 为 `http://8.130.9.163:3000`，默认模型为 `molin/qwen-turbo`。
- 平台 SK 只从当前进程环境变量 `MOLIN_API_KEY` 读取，不接受命令行密钥参数。
- 不输出完整 SK、Authorization Header、完整响应 Header、内部地址或上游错误正文。
- 真实请求固定 `stream=false`、提示词“仅回复 OK”、`max_tokens=16`，只执行一次。
- 所有新增代码注释、提交说明和文档使用中文。
- 本工作只覆盖测试环境，不代表生产开放或商业验收。

---

### Task 1: PowerShell 冒烟客户端与 Fake 回归测试

**Files:**
- Create: `scripts/test_molin_chat.ps1`
- Create: `scripts/tests/test_molin_chat.ps1`

**Interfaces:**
- Consumes: 环境变量 `MOLIN_API_KEY`；参数 `BaseUrl:string`、`Model:string`、`Prompt:string`、`MaxTokens:int`、`PollCount:int`、`PollIntervalMilliseconds:int`。
- Produces: 标准输出中的脱敏测试摘要；成功退出码 `0`；配置、鉴权、调用或结算失败退出码非 `0`。

- [ ] **Step 1: 写入缺失密钥和成功结算的失败测试**

在 `scripts/tests/test_molin_chat.ps1` 中实现测试入口：

```powershell
$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\test_molin_chat.ps1'

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

$oldKey = $env:MOLIN_API_KEY
try {
    Remove-Item Env:MOLIN_API_KEY -ErrorAction SilentlyContinue
    $missingOutput = & pwsh -NoProfile -File $scriptPath 2>&1 | Out-String
    Assert-True ($LASTEXITCODE -ne 0) '缺少密钥时必须失败'
    Assert-True ($missingOutput -match 'MOLIN_API_KEY') '缺少密钥时必须给出安全提示'
} finally {
    if ($null -ne $oldKey) { $env:MOLIN_API_KEY = $oldKey }
}
```

同一测试文件启动一个随机本机端口的 `HttpListener` Fake 服务，记录收到的模型、`max_tokens`、幂等键和 Authorization 是否等于测试值，但不把 Authorization 写入输出。Fake Chat 返回 `X-Request-ID: req_fake_settled` 和 HTTP 200；Fake 状态接口返回：

```json
{"code":0,"message":"ok","data":{"request_id":"req_fake_settled","execution_status":"succeeded","billing_status":"settled","input_tokens":"4","output_tokens":"1","settled_amount":"0.00000100"}}
```

测试断言脚本退出码为 `0`、输出包含 `req_fake_settled/succeeded/settled`，且不包含测试 SK 完整值。

- [ ] **Step 2: 运行测试并确认因生产脚本不存在而失败**

Run:

```powershell
pwsh -NoProfile -File scripts/tests/test_molin_chat.ps1
```

Expected: FAIL，错误明确指向 `scripts/test_molin_chat.ps1` 不存在或无法执行。

- [ ] **Step 3: 实现最小安全客户端**

在 `scripts/test_molin_chat.ps1` 中实现：

```powershell
[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://8.130.9.163:3000',
    [string]$Model = 'molin/qwen-turbo',
    [string]$Prompt = '仅回复 OK',
    [ValidateRange(1, 128)][int]$MaxTokens = 16,
    [ValidateRange(1, 60)][int]$PollCount = 10,
    [ValidateRange(0, 30000)][int]$PollIntervalMilliseconds = 1000
)
```

实现以下内部函数：

```powershell
function ConvertFrom-SafeJson([string]$Content) { }
function Get-PublicErrorSummary($Body, [int]$StatusCode) { }
function Invoke-MolinRequest([System.Net.Http.HttpClient]$Client, [System.Net.Http.HttpRequestMessage]$Request) { }
function Get-RequestId($Response, $Body) { }
function Test-TerminalLedger($Ledger) { }
```

行为要求：

- 密钥为空时向错误流输出“请先设置 MOLIN_API_KEY 环境变量”，退出 `2`。
- `HttpClient` 默认超时 30 秒，Base URL 去掉尾部 `/`。
- Chat body 使用 `ConvertTo-Json -Depth 8 -Compress`，包含 `model`、`messages`、`max_tokens`、`stream=$false`。
- `Idempotency-Key` 使用 `molin-smoke-<Guid N>`。
- Chat 只接受 HTTP 200 或 202；其他状态只输出公开 `code/error/message/request_id` 摘要，退出 `3`。
- 从 `X-Request-ID` 或 JSON 顶层 `request_id`、`data.request_id` 提取 ID；缺失时退出 `4`。
- 状态接口使用同一个 Bearer SK；兼容统一响应包装 `data` 和直接对象。
- `billing_status=held|settlement_pending` 时等待后重试；`settled` 为成功终态；`released|exception` 为失败终态。
- 成功输出 `request_id`、`execution_status`、`billing_status`、输入/输出 Token、结算金额，退出 `0`。
- 轮询耗尽退出 `5`；所有 `HttpClient`、请求和响应对象在 `finally` 中释放。

- [ ] **Step 4: 运行 Fake 测试并确认通过**

Run:

```powershell
pwsh -NoProfile -File scripts/tests/test_molin_chat.ps1
```

Expected: PASS，输出 `PASS: 墨灵 Chat 冒烟脚本 Fake 测试通过`，进程退出码 `0`。

- [ ] **Step 5: 增加 202 轮询与错误脱敏测试**

扩展 Fake 测试覆盖：

- Chat 202 body 返回 `request_id=req_fake_pending`，第一次状态为 `settlement_pending`，第二次为 `settled`。
- Chat 403 body 返回稳定错误码和中文消息，脚本退出非 `0`。
- Fake 测试 SK 为 `sk-molin-fake-secret-never-print`；收集 stdout/stderr 后断言该完整字符串不存在。
- Fake 服务断言只收到一次 Chat POST，账本轮询不会重复产生模型调用。

- [ ] **Step 6: 运行全量 Fake 测试并检查脚本语法**

Run:

```powershell
pwsh -NoProfile -Command "[void][scriptblock]::Create((Get-Content -Raw scripts/test_molin_chat.ps1)); [void][scriptblock]::Create((Get-Content -Raw scripts/tests/test_molin_chat.ps1))"
pwsh -NoProfile -File scripts/tests/test_molin_chat.ps1
git diff --check
```

Expected: 三条命令退出码均为 `0`，Fake 用例全部 PASS，输出不含完整 SK。

- [ ] **Step 7: 提交脚本与测试**

```powershell
git add scripts/test_molin_chat.ps1 scripts/tests/test_molin_chat.ps1
git commit -m "新增墨灵模型调用冒烟测试脚本"
```

---

### Task 2: 使用说明与一次真实测试环境验证

**Files:**
- Create: `docs/molin-chat-smoke-test.md`
- Modify: `scripts/test_molin_chat.ps1`（仅在真实验证暴露契约偏差时按失败测试修正）
- Modify: `scripts/tests/test_molin_chat.ps1`（为真实验证发现的契约偏差补回归用例）

**Interfaces:**
- Consumes: Task 1 的 `scripts/test_molin_chat.ps1` 和用户已授权的剪切板平台 SK。
- Produces: 可复制的安全运行命令、退出码说明和一次当前测试环境验证摘要。

- [ ] **Step 1: 编写中文使用说明**

文档必须包含：

```powershell
$env:MOLIN_API_KEY = (Get-Clipboard).Trim()
try {
    .\scripts\test_molin_chat.ps1
} finally {
    Remove-Item Env:MOLIN_API_KEY -ErrorAction SilentlyContinue
}
```

说明 Project SK 必须已授权 `molin/qwen-turbo`，账户已实名、钱包余额充足；列出退出码 `0/2/3/4/5`；明确不要把 SK 写入 `.ps1`、`.env`、浏览器存储、截图或聊天内容。

- [ ] **Step 2: 重跑 Fake 测试作为真实调用门禁**

Run:

```powershell
pwsh -NoProfile -File scripts/tests/test_molin_chat.ps1
```

Expected: PASS 且退出码 `0`。未通过不得读取剪切板或发起真实请求。

- [ ] **Step 3: 从剪切板临时注入 SK 并执行一次真实请求**

Run:

```powershell
$env:MOLIN_API_KEY = (Get-Clipboard).Trim()
try {
    .\scripts\test_molin_chat.ps1 -BaseUrl 'http://8.130.9.163:3000' -Model 'molin/qwen-turbo' -Prompt '仅回复 OK' -MaxTokens 16
} finally {
    Remove-Item Env:MOLIN_API_KEY -ErrorAction SilentlyContinue
}
```

Expected: 只产生一次 Chat 请求；输出非空 `request_id`，最终 `execution_status=succeeded`、`billing_status=settled`，且不出现完整 SK。

- [ ] **Step 4: 真实验证失败时先补 Fake 回归再修正**

如果真实接口返回与设计不同，只记录 HTTP 状态、平台稳定错误码、错误类型、中文消息和 `request_id`。先在 Fake 测试中复现相同响应形状并观察失败，再对生产脚本做最小修改；禁止盲目重复真实 Chat 请求。

- [ ] **Step 5: 最终验证并提交文档或必要修正**

Run:

```powershell
pwsh -NoProfile -File scripts/tests/test_molin_chat.ps1
git diff --check
git status --short --branch
```

Expected: Fake 测试 PASS、`git diff --check` 无输出，工作树仅包含本任务预期文件。

Commit:

```powershell
git add docs/molin-chat-smoke-test.md scripts/test_molin_chat.ps1 scripts/tests/test_molin_chat.ps1
git commit -m "补充墨灵模型调用测试说明"
```
