# VID-G6 原任务管理轮询（开发与验证中）

## 功能边界

`POST /api/admin/token/video-tasks/{task_id}/poll`供具有当前管理员JWT、双MFA和`ai_gateway:task_manage`的管理员追踪原已提交视频任务。请求仅reason和version_no，必须Idempotency-Key，使用既有严格管理JSON解析；不接受Provider ID、外部URL、Prompt或参考图。

默认关闭，须显式提供专用原因加密器及只含Name/Query能力的`VideoAdminPollProvider`。本阶段只接受fake-native-async，不装配真实Provider、Bifrost或密钥。依赖接口没有Submit、媒体读取和删除方法，管理轮询不会重新生成。

原用户/Project Key被停用不解除平台追踪已提交任务的责任。管理恢复只证明原Task/Request/Project/Key/Provider绑定，读取必要的原规格与状态；不解密Prompt、不重新读取或上传I2V参考图、不恢复原用户的生成或下载权限。

## 执行与幂等

1. 实时鉴权，先锁Task/Request，核对原版本和唯一提交绑定。
2. 同事务追加前审计及running命令，冻结原Provider绑定摘要和原因AES-GCM信封。
3. 提交后再次鉴权，在数据库事务外执行一次原Provider Query；不会在SQL重试闭包中重复Query。
4. 新事务锁原任务及命令，复验当前管理员及Provider绑定，通过原G4/G5观察结果处理器写入事实，再写后审计并关闭命令。

同管理员同key绑定原Task、初始版本、原因及Provider绑定，重放核对密文和审计，不再次Query。运行中重放202；完成或未知回执200。不同意图409，数据库与依赖故障503，鉴权及MFA沿用原错误码。

单任务最多一个running管理轮询命令，数据库使用生成列唯一索引限制。命令执行期限30秒，外部调用同时受当前JWT截止限制。RPC后认证/数据库/连接失效时只尝试为已有命令追加低敏unknown后审计，不借善后流程推进Task或退款；无法确认时保留原running，不伪造完成。过期重放须重新读取善后实际状态。

响应七字段：command_id、task_id、request_id、status、execution_status、version_no、idempotent。status描述管理操作running/completed/unknown，不是Video Job状态。completed仅表示本次观察已处理，不表示视频可交付或财务闭合；`X-Molin-Request-ID`保持原业务请求。

## 状态与事实

- submitted/processing：查询原任务，复用原G4/G5状态推进与成本观察校验。
- pending_reconcile且有可信原Provider绑定：允许Query并追加观察，Task保持pending_reconcile，不回退fetching或processing，不因成功回执直接交付。
- Query期间发生真实终态竞争：不能覆盖相反终态；矛盾观察保留原G5冲突事实。
- 没有可信绑定、未提交或真实不适用状态：409，不猜测ID、新建UUID或假称完成查询。

归档重试仍是独立未完成接口。它不能把pending_reconcile简单回退为fetching；需在原Task下维护可证明的技术恢复进度，完成媒体与安全事实后遵循G3终态矩阵。财务补偿不获得Provider或抓取能力。

## 开发与迁移

- `service/video_admin_poll.go`：持久化管理命令、只查询依赖、前后鉴权/审计、原G5应用及未知善后。
- `service/video_admin_recovery_metadata.go`：原任务专用低敏恢复读取，私有task绑定，不能用于Submit。
- `service/video_g4_repository_ledger.go`：仅私有恢复实例使用该读取；普通生成/Worker读取与G5财务模式不变。
- `video/gateway.go`：原Poll拆分“Query／应用观察”，新增ApplyPolledResult支持待核对追加事实。
- `000098_video_admin_poll_commands.up.sql`：running/version1→completed或unknown/version2；原归属/绑定/原因/前审计不可变，完成后UPDATE/DELETE拒绝。仅增加操作命令，不建立平行任务或财务账本。

原因AAD使用独立管理poll领域，不能借用取消或隔离信封。普通响应及日志不保存Provider正文、Key、Prompt或Base64。

## 验证与未完成项

默认关闭真实HTTP先404红例，注册后503通过。首版真实MySQL/HTTP测试覆盖T2V/I2V、原用户和Key停用、一次Query和同键重放、Query超时后待核对、pending继续查询不回退、每个命令两份审计及Submit不增加；与解除隔离新增精确断言一同运行。

终审整改已补100同键并发、最终命令COMMIT确认丢失、Query后管理员撤权和MFA入口过期：并发只形成一条命令和一次Provider Query；提交未知重放读取原completed回执；Query后撤权不应用观察，只将命令善后为unknown；MFA过期时命令、审计和Query均为0。不同管理员超时接管、专用AAD/SQL篡改及完整归档竞争继续随最终全量和独立复核验证。

本轮缺陷台账与执行边界：

| 缺陷 | 等级 | 处置与证据 |
|---|---|---|
| G6-ADMIN-POLL-001 | P2 | 待核对分支曾跳过携带有效确认的ErrProviderExplicitFailure；现与原Poll一致豁免并保存确认/冲突，不回退状态。静态复核通过，明确失败反例待动态结果 |
| G6-ADMIN-POLL-002 | P2 | 原G5嵌套保存点仍可能自行重试失效事务；管理应用使用私有外层事务上下文标记，内部只执行一次，原驱动错误交还最外层。Query在重试闭包外；静态复核通过，真实1213/1205仍待动态验证 |
| G6-ADMIN-POLL-003 | P2 | 过期命令善后后使用旧running内存值；现重新读取真实回执并复验审计/权限。静态复核通过，过期与DB故障矩阵待验证 |
| G6-ADMIN-POLL-TEST-001 | P2 | 61365夹具幂等键长度不足16导致400；只修正测试键，不改变接口校验 |
| G6-ADMIN-POLL-TEST-002 | P2 | 69395已成功推进processing，但快照把G3规定的Request执行态/版本/更新时间同步当作财务变化；现精确校验目标Request粗态running及version+1，只归一化这三字段，其余所有请求、钱包、Hold、Usage、Quote与Outbox逐字不变 |

61365和69395两批均为FAIL，schema98及其余六项通过，不用于宣称轮询HTTP通过。两批均实际通过解除隔离精确字段/四审计及上一批隔离增强断言，回执须绑定各自复制树SHA256。额外处理了Query期间Worker已进入归档时的晚到观察，避免技术阶段前进吞掉确认；该竞争仍待单独反例，不以普通轮询测试替代。

最新整改批11项全部RUN/PASS、无SKIP，schema109及Linux race通过；复制树SHA256为`75f39446419360d20144ba3e5ab4348cafe7924c7d946fcb31ef2798678a2a97`。同时回归输出隔离/解除、queued Provider观察及Poll/Callback/Cancel竞争。该切片仍不替代最终SOURCE_STATE、Chat/Image/G0—G5兼容或四轴验收。

## 回滚

关闭管理轮询依赖和路由，保留已提交命令、原任务、Provider观察、Usage和审计。down保留结构与事实，不重放Provider调用，不回退执行状态或释放未知资金/输入租约，不部署共享环境，不进入G7。
