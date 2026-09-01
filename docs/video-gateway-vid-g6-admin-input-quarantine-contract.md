# VID-G6 管理员输入隔离（开发与验证中）

## 功能与使用边界

`POST /api/admin/token/video-input-assets/{input_asset_id}/quarantine`供持有`ai_gateway:safety_review`、当前JWT和有效手机/邮箱双MFA的管理员隔离原输入资产。目标用户、Key或来源图片已停用/到期，不影响有权管理员处置历史输入，但原来源归属必须可证明。

默认关闭，仅本地测试显式装配；缺少专用原因加密器返回503。此操作不调用Provider、不读取或删除正文、不改变任务或钱包、不释放TaskInput执行租约，不需要maker/checker。它不提供解除隔离权限。

## 接口参考

单值16—128字节`Idempotency-Key`，UTF-8 `application/json`，正文上限4KiB，仅允许：

```json
{"reason":"待完成安全复核","version_no":1}
```

与管理员取消共用严格解析器：拒绝query、Content-Encoding、未知参数、重复字段和非法原始UTF-8。reason去首尾空白后1—256字符、最多1024字节且无控制字符。version_no为原InputAsset版本，正整数且不超过uint64最大值减8。客户端不能提交owner、原审核结论、对象位置或checker。

允许原状态：pending、normalizing、moderating、ready。成功仅将lifecycle_state变为quarantined、version_no递增1及更新必要时间。已隔离的新命令、rejected、pending_delete、expiring、deleting、deleted、delete_failed均409，不扩展既有状态矩阵。

同管理员同key绑定原输入、版本和规范化原因。重放重新鉴权并验证原密文/审计，不重复隔离；后续合法状态变化只能展示当前元数据，不能由旧命令把它改回隔离。异目标、原因或初始版本409。

成功HTTP200，data为[管理输入列表](./video-gateway-vid-g6-admin-read-contract.md)固定21字段，加`idempotent`共22字段。仍保留原审核状态及版本、归属、来源公开ID、期限、保全和删除时间。共享输入不对应唯一业务任务，不设置单一`X-Molin-Request-ID`。不返回原因、hash、对象位置、正文或使用许可。

错误沿用管理合同：401/40001认证，403/40003权限，403/40031双MFA，400/40000正文，415/40000媒体类型，404/40400未知输入，409/40900版本/状态/幂等冲突，503/50300依赖、来源、密文或审计故障。

## 开发实现

- `handler/video_admin_input_quarantine_handler.go`：显式依赖与HTTP映射。
- `handler/video_admin_write_request.go`：取消/隔离共用原因和CAS解析，保留取消原有错误语义。
- `service/video_admin_input_quarantine.go`：当前管理员前后复验、输入锁、历史来源/Key证明、幂等及审计。
- `repository/video_input_repository.go`：复用原状态矩阵和CAS；管理专用方法只允许增加隔离。调用方必须先完成管理员权限及来源证明；不复用用户端“来源当前可生成”的资格过滤。
- `service/video_admin_reason.go`：输入原因与任务取消采用不同AAD领域，并要求TaskID/InputAssetID二选一。旧取消AAD字节格式不变。
- migration000095：`ai_video_admin_input_quarantines`引用原InputAsset、Key和前后audit_logs；保存原因AES-GCM信封、HMAC、版本及原生命周期。回执不可UPDATE/DELETE，不建立平行任务或财务账本。

前审计、生命周期CAS、后审计和回执INSERT在同一外层事务；任何失败整体回滚。只允许最外层按原数据库错误链重试。重放先验证信封，再比较原因HMAC，不能把不可用密钥误报成客户端异意图。

保全标记可以保持true并增加隔离，不能自动清除。原moderation_status/policy_version描述历史审核结论，不改写为新的拒绝。原hash、规格、来源、对象位置、expires_at及TaskInput不变。隔离发生时间由不可变回执created_at追溯，接口不擅自重置留存期限；后续按既定隔离保留策略处理。

## 验证与未完成边界

新增关闭态测试先404后503；原因AAD测试验证输入/任务同名也不能跨域解密。42546真实HTTP/MySQL输入隔离用例通过（6.94秒），覆盖ready输入、保全输入、权限/MFA/CAS/重放、原快照及租约/资金/正文不变、隔离后原Provider提交前失败关闭、目标停用及密文受控审阅。该批整体因I2V测试误用T2V金额失败；修正为原G5夹具0.15元/秒×5秒=0.75后，65318只重验I2V一项并通过，不改计费代码。不能据局部通过宣称完成全部输入隔离或G6验收。

仍须补齐三种处理中状态、全部禁止状态、上传来源及损坏来源、100并发、发布/提交竞争、前后审计和回执故障、提交未知、密钥变更、权限期限，以及隔离留存闭环。当前没有产品前端或共享环境部署。

本批只跑受影响的`admin-mutations`范围，包含本接口及共用解析/加密/输入仓储、相关取消矩阵；不为单个文档或断言修改重复跑全量。完成一组功能后再统一扩大回归，最终仍须精确源码验收。

## 回滚边界

关闭入口即可停止新隔离；000095 down保留原资产、原因密文、命令和审计，不自动解除隔离或删除媒体，不释放仍被任务保护的输入。恢复使用必须另经已授权流程，不能把代码回滚当成业务解除隔离。

相关：[G6完整合同](./video-gateway-vid-g6-http-project-sk-contract.md)、[管理员取消](./video-gateway-vid-g6-admin-cancel-contract.md)、[数据库设计](./database-schema-design.md)、[测试计划](./test-plan.md)。
