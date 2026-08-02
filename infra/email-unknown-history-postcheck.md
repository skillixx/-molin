# Redis unknown 历史夹具清理后只读复核

## 用途

本复核用于 Redis `provider_outcome_unknown` 历史夹具完成精确清理后，独立确认数据库、Redis、恢复点、清理二进制和两套 `000057` 证据仍满足验收契约。它不依赖清理后已删除的 state 文件，也不执行恢复、清理、重启或任何数据库写入。

## 身份来源

运维人员必须显式提供已发布恢复点的文件名、恢复点 SHA-256、本次清理使用的二进制 SHA-256，以及两份 `000057` dump 的预期 SHA-256。runner 只接受以下严格格式：

- 恢复点：`molin-email-unknown-` 加 32 位小写十六进制随机标识，再加 `.sql`。
- 三类摘要：均为 64 位小写十六进制且不能全为零；两份 cycle dump 摘要还必须互不相同。

恢复点文件名中的 operation nonce 只约束 artifact 格式，不参与夹具身份计算。payload 从完整恢复 SQL 中唯一识别 `qa-phase4-<fixture_nonce>`，再用 fixture nonce 重算 HMAC、模板标识、两项 key hash、scope 和 Redis key，并与两条日志、一个白名单、一个模板交叉核验。恢复 SQL 必须包含可信 mysqldump 头尾、四张目标表各自唯一且闭合的实际 `CREATE TABLE` 块，以及 `schema_migrations` 的 version、dirty 和主键精确结构；三张业务表还会按 migration 契约核对完整列数量、顺序、定义、`PRIMARY KEY(id)`、InnoDB 引擎、字符集和排序规则，注释中的伪结构不能代替真实 DDL。受限 SQL 词法器会跳过字符串、反引号标识符、行注释、普通块注释和 optimizer hint；MySQL `/*!...*/` 可执行版本注释则去除合法的可选 5/6 位版本号后交回同一词法与语句路径，因此常见版本 `SET` 可以通过，但其中隐藏、穿插或跨行的目标 `INSERT`/`CREATE` 仍会被计数并拒绝。四张目标表各自只能有一条单行规范 `INSERT INTO \`表名\` VALUES ...;`。非标准大小写或空白、跨行语句、列清单、注释插入、额外目标 `INSERT`、递归或未闭合版本注释、重复或缺失 DDL、业务列漂移、主键或表选项错误、dirty 结构错误及伪最小 dump 均拒绝。最终输出不包含恢复点文件名、完整随机标识、业务原值、数据库凭据或 Redis 密码。

## 只读门禁

一次执行只建立一条 SSH 连接，且不重试。远端仅允许以下读取：

- 使用 `GET` 检查 `/api/health` 与 `/api/ready`，并严格解析 JSON；重复键、未知字段、错误类型和尾随第二个 JSON 均拒绝。
- 使用 MySQL `SELECT` 确认 schema 版本为 57、dirty 为 0、两条目标日志不存在、对应 scope 无残留、白名单和模板均不存在。
- 使用 Redis `PING` 与精确 `EXISTS` 确认服务可读且派生锁键不存在。
- 使用 `find`、`stat` 与 `sha256sum` 确认恢复点为当前用户所有的 `600` 非空普通文件且 SHA-256 等于冻结值，清理二进制仍为 `500` 且 SHA-256 与冻结值一致；父目录和最终 inode/size/SHA 也必须稳定。
- 确认两套 `000057` 根目录及 `evidence` 中间目录均为非符号链接的 `root:700`，零字节也合法的 `cycle_completed` 为 `root:600` 普通文件，非空 dump 为 `root:600` 且 SHA-256 与受控双摘要 manifest 精确匹配。
- 两个隔离 schema 都必须为 version57、dirty0、69 张基础表、一个专用备份表，且 receipt `created_at` 为秒精度；最终再次核验目录、marker、dump 的类型、inode、size 和 SHA。

禁止通配 Redis 查询、`KEYS`、`SCAN`、`FLUSHDB`、`FLUSHALL`、写 SQL、文件写入、服务重启和清理调用。

## 本地离线自检

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/run-email-unknown-history-postcheck.ps1 -SelfTest
```

SelfTest 只读取本地 payload，并启动本机 Python 验证 payload 内的真实严格 JSON 解析器；不会发现或启动 SSH，也不会访问数据库、Redis 或 API。

## 人工执行门禁

实际运行前，应从受控运维记录取得恢复点文件名与已冻结二进制 SHA-256，不要把随机标识、密钥或密码粘贴到聊天、日志或工单正文。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/run-email-unknown-history-postcheck.ps1 `
  -Confirm I_CONFIRM_EMAIL_UNKNOWN_HISTORY_POSTCHECK_ONCE `
  -RecoveryFileName '<受控恢复点文件名>' `
  -ExpectedCleanupBinarySHA256 '<受控二进制 SHA-256>' `
  -ExpectedRecoverySHA256 '<受控恢复点 SHA-256>' `
  -ExpectedCycleDumpSHA256One '<第一份受控 dump SHA-256>' `
  -ExpectedCycleDumpSHA256Two '<第二份受控 dump SHA-256>'
```

成功摘要只报告固定布尔值和计数。任一门禁失败只输出固定分类与阶段，不输出原始远端错误或敏感身份。
