# VID-G4：PR #420 图片隔离兼容回归修复

## 1. 范围与当前状态

- 修复基线为 `77d0be6fe594062bcad9dbf2952b4867d1742cb5`，分支保持 `feature/video-gateway-vid-g4-fake-async-media-safety`。
- 用户已确认同步修复五份隔离脚本、两个测试文件及验证文档，仅本地修改，不提交、不推送。
- 缺陷编号：`VID-G4-025`，P2，当前为 `CLOSED_LOCAL_VERIFIED`：本地修复与回归通过，远程CI尚未验证新源码。
- 不修改业务实现、正式 migration、CI分类器、测试选择范围、并发数、race或任何交付安全约束。
- 不增加正式视频HTTP接口，不启用真实Provider，不使用真实Key，不写真实用户钱包，不部署测试服或生产，不进入VID-G5。

## 2. 现象与根因

[PR #420 的 CI 运行 33287030342](https://github.com/skillixx/-molin/actions/runs/33287030342)在“AI 网关 G7 可靠性与零差额验收”的图片隔离门禁失败：

```text
TestImageTaskAssetRepositoryMySQLIsolationAndStates
重复主图必须只有一个写入胜者: winners=0 duplicates=100
```

原测试把所有资产Create错误计为duplicates，掩盖了实际数据库错误。增强分类后，在相同脚本和一次性MySQL中得到红灯证据：

```text
主图并发写入遇到非重复键错误: count=100
Error 1054 (42S22): Unknown column 'moderation_policy_version' in 'field list'
```

`AIImageAsset`同时承载共享资产字段。VID-G4增加了 `moderation_policy_version`、`explicit_label_version`、`implicit_label_version`，GORM创建图片资产也会提交这些列。旧图片隔离脚本补装到 `000075` 后直接运行当前源码，没有装载包含这些列的 `000076`，因此全部Create失败，而不是有100个真实重复键冲突。

这是验收环境与当前源码的Schema不一致；不能通过删除安全字段、跳过并发测试、放宽约束或为图片另建账本修复。

## 3. 最小修复

| 文件 | 调整 | 保留边界 |
|---|---|---|
| `infra/scripts/verify-image-gateway-migration-000069.sh` | 在已有000075之后补装000076 | 000069历史up/down/re-up断言仍先执行 |
| `infra/scripts/verify-image-gateway-migration-000070.sh` | 同上 | 000070历史断言、100并发、隔离与争议测试不减少 |
| `infra/scripts/verify-image-gateway-migration-000071.sh` | 同上 | 000071财务保留断言和图片计费夹具不改变 |
| `infra/scripts/verify-image-gateway-img-g6-http.sh` | 迁移选择增加精确版本76 | 不加载未来未知版本，不更改HTTP测试范围 |
| `infra/scripts/verify-image-gateway-img-g7-infrastructure.sh` | 迁移选择增加精确版本76 | 保持旧图片基础设施测试与原有隔离策略 |

五个成功摘要统一为 `current_head_compat_72_74_75_76=true`，避免摘要继续漏报实际兼容层。没有新增migration，也没有回滚、覆盖或删除项目数据库事实。

## 4. 回归测试

- `server/internal/modules/token_gateway/repository/image_task_asset_repository_mysql_test.go`：仅MySQL1062算重复键；其他错误独立计数，通过 `sync.Once` 保存首个错误，等待全部goroutine完成后报告。仍严格要求100并发恰好1成功、99重复。
- `server/migrations/ai_gateway_video_g4_migration_test.go`：逐一校验五个脚本在Go测试前实际装载000076，并校验成功摘要。只写注释或摘要不能满足执行语句检查。
- 红灯已验证：缺少补装时原MySQL测试报1054，新装载契约的五个子测试全部失败。
- 静态契约仅防止再次遗漏，不能替代下节真实隔离MySQL复验。

## 5. 本地验证结果

| 检查 | 状态 |
|---|---|
| 五个脚本兼容装载回归 | PASS |
| 五个脚本Bash语法与默认关闭（退出码3） | PASS |
| Python探针契约 | PASS，24/24 |
| 全量Go测试、vet、依赖校验、build | PASS |
| 图片G1、G2、G3、G5、G6、G7隔离门禁 | PASS，全部沿用原测试范围与并发断言 |
| VID-G4完整000001→000076与四包Linux race | PASS，含重复up、保留式down/re-up与三类100并发/重放 |
| CI同款敏感信息扫描 | PASS，0项发现；未执行任何外部动作 |
| 最终差异格式与源码指纹 | PASS，逐文件清单及复算规则见修复证据 |

Windows全量命令在 `server` 目录执行：

```powershell
go test ./... -count=1
go vet ./...
go mod verify
go build ./...
```

敏感扫描使用CI现有命令，不依赖不可用的外部技能扫描器：

```powershell
python scripts/verify-sms-phase5-sensitive-data.py --repo-root . --base-ref origin/main
```

隔离脚本使用各自既有审批环境变量，必须明确目标为本机一次性容器后设置为YES，完成后清除。不得复用项目DSN、环境文件或真实凭据。源码与模块缓存以只读方式挂载，MySQL数据放TMPFS，脚本仅清理自己创建的精确命名资源。

图片G7回归会运行原有一次性RabbitMQ/MinIO基础设施夹具，使用本机已有镜像和临时凭据，不使用现有共享容器。这只证明旧图片链路兼容，不能解释为视频RabbitMQ/MinIO数据面已经启用。图片G5/G6/G7测试名称不是VID-G5/G6/G7阶段开发。

## 6. 证据与后续门禁

原PR HEAD、CI失败与本地修复是三个不同事实。修复未提交、未推送，因此PR #420仍指向原提交，不能把本地PASS改写成远程CI已通过。

本次增量的基线、文件SHA-256清单、测试结果和本地修复指纹保存在[修复证据](./evidence/video-gateway-vid-g4-ci-compat-fix.json)。该指纹只绑定本次修复，不能替代原阶段的独立验收签字。复算方式为：对清单逐项核对文件字节SHA-256，再按清单顺序以LF连接 `BASE_COMMIT=<基线>` 与每一项 `path|sha256`，对UTF-8字节计算SHA-256；不添加末尾换行。证据JSON本身不参与清单，避免自引用。

后续必须取得新的提交和推送授权，在修复提交上重跑Ready完整CI并核对审查证据；合并仍需按实际PR编号授权。旧的源码状态、阶段验收和独立审查JSON/Markdown保持历史快照，不伪造新审核签字。

回滚本地修复仅移除这批脚本兼容补装与测试改动；不得因此回滚000076或删除已形成的安全事实。回滚后旧脚本重新失配是已知结果，不得继续将其报告为通过。
