# AI 网关百炼渠道名称乱码修复

## 功能说明

AI 网关工作台的 Bifrost 路由列表应显示渠道名称“百炼 Bifrost”。本次修复处理历史运维写入时发生的 UTF-8 二次编码，避免管理后台继续显示 `ç™¾ç‚¼ Bifrost`。

使用角色：具有 AI 网关查看或路由管理权限的管理员。

页面入口：管理后台 → AI 网关工作台 → Bifrost 路由。

## 业务规则

- 只修复渠道编码为 `bailian`，且名称字节精确等于已确认乱码字节的记录。
- 不修改渠道密钥、地址、状态、优先级、健康状态或路由关系。
- 如果管理员已经将名称改为其他合法值，迁移必须保持该值不变。
- 数据纠错不能在应用版本回退时重新写入乱码，因此 down migration 为安全空操作。

## 开发说明

核心文件：

- `server/migrations/000067_fix_bailian_channel_name_utf8.up.sql`：使用十六进制 UTF-8 常量修复精确匹配的错误数据。
- `server/migrations/000067_fix_bailian_channel_name_utf8.down.sql`：保留已纠正的数据。
- `server/migrations/ai_gateway_channel_name_migration_test.go`：锁定渠道编码、错误字节和正确字节三重契约。

本次不改变接口字段和数据库表结构。管理后台仍通过既有 AI 网关渠道/路由查询接口读取 `token_channels.name`。

## 测试方式

```powershell
cd server
go test ./migrations -run TestAIGatewayBailianChannelNameRepairContract -count=1
go test ./migrations -count=1
```

数据库验收应核对：

- 修改前只有目标记录匹配错误十六进制字节。
- 修改行数严格等于 `1`；重复执行时应为 `0`。
- 修改后名称为“百炼 Bifrost”，十六进制字节为 `E799BEE782BC20426966726F7374`。
- 工作台页面刷新后不再出现历史乱码文本。
