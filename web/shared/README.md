# 前端共享代码

这里存放管理后台和用户控制台共用的前端代码。

建议拆分为：

- `api-client`：HTTP 客户端、请求拦截器、响应拦截器。
- `components`：共享 Vue 组件。
- `constants`：枚举选项、权限码、商品类型、状态值。
- `types`：共享 TypeScript DTO 类型。
- `utils`：格式化、校验、日期处理、金额处理工具。
