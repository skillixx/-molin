# VID-G6 输出解除隔离双人审批（实现与验证中）

## 功能和授权边界

`POST /api/admin/token/video-assets/{asset_id}/release`只处理输出视频资产。当前默认关闭、未装配bootstrap、未部署，不新增输入解除隔离接口。

操作者必须有当前管理员JWT、手机/邮箱双MFA和`ai_gateway:safety_review`。解除需要两名不同管理员分别发起真实认证请求；不接受客户端maker_id/checker_id，不由system批准。测试仅使用两个合成管理员，外部系统仍为Fake。

## 两步调用

每步必须有独立的单值`Idempotency-Key`，正文为UTF-8 JSON，拒绝重复或未知字段、query、压缩正文及伪造身份。

发起申请：

```json
{"action":"request","reason":"申请复核行政隔离","version_no":12}
```

HTTP202返回公开approval_id。申请不改变原隔离状态或版本。冻结目标、原隔离命令、资产版本、安全快照和加密原因，操作审批15分钟有效；这是审批时效，不是媒体保留期限，不延长原expires_at。

另一管理员复核执行：

```json
{"action":"approve","approval_id":"vapproval_公开审批号","reason":"独立复核确认","version_no":12}
```

只有独立有效复核才能HTTP200返回released。复核者、发起者当前用户/权限/MFA均复验；发起者原JWT不会被伪造重用。操作者本人JWT仍逐请求复验。过期申请、同人审批、目标/版本漂移、已被另一命令消费均失败关闭。

响应固定九字段：approval_id、asset_id、video_id、request_id、status、restore_state、version_no、expires_at、idempotent。status是审批命令pending/released/expired，不是公开Video Job状态；released重放只表示原解除已执行，不承诺当前仍可交付。restore_state是原恢复目标，不代替当前媒体安全门禁。expires_at为审批期限。原业务请求写入X-Molin-Request-ID，原因、密文、内部身份和对象位置不返回。

## 恢复规则

只有原行政隔离的可信completed凭据能够发起。原available可恢复available；原temporary只恢复temporary，不能提前交付。原rejected/error或标识failed不能由批准改为passed/applied。保全、争议、删除、过期或已经接受的删除计划阻止解除；不能清除legal hold、延长expires_at或重新创建媒体。

只恢复指定资产，不改兄弟资产、hash、规格、审核、双标识、位置、Task/Request三轴或财务。仍有其他角色隔离时整体交付继续被拒绝，但允许各角色分别复核，避免相互等待。长期副本、临时读取和新签名仍执行原安全/结算/归属门禁。

## 开发结构

- `service/video_admin_output_release.go`：Task/Request优先加锁，原资产锁、不可变申请、独立复核、CAS和前后审计。
- `handler/video_admin_output_release_handler.go`：严格request/approve联合字段及HTTP响应。
- `service/video_admin_reason.go`：解除专用AAD领域，隔离/输入/任务信封不能跨用途解密；复核者与发起者原因分别加密。
- `000097_video_output_release_approval.up.sql`：不可变`ai_video_output_release_requests`和`ai_video_output_release_executions`。后者prepared/version1→completed/version2，仅一次消费原quarantine_id。

复核事务内顺序为前审计→独立复核prepared→原状态恢复及清隔离指针→后审计→completed→末尾授权，任一步失败整体回滚。SQL恢复分支必须找到独立复核者、精确原隔离/资产/版本/快照及有效期限；保留000096普通隔离守卫，不能裸UPDATE清指针。

原隔离命令始终保留，可在新版本上再次行政隔离，新隔离不能借用旧解除凭据。两张新表不包含Prompt或Provider正文，不创建钱包、Usage、Outbox或平行资产。

## 验证与当前缺口

默认关闭真实HTTP测试先出现404红例，注册后503通过。真实双人HTTP/MySQL首版验证包含申请不解除、申请重放、同人拒绝、伪造checker字段拒绝、独立复核、完成重放、只修改原版本及生命周期、六资产安全事实/财务/Provider/Store不变。运行结果必须以隔离专项实际输出为准，本文件不签PASS。

仍需补齐两角色逐一恢复、temporary不提前交付、原审核拒绝、保全/争议/到期、版本和100并发竞争、审批/审计/提交故障、时效与撤权、再次隔离后的历史重放、SQL旁路及密文完整性矩阵。与上批输出隔离补强断言一起在受影响范围验证，不以原生MySQL SKIP或旧源码绿色证明新代码。

本轮缺陷台账：

| 缺陷 | 等级 | 处置与证据边界 |
|---|---|---|
| G6-ADMIN-RELEASE-001 | P1 | 仅用maker用户ID调用JWT鉴权导致合法复核失败；新增私有持久用户/IAM/MFA资格复验，当前HTTP操作者完整JWT门禁不变。独立静态复核通过，实际双人HTTP待运行结果 |
| G6-ADMIN-RELEASE-002 | P2 | 审批/媒体/发起者资格期限在最后checker查询之前检查，等待可能跨期；现所有查询后统一检查，并区分权限到期40003与MFA到期40031。自然跨maker权限期限及事务回滚用例已加入；完整期限矩阵待验证 |
| G6-ADMIN-RELEASE-TEST-001 | P2 | 初始隔离夹具未携带认证得到的credential，9508在进入解除流程前401；现由真实JWT.Authenticate取得绑定身份，不放宽服务鉴权 |

9508整轮为FAIL：schema97通过；解除HTTP未进入审批，其他六项通过，其中上一批输出隔离的额度全快照和数据库读回跨期屏障实际通过。此结果不能证明解除隔离已通过。随后修复批与新增断言的来源和覆盖分开记录；新增精确九字段/审批与四审计全行重放断言及MFA错误分类不借用更早副本的绿色。

97944修复批8项全部RUN/PASS、无SKIP，schema97和Linux race通过；service 40.679秒、root 1.093秒、解除HTTP 8.13秒。复制树SHA256为`3f7082a3ef73f3dcac9056d4274fd474e045f7adc29c9d9a7709cc1d36dc200e`。覆盖真实双人申请/复核、同人/伪造身份拒绝、maker权限在最后checker读取期间到期的整事务回滚及原隔离/保存兼容。之后增加的精确字段/审批审计快照和maker MFA错误分类仍待数据库复验，不标完整接口或G6通过。

隔离到期清理是独立待完成路径：release拒绝过期恢复不等于无限保留，后续仍须遵守G0既有保留政策。down只保留结构和事实；应用回退关闭入口，不恢复旧指针、不撤销已完成业务事实，不进入G7。

## 终审整改结果

最新release整改批全部RUN/PASS、无SKIP，schema109/Linux race通过，复制树SHA256为`295f5a796da0dd38af1c369556c11d8f57073306f7a2b56349e4a24c64f839a2`。在原双人HTTP、maker末尾期限和审批审计矩阵上，新增100个checker同命令并发、checker MFA过期及最终execution事务COMMIT确认丢失；实际执行或确认丢失合计一次，申请、执行、资产CAS和四份审计保持唯一，HTTP重放只读取原released回执。上文“尚未完成”的历史描述由本节取代，但该切片仍须进入最终SOURCE_STATE和四轴验收。
