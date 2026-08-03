# 阿里云短信验证码阶段 2 开发说明

## 功能范围

阶段 2 提供阿里云已审核验证码模板的只读同步、固定五场景绑定、本地启停、白名单测试发送、脱敏发送记录和九个管理 API。阶段 3 管理后台页面不在本阶段范围。

使用角色为已登录、拥有对应短信权限且手机与邮箱双重认证仍有效的管理员。

## 核心业务规则

1. 模板同步只调用阿里云模板列表和新版模板详情查询，不创建、修改或删除云端模板和签名。
2. 只保存固定签名下、验证码类型且包含 `${code}` 的模板；供应商查询全部成功后才开启数据库事务。
3. 固定场景为 `register/login/reset_password/bind_phone/admin_verify`，签名只读取 `SMS_ALIYUN_SIGN_NAME`，客户端不能提交自由签名。
4. 模板和场景更新均使用 `version` 乐观锁；审核失效或供应商移除会安全停用模板及其绑定。
5. 测试发送必须同时满足 `SMS_ENABLED=true`、`SMS_TEST_MODE=true`、白名单、双重认证、细分权限、1～128 字节幂等键和管理员/手机号 Redis 双维限流。
6. 测试发送先用数据库唯一约束抢占，再调用阿里云；相同请求重放不再次限流或发送，修改业务参数返回冲突。
7. 首次限流的恢复秒数写入内部 `retry_after_seconds`，幂等重放据此返回完全一致的 `Retry-After`；该字段不进入发送记录管理响应。
8. `accepted` 只表示阿里云受理，不代表运营商最终送达。响应、数据库和审计均不记录验证码、完整手机号、AccessKey 或供应商原始响应。
9. 测试发送的管理员维度和手机号 HMAC 维度分别限制为每分钟 10 次；任一维度超限都返回相同恢复秒数，幂等重放不新增计数。

## 代码结构

- `server/internal/modules/sms/sender/aliyun_template_provider.go`：模板列表与新版详情只读适配器。
- `server/internal/modules/sms/service/admin_service.go`：同步、查询、场景、启停、测试发送和限流规则。
- `server/internal/modules/sms/repository/sms_repository.go`：同步事务、乐观锁、幂等抢占及分页查询。
- `server/internal/modules/sms/handler/admin_handler.go`、`route.go`：九个管理接口、统一错误和权限/MFA 链。
- `server/migrations/000059_add_sms_phase2_management.*.sql`：表结构、最小权限及可逆所有权记录。

## 状态流转

- 模板审核快照：`pending/approved/rejected`；只有 `approved` 可本地启用。
- 测试发送内部记录：`pending→accepted/failed`；`pending` 不作为成功公开。
- 现有 OTP 继续使用阶段 1 的 `pending→accepted/failed`，五个用户端接口路径不变。

## 本地验证

```powershell
cd server
go test ./... -count=1
go vet ./...
go mod verify
go test ./migrations -run TestSMSPhase2FullMySQL8Matrix -count=1 -v
```

最后一条迁移测试只有在显式配置 `SMS_MIGRATION_TEST_DSN`、目标库名以 `molin_sms_test_` 开头且服务端确认为 MySQL 8 时才执行；否则安全跳过，不能计为数据库通过。CI 已配置隔离 MySQL 8 数据库并执行全库 Linux race，但远程流水线尚未运行，所以当前仍没有 MySQL 8 或 race 的 PASS 证据。

当前开发机没有 Docker/MySQL 客户端，也没有可复用的隔离 Redis；MySQL 8 全迁移/回滚矩阵和真实 Redis 7 限流恢复周期仍是 QA 前置门禁。真实短信测试、远程推送、PR、合并和部署均需项目负责人另行授权。
