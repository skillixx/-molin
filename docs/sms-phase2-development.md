# 阿里云短信验证码阶段 2 开发说明

## 功能范围

阶段 2 提供阿里云已审核验证码模板的只读同步、固定五场景绑定、本地启停、白名单测试发送、脱敏发送记录和九个管理 API。阶段 3 管理后台页面不在本阶段范围。

使用角色为已登录、具备对应短信权限且管理员手机与邮箱双重认证仍有效的管理员。

## 核心业务规则

1. 模板同步只读取阿里云模板列表和新版模板详情，不创建、修改或删除云端模板和签名。
2. 只保存固定签名下、验证码类型且变量集合精确为 `code` 的模板；含额外变量的模板忽略，供应商查询全部成功后才开启数据库事务。
3. 固定场景为 `register/login/reset_password/bind_phone/admin_verify`，签名只读取 `SMS_ALIYUN_SIGN_NAME`；五场景必须分别绑定独立模板。同一模板启用到其他场景时返回稳定的 `409/40900`，仓储事务通过模板行锁保证并发请求最多一个成功，同时允许停用历史共用绑定完成整改。
4. 模板和场景更新使用 `version` 乐观锁；审核失效或供应商移除会安全停用模板及其绑定。
5. 测试发送必须同时满足 `SMS_ENABLED=true`、`SMS_TEST_MODE=true`、白名单、双重认证、细分权限、幂等键和管理员/手机号 Redis 双维限流。
6. 测试发送先用数据库唯一约束抢占，再调用阿里云；相同请求重放不再限流或发送，异参复用返回冲突。
7. `accepted` 只表示阿里云受理，不代表运营商最终送达；实际收件必须独立记录。
8. 响应、发送日志和审计不记录验证码、完整手机号、AccessKey 或供应商原始响应。
9. 手机号或邮箱变化时必须原子清除对应管理员 MFA 时间戳，避免新收件人继承旧身份验证。
10. 管理写操作在业务调用前写请求审计并失败关闭；业务完成后的结果审计失败只记录安全告警，响应保持真实业务结果，避免客户端误判后重试造成重复改绑或重复发送。

## 代码结构

- `server/internal/modules/sms/sender/aliyun_template_provider.go`：模板列表与新版详情只读适配器。
- `server/internal/modules/sms/service/admin_service.go`：同步、查询、场景、启停、测试发送和限流规则。
- `server/internal/modules/sms/repository/sms_repository.go`：同步事务、乐观锁、幂等抢占及分页查询。
- `server/internal/modules/sms/handler/admin_handler.go`、`route.go`：九个管理接口、统一错误和权限/MFA 链。
- `server/internal/modules/auth/repository/user_repo.go`：联系方式变更与对应管理员 MFA 原子清除。
- `server/migrations/000059_add_sms_phase2_management.*.sql`：表结构、最小权限及可逆所有权记录。

## 状态流转

- 模板审核快照：`pending/approved/rejected`；只有 `approved` 可本地启用。
- 发送记录：`pending→accepted/failed`；`pending` 不作为成功公开。
- OTP 沿用阶段 1 的 `pending→accepted/failed`，五个用户端入口路径不变。

## 自动化与 CI

```powershell
cd server
go test ./... -count=1
go vet ./...
go mod verify
go test ./migrations -run TestSMSPhase2FullMySQL8Matrix -count=1 -v
```

迁移测试只有在显式配置 `SMS_MIGRATION_TEST_DSN`、目标库名以 `molin_sms_test_` 开头且服务端确认为 MySQL 8 时才执行；否则安全跳过，不能计为数据库通过。提交 `34b69a4` 对应的 [GitHub Actions 运行 #375](https://github.com/skillixx/-molin/actions/runs/30820296650) 已在隔离 MySQL 8、Redis 7 环境执行全库 Linux race 并通过，管理后台和用户控制台构建也通过。

## 隔离测试环境验证（2026-08-03 至 2026-08-04）

- 当前部署提交：`e06e0cc`；数据库：`59:0`；健康检查：200。最新代码变更提交 `8aca288` 包含请求审计失败关闭、结果审计语义、精确变量门禁和统一五场景校验，尚待推送、CI 与部署态复核；其后只有验收文档更新。
- 五个审核通过、变量为 `${code}` 的独立模板已完成只读同步；重复同步为 0 新增、0 更新、5 未变化。
- `register/login/reset_password/bind_phone/admin_verify` 已分别绑定不同模板，后四位依次为 `5137/0141/0117/0118/5093`。
- 五模板真实窗口新增 6 条阿里云 `Code=OK/accepted`，失败 0；其中管理测试 1 条、OTP 5 条，覆盖五业务入口。
- 用户确认收到 6 条，并确认统一签名“爱斯琴山东网络科技”和六条文案正确；`accepted` 仍只作为供应商受理证据。
- 历史窗口 7 条受理记录与管理员真实换绑/MFA 消费证据继续保留，但不与本轮 6 条计数混算。
- 管理员联系方式、验证码和其他敏感信息在文档及日志中仅保留脱敏或哈希值。
- 运行时泄露扫描通过：完整手机号、明文验证码、AccessKey、Bearer Token、JWT 均为 0 命中。
- 窗口结束后恢复 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`、原白名单 1 个号码。

测试服原环境仍存在废弃键名 `SMS_ACCESS_KEY`、`SMS_ACCESS_SECRET`、`SMS_SIGN_NAME`。真实窗口中为满足新版本 fail-closed 校验曾临时移除旧键并使用 `SMS_ALIYUN_*`；窗口结束后按授权恢复原环境。未来再次启用短信前应单独批准并永久清理废弃键名。

独立复验已确认历史“单模板覆盖五场景”P1 关闭，当前 P0=0、P1=0；产品经理的五模板、统一签名和六条文案业务复验通过。阶段 2 整体仍待最新代码部署、九管理 API 独立 HTTP、`register/login/reset_password` 后续业务消费 E2E、QA 最终签署、正式评审与 PR 合并，因此暂不进入阶段 3。
