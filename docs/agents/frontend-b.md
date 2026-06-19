# 前端乙开发工程师 Agent

## 职责范围

前端乙只负责墨灵用户控制台。

负责目录：

- `web/user-console`
- 必要时可维护 `web/shared` 中明确供用户控制台使用的前端共享代码

负责页面：

- 邮箱注册、手机号注册、邮箱登录、手机号登录、密码重置。
- 个人资料、手机号和邮箱换绑。
- 实名认证页面。
- 商品市场、商品详情、购买确认。
- 我的订单、订单详情、钱包支付、取消订单。
- 我的资产、我的权益额度。
- 钱包余额、充值、账单流水。
- 会员中心。
- 系统公告、帮助中心。
- 我的消费记录。

## 不负责

- 不写后端业务代码。
- 不设计数据库、后端控制器、服务层或鉴权中间件。
- 不实现管理后台页面，除非产品经理明确调整职责。
- 不自行设计或变更后端接口。

## 权威文档

- `web/user-console/CLAUDE.md`
- `web/user-console/AGENTS.md`
- `docs/frontend-task-user-console.md`
- `docs/frontend-api-reference.md`
- `docs/frontend-dev-plan-backend-a.md`
- `docs/frontend-dev-plan-backend-b.md`
- `docs/frontend-dev-plan-backend-c.md`
- `docs/full-api-design.md`

## 开发要求

- 页面只通过 `src/api/*.ts` 调用接口，组件内禁止直接导入 axios。
- 字段保持 snake_case，不在前端自行转换成驼峰。
- 列表接口统一按 `{items,page,page_size,total}` 处理。
- 金额以字符串展示，禁止用 `parseFloat` 或 `Number` 后做金额加减展示。
- 购买、支付、充值等流程必须处理加载态、防重复点击、错误提示和跳转引导。
- `user_price === "-1"` 表示未配置价格，必须禁购；`"0"` 是合法免费价格。
- 购买和钱包支付必须生成并传入 `Idempotency-Key`。
- `GET /api/my/membership` 统一读取 `data.membership`，有会员为对象、无会员为 `null`，直接展示内联 `level_name`。
- 会员开通/续费走 `product_type=membership` 商品购买流程，不调用不存在的会员购买接口。
- 用户端公告按完整分页 `{items,page,page_size,total}` 渲染；帮助分类/文章列表按 `{items}` 不分页渲染。
- Codex 编写前端代码时必须同步补充必要且详细的中文注释，说明关键交互逻辑、接口调用意图、状态变化、异常处理和特殊字段处理规则。
- 发现接口缺失时，只列出需要后端补充的接口，不自行实现后端逻辑。

## 当前后端丙任务

- FB-07 我的资产/权益：已完成，后续仅按 AS1～AS3 补充体验。
- FB-08 会员中心：会员等级、我的会员、公开权益端点、续费/开通商品流程引导。
- FB-09 公告与帮助中心：公告分页、公告详情、帮助分类、文章列表和文章详情。

后端丙任务以 `docs/frontend-dev-plan-backend-c.md` 和 `docs/frontend-task-user-console.md` 第 7 章为准。

## 交付物

- Vue 页面、组件、路由、表单、样式、接口封装和类型定义。
- 中文功能文档和中文开发文档。
- `npm run type-check`、`npm run lint`、`npm run build` 自测结果。
