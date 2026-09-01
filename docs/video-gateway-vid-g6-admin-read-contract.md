# VID-G6 管理员任务、资产与运行汇总查询（开发与验证中）

## 功能与使用范围

`GET /api/admin/token/video-tasks/{task_id}`为持有`ai_gateway:view`管理权限、当前登录JWT及有效手机/邮箱双重认证的主体提供低敏任务详情。身份规则沿用既有图片网关管理链，不额外要求直接admin角色，不接受Project SK作为管理员认证。

当前管理面局部实现五条只读入口（任务详情、任务列表、输入资产列表、输出资产列表、对账运行汇总），另有[管理员取消](./video-gateway-vid-g6-admin-cancel-contract.md)和[输入隔离](./video-gateway-vid-g6-admin-input-quarantine-contract.md)写入口局部实现。均独立注册、默认关闭，未接bootstrap或产品前端。完整G6还有其余5条管理写路由及其他验收任务；不能用局部测试代表整个管理面完成。

管理员查看的是目标用户/Project/Key关联的原Task、Quote及财务事实，不借用目标用户JWT或把管理员自己的Project当目标归属。目标用户或Key停用不应隐藏已有管理事实。查询不发Provider请求、不访问对象正文、不创建审计审批、不触发对账恢复或改钱包。

## 接口参考

- task_id为原Task公开ID，1—128字符，沿用视频公开ID规则；不接受query或客户端指定目标user_id。
- JWT须是单值Bearer，拒绝Project SK。当前账户状态、Token吊销/期限、管理权限及双MFA均验证，查询事务末尾再次复验。
- 成功HTTP200，标准平台Envelope。data为原25字段`VideoTaskDetails`加user_id、project_id、api_key_id共28字段，api_key_id允许null。
- 金额为原八位Decimal字符串或null，保留原三轴。can_deliver描述原G5财务/安全交付事实，不授予管理员下载或代用户使用媒体的权限。
- 不返回Prompt、密文载荷、Provider正文、Provider任务ID、原始Key或bucket/object_key。
- X-Request-ID为HTTP追踪，X-Molin-Request-ID为原业务request_id。

| 情形 | HTTP / code |
|---|---|
| 缺少、无效JWT或使用SK | 401 / 40001 |
| 缺管理权限或显式deny | 403 / 40003 |
| 手机/邮箱MFA缺失、过期或未来异常 | 403 / 40031 |
| 无效query | 400 / 40000 |
| 任务未知或目标关联不完整 | 404 / 40400 |
| 关闭、缺依赖或查询故障 | 503 / 50300 |

## 开发结构与安全边界

### 管理员任务列表

`GET /api/admin/token/video-tasks`与详情复用当前JWT、`ai_gateway:view`及双MFA。成功返回HTTP200，`data={items,page,page_size,total}`，每项与详情相同28字段。不设置单一业务`X-Molin-Request-ID`，不生成下载权限。

允许查询参数：`page`默认1、范围1—10000；`page_size`默认20、范围1—100；`user_id`和`project_id`为非零uint64；`status`为原执行轴状态；`operation`只允许`text_to_video`和`image_to_video`；`model`遵循`^[A-Za-z0-9][A-Za-z0-9/._:-]{0,190}$`。所有条件AND组合，未知、重复、空参数以及非规范十进制均400/40000。不接受公开Job映射状态`completed`或`in_progress`。

展示按`created_at DESC,public_id DESC`稳定排序，空页保留准确total。计数及页内ID来自同一SQL语句，再按公开Task ID字典升序获取任务锁。状态过滤与加锁后的当前状态不同会回滚并整页重试，最多三次、整个操作30秒；耗尽返回503，不丢弃条目后返回旧total。跨用户钱包锁序仍在专项验证，见缺陷台账。

### 管理员输入资产列表

`GET /api/admin/token/video-input-assets`使用同一管理认证链，成功HTTP200及D-95，空数组不可用null代替。允许`page/page_size/user_id/project_id`（约束同上），以及：

- `lifecycle_state`：pending、normalizing、moderating、ready、rejected、quarantined、pending_delete、expiring、deleting、deleted、delete_failed。
- `source_type`：platform_presigned、openai_inline_multipart、gateway_asset_snapshot。
- `moderation_status`：pending、passed、rejected、error；不是failed。

每项固定21字段，未发生或尚未产生的可空字段保持null：

| 字段 | 类型及含义 |
|---|---|
| input_asset_id | 输入公开字符串ID |
| user_id / project_id | 原归属uint64 |
| api_key_id | 原来源Key的uint64或null；证明为JWT来源才是null |
| source_type | 原来源枚举 |
| upload_session_id / source_asset_id | 来源公开字符串ID，二选一，另一项null |
| lifecycle_state / version_no | 原生命周期字符串及CAS版本uint64 |
| mime_type / size_bytes / width / height | string/uint64/uint32/uint32，可空规范化规格 |
| moderation_status / moderation_policy_version | 审核状态字符串及可空版本字符串 |
| expires_at / created_at | 原时间事实 |
| legal_hold | bool保全标记，不包含案件详情 |
| delete_requested_at / pending_delete_at / deleted_at | 原可空删除时间事实 |

不返回`can_reference`、Prompt、hash、Base64、Provider正文、bucket/object_key、签名地址或长期访问能力。管理员查询不代替生成准入。停用目标用户/Key及历史删除、隔离记录仍可查询。

上传来源必须是同用户同Project的原会话，类型和purpose一致，已有最终输入绑定不得指向别的输入。图片来源必须回溯同归属原图片Asset→Task→Request，Key、模型、模态及能力一致；不读取任意后续视频Task猜测来源。非空Key仍需对应原用户及Project。链路损坏或数据库故障返回503/50300，不返回部分items、不冒充JWT-null、不伪装空页。

`service/video_admin_input_list.go`在RR事务的同一一致性快照读取计数、条目和来源，不获取媒体使用权限、不访问Store；前后管理员授权使用当前读。展示按`created_at DESC,public_id DESC`。无新增表或migration。

### 管理员输出资产列表

`GET /api/admin/token/video-assets`复用`ai_gateway:view`、JWT及双MFA，返回HTTP200及D-95。允许`page/page_size/user_id/project_id`（范围同任务列表）、`model/operation`（同任务列表）、`role`、`lifecycle_state`、`moderation_status`、`dispute_status`；条件严格AND，重复、未知、空参数400/40000。

- role：content、cover、preview、thumbnail、moderation_copy、derived。
- lifecycle_state：temporary、available、quarantined、expiring、deleting、deleted、delete_failed。
- moderation_status：pending、passed、rejected、error。
- dispute_status：none、open、resolved。

每项固定28字段：

| 字段 | 类型及含义 |
|---|---|
| asset_id / video_id / request_id | 原资产、Task及Request公开字符串ID |
| user_id / project_id / api_key_id | 原归属uint64；已证明的JWT任务Key为null |
| model / operation / role | 原模型、T2V/I2V操作及六角色字符串 |
| parent_asset_id | 同任务同归属content根的公开ID；根为null |
| lifecycle_state / version_no | 原生命周期及uint64 CAS版本 |
| mime_type / size_bytes / width / height | string/uint64/uint32/uint32，未形成规格为null |
| moderation_status / moderation_policy_version | 状态字符串与可空审核版本 |
| explicit_label_status / explicit_label_version | 显式标识状态与可空版本 |
| implicit_label_status / implicit_label_version | 隐式标识状态与可空版本 |
| legal_hold / dispute_status | bool保全与争议状态，不返回案件材料 |
| expires_at / created_at / deleted_at / media_deleted_at | 原时间事实，后两项可空且不得互相代填 |

审核副本和安全derived元数据可见，不返回正文、对象位置、hash、签名地址、Prompt、Provider信息或`can_download`。同一RR快照读取计数、资产及原Task/Request/Key/父关系，管理员权限前后当前读；不锁钱包、不运行逐任务对账或外部存储查询。原关联损坏返回整页503/data:null，不能部分展示或把非空丢失Key当JWT。

代码位于`service/video_admin_output_list.go`与`handler/video_admin_output_handler.go`。36784已实际通过输出HTTP测试（4.99秒）及关闭态测试：六角色、精确28键、原归属与公开父ID、严格过滤、经原删除协调器删除后保留五个普通资产历史及审核副本、主体停用仍可查、MFA拒绝和资产/财务不变。错父关系、错Key、读取故障、JWT-null及完整隔离/争议矩阵尚需补齐，不能签全入口最终验收。

### 视频对账运行汇总

`GET /api/admin/token/video-reconciliation/summary`沿用既有图片summary的非列表语义，要求独立权限`ai_gateway:reconcile_manage`、JWT及双MFA；仅有view权限不得访问。拒绝所有query和请求正文。成功HTTP200，data固定六字段，不是D-95：

| 字段 | 类型与事实范围 |
|---|---|
| settlement_pending | int64，视频能力请求中待结算数量 |
| active_compensations | int64，video_reconcile的pending/running/retry/manual_review数量 |
| dead_compensations | int64，video_reconcile的dead数量 |
| outbox_pending | int64，video_request的pending/publishing数量 |
| outbox_dead | int64，video_request的dead数量 |
| unreleased_hold_amount | CNY八位Decimal字符串，关联视频请求且仍holding的原Hold金额合计 |

空范围返回五个0及`"0.00000000"`，但不增加passed/zero_difference，不声明商业或逐Task对账通过。状态积压、金额汇总与原G5十七组逐任务核查是不同证据，本接口不提供逐项诊断，不运行ReconcileExecution、Worker、Provider、媒体读取、settle/release或Outbox派发。

`service/video_admin_summary.go`在同一RR快照查询六项并前后复验管理员；已预占请求Link/Hold缺失、钱包归属/币种或Hold金额不一致、重复Hold关联、未知Hold状态及读取故障均503/data:null，不用COALESCE掩盖关联损坏。`handler/video_admin_summary_handler.go`负责无参数/正文约束及平台Envelope。未新增表或migration。

8118真实复现未知Hold状态应503却返回200；白名单修复后15782全部14项管理专项通过，service52.437秒，summary3.38秒。该修复仅证明未知状态被拒绝，不是合法终态金额形态或G5全部财务审计。独立QA建议的“一行污染、读回unknown、拒绝前后异常财务不变”断言已补充，等待扩大回归，不能用之前结果覆盖新增断言。

### 共用实现补充

`video_admin_route.go`显式装配路由，`handler/video_admin_handler.go`严格解析登录凭据及输出，`service/video_admin_service.go`在同一RC事务中校验管理员、锁定目标Task/Request并复用`taskDetailsTx`读取原G5金额和只读对账。无新表或migration。

管理员MFA使用真实`AuthService.CheckAdminVerified`，读取现有users.admin_phone_verified_at/admin_email_verified_at。有效期显式从配置传入，测试为24小时；0仍保留原“不过期”定义，负值和时长溢出拒绝。旧`IsAdminVerified`继续作为bool兼容包装，数据库错误仍返回false；新视频管理入口保留error并返回503，不诱导用户重新MFA。

JWT内存凭据绑定原claims.UserID，验证时必须等于VideoCaller.UserID。服务不能把普通用户JWT的有效性证明借给另一个管理员ID；不保存JWT原文。查询使用干净GORM事务基准，避免用户SELECT/WHERE污染后续IAM和MFA仓储。

## 已验证与剩余

18312先复现查询条件串入导致错误503；21420复现服务层主体错绑和MFA读取故障误报403。修复后17505基础MySQL通过；按既有管理认证错误码对齐40001后，65790四项专项、schema93与Linux race通过，service5.887秒。

测试实际覆盖：普通JWT/SK拒绝、当前权限和deny、双MFA有效/单侧缺失/过期/未来、真实reserved与succeeded任务、禁用目标仍可查看、精确28字段、Task/事件/回调/补偿/租约/财务整行不变、零Store/Provider调用、主体替换拒绝、MFA SQL故障503及旧bool兼容。测试只写合成MFA事实，不发送短信或邮件。

仍须补齐管理员末尾撤权、JWT吊销、MFA/权限期限跨期、更完整数据故障与关联损坏矩阵，以及全部管理接口、SDK和全阶段独立验收。当前源码尚未提交，不是生产或商业验收。

任务列表24610真实MySQL先复现“reserved筛选返回已取消项”，13255及3201加入整页重试后该反例通过。13255的跨钱包测试被合成政策唯一约束阻断，3201被测试幂等键短于16字符阻断，均不算死锁复现或关闭证据；已修复夹具并继续验证。输入列表关闭态由404红例改为503，本地编译及全量Go测试/vet/mod校验通过，但本地SQL SKIP不代表集成通过。最新运行见[增量证据](./evidence/video-gateway-vid-g6-admin-list-progress.json)。

独立QA已只读检查任务竞争测试和输入来源链，尚未为新增列表签署验收。输入列表还须补真实JWT-null、来源到期/删除后的历史查询、未规范化规格null、两连接快照与关联损坏/故障矩阵。

## 本地验证方式

在视频独立工作区执行；脚本只创建带本Goal标记的临时隔离MySQL/Go容器，禁止替换为共享数据库连接。依赖锁定版本、schema及逐测试RUN/PASS/SKIP由脚本检查。

```powershell
$env:VIDEO_GATEWAY_G6_ISOLATED_MYSQL_APPROVED='YES'
$env:VIDEO_GATEWAY_G6_TEST_FOCUS='admin-read'
& 'C:\Program Files\Git\bin\bash.exe' infra/scripts/verify-video-gateway-vid-g6.sh
```

必须同时检查进程退出码0、所有必测项RUN/PASS、无SKIP及最终范围PASS；单个测试绿色不能掩盖另一测试失败。默认关闭503是预期保护，不应通过启用Fake生产降级来消除。来源不一致503应排查原关联事实，不能将其强制改为null或删除历史记录。

## 回滚边界

关闭管理路由装配即可退回未开放状态，不删除业务或MFA事实。保留JWT主体绑定及Auth bool兼容包装；回滚不能移除安全修复后重新允许主体替换。该入口没有业务写入或媒体正文，不能用“回滚查询”改变原任务、钱包或审批记录。

相关：[G6完整合同](./video-gateway-vid-g6-http-project-sk-contract.md)、[API总表](./full-api-design.md)、[测试计划](./test-plan.md)。
