# 测试报告 — 后端丙会员模块 M10/M11 管理端接口

- **统计**：总用例 **29** / 通过 **28** / 失败或缺陷 **1（P2）**
- **结论**：**部分通过**。功能契约（开通/永久/续期叠加/取消/改期/跨接口一致/鉴权/参数校验）全部符合；发现 1 个 P2 缺陷。
- **测试脚本**：`tests/test_m10_m11_user_memberships.py`（已同步测试服 `~/molin/`）
- **环境**：测试服 `8.130.9.163:8080`（部署分支 main），MySQL `127.0.0.1:13306` 库 `molin`。仅操作自造用户数据（admin uid=446 / target uid=447 / noperm uid=448；等级 8/9/10/11），残留孤儿记录已清理。
- **被测契约**：`docs/frontend-api-reference.md` §11.6

## 逐项结果

| 场景 | 用例 | 结果 | 佐证 |
|---|---|---|---|
| 1 | M10 `duration_days=30` 开通 | PASS | 返回 `data.message="开通成功"`；DB 1 条 `active`，`expires_at`≈now+30d（实测 30.000d）|
| 2 | M10 `duration_days=null` 永久 | PASS | DB 1 条 `active` 且 `expires_at IS NULL` |
| 3 | C-FIX-1 续期叠加（再 +30d）| PASS | 仍同一条 id=11，不新增；`expires_at` 叠加 +30d（距今 60.000d）|
| 4 | M11 `{action:"cancel"}` | PASS | 返回 `更新成功`；DB `status=cancelled` |
| 5 | M11 `{expires_at}` 覆盖 | PASS | DB `expires_at` = UTC `2026-12-31T00:00:00Z`，时区一致 |
| 6 | 交叉校验 M9 列表 / M2 我的会员 | PASS | M9 扁平分页含该记录；M2 返回 active 会员，`id`/`status` 与 DB/M9 同一条一致 |
| 7a | 无权限调 M10 | PASS | HTTP 403 / code=40003 |
| 7b | 无权限调 M11 | PASS | HTTP 403 / code=40003 |
| 7c | 无 token 调 M10 | PASS | HTTP 401 |
| **7d** | **不存在 user_id 调 M10** | **FAIL** | 见 BUG-M10-01 |
| 7e | 不存在 level_id 调 M10 | PASS | HTTP 400「会员等级不存在」|
| 7f | 缺 user_id/level_id | PASS | HTTP 400 |
| 7g | 非法 body（user_id 类型错）| PASS | HTTP 400 |
| 7h | M11 不存在 {id} | PASS | HTTP 400「会员记录不存在」|
| 7i | M11 无效 action | PASS | HTTP 400 |
| 7j | M11 空 body | PASS | HTTP 400「无可更新字段」|

## 缺陷清单

### [membership][P2] BUG-M10-01 — M10 手动开通会员不校验 user_id 存在性，产生孤儿记录

- **优先级**：P2（管理端 UI 一般从用户列表选取，正常路径不易触发；但接口层缺校验，存在脏数据/越权写入风险）
- **接口**：`POST /api/admin/user-memberships`
- **复现**：管理员 token，body `{"user_id":999999999,"level_id":8,"duration_days":30}`
- **期望**：返回 4xx（user 不存在），不写库
- **实际**：`HTTP 200 {"code":0,...,"data":{"message":"开通成功"}}`，写入 `user_memberships` 一条 `user_id=999999999` 的 active 记录
- **根因定位（供后端丙参考）**：`server/internal/modules/membership/service/membership_service.go` 的 `AdminGrantMembership` 仅校验 `user_id!=0` 与 `level_id` 存在性，未校验 user 是否存在；`CreateOrRenewMembership` 直接 `tx.Create`。`level_id` 已做存在性校验（7e 正确 400），user_id 缺对称校验
- **影响**：误传/越权 user_id 产生无主会员数据，且会进入 `ExpireMembershipsJob` 等后续处理。测试残留记录已 `DELETE` 清理

## 契约/文档观察（非缺陷，建议跟进）

1. **M11 `expires_at` 用法不一致**：`docs/apipost-test-guide-backend-c.md` §2-C3 示例写成 `{"action":"update","expires_at":...}`，但实现只认 `action:"cancel"`，传 `action:"update"` 返回 400「无效 action」。覆盖到期时间应只传 `{"expires_at":...}`（与 §11.6 一致）。建议修正指南示例。
2. **`/api/my/membership` 响应形态差异**：测试服部署版本 `data` 为扁平会员对象（永久会员省略 `expires_at` 而非显式 `null`），而 §11.2 与当前源码 handler 为 `data.membership` 包裹。疑似测试服二进制较旧。建议确认部署版本与 §11.2 一致，避免前端按 `data.membership` 取值取空。不影响 M10/M11 写入正确性。

## 上线建议

M10/M11 核心功能契约全部通过；BUG-M10-01 为 P2（有 UI 层规避路径），建议修复 user_id 存在性校验后随下一批合入，不构成 P0/P1 阻断。
