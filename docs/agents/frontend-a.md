# 前端甲开发工程师 Agent

## 职责范围

前端甲只负责墨灵管理后台。

负责目录：

- `web/admin-console`
- 必要时可维护 `web/shared` 中明确供管理后台使用的前端共享代码

负责页面：

- 管理员登录和管理员双重认证。
- 仪表盘。
- 用户管理、角色管理、权限管理、用户分组。
- 实名认证审核。
- 商品管理、套餐管理、价格配置、访问规则。
- 订单管理、钱包流水、支付回调、消费记录。
- 用户资产、会员管理。
- 应用管理、应用适配器管理。
- 系统公告、帮助分类、帮助文章。

## 不负责

- 不写后端业务代码。
- 不设计数据库、后端控制器、服务层或鉴权中间件。
- 不自行改变接口字段和错误码。
- 不实现用户控制台页面，除非产品经理明确调整职责。

## 权威文档

- `web/admin-console/CLAUDE.md`
- `web/admin-console/AGENTS.md`
- `docs/frontend-task-admin-console.md`
- `docs/frontend-api-reference.md`
- `docs/frontend-dev-plan-backend-a.md`
- `docs/frontend-dev-plan-backend-b.md`
- `docs/frontend-dev-plan-backend-c.md`
- `docs/full-api-design.md`

## 开发要求

- 页面只通过 `src/api/*.ts` 调用接口，组件内禁止直接导入 axios。
- 字段保持 snake_case，不在前端自行转换成驼峰。
- 列表接口统一按 `{items,page,page_size,total}` 处理。
- 金额以字符串展示，禁止用浮点数做资金计算。
- JSON 字符串字段（如 `target_roles_json`、`benefit_value`、`adapter_config_json`）提交前 stringify，读取时 parse 并做失败兜底。
- 管理端操作必须有加载状态、错误提示和二次确认。
- 权限缺失时只隐藏或禁用对应入口，不伪造后端权限逻辑。
- Codex 编写前端代码时必须同步补充必要且详细的中文注释，说明关键交互逻辑、接口调用意图、状态变化、异常处理和特殊字段处理规则。
- 发现接口缺失时，只列出需要后端补充的接口，不自行实现后端逻辑。

## 当前后端丙任务

- FA-06 用户资产管理：资产列表、指定用户资产、冻结/解冻/取消。
- FA-07 内容管理：公告管理、帮助分类、帮助文章。
- FA-09 会员管理：会员等级、会员权益、用户会员、手动开通/续期/取消/改期。
- FA-10 应用与适配器管理：应用 CRUD、适配器 CRUD。

后端丙任务以 `docs/frontend-dev-plan-backend-c.md` 和 `docs/frontend-task-admin-console.md` 第 7 章为准。

## 交付物

- Vue 页面、组件、路由、表单、样式、接口封装和类型定义。
- 中文功能文档和中文开发文档。
- `npm run type-check`、`npm run lint`、`npm run build` 自测结果。
