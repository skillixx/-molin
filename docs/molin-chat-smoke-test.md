# 墨灵 Chat Completions 冒烟测试

## 功能说明

`scripts/test_molin_chat.ps1` 用于验证平台 SK 是否可以调用指定模型，并根据 Chat 响应中的 `request_id` 查询最终结算账本。

默认配置：

- Base URL：`http://8.130.9.163:3000`
- 模型：`molin/qwen-turbo`
- 提示词：`仅回复 OK`
- 最大输出 Token：`16`
- Chat 请求：只发送一次，不会因结算轮询而重复调用模型

该脚本只证明本次测试请求及其账本状态，不代表生产开放或商业验收完成。

## 使用前提

1. 已在平台创建 Project 和平台 SK。
2. Project 已允许调用 `molin/qwen-turbo`。
3. 调用账户已满足实名、钱包余额及平台访问策略。
4. 平台 SK 只放入执行脚本的服务端进程环境变量，不写入浏览器、源码、文档或提交记录。

## 从密钥文件执行

如果平台 SK 临时保存在 `D:\test.txt`，可在 PowerShell 中执行：

```powershell
$env:MOLIN_API_KEY = (Get-Content -LiteralPath 'D:\test.txt' -Raw).Trim()
try {
    .\scripts\test_molin_chat.ps1
} finally {
    Remove-Item Env:MOLIN_API_KEY -ErrorAction SilentlyContinue
}
```

脚本不会输出完整 SK。测试结束后，`finally` 会清除当前进程中的环境变量。

## 从剪贴板执行

```powershell
$env:MOLIN_API_KEY = (Get-Clipboard).Trim()
try {
    .\scripts\test_molin_chat.ps1
} finally {
    Remove-Item Env:MOLIN_API_KEY -ErrorAction SilentlyContinue
}
```

## 自定义参数

```powershell
.\scripts\test_molin_chat.ps1 `
    -BaseUrl 'http://8.130.9.163:3000' `
    -Model 'molin/qwen-turbo' `
    -Prompt '仅回复 OK' `
    -MaxTokens 16
```

## 成功判定

脚本只有同时满足以下条件才返回成功：

1. Chat 接口返回 HTTP `200` 或 `202`。
2. 响应头或响应体包含 `request_id`。
3. `/v1/requests/{request_id}` 最终返回 `execution_status=succeeded`。
4. 同一账本最终返回 `billing_status=settled`。

成功输出只包含脱敏后的请求标识、执行状态、计费状态、Token 数和结算金额。

## 退出码

| 退出码 | 含义 |
|---|---|
| `0` | 模型执行成功且账本完成结算 |
| `2` | 未设置 `MOLIN_API_KEY` |
| `3` | 鉴权、调用、查询或最终状态失败 |
| `4` | Chat 响应缺少 `request_id` |
| `5` | 轮询期限内账本仍未完成结算 |

## 常见问题

### `/v1/chat/completions` 返回 HTTP 405

先检查测试服务器的 Nginx 是否已将 `/v1/` 转发到 Molin API。仓库中的标准配置位于 `infra/nginx/api.conf`，其中必须包含 `/v1/` 的 `proxy_pass`。如果 `/api/token/chat/completions` 可识别 POST、但 `/v1/chat/completions` 返回 405，通常表示线上 Nginx 配置或部署版本尚未同步。

不要在未确认请求是否已经被后端受理前反复重试 Chat 请求。先修复代理配置，再使用新的幂等键执行一次验证。

## 本地回归测试

Fake 测试不会访问真实上游，也不会产生真实费用：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/tests/test_molin_chat.ps1
```

测试覆盖缺少密钥、立即结算、异步结算和模型无权限四种场景，并校验测试密钥不会出现在命令输出中。
