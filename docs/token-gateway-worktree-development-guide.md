# Token 网关独立 Worktree 开发说明

## 1. 固定开发位置

Token 网关后续开发统一使用：

```text
目录：D:\molingproject\molin-gateway-worktree
分支：feature/bifrost-ai-gateway
```

短信和邮件开发分别保留在其他工作目录，不能通过切换原目录分支的方式开展网关开发。

```text
短信及其他既有任务：D:\molingproject\molin
邮件开发：D:\molingproject\molin-email-worktree
```

## 2. 每次开始前检查

在 PowerShell 中执行：

```powershell
Set-Location D:\molingproject\molin-gateway-worktree
git branch --show-current
git status --short
go version
node --version
```

预期分支必须是：

```text
feature/bifrost-ai-gateway
```

如果分支、目录或工作树状态与预期不一致，应先停止修改并核对，不得清理、覆盖或暂存来源不明的改动。

## 3. 本地与远程职责

本地 Windows 负责：

- Go、Vue3 和 TypeScript 代码开发。
- Go 单元测试、前端类型检查、Lint 和构建。
- Git 提交、代码评审和文档维护。
- 通过 SSH 隧道访问测试环境服务。

测试 Linux 负责：

- Molin API 集成部署。
- MySQL、Redis、RabbitMQ 和 MinIO。
- Bifrost 双节点及负载均衡。
- 百炼、OpenRouter 等真实上游联调。
- migration、钱包计费、并发、故障和恢复验收。

本地不保存真实上游 SK，也不要求运行完整数据库和中间件。

## 4. 建议的 SSH 隧道

```powershell
ssh -N `
  -L 18081:127.0.0.1:8080 `
  -L 28080:127.0.0.1:18080 `
  -p 10003 pc@8.130.9.163
```

隧道建立后：

```text
Molin API：http://127.0.0.1:18081
Bifrost：http://127.0.0.1:28080
```

## 5. 标准开发流程

```text
确认 worktree 和分支
→ 更新规划、接口和数据库契约
→ 本地编码与单元测试
→ 前端 type-check、lint、build
→ 检查差异和敏感信息
→ 提交 feature/bifrost-ai-gateway
→ CI 构建 Linux amd64 制品
→ 部署到测试 Linux
→ 执行获批的 migration
→ 验证 Molin → Bifrost → 上游 → usage → 钱包结算
→ QA 与产品验收
```

## 6. 禁止事项

- 禁止在 `D:\molingproject\molin` 直接开发 Token 网关。
- 禁止为了切换任务而清理、stash 或覆盖其他工作区改动。
- 禁止把真实 SK、密码、Token 和完整用户隐私数据写入仓库。
- 禁止未经确认执行生产部署、不可逆数据库操作和真实资金调整。
- 禁止把 Bifrost 的内部路由信息、上游响应头或密钥别名原样返回终端用户。

## 7. Codex 任务说明模板

新建 Token 网关 Codex 任务时，可以使用：

```text
请在 D:\molingproject\molin-gateway-worktree 中开发 Token 网关。
目标分支必须是 feature/bifrost-ai-gateway。
本地负责开发和单元测试，集成测试部署到测试 Linux。
不得修改短信工作区和邮件 worktree，不得在代码、文档或日志中保存真实密钥。
```
