# 阿里云短信验证码阶段 5 回滚手册

## 1. 第一动作

任何短信故障先将 `SMS_ENABLED=false`，通过受控配置发布重启或滚动替换 API。不得删除发送日志、验证码记录、
审计日志或模板绑定，不得回退到 Mock、固定验证码或接口明文验证码。

## 2. 标准顺序

1. 记录当前版本、health/ready、指标时间窗、五场景聚合和脱敏日志计数。
2. 关闭 `SMS_ENABLED`，保留 `SMS_TEST_MODE` 和白名单现状用于证据对账。
3. 验证所有手机发码入口返回 `503/50300`，供应商调用计数停止增长。
4. 验证邮箱验证码、管理员登录、两套控制台和九个短信管理只读接口未被误伤。
5. 必要时回滚到已验证应用二进制/镜像；恢复前一份不入库配置时不得打印差异值。
6. 默认保留 `000058/000059`。只有确认无引用、已备份且获数据库专项授权时才讨论 down。

## 3. 反向代理回滚

可信代理配置异常时，先关闭短信，再恢复上一份已验证配置。不得用清空安全校验代码、信任 XFF 或加入全网 CIDR
作为恢复手段。代理仍须覆盖 X-Real-IP 并删除 XFF/Forwarded。

固定代理网络变更必须保存原容器 inspect、原网络列表、原 API 环境文件和原二进制。回滚顺序为：关闭短信，
恢复原环境文件并重启旧二进制，按原 inspect 参数恢复两个前端容器，验证 health/ready 与关闭态后再删除本轮专用网络。
删除网络只能在确认没有容器连接后执行；不得用通配名删除 Docker 网络。正式命令中的备份路径、时间戳、容器 ID、
镜像摘要和旧配置哈希必须由部署窗口现场快照填充，计划文档不伪造未知值。

测试服前端手动部署工作流会把部署前正在运行的镜像按时间戳打成 `molin-admin:rollback-*` 和
`molin-user:rollback-*`，新容器启动、固定 IP 校验或健康检查任一失败时自动恢复原镜像。自动恢复保留固定代理专网，
因为 API 的可信代理配置依赖该网段；只有整个阶段 5 代理方案被撤销且容器已迁出时，才允许单独删除网络。

## 4. Dry Run 证据

本地已执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-rollback-dry-run.ps1 -Environment test -CurrentSmsEnabled false
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-rollback-dry-run.ps1 -Environment production -CurrentSmsEnabled true
```

两次均输出 `rollback_dry_run=passed`，远端连接、配置写入、重启、migration 和真实短信均为 0。
