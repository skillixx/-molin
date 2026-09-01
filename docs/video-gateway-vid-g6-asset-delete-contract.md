# VID-G6 平台视频资产删除（开发中）

## 功能与删除范围

`DELETE /api/token/video-assets/{asset_id}`允许当前归属用户删除临时视频媒体。支持JWT和Project SK，精确隔离原用户、Project与来源Key；不删除原任务、报价、生成账单、存储容量或审计事实，不请求Provider。

| 选定资产 | 实际删除范围 |
|---|---|
| content根 | 根与四类普通交付派生物，保留审核副本 |
| cover、preview、thumbnail、普通derived | 仅该子资产，不删除父或兄弟 |
| moderation_copy、安全用途派生物 | 普通用户不可删除，返回404 |
| 已独立保存的长期副本 | 不属于本接口范围，保留并按原资格读取 |

粒度依据综合多模态规划的父删子联动和G3独立资产树；G6原文未逐字规定子资产细节，该细化已由产品工程角色确认。不能把子资产ID作为整视频删除别名。

## 接口参考

必须携带当前Bearer、单值16—128字节Idempotency-Key和精确JSON `{"version_no": <当前资产版本>}`。不接受查询参数、大小写字段别名、重复字段、目标数组、bucket、object_key、URL或额外参数；版本是正整数，预留生命周期递增空间。根组与单子资产作用域不能复用同一键。

成功HTTP200，平台data固定8字段：asset_id、video_id、request_id、version_no、lifecycle_state、media_deleted、scope、idempotent。scope为video或asset，version_no为实际完成后的版本；原键重放使用原请求版本，不会重新选择对象。X-Molin-Request-ID仍为原业务请求。

越权或非公开角色404；非法正文400；运行/待对账409；保全、争议、隔离或证据冲突409；错误版本/命令范围409 `video_asset_delete_conflict`；存储删除或确认失败503 `video_media_delete_unavailable`。失败恢复必须沿原键、原版本和原计划，不提升到整组删除。

## 部分删除的查询含义

- 单资产删除意图接受后，兼容v1 retrieve/content返回404、list隐藏该Job。表示兼容交付不再完整，不表示根正文已经删除。
- 平台保留原execution_status=succeeded、billing_status=settled及三轴事实。新增media_deletion_pending表达未确认/失败删除；确认子资产删除后才有media_partially_deleted=true。
- 根正文未删除时原media_deleted保持false；整个根组完成删除后media_deleted=true，部分标记不再单独置true。
- can_deliver=false，不放宽G5完整六资产要求，不伪装成重新生成或新生成失败。
- 每个生命周期仍描述自身真实状态；deletion_status优先显示已有整组删除状态，否则显示选定子资产删除状态。
- 独立长期副本允许已证明的临时生命周期差异，但必须核对单删见证的归属、目标、hash、规格、版本和状态；不能直接忽略源资产安全信息。

## 开发结构

- `handler/video_asset_delete_handler.go`：严格JSON、CAS、平台错误与8字段响应。
- `service/video_asset_delete_service.go`：单删准备/执行、不可变见证和命令归属；根请求携带额外CAS约束进入原媒体删除流程。
- `service/video_media_delete_service.go`：整组删除复用原协调；计划的可选PreDeleted仅用于已验证的单删子资产，不再推进该子资产版本。
- `000092_video_asset_deletion`：原Asset/Task上的单删协调和平台命令，不是新的任务或财务账本。命令不可更新/删除；状态deleting→delete_failed→deleting→completed，按version CAS。
- Task详情、列表、请求查询和生命周期同步部分删除状态；v1查询与内容隐藏不可完整交付的Job。

所有目标均在原Task、G5财务与六资产锁内确定。单删、根删、保存和下载沿原锁序协调，不能在读取当前对象A后，删除计划中不同的对象B。

## 当前测试与缺陷

关闭态反例5006c1先复现404，注册后503通过。95955真实HTTP单删成功，但v1错误返回200；投影同步后46196的父/兄弟不变、根后续联动、长期副本可读及财务/容量不变通过。

P1目标绑定问题由独立审查发现：metadata摘要不包含存储位置，异常自洽plan可能校验A却删除B。46196实际复现不应发生的1次Delete。已同时补运行时显式ref/hash/size绑定与SQL计划/版本约束；52412写入侧反例转绿。23524增强5项通过，合法失败记录的读回篡改也被运行时拒绝，Delete增量为0。独立复核确认该P1局部关闭，不代表整个资产删除或G6通过。

执行`verify-video-gateway-vid-g6.sh`的`asset-delete`范围，只使用一次性MySQL、回环HTTP与Fake存储。完整Root/Leaf并发、JWT失效、I2V、每写点/COMMIT未知及全G6验收仍未完成，不能用局部绿色宣称已交付整个阶段。

## 回滚边界

本入口仅本地验证，未接bootstrap、未部署。回滚关闭相关读取、保存与删除入口，或使用理解单删见证和PreDeleted计划的兼容版本。down保留意图、对象摘要、版本和命令；不恢复被删除正文，不删除原财务/审计，不释放长期副本容量。

相关：[阶段合同](./video-gateway-vid-g6-http-project-sk-contract.md)、[长期副本读取](./video-gateway-vid-g6-saved-read-contract.md)。
