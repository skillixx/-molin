# VID-G5完整目标覆盖审计（未验收）

独立任务`vid_g4_final_spec`本次按VID-G5 Goal进行只读审计；名称沿用历史任务，不表示审查对象仍为G4。基线为`36b6a5c5f9e60a4ef182ae434337bb05e165477c`，对象为本地未提交源码，未独立执行测试。

## 审计结论

`SPEC_REVIEW=FAIL`，原因是明确的测试覆盖缺口。此次发现P0=0、P1=0、P2=1，仅为此轮发现数量，不是完整阶段最终缺陷数量。

| 编号 | 缺口 | 关闭所需证据 |
|---|---|---|
| G5-SPEC-001（P2） | Goal第十四节要求同request settle/release直接竞争；现有结算/释放各100并发、释放后顺序拒绝结算、取消对提交权竞争不能替代 | 同一真实临时MySQL请求同步调用相反财务入口；成功/失败/queued与T2V/I2V组合；唯一合法终态、单套资金和Usage/Outbox、守恒及17组对账 |

定位：`video_billing_settle_mysql_test.go`中的`TestVideoG5SettleMySQLConfirmedCostAndSingleConsumption`仅结算100并发；`video_billing_release_mysql_test.go`中的`TestVideoG5ReleaseMySQLDefiniteFailureMatrix`仅释放100并发；`video_billing_release_safety_mysql_test.go`只有顺序相反入口拒绝。补测试后需要独立复核，不自动改写本次历史结论。

## 已找到的实现和测试覆盖

- 十二金额金样齐全，显式/隐式标识失败另有释放矩阵。
- 对账实际包含17组事实检查；T2V/I2V各有并发交付与逐组零差异断言。
- 原子预占/结算/释放各100并发、事务故障、输入漂移、权限失效重放、跨Project/SK、未知/ACK、最多八次补偿、租约隔离、maker/checker及缺失钱包动作已有测试。
- 未知、拒绝取消、归档失败、Usage冲突的持有款和禁止交付应继续失败关闭；不能强行让这些金样财务终结。

## 执行与验收待项

- 旧Chat G4预算、G5管理、G6用户双层、G7可靠性的独立MySQL执行记录待补；默认Go测试Skip不能替代。
- 所有允许范围内的纯Go/临时MySQL/Fake测试需要覆盖；RabbitMQ、Redis、真实MinIO段须明确NOT_RUN，不因“全量兼容”突破OFF授权。
- 历史切片检查点不是最终同源源码指纹；仍需最终SOURCE_STATE、MySQL/金额/对账证据、独立QA/PM/Standards及Spec复核。

上述缺口均保留在完整Goal内，不缩小交付目标，不授权Git动作或G6。

## 后续兼容补测定位

### G5-SPEC-001后续关闭记录

新增`video_financial_terminal_race_mysql_test.go`的六组T2V/I2V×成功/失败/queued，在同请求100个同步入口中断言1次合法写入、49次幂等重放及50次明确财务状态冲突，逐项核验流水、互斥Outbox、资金守恒、交付与17组对账。初轮4组通过、2组因夹具预期的Provider失败处理错误中断；只修正夹具错误分类并增加Task已落库failed检查，相反入口也收紧为明确状态冲突。

独立Spec复核认为源码覆盖充分且修正未弱化断言；主代理后续完整隔离迁移/race运行六组全部通过（26.725秒，会话49086）。因此`G5-SPEC-001=CLOSED_VERIFIED`，不改写上方原始失败审计记录。详见[终态竞争检查点](./video-gateway-vid-g5-terminal-race-checkpoint.json)。完整阶段SPEC仍待兼容与最终同源验收。

旧Chat G7本地HTTP Fake性能门禁已实际运行通过（会话36132）：JSON与SSE各1000请求、100并发、全部成功，P95分别5.8525ms/0.5433ms，原阈值20ms/30ms未改。此项不包含数据库、真实Provider或生产性能证明。下表其余MySQL项仍为待补。

独立任务`vid_g5_cancel_slice_qa`只读核对了原脚本及夹具，建议全部采用独立临时库、完整1—77迁移、内部网络、只读依赖缓存及`--pull=never`，不直接运行旧脚本的外部访问部分。

| 用例 | 环境开关 | 夹具与隔离要求 |
|---|---|---|
| `TestG4MySQLBudgetIntegration` | `G4_ISOLATED_TEST=YES`、`G4_MYSQL_DSN` | user/project=1；key1、2同属该Project；无WHERE清理要求独立库 |
| Repository `TestG5MySQLIntegration` | `G5_ISOLATED_TEST=YES`、`G5_MYSQL_DSN` | 有效user=901，模型/渠道/路由/价格由测试创建 |
| `TestG6UserRepositoryMySQLIsolation`、`TestG6UserServiceMySQLReconciledDetail` | `AI_GATEWAY_G6_MYSQL_DSN` | 复用原脚本965—967、归档/无预算Project、预算增额、双请求、Usage/reservation/dispute，固定2026-08-08语义 |
| `TestG7MySQLReliabilityIntegration` | `G7_ISOLATED_TEST=YES`、`G7_MYSQL_DSN`、`G7_ISOLATED_DATABASE` | 库名前缀molin_g7_reliability_；701—803租户每钱包10元；qwen-plus成本1/1、销售2/2，每百万；全库积压断言要求独立库 |
| `TestG7GatewayAddedOverhead` | `G7_PERFORMANCE_TEST=YES` | 本机HTTP Fake，不连接公网或Provider |

还需检查旧000062重复up、保留式down/re-up、精度/负余额以及G5/G6权限/约束断言是否有等效当前执行证明。这里只记录待核对目录，不宣称缺少代码实现或测试已通过。
