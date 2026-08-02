# 墨灵 AI 网关上游 SK 接入信息表

> 用途：收集 AI 上游联调所需的最小信息，供 Codex 配置和测试墨灵 AI 网关。
>
> 重要：本文档禁止填写真实 SK。真实 SK 只能配置在测试 Linux 的环境变量或密钥管理服务中。

## 1. 需要提供的内容

每个上游只需要提供以下四项：

```text
上游名称：
API Base URL：
允许调用的模型 ID：
SK 环境变量名称及配置状态：
```

说明：

- `API Base URL` 填写官方兼容接口地址，不要包含 SK 或其他敏感查询参数。
- `模型 ID` 必须与上游控制台或官方 API 文档中的实际值一致。
- `SK 环境变量名称` 只填写变量名，例如 `BAILIAN_API_KEY`，不得填写变量值。
- `配置状态` 填写“未配置”或“已配置到测试 Linux”。

## 2. 上游一：阿里云百炼

```text
上游名称：阿里云百炼
API Base URL：https://dashscope.aliyuncs.com/compatible-mode/v1
允许调用的模型 ID：qwen-turbo（已完成最小请求验证，其他模型待自动发现）
SK 环境变量名称：BAILIAN_API_KEY
配置状态：本机临时鉴权验证通过，尚未配置到测试 Linux
账号地域：中国大陆
备注：2026-07-30 使用剪贴板中的 SK 完成一次最小真实请求；文档未保存 SK 明文
```

参考资料：

- API Key 配置：<https://help.aliyun.com/zh/model-studio/get-api-key/>
- 免费额度：<https://help.aliyun.com/zh/model-studio/new-free-quota>
- 模型价格：<https://help.aliyun.com/zh/model-studio/model-pricing>

## 3. 上游二：OpenRouter

```text
上游名称：OpenRouter
API Base URL：https://openrouter.ai/api/v1
允许调用的模型 ID：cohere/north-mini-code:free（已完成最小请求验证，其他模型待自动发现）
SK 环境变量名称：OPENROUTER_API_KEY
配置状态：本机临时鉴权验证通过，尚未配置到测试 Linux
账号地域：全球
备注：2026-07-30 使用剪贴板中的 Key 完成一次最小免费请求；文档未保存 Key 明文
```

参考资料：

- API 快速入门：<https://openrouter.ai/docs/quickstart>
- Bifrost 接入说明：<https://docs.getbifrost.ai/providers/supported-providers/openrouter>

## 4. 上游三：硅基流动（可选）

```text
上游名称：硅基流动
API Base URL：https://api.siliconflow.cn/v1
允许调用的模型 ID：
SK 环境变量名称：SILICONFLOW_API_KEY
配置状态：未配置 / 已配置到测试 Linux
账号地域：中国大陆
备注：
```

参考资料：

- API 文档：<https://docs.siliconflow.cn/en/api-reference/chat-completions/chat-completions>
- 模型价格：<https://siliconflow.cn/pricing>

## 5. 模型清单

> 每个准备接入的模型填写一行。初次联调只需模型 ID，价格可以后续补充。

| 上游名称 | 模型 ID | 模型用途 | 是否允许调用 | 价格资料地址 | 备注 |
|---|---|---|---|---|---|
| 阿里云百炼 | `qwen-turbo` | 低价快速、连通性验证 | [x] 是 [ ] 否 | <https://help.aliyun.com/zh/model-studio/model-pricing> | 2026-07-30 最小请求验证通过，其他候选模型待发现 |
| OpenRouter | `cohere/north-mini-code:free` | 免费冒烟测试 | [x] 是 [ ] 否 | <https://openrouter.ai/docs/quickstart> | 2026-07-30 最小请求验证通过，其他候选模型待发现 |
| 硅基流动 | 待填写 | 备用 / 多模态预研 | [ ] 是 [ ] 否 | 待填写 | 待填写 |

## 6. 测试 Linux 配置确认

项目负责人完成 SK 配置后，只在本表中勾选状态，不记录密钥内容。

### 6.1 已完成的本机验证

| 验证日期 | 上游 | 接口 | 模型 | 结果 | 输入 Token | 输出 Token | 密钥保存情况 |
|---|---|---|---|---|---:|---:|---|
| 2026-07-30 | 阿里云百炼 | OpenAI 兼容 Chat Completions | `qwen-turbo` | 鉴权及请求成功 | 14 | 1 | 未写入项目文件，仅从剪贴板临时读取 |
| 2026-07-30 | OpenRouter | OpenAI 兼容 Chat Completions | `cohere/north-mini-code:free` | 鉴权及请求成功 | 3 | 1 | 未写入项目文件，仅从剪贴板临时读取 |

### 6.2 测试 Linux 配置状态

| 环境变量名称 | 对应上游 | 配置状态 | 配置日期 | 配置人 | 备注 |
|---|---|---|---|---|---|
| `BAILIAN_API_KEY` | 阿里云百炼 | [x] 未配置 [ ] 已配置 | - | 待填写 | 本机验证成功不代表测试 Linux 已配置 |
| `OPENROUTER_API_KEY` | OpenRouter | [x] 未配置 [ ] 已配置 | - | 待填写 | 本机验证成功不代表测试 Linux 已配置 |
| `SILICONFLOW_API_KEY` | 硅基流动 | [ ] 不使用 [ ] 未配置 [ ] 已配置 | 待填写 | 待填写 | 待填写 |

安全要求：

1. SK 不得发送到聊天、Git、截图、邮件或测试报告。
2. SK 不得写入源码、Markdown 文档或前端环境变量。
3. 环境变量文件只允许部署账号和服务进程读取。
4. 测试账号使用独立 SK，不能与生产环境共用。
5. SK 泄露或人员变动后必须立即吊销并重新生成。

## 7. 提交给 Codex

完成后，只需向 Codex 发送以下非敏感信息：

```text
已填写：docs/ai-upstream-sk-access-form.md
已配置的上游：
已配置的环境变量名称：
测试 Linux 是否已经完成配置：是 / 否
允许 Codex 执行上游连通性测试：是 / 否
```

Codex 收到确认后，再执行接口连通性、非流式响应、SSE 流式响应、Usage 字段和错误码测试。
