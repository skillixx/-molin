# VID-G6 管理员输出资产隔离（开发与验证中）

## 功能与调用合同

`POST /api/admin/token/video-assets/{asset_id}/quarantine`供具有当前管理员JWT、手机/邮箱双MFA和`ai_gateway:safety_review`权限的管理员隔离指定视频资产。仅本地显式装配，默认关闭；缺少专用原因加密器返回503。目标用户或原Key已停用不妨碍管理处置，但必须证明原Task、Request、Project、Key与资产归属。

沿用管理写入的严格JSON解析，正文仅允许`reason`和原资产`version_no`，必须有单值`Idempotency-Key`。拒绝未知/重复字段、非法原始UTF-8、query及压缩正文。reason去首尾空白后1—256字符、不超过1024字节、无控制字符。客户端不能指定bucket、object_key、审核结论、操作者或审批者。

只允许指定资产的temporary或available变为quarantined，资产version_no递增1。所有六种角色均可处置，包含审核副本；不批量修改兄弟资产。任一角色隔离后，原六资产交付门禁阻断整条视频的临时内容、短效下载、保存及长期副本读取。既有下载签名不代表当前仍可访问。

已有任务级或单资产删除计划时，新隔离返回409，避免与已接受的清理计划交叉。本实现采用任务级保守冲突边界，不能用新隔离覆盖已有删除事实。保全及争议可保持原状并增加隔离，不自动清除。

响应HTTP200，data为管理输出列表的原28字段加`idempotent`，共29字段；`X-Molin-Request-ID`为原业务请求。原审核/标识、规格、期限、父子关系、财务和Task三轴不改写，不返回原因、对象位置或内部行政凭据。错误沿用管理合同：401/40001认证，403/40003权限，403/40031双MFA，400/40000参数，415/40000媒体类型，404/40400资产不存在，409/40900冲突，503/50300依赖或持久事实异常。

同管理员同key冻结目标、初始版本和规范化原因。重放先验证专用密文及原前后审计，再复验当前管理员权限。prepared不是完成事实，必须失败关闭；重放不重复改变资产或建立审计，不因密钥不可用误报异意图。

## 实现与事务

- `service/video_admin_output_quarantine.go`：先锁Task/Request，再锁资产、命令；单事务执行前审计、prepared命令、资产CAS、后审计、completed命令及最终授权。
- `handler/video_admin_output_quarantine_handler.go`：默认关闭、严格解析和低敏响应。
- `service/video_admin_reason.go`：输出隔离专用AAD领域；Task/Input/Output身份互斥，旧任务和输入AAD不变。
- `000096_video_admin_output_quarantine.up.sql`：增加`ai_video_admin_output_quarantines`及原资产上的可空`admin_quarantine_command_id`，不建立平行资产或财务账本。

原成功审核不可伪造为rejected。000096保留原隔离CHECK的其他分支，仅为有真实prepared命令的video资产增加窄例外；新增约束要求非空指针仅用于隔离视频。激活指针时比较OLD/NEW完整安全、归属、位置、期限与保全快照，允许变化的只有生命周期、版本、更新时间和指针。原列摘要由MySQL统一计算，只持久化hash，避免不同JSON规范化形成假相等。

命令完成触发器核对原资产指针、版本、实际快照和后审计。completed回执禁止UPDATE/DELETE；不能插入自带指针的新资产，也不能清空、替换指针或用通用生命周期更新恢复available。原因只保存AES-GCM信封、HMAC、长度和摘要；不访问Provider、存储正文或钱包。

保存重放仍可在临时正文正常删除后访问独立长期副本，但必须先复查全部六资产当前隔离、legal hold和争议状态。文件hash和原审核标识不能替代行政保护状态；拒绝发生在存储Head之前。

## 验证和缺陷台账

专项`admin-output-quarantine`包含11个必选顶层测试：原因加密/三个用途、默认关闭、输出列表、隔离HTTP、生命周期、短效下载、保存、长期读取及媒体删除兼容。外部Provider/对象存储为Fake，核心HTTP、认证、MySQL和事务真实执行。

93502运行：schema96通过，复制树SHA256为`ac635bf15f54a117229af2705d158cb2d6a25f9345e9b58d86590580cd9cfc25`；11项中10项通过，输出隔离用例失败。不能把该批标为PASS。

| 缺陷 | 等级 | 原因与处置 | 当前证据 |
|---|---|---|---|
| G6-ADMIN-OUTPUT-001 | P1 | 保存已完成重放只比媒体摘要，遗漏当前隔离/保全/争议；已增加六资产门禁，同键与新键都拒绝且零Head | 93502真实失败；69856原反例通过，完整保护组合与独立关闭待补 |
| G6-ADMIN-OUTPUT-002 | P2 | 最终authorize后仍读取详情，数据库等待可能跨越权限期限；已将详情移到最后鉴权之前 | 独立Standards发现；69856写后详情跨权限期限及整事务回滚通过，进一步屏障证据待复验 |

69856修复批11项全部RUN/PASS、无SKIP，service为60.783秒、root为1.105秒，隔离HTTP为10.37秒，schema96与Linux race通过；复制树SHA256为`ebbc576a4ba52955e789f6257e9d791df9ae0bde0349b44a5d2c0f8d2d9b1efe`。该副本不包含随后按QA建议增加的保存/额度完整快照、数据库读回期限及有效→到期屏障断言；新断言标为NOT_RUN并入下一批，不借旧绿色冒充新断言已经通过。

尚未完成：六角色/全状态/100并发及清理竞争完整矩阵，SQL多种篡改与故障注入，JWT/MFA末尾时效，解除隔离及隔离清理证明链。当前局部结果不替代完整G6、独立QA/PM/Standards/Spec或最终精确源码CI。

## 终审整改结果

最新quarantine整改批全部RUN/PASS、无SKIP，schema109/Linux race通过，复制树SHA256为`2cea5ec582ce3317c01833d3599c183b5fc8bf27375826a4f856e70549133723`。在原六角色、权限末尾到期、SQL守卫和读/存/删保护矩阵上，新增100同键并发、管理员MFA过期及completed命令COMMIT确认丢失；实际执行或确认丢失合计一次，资产CAS、命令和两份审计唯一，HTTP重放只读取原quarantined事实。上文历史“尚未完成”由本节取代，但该切片仍须进入最终SOURCE_STATE和四轴验收。

## 保留与回滚边界

000097及[双人解除接口](./video-gateway-vid-g6-admin-output-release-contract.md)正在接入独立maker/checker的窄恢复分支，完整矩阵未验收；隔离到期清理仍未实现，不能将此限制解释为新的无限保留政策。后续必须按既有G0保留期限、legal hold和争议优先级实现清理证明，不得由system自动批准解除，也不得先恢复available再清理。

down只保留结构、资产和行政审计事实。应用回滚关闭接口，不自动解除隔离、删除媒体、退款或释放租约；不部署共享环境，不进入G7。

相关：[完整G6合同](./video-gateway-vid-g6-http-project-sk-contract.md)、[管理查询](./video-gateway-vid-g6-admin-read-contract.md)、[输入隔离](./video-gateway-vid-g6-admin-input-quarantine-contract.md)。
