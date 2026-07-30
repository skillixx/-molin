# 历史失效证据 — Phase 4 数据库版本 54 只读报告

> 本报告仅保存 2026-07-23 的历史 `54/0` 只读结果，已经被当前 `57/dirty=0` 门禁取代。禁止将本文作为当前 Phase 4、发布或 Redis 重启墓碑测试的前置通过证据。

## 验收范围

- 仅查询 `schema_migrations` 的 `version`、`dirty` 两个字段。
- 仅精确运行 Go 测试 `TestEmailSchemaReadonlyGate54`。
- 不执行 migration、DDL、DML、事务控制、锁表或数据清理。
- 不输出 DSN、主机、端口、账号、密码、工具路径或原始异常。

## 安全门禁结果

2026-07-23 使用固定确认短语、受控 Go 可执行文件和受控临时 modfile 执行一次真实只读检查，安全输出如下：

```json
{"reachable":true,"version":54,"dirty":false,"is_54_0":true}
```

进程退出码为 `0`，说明测试库可达，当前 migration 状态精确为 `54/0`。本次执行未应用 migration。

## 负向检查

- 确认短语错误：输出固定 `confirmation_required`，退出码 `2`。
- 显式 Go 文件不是 `go.exe`：输出固定 `go_invalid`，退出码 `2`。
- PowerShell 语法解析：通过。
- Python 数据库双实现：已删除，避免出现两套查询逻辑。

## 结论

截至 2026-07-23，当时针对版本 54 的 `schema_migrations` 安全只读检查通过。该结果现已失效，只证明该历史时间点数据库状态为 `54/0`；当前验收必须重新取得 `57/dirty=0` 证据，本文不等同于当前前置门禁、外部邮件投递或发布验收通过。
