# 阿里云短信验证码阶段 2 开发说明

## 功能范围

阶段 2 提供阿里云已审核验证码模板的只读同步、固定五场景绑定、本地启停、白名单测试发送、脱敏发送记录和九个管理 API。阶段 3 管理后台页面不在本阶段范围。

使用角色为已登录、具备对应短信权限且管理员手机与邮箱双重认证仍有效的管理员。

## 核心业务规则

1. 模板同步只读取阿里云模板列表和新版模板详情，不创建、修改或删除云端模板和签名。
2. 只保存固定签名下、验证码类型且包含 `${code}` 的模板；供应商查询全部成功后才开启数据库事务。
3. 固定场景为 `register/login/reset_password/bind_phone/admin_verify`，签名只读取 `SMS_ALIYUN_SIGN_NAME`。
4. 模板和场景更新使用 `version` 乐观锁；审核失效或供应商移除会安全停用模板及其绑定。
5. 测试发送必须同时满足 `SMS_ENABLED=true`、`SMS_TEST_MODE=true`、白名单、双重认证、细分权限、幂等键和管理员/手机号 Redis 双维限流。
6. 测试发送先用数据库唯一约束抢占，再调用阿里云；相同请求重放不再限流或发送，异参复用返回冲突。
7. `accepted` 只表示阿里云受理，不代表运营商最终送达；实际收件必须独立记录。
8. 响应、发送日志和审计不记录验证码、完整手机号、AccessKey 或供应商原始响应。
9. 手机号或邮箱变化时必须原子清除对应管理员 MFA 时间戳，避免新收件人继承旧身份验证。

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

## 隔离测试环境验证（2026-08-03）

- 部署提交：`34b69a4`；数据库：`59:0`；健康检查：200。
- 模板同步重复执行为 0 新增、0 更新、1 未变化；当前五场景均绑定同一个已审核、已启用模板。该配置证明绑定和发送链路可运行，但违反“五场景分别使用独立模板”的产品规则，不能通过阶段验收。
- 真实发送记录共 7 条，全部为阿里云 `Code=OK/accepted`；其中管理测试 1 条、OTP 6 条。
- 阶段要求的管理测试发送和五业务入口均覆盖。额外 1 条 `bind_phone` 为首次流程停止超时留下的受理记录，不重复计入入口验收。
- 用户确认最后四条收件分布为原白名单手机号 3 条、新绑定管理员手机号 1 条；`bind_phone` 和 `admin_verify` 验证码此前已成功消费。
- 管理员 259 已换绑为用户控制的真实手机号并重新完成手机 MFA；文档和日志只保留脱敏值。
- 运行时泄露扫描通过：完整手机号、明文验证码、AccessKey、Bearer Token、JWT 均为 0 命中。
- 窗口结束后恢复 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`、原白名单 1 个号码。

测试服原环境仍存在废弃键名 `SMS_ACCESS_KEY`、`SMS_ACCESS_SECRET`、`SMS_SIGN_NAME`。真实窗口中为满足新版本 fail-closed 校验曾临时移除旧键并使用 `SMS_ALIYUN_*`；窗口结束后按授权恢复原环境。未来再次启用短信前应单独批准并永久清理废弃键名。

独立验收结论为不通过：产品经理将“单模板覆盖五场景”判定为 P1；测试工程师要求补齐九管理 API HTTP 复核、`register/login/reset_password` 后续业务 E2E，并先提交最新证据文档。阶段 2 禁止合并，也不能进入阶段 3。
