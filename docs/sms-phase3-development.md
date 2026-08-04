# 阿里云短信验证码阶段 3 前端开发说明

## 基线与范围

- 基线：`origin/main@9e50ee1`，阶段 2 PR #315 已合并并验收通过。
- 分支：`codex/aliyun-sms-phase3-admin-ui`。
- 工作树：`D:\molingproject\molin-sms-phase3`。
- 范围：`web/admin-console` 及相关中文文档。
- 排除：后端、数据库、migration、部署、真实短信、白名单修改和阶段 4 消费 E2E。

## 代码结构

- `src/types/sms.ts`：九接口 DTO、五场景枚举和 D-95 分页。
- `src/api/sms.ts`：九个短信管理 API 封装。
- `src/components/sms/sms-template-policy.ts`：模板合规、场景独占和完整手机号内存校验。
- `src/views/sms/SmsManagementView.vue`：概览、模板、场景、日志、详情、同步、启停和测试提交。
- `src/router/index.ts`：短信管理路由及查看权限/MFA 门禁。
- `src/components/layout/SideMenu.vue`：消息中心短信菜单。
- `tests/sms-management-contract.test.mjs`：API、权限、安全策略、五态和响应式契约测试。

## 数据库与后端依赖

- 本阶段不新增或修改数据库表、索引、migration、seed 与后端路由。
- 页面只消费阶段 2 已合并的九个 `/api/admin/sms/*` 管理接口；数据库版本仍以阶段 2 的 `000059` 为准。
- 不部署测试服、不修改 `SMS_ENABLED`、白名单或阿里云配置，不执行真实短信发送。

## 权限点

- `sms:template:view`：进入页面并查看概览、模板、场景和脱敏日志。
- `sms:template:manage`：启停模板并更新五场景绑定。
- `sms:template:sync`：执行阿里云模板只读同步。
- `sms:template:test`：打开受控测试弹窗并提交测试请求。
- 路由同时要求管理员登录与双重认证；普通 HTTP 403 不会误跳双重认证页。

## 关键实现

1. API 字段保持 snake_case，列表统一消费 `{items,page,page_size,total}`。
2. 同步接口按实际后端契约发送空 body，页面使用共享操作 loading 防止重复点击；测试提交才携带 `Idempotency-Key`。
3. 场景写入以服务端 `version` 为准；409 后重新加载服务端版本，同时把管理员的目标模板和启停选择恢复到草稿。
4. 候选模板加载失败时场景配置失败关闭；候选超过单页上限时不使用不完整快照做绑定。
5. 测试请求遇到不确定结果时保留幂等键；只有管理员修改参数或明确选择“使用新请求”时清空旧键。
6. 完整手机号不进入浏览器持久层、URL、页面列表和确认文案，成功或关闭弹窗后立即清空。
7. 全局管理员验证策略同时精确识别邮件历史 `403/40003` 与短信正式 `403/40031`，普通 403 不会被误判。

## 页面状态与响应式

概览、模板、场景和日志分别维护加载、空数据、错误、无权限和正常状态；局部失败允许单独重试。1440px 使用高密度表格与六卡概览，1024/768px 自动减少卡片列数并让筛选换行，390px 使用卡片列表和筛选抽屉。所有按钮、输入和开关触控高度不少于 44px。

## 状态流转

- 页面读取：`loading -> normal | empty | error`；无查看权限时直接进入 `forbidden`，不会发起管理 API。
- 模板启停：管理员确认后提交 `{enabled,version}`；成功刷新快照，409 时加载最新版本并要求重新确认。
- 场景绑定：候选加载失败或模板不合规时保持失败关闭；提交 `{template_id,enabled,version}`，409 时刷新并保留未提交草稿。
- 测试提交：`idle -> confirming -> submitting -> accepted | retryable_error`；409、429、503 等不确定结果保留原幂等键，只有成功、关闭弹窗、修改任一测试参数或明确选择“使用新请求”才清除。
- `accepted` 只表示供应商受理，不表示运营商送达或用户实际收到。

## 测试方式

```powershell
cd web/admin-console
npm.cmd run test:sms-management
npm.cmd run test:admin-verification
npm.cmd run type-check
npm.cmd run lint
npm.cmd run build
```

浏览器验收需使用拦截后的九接口脱敏 fixture 或 `SMS_ENABLED=false` 的隔离环境，禁止在阶段 3 自动测试中真实发送短信。

### 2026-08-04 本地自测结果

- `test:sms-management`：9/9 通过。
- `test:admin-verification`：7/7 通过。
- `test:email-management`：11/11 通过。
- `test:outbound-url`：4/4 通过。
- `type-check`、`lint`、`build`：全部通过；构建仅保留既有的大分块和模块类型提示。
- 浏览器 Mock 验收：1440、1024、768、390 四档均无横向溢出；详情、五场景、脱敏日志、503 幂等重试均符合预期，未连接测试服、未发送短信。
