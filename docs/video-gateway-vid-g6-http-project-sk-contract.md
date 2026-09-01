# VID-G6：HTTP、Project SK、回调与查询合同

## 当前状态与功能范围

本阶段正在开发，尚未验收。基线为 `52563ba450c6d488456137162580022deb06acc8`，分支为 `feature/video-gateway-vid-g6-http-project-sk-contract`。G5 已合并为 PR #421，精确源码、Ready CI、独立验收与三处 SQL 尾空行规范化已重新核验，见[最终合并证据](./evidence/video-gateway-vid-g5-final-merge.json)。原 G5 回执保留历史时点，不改写其未提交状态。

当前完整缺口以[独立规格盘点](./evidence/video-gateway-vid-g6-current-spec-gap-audit.md)区分“缺实现”与“缺当前验收”。下文带运行编号的增量记录是历史检查点，旧的“清理、归档、调账未实现”等不能用于重复开发或推断当前阻塞；也不能把历史专项通过当作当前精确源码的完整验收。

目标使用者包括登录用户、持 Project SK 的 API 客户、管理员和内部回调接收器。完整范围以[阶段规划的 VID-G6 节](./video-gateway-goal-stage-execution-prompt.md#vid-g6httpproject-sk回调与查询合同)、[兼容快照](./video-gateway-openapi-snapshot-v1.yaml)及本次 Goal 为准。[需求追踪清单](./evidence/video-gateway-vid-g6-requirements.json)列出 43 个明确方法/路径及额外准入、目录、生命周期、兼容和交付门禁；不能将路由计数等同于完整验收。

没有新增产品前端页面。目前独立注册函数提供47个局部入口，新增第47条为[Project视频模型授权管理](./video-gateway-vid-g6-project-grant-contract.md)。仅供本地测试，未接入bootstrap。原43个明确路由之外，临时内容兑换1条、长期读取2条及Project授权1条也纳入完整测试与权限清单，不能漏验。I2V应用服务接入当前权利、ready参考图与G5报价/预占事务，缺少参考图读取依赖仍失败关闭。上传、平台SK图生及[v1 inline multipart图生](./video-gateway-vid-g6-inline-i2v-contract.md)已有局部真实MySQL测试；完整故障/清理矩阵和全阶段验收尚未完成。真实Provider、Key、钱包资金、调账、测试服务器、生产及G7均未操作。工程Git闭环已获本阶段一次性授权，但须完成全部独立验收和精确HEAD的Ready CI后合并。

## 开发参考：当前已实现边界

Project SK已接入[显式视频能力签发与轮换合同](./video-gateway-vid-g6-project-key-capability-contract.md)，旧Key/all模式默认关闭，事务内复验发布快照与Project grant。Project grant管理接口与[Key生命周期持久化幂等](./video-gateway-vid-g6-project-key-idempotency-contract.md)已形成局部真实MySQL纵向切片；完整COMMIT未知/写点故障及全阶段准入矩阵仍未闭合，不能据局部专项签完整准入链。

原模型管理URL新增可选[受控视频草稿命令](./video-gateway-vid-g6-model-draft-contract.md)，具备真实JWT/MFA、幂等、版本和加密审计事务，并已接入显式编辑视图与摘要绑定的历史接管；默认未装配。发布、回滚及完整管理矩阵仍待完成，不能把草稿保存或接管当模型已发布。

视频公开模型目录新增发布快照投影与当前Project SK视频准入，详见[目录功能与开发合同](./video-gateway-vid-g6-model-catalog-contract.md)。后台视频合同编辑/发布、Key显式配置和生成资源准入仍是完整阶段缺口；不能以公开字段已补齐替代这些功能。

模型管理基础新增共享七键解析器、原模型表JSON工作副本与快照校验；不是完整发布入口。独立产品核对确定采用专用native执行就绪校验，不能伪造Bifrost健康路由；剩余门禁见[模型管理开发文档](./video-gateway-vid-g6-model-management-development.md)。

资产保存已接入局部服务，工程合同见[保存合同](./video-gateway-vid-g6-asset-save-contract.md)。43262的12项专项已证明HTTP、不同幂等键100并发唯一用户资产/一次容量、四层容量拒绝、独立Store、部分复制恢复及财务整行不变。6710定向证明最终写入跨source/entitlement/JWT期限时整个完成事务回滚。未发布清理与实际COMMIT丢失确认已有局部证据；[长期读取合同](./video-gateway-vid-g6-saved-read-contract.md)正在补齐当前资格、共享名额与独立Store测试。完整保存/删除并发、发布各写点及COMMIT未知恢复、清理后新尝试尚未完成，不能据此签完整保存或G6验收。

以下路径以仓库根目录为起点。表内仅描述已有源码，不表示完整应用已装配。

| 入口 | 文件 | 合同 |
|---|---|---|
| `ServeVideoContent(w,r,content)` | `server/internal/modules/token_gateway/handler/video_content.go` | 传输已授权 MP4；不承担身份、财务或对象归属判定，不持有 Provider |
| `VideoHTTPContent` | 同上 | `Size int64` 为 1 至 256MiB；`SHA256` 为 64 位小写十六进制；`OpenRange` 接受 context、offset、length，返回私有内容 Reader |
| `IAMService.CheckPermissionFresh(ctx,userID,permission)` | `server/internal/modules/iam/service/permission_fresh.go` | 复用现有覆盖、角色和组权限仓储，逐次读库，无 Redis；返回 `(bool,error)`，错误不可按允许处理 |
| `Recovery` | `server/internal/middleware/recovery.go` | 普通异常仍返回原低敏 500；`http.ErrAbortHandler` 交给 net/http 中断连接，禁止向媒体追加 JSON |
| `VideoAccessService.Resolve/AuthorizeTx` | `server/internal/modules/token_gateway/service/video_access_service.go` | 真实身份、显式授权、发布快照、IAM、商品/资产/指定权益/会员校验；原事务内使用当前读观察撤权 |
| `VideoAccessService.AuthorizeSubjectTx` | 同上 | 不依赖模型的用户/Project/Key、实名及权限准入；空模型授权集合也不能跳过 |
| `ParseVideoModelContract` | `server/internal/modules/token_gateway/service/video_model_contract.go` | 七个必填键的版本化非商业配置；缺项、重复键、未知字段及矛盾配置拒绝 |
| `NewVideoHTTPService/Create/GetVideo` | `server/internal/modules/token_gateway/service/video_http_service.go` | 使用真实 G5 Quote/Hold/Task 协调器；当前 T2V 创建和原 Job 查询，未实现完整阶段 |
| `RegisterVideoUserRoutes` | `server/internal/modules/token_gateway/video_route.go` | 二十五个局部入口；未被 bootstrap 调用，不能视为部署开放 |
| `VideoHTTPService.GetContent` | `server/internal/modules/token_gateway/service/video_content_service.go` | 原任务鉴权、G5完整对账、六资产交付校验后生成逐片复验能力；仅ContentStore外部边界可Fake |
| `VideoUploadService` | `server/internal/modules/token_gateway/service/video_upload_service.go`、`video_upload_complete.go` | 创建、查询、封存完成、取消和私有规范化读取；对象IO在事务外，发布使用当前鉴权与版本围栏 |
| `VideoRightsService` | `server/internal/modules/token_gateway/service/video_rights_service.go` | 合成政策读取、所有者明确接受、追加式幂等回执和历史有效性判断；不代表生成事务权利校验已接入 |
| `VideoHTTPService.ListVideos` | `server/internal/modules/token_gateway/service/video_list_service.go` | 当前授权模型下按创建时间和公开ID稳定分页；同一快照生成Job，隐藏已删除媒体，不查询Provider |
| `VideoJWTAuthenticator.Authenticate` | `server/internal/modules/token_gateway/service/video_jwt_auth.go` | 既有JWT验签、users状态与吊销存储；缺依赖或存储错误拒绝 |

内容传输支持无 Range 的 200、单 Range 的 206、非法/多值/多范围/越界的 416。前缀、后缀、开尾范围按对象长度截取；416 带 `Content-Range: bytes */<size>`。合法单范围遇不匹配或弱 If-Range 时返回完整 200；语法非法与多范围仍拒绝。响应包含长度、ETag、Accept-Ranges、私有 no-store 和 nosniff。仅接受无 query 或 `variant=video`，未知、重复或损坏 query 返回兼容错误信封。

每次最多打开 1MiB；首片 OpenRange 失败返回低敏 503，已经开始发送后的短读、后续打开或 Close 失败中止连接。首片 Read 失败不保证仍能改写响应状态，客户端必须把正文截断当成失败。每片设置 30 秒写期限；读取取消/期限、下载带宽与用户/Project 并发还需在后续接入中补齐并验证。

实时 IAM 入口按显式 deny、显式 allow、角色与组权限、默认拒绝处理。任何仓储查询失败均返回错误，不使用已经读到的部分授权集合。G6应用另校验账号/Project/Key、实名、模型和明确配置的权益，并在G5原事务重放/写入时复验；完整预算、队列、上传及管理准入仍待实现。

### 显式授权与模型要求

`000078` 为 Key 新增默认 0 的 `video_generate_allowed`，为 Project 新增 `ai_project_model_capability_grants`，并补六个权限及 admin 映射。旧 Key、全模型模式和模型发布均不能自动生成视频授权。Key模型范围继续使用既有 `api_key_model_scopes`，不新建财务账本。该迁移仅在临时 MySQL 验证，未部署。

当前应用读取 `ai_model_release_versions.snapshot_json.video_contract`。其七键均必填：`schema_version=1`、`purpose=non_commercial_test_fixture`、`supported_operations`、`default_model`、`asset_required`、`required_entitlement_type`、`required_membership_levels`。后两者可显式为 null 和空数组；整个字段缺失不能当作无需权益。配置只用于资格校验，不扣权益、不改变G5销售价格，不表示真实商业映射已批准。

关联商品必须active且当前角色具有can_use，不能以can_view/can_buy替代。声明asset_required时检查同用户同商品的有效资产；声明权益类型时再精确匹配该类型、父资产、时间和剩余额度。仅资产商品不要求存在配额行。会员只匹配显式等级集合与有效时间，不推导等级继承或购买门槛。模型管理端对配置的编辑/发布/审计入口仍待本阶段后续接入，当前只由合成夹具提供发布快照。

## 设计原因与工程决定

### 输入媒体实际清理（同步Fake上传增量，未完成全矩阵）

`CleanupInput(ctx, inputID, owner, policy)`是内部清理入口，不注册为普通用户路由，也不启动Worker。policy必须显式为non_commercial_test_fixture、提供有效版本和7天绑定保留期；测试可注入时钟推进留存，不代表实际等待7天或正式法律批准。清理已有删除申请不要求原Key仍可生成，但必须证明原User/Project/Key归属。用户读取历史回执仍复验当前主体权限，历史删除身份不扩展到内容访问或新引用。

先发现绑定任务集合并按稳定公开ID锁Task/Request，再锁Input、绑定和来源/控制记录。输入原到期时间与所有绑定的lease_released_at+7天取最晚；租约未释放、任务非安全终态、财务未闭合不清理，缺失或倒置完成时间失败关闭。原输入和来源图片的保全、争议及隔离阻止删除；来源到期或已删除不等于来源账本缺失，不能借清理删除原来源图片。

适配器须显式实现SupportsSynchronousDeletion及VerifyDiscarded，不具备能力不能自动降级。上传清理目标由原会话与控制记录生成，包含原件、封存和规范化副本；导入只针对独立规范化目标。顺序为Discard→VerifyDiscarded→Input deleted→控制记录cleaned_at→000084不可变完成事实。对象IO与数据库提交不是跨系统原子操作：若确认失败或DB写入失败，不返回完成；原申请和容量保留，同一不可变目标可再次验证和恢复。数据库上下文与请求取消分开，有界等待同步适配器返回；这不证明忽略取消或异步执行的真实存储安全。

已有完成事实与Input deleted、版本、hash和归属一致时，历史删除回执返回media_deleted=true，Handler映射200；pending_delete仍202。不能仅凭普通deleted字段返回完成。当前上传服务层回执已测，真实HTTP200/来源失效历史/跨Key全矩阵仍待完成。

65828缺入口红例；5609真实三类Fake对象清理、墓碑阻止复活、唯一事实与容量标记通过。71087验证不支持同步、Discard/Verify失败和同目标确认重试；19359整轮36项通过。QA指出原确认写失败场景开始前正文可能已被前一轮删除；现已调整顺序，补“调用前正文存在、事实INSERT前正文已消失”断言，等待复验。多绑定截止、导入Verifier/清理、保全固定窗口、100并发、COMMIT响应未知及异步存储围栏仍未验收。

### 输入删除申请HTTP（增量验证中，实际清理待完成）

新增`DELETE /api/token/video-inputs/{input_asset_id}`，由`video_input_handler.go`进入`RequestInputDeletion`，复用000083不可变命令和原InputAsset，不写财务、用量或Outbox。仅接受Idempotency-Key和精确JSON键version_no；JWT在自身无Key来源中派生Project，SK复验当前身份及来源Key。首次和原键重放均202，固定六字段与null规则见完整API文档；当前media_deleted始终false，只确认pending_delete，不承诺正文已删除。

产品工程确认：主动删除不覆盖G0/G3既有留存窗，未绑定输入保持原期限，绑定输入受所有执行租约及安全终态后7天保护；不得从删除申请重新计时。原键使用原CAS意图，不能因本次申请版本递增而误冲突；额外版本、hash、审核或删除时间漂移则必须冲突。实际清理及完成回执尚未实现，不能把此增量当完整DELETE验收。

关闭态反例先返回405，注册后503；91600真实HTTP及35项局部回归通过，覆盖同主体来源导入后的删除、保全拒绝、跨Key/JWT404、六字段202、原键重放、原期限与绑定/资金保持不变。独立QA随后发现大小写别名和漂移重放缺口，65545实际复现VERSION_NO被接受及原键在额外版本后仍成功。现已改为精确单键解析并共用完整删除凭据匹配，等待复验；旧通过结果不覆盖这两项修复。

### 按已有任务授权的私有参考图读取（增量验证中）

`VideoHTTPService.NewTaskLedger(owner, locator)`复用原G5账本，装配包内`loadTaskReference(ctx, db, taskID, owner)`；不创建Provider、Worker或后台循环。G4账本所有非终态I2V读取使用该入口，`withDB`传播原事务连接与专用读取器，避免另借连接；旧构造器没有该能力时仍拒绝pending_delete。

专用入口当前读Task/Request、唯一未释放TaskInput及InputAsset，执行绑定版本/hash/删除凭据复验；不接受客户端传入asset或allow_pending_delete，不把资产改成ready。上传来源锁定原completed会话；导入来源读取完成回执，复验原源快照和显式图片模型scope。规范化规格检查与ready资格拆开，只有绑定凭据通过后才调用包内私有对象读取。普通Upload/Import LoadReference继续要求ready。

Task/Input/绑定锁覆盖有界对象IO，防止最后执行租约在读取途中释放并进入清理；IO后重查输入期限与权限。上下文上限30秒是协作式取消，不代表不遵守context的Store能被硬中断。单连接嵌套事务、导入来源和固定读取/清理窗口仍需专门验证。

20491缺装配入口红例后，99398整轮35项MySQL/race通过，包含上传来源参考图pending_delete后原Fake Submit/Poll/归档、真实G5结算/交付、原绑定未改写及安全终态释放租约，且Submit一次、正文仍保留。该夹具使用真实规范化图及数据库业务链，存储边界为Fake，不代表MinIO、HTTP删除或到期清理已完成。独立QA发现两次AuthorizeTx未传operation；53246实际复现发布撤下I2V后仍返回成功且正文读取增量1。现已将原任务operation传入IO前后两次准入，等待反例复验；不能用99398覆盖此次修复。

### 延迟删除输入的租约兼容基础（开发中，入口未装配）

`VideoInputAssetRepository.RequestDeferredDelete(ctx, publicID, owner, expectedVersion, commandKeyHash, now)`新增同事务删除申请。仅ready/passed且无保全的输入可首次申请，原Key和版本意图重放返回已有事实；同键更换CAS版本冲突。返回原InputAsset和重放标记，不返回“正文已删除”。000083只追加删除命令凭据并推进pending_delete，原TaskInput不改写。旧`RequestDelete`仍保持G3拒绝活跃租约的合同，新增强路径不能借旧接口偷偷改变行为。

已有绑定只有在凭据原版本等于TaskInput版本、当前版本等于唯一删除后版本、hash/审核/期限与删除时间全部匹配时才能通过仓储复验。新报价和绑定保持ready限制。执行复验在调用方事务内按当前Task/Request、绑定、InputAsset及凭据读取，依赖故障与不存在凭据分别处理；删除重放也用当前读，避免旧RR误冲突。租约读取使用共享锁，与执行校验兼容；不能让删除持Input锁再等待绑定排他锁，形成反向等待。

33908先证明缺入口，85658首个真实I2V删除/重放/新Quote拒绝/SQL清理拒绝/版本漂移反例通过；51907进一步复现旧RR漏读版本与删除赢家，以及依赖错误被吞。修复后的两连接反例在99398通过。旧G4构造器没有按任务授权能力，对pending_delete非终态输入仍失败关闭；G6新增专用装配入口见上一节，不假装ready。单连接、所有异常状态及完整读取/清理竞争仍待验证。

此增量不增加HTTP路由，不删除对象，不释放容量或执行租约，不证明完整输入删除、留存和清理闭环。完整G6仍要求该闭环及全部其余路由。

### 平台任务、事件与请求查询（增量验证中）

新增五条局部GET：`/api/token/video-tasks`、`/api/token/video-tasks/{task_id}`、`/api/token/video-tasks/{task_id}/events`、`/api/token/videos/requests/{request_id}`、`/api/token/videos/requests/by-video/{video_id}`。均由`video_task_query_handler.go`进入原Task/Request查询，不新增账本；只在显式本地注册函数中装配，未接bootstrap。

详情固定25个字段：`task_id/video_id/request_id/quote_id/model/operation/execution_status/billing_status/delivery_status/progress/version_no/request_version_no/quoted_amount/held_amount/current_frozen_amount/settled_amount/net_released_amount/hold_status/currency/created_at/completed_at/media_deleted/media_partially_deleted/media_deletion_pending/can_deliver`。`task_id`与`video_id`都是原Task.PublicID的别名，不暴露内部自增ID；`quote_id`为原Quote.PublicID。原执行状态可为reserved，不能把兼容Job的queued映射回写到账本。completed_at仅指执行终态时间。两个新增删除状态均为显式bool，未删除时为false；确认部分删除和其他对象待删除可以同时成立，详见[资产删除合同](./video-gateway-vid-g6-asset-delete-contract.md)。

金额均为8位小数CNY字符串。quoted_amount是冻结报价，held_amount是原预占，current_frozen_amount是原Hold当前冻结额，settled_amount在尚未结算时为null、合法零结算时为零字符串。net_released_amount表示原Hold净释放：holding为零，released为原预占，settled为原预占减原结算；不表示解冻流水总和或包含后续调账的净退款。不存在Hold的合法未预占状态返回对应null；已预占却缺失关联、Quote/模型/归属错绑或Hold身份不一致时失败关闭，不伪造零金额。

三条详情入口共享同一查询实现，先在当前User/Project/Key范围解析公开ID，再锁Task与Request并复验模型权限。JWT只能访问无Key来源；列表JWT必须明确project_id，SK只能使用自身Project。列表采用D-95，page默认1、最大10000，page_size默认20、最大100；拒绝重复、未知、空值和非正十进制参数，空页items=[]且保留total。详情不接受附加query。

查询事务显式使用读已提交，与G5对账一致。先锁Task/Request再读取Quote、钱包关联和Hold，避免普通RR旧快照与当前锁读拼接成“已结算但对账仍未结算”。列表总数和页内ID在同一条SQL读取，随后按稳定ID顺序锁全部Task，避免展示顺序引起共享钱包死锁。代价是列表页内不同任务不是一个全库时间点快照；每条任务的状态/财务/交付事实仍须在锁保护下自洽。

事件返回`event_id/event_type/axis/from_status/to_status/created_at`及D-95分页。仅白名单执行、计费、交付、取消申请和待对账事件可见；SQL按二进制大小写过滤类别和对应轴状态，total同样过滤。事件公开ID由原Task公开ID与内部事件序号摘要生成，不读取或返回原event_id、source、safe_detail_json。非迁移事件from/to为null；按追加序号排序，不按可相同的时间戳排序。

只有执行成功、settled/available、媒体未删除且G5完整对账通过时can_deliver=true。删除媒体记录后保留原请求/Quote/财务/完成时间并置media_deleted=true、can_deliver=false；本增量的删除元数据测试不等于媒体删除接口或对象正文清理已完成。

已观测：3079复现RR混合快照；42412中对应反例改为PASS，但该整轮因测试诊断正文被数据库白名单拒绝而FAIL。保留“任意诊断正文拒绝”断言，改用合法低敏事件后39711四项查询专项通过，包含释放/删除记录保留及平台列表100并发。随后89610复现Hold与Link结算不一致仍返回成功，52429进一步复现缺额、未知状态、holding含结算额和历史exception事件泄露；缺Link与错Hold身份反例当轮已经正确拒绝。现已补金额一致性、终态完整性及状态白名单校验，移除视频事件exception；新源码等待完整复验，不能复用39711作修复后PASS。完整阶段未验收。

### 从已有图片导入参考图（增量验证中）

导入HTTP增量由外部测试包`service_test`通过实际`RegisterVideoUserRoutes`执行。`export_video_import_http_test.go`仅在测试构建中提供夹具：来源图片走真实IMG-G5处理/结算/对账，应用使用真实NewVideoHTTPService、HMAC Key和JWT；生产GoFiles不包含这些测试入口。回环验证包括来源七字段与Key隔离、导入202/201/200及固定五字段/null、原键不续期、Owner JWT接受合成权利政策，再以导入输入完成I2V报价及G5预占。不将此检查当作视频执行、SDK或浏览器交付通过。

导入前后对比八类请求/报价/任务/财务/用量/Outbox计数和钱包余额/冻结额，并检查全隔离库无新增Outbox。生成后固定合成报价与预占为0.75000000，检查原Hold/流水/钱包关联及held事件，不修改价格合同。源撤权反例在发布事务首个一致性读完成后，由另一连接删除精确图片模型scope，必须实际删除一行、返回404并清理未发布目标；独立FOR SHARE不把旧RR快照当授权。真实JWT自有来源正向导入、混合额度及完整故障矩阵仍待补齐。

新增`POST /api/token/video-inputs/from-image-asset`：严格JSON只接收公开`source_asset_id`及JWT必需的`project_id`，写入使用原Idempotency-Key，不接受客户端hash、version、bucket或URL。`VideoHTTPOptions.Imports`显式装配受限Store、共享Safety、规范化bucket、审核版本和容量上限；与Uploads同时配置时容量上限必须一致。未装配仍503，不自动创建Fake。

`video_input_import_service.go`先锁用户、复验来源资格及显式源模型授权，再原子创建原InputAsset和000082非财务控制行。输入source_type为gateway_asset_snapshot，关联原source_gateway_asset_id，upload_session_id为空，不伪造上传会话。来源ID、版本、hash、规格和位置首次冻结，命令重试不能换成新源版本。进行中先预留10MiB，与上传共享用户2/Project4并发及容量；成功发布后改为实际规范化字节占用。失败目标未确认清理时仍占额，不再次收取图片费用，不创建Quote/Hold/视频Task。

工作租约2分钟、对象IO上下文30秒、处理命令期限24小时，输入初始7日不因重放续期。此7日不是已绑定任务的强制删除期限，执行租约、待对账与任务终态后保留仍优先。Store必须限长读取不可变副本、同目标同hash幂等写入、建立禁止迟到写入复活的清理围栏。读取和审核在事务外，发布时再次读取源、精确源模型授权行和归属，使用CAS将normalizing→moderating→ready及完成回执一起提交。

响应固定import_id/status/input_asset_id/processing_expires_at/idempotent；处理中202及Retry-After:1，input_asset_id为null，首次完成201、已有命令完成重放200。processing_expires_at是导入处理期限，不是媒体删除期限。未知对象写入/临时DB故障保留原命令，重复请求先查账本；成功或新租约赢家不得被旧失败者清理。源漂移拒绝并只清理本次目标，原图及其财务事实保持不变。目标保全优先于清理；完整保全解除恢复、源撤权竞态及提交未知矩阵正在补验，不能把此增量当成完整导入验收。

### 可引用来源图片候选

`GET /api/token/video-input-source-images`新增本地D-95候选列表，共用输入列表的严格page/page_size/project_id解析。入口仍未接bootstrap。候选只返回asset_id/mime_type/size_bytes/width/height/version_no/expires_at七字段，不返回对象位置、下载URL或原Provider资料，也不创建输入或Quote；from-image-asset导入及其幂等/容量/源图并发复验仍待实现。

`video_source_image_service.go`从原图片Asset、Task、Request联合查询同用户/Project及精确Key，Task和Request必须属于同请求及同模型；执行成功来自Task，settled/available来自Request，不能在Task上另建平行财务状态。仅可计费主图、审核及双标识通过、无保全/争议/删除、未到期、PNG/JPEG、640—4096边长及10MiB以内的完整对象元数据可列出。Key即使scope_mode=all，也必须有源图片模型的显式授权；JWT只看到无Key来源。候选不是导入授权，使用时必须再校验并重新规范化。

测试夹具使用Fake640px图片，但实际执行原IMG-G5规范化、归档、钱包预占、结算与零差异对账。媒体已经入库而结算未完成时不得列出；不手工填充settled作为正例。新增候选查询最初错误引用Task不存在的财务字段，真实HTTP反例返回503，已改为原Request账本字段。完整来源安全/删除/规格/并发矩阵仍待后续验收。

### 输入资产元数据与使用权限分离

新增两个局部GET：`/api/token/video-inputs`和`/api/token/video-inputs/{input_asset_id}`，由`video_input_handler.go`进入`video_input_query_service.go`。不依赖对象存储，不发放媒体能力，也不修改输入、租约或财务事实。列表采用D-95，page默认1且最大10000，page_size默认20且最大100；只接受page/page_size/project_id单值正十进制参数，未知、重复、空值及越界均400。JWT列表须显式Project；SK只能使用自身Project。详情不接受query，JWT先在认证用户且无Key的可信来源中派生Project，再复验主体准入。

详情固定十键：input_asset_id/source_type/lifecycle_state/mime_type/size_bytes/width/height/expires_at/version_no/can_reference。未形成的规格字段保留null，不伪造零值；不返回用户/Key内部ID、源图片内部ID、hash、对象位置、签名URL或保全原因。列表items为同一DTO，空页items=[]，total只统计同一归属条件内的记录；查询失败返回不可用，不伪装为空页或不存在。

产品子项工程确认：合法所有者可看仍被保留的失效输入元数据，但不等于媒体访问或生成授权，不扩大留存期限。列表和详情共用原可信来源过滤：上传必须有同归属、同Key且最终绑定的completed来源；生成图片来源必须仍满足原可见性规则，来源图过期、隔离、删除或争议时详情404、列表隐藏。因此不是承诺所有来源的永久历史可见。`can_reference`只代表快照前置条件，不代表模型、权利协议、预算或钱包通过。

G6查询、报价预检、Quote复验和预占事务共用完整快照校验：ready/passed、有效规范化hash/version及对象定位、审核版本、PNG和冻结尺寸/体积范围、未到期、无删除申请/删除时间及legal hold；不替代后续真实对象字节校验。历史未启用G6的协调器不改变G5合同。41837先复现列表计数503及保全输入仍能报价，随后修正Count投影并接入共享校验；当前复验结果见后续输入查询检查点，不能用旧上传25项证明该增量已完成。

### 受控上传与发布恢复

上传先创建原`ai_upload_sessions`和000081控制行，不创建Quote、Task或钱包记录。创建仅接受文件名、MIME、大小、SHA256及JWT必需的Project；服务端保存规范扩展名而非任意文件名，生成私有原件与规范化位置。PNG/JPEG源文件1字节至10MiB，完整解码后输出PNG；尺寸640—4096、像素不超过16777216、宽高比0.5—2。会话24小时，上传能力固定15分钟，创建重放不得续期。用户同时活动上传最多2、Project最多4，创建前锁用户行并预留原件声明大小加10MiB规范化上限。

`VideoHTTPOptions.Uploads`必须显式提供Store、两个不同私有bucket、审核策略版本及非零`MaxUserReservedBytes`（最大1TiB，测试128MiB不是商业额度）。HTTP应用强制共用G5 Safety；未指定ReferenceLoader时使用上传服务私有读取。默认不提供上传依赖，不自动启用Fake。Store须提供Issue、Seal、PutNormalized、ReadNormalized、Discard；Seal不可变，Put同位置同hash幂等，Discard必须建立不可复活的写入围栏，普通S3删除本身不满足该合同。

complete使用2分钟版本化工作租约及30秒IO上下文。验证封存原件hash/MIME/大小、完整解码和五类审核后写入不可变规范化对象；随后同一MySQL事务复验身份、期限和租约，原子创建ready InputAsset并完成原会话。有效租约内重复完成返回verifying，终态重放返回原InputAsset。内容无效或审核拒绝可拒绝并清理；取消/超时、依赖不可用、鉴权数据库读取不可用及MySQL1213/1205只终止本次租约，保留verifying供原完成键恢复，不能误报恶意图片。真实撤权、约束冲突不能归入允许继续的临时故障。

取消先落不可逆终态与cleanup_pending，再调用围栏Discard，成功才记录cleaned_at释放预留。失败保留待清理事实；同取消键可重试。已发布输入不能通过取消会话删除。旧执行者的发布或失败处理必须检查版本，不能覆盖新租约赢家。输入后续删除/容量回收及完整清理重试矩阵仍属未完成范围。

已发现上传发布临时错误误转rejected：56971在真实临时MySQL业务流程中对发布鉴权读一次性注入驱动1213，复现原对象被清理；这不是实际数据库死锁复现。新增恢复分类与HTTP响应空DTO/必需字段断言后94572的24项通过；67720进一步通过25项，覆盖发布INSERT的1213/1205及B已完成/B仍verifying两种旧执行者迟到场景。运行副本1641文件与工作区逐项字节相同；显式string[]按ordinal排序后，主机整体摘要与容器输出一致。测试运行器自动输出复制源码树SHA256，排除docs/evidence，不能将事后文档回填摘要冒充运行副本摘要。详见[上传检查点](./evidence/video-gateway-vid-g6-upload-checkpoint.json)，不代表完整G6通过。

### I2V报价与预占的原子权利声明

平台JSON增量接收rights_confirmed、rights_policy_version及rights_attestation。JWT图生必须逐请求明确确认及当前版本；平台SK必须attestation并有有效项目接受，版本可省略但提供时必须匹配。内部v1来源仅使用项目接受，不增加multipart字段；文件经显式inline Store进入原UploadSession/InputAsset链，缺依赖503。来源、接受ID与私有证明由服务端构造，客户端不能提交这些内部字段。

000080的`ai_video_rights_declarations`分别关联原Quote和Request，冻结归属、政策版本/正文SHA、来源、原确认时间与机器复验时间，不建立视频或财务平行账本。Quote声明与Quote一起提交；生成声明在原G5预占事务内提交。源、trace、接受ID不进入G5生成指纹，保持跨门面逻辑意图合同。重放只检查原声明，不回写或给旧Quote补新版本；历史G5 I2V没有声明时，G6需要证明的路径失败关闭。

报价、价格与可信输入SQL读取都绑定同一事务连接；事务前后核验政策/接受期限及输入hash/version。T2V完全不访问权利表。仅access与rights都未启用的历史协调器沿用G5合同，部分G6装配失败关闭。生成声明的数据库约束同时校验原Quote消费的request_id，不能只因同owner而错绑另一请求。

安全执行/计费终态、已释放输入租约且请求仍指向原InputAsset时，生成重放可使用冻结TaskInput快照，不重新读取已删除正文、不重新绑定或提交Provider。该提示不用于Quote，也不构成授权；之后仍核验完整生成指纹、当前权限/权利及原声明。非终态、未释放租约或别名输入仍走当前输入校验。73397复现部分装配、SQL错Quote及删除后重放三项缺口，修复后20067默认19项局部用例通过；完整I2V HTTP、单连接/100并发、日期/故障矩阵和上传仍待验证。

### 图生视频权利政策与Project接受

000079只创建`ai_video_rights_policies`与`ai_project_video_rights_acceptances`，不seed任何真实政策、接受记录或法律默认期限。政策必须显式为`non_commercial_test_fixture`，版本、中文正文及SHA256、起止时间和接受TTL来自合成配置；active版本最多一个。正文、标题、版本和期限不可原地修改，修改内容必须新版本；退役/撤销使用version_no增加，down保留全部事实和约束。

全局`GET /api/token/video-rights-policy`要求有效认证、账户及凭据状态，仅供阅读，不要求Project或模型准入，也不返回生成授权结论。`GET/POST /api/token/projects/{project_id}/video-rights-acceptance`要求当前Project归属、实名与基础权限；POST仅允许Project所有者JWT，不允许SK代签，不要求具体模型grant。该认证区分由本阶段产品子项确认，属于关闭态工程澄清，不是新增法律批准。

POST仅接收`rights_policy_version`与`rights_confirmed=true`，强制16—128字节单值Idempotency-Key。命令域固定rights_accept，原始键只存SHA256；回执保存认证签署主体、政策版本及正文hash、原接受时间、截止时间和HTTP request_id。截止时间不晚于政策到期，TTL不作为正式用户协议默认值。首次接受201，重复200；同键不同版本409，重放不能续期。SQL同时限制同Project所有者、政策三元身份及UPDATE/DELETE不可用。

回执DTO明确区分历史与当前：`acceptance_id`、`accepted_policy_version`、`accepted_at`、`expires_at`无记录时为null；`rights_policy_version`没有当前版本时为null；`valid`不能从“曾经接受过”推导。政策过期、撤销/退役无active或升级后，历史查询及同键重放保留原事实并返回valid=false；新接受和当前政策阅读仍失败关闭。数据库错误、多active或配置hash损坏报503，不能伪装成“未接受”。政策升级后须所有者重新阅读，用新键明确接受新版本。

51255原始17项局部回归通过；90629扩展反例复现政策失效后历史回执503。拆分当前政策准入和历史回执有效性判断后89677通过；14034再次通过17项，包含真实HTTP的null字段/历史重放/新键拒绝、T2V不受政策退役影响，以及商业用途、双active、签署人和复合外键的精确MySQL错误号反例。59个文件与当轮运行容器逐项哈希一致，见[权利检查点](./evidence/video-gateway-vid-g6-rights-checkpoint.json)。后续原子声明增量见前节，不能将该历史检查点作为完整I2V生成链通过证据。

平台报价返回201和仅含客户价格的Quote DTO，平台生成必须传quote_id并返回202；两门面进入同一VideoCommand与G5生成意图命名空间。平台使用数字code和HTTP request_id，兼容门面保留冻结错误信封。JWT只用于平台接口，Project必须显式指定；JWT吊销读取既有auth的Token SHA256存储语义，隔离测试只替换缓存存储，签名校验和数据库用户/权限逻辑实际执行，缺依赖不放行。Redis适配器存在不表示本阶段运行了Redis。

41327首次100并发和95303诊断均失败，后者捕获MySQL1040；单连接最小反例71882在2.05秒因价格不可用失败。根因为自动报价外层持锁时，SQL价格仓储仍从主池借第二连接。修复将SQL价格读取器和Quote仓储同时绑定外层tx，不扩大池、不删除Key Touch、不改变金额。78317修复后通过；移除临时诊断后的19132也通过13个指定顶层用例，单连接用例0.23秒通过，见[平台检查点](./evidence/video-gateway-vid-g6-platform-checkpoint.json)。该历史通过不能覆盖后续源码变化。

独立审查新增的显式Quote问题在72073复现：五类业务拒绝均错误返回500；首次100并发报价出现500；双连接固定顺序反例在0.05秒返回`record not found`。重复插入后的查询沿用外层RR旧快照，savepoint不会刷新它。修复只把赢家回读改为共享当前读，不对不存在范围提前加锁，不改变价格或财务动作。平台Quote不存在/跨主体为404/40420/quote_not_found；过期为409/40920/quote_expired；异意图或其他请求已消费为409/40901/idempotency_conflict。合法原生成重放继续返回原Job。

列表只接受after、limit、order，默认20/desc，limit为1—100；显式空值、未知/重复字段及非法数字返回400。跨主体或未知合法格式cursor返回404。数据及空页使用兼容VideoList而非D-95；同秒以公开ID排序。删除媒体后的任务事实保留用于原主体继续分页，这属于满足稳定分页和保留账本要求的工程解释，不是重新公开已删除Job。7100已通过基础MySQL游标/Key隔离，见[报价与列表检查点](./evidence/video-gateway-vid-g6-quote-list-checkpoint.json)。删除竞争、完整边界和最终SDK证据仍需分别验证。

39496新增空grant/未实名和两个completed任务的100并发反例均失败。32697确认主体准入提取后返回精确400/70001；同轮安全探针捕获52次MySQL1213，证实完成态列表按不同展示顺序持Task/钱包/下一Task形成锁环。修复使本页所有Task按公开ID固定顺序先锁定，再按asc/desc展示顺序完成原17项对账，不移除交付检查。临时错误号探针已删除，31011通过全部16个指定局部用例，完成态100并发20.83秒通过；37个Go/infra输入文件与运行容器哈希一致。独立测试角色已确认修复及用例强度，见[列表检查点](./evidence/video-gateway-vid-g6-list-checkpoint.json)，不能推导完整阶段通过。

锁定SDK执行器位于`tests/api/video-gateway-vid-g6-sdk/`。Python openai2.45.0与TypeScript openai6.39.0已通过真实本机loopback HTTP、SK HMAC、临时MySQL、T2V/I2V、retrieve/list、MP4 Range、删除和双向账单保留；可复现父runner为`infra/scripts/verify-video-gateway-vid-g6-sdk.ps1`，最近运行`TestVideoG6LockedSDKHTTPMySQL`用时36.11秒。Provider和对象存储仍为合成边界，浏览器播放、真实MinIO/Provider及完整阶段验收不由本证据覆盖。

- HTTP 传输与业务准入分开：应用先验证当前主体、六类资产安全、结算和零差异，再提供受控读取能力。传输模块无法根据客户端 bucket、object_key 或 URL 获取对象。
- 权限事实继续由 IAM 管理，不新建视频角色账本。实时查询增加数据库读取开销，但可在没有 Redis 的授权隔离测试中执行真实规则，也避免高成本重放依赖旧缓存。
- 内容错误必须区分响应开始前后。已发送的 MP4 后不能追加 JSON；专用中断哨兵必须穿过全局恢复中间件。
- 两门面仍计划复用 G5 的共享生成指纹、Quote/Hold/Task 事务；本阶段不重新定义价格或账务政策。

## 本地验证方法

前提：Go 版本满足 `server/go.mod`，在专用 worktree 的 `server` 目录执行。以下命令不需要业务凭据、真实 Provider 或共享服务。

```powershell
go test ./internal/modules/token_gateway/handler -run '^TestVideoG6ContentHTTP' -count=1 -v
go test ./internal/modules/iam/service -run '^TestPermissionFresh' -count=1 -v
go test ./internal/middleware -run '^TestRecoveryPreservesStreamingAbortAndOrdinaryFailure$' -count=1 -v
```

完整本轮隔离用例由仓库根目录的 `infra/scripts/verify-video-gateway-vid-g6.sh` 执行，需显式 `VIDEO_GATEWAY_G6_ISOLATED_MYSQL_APPROVED=YES`。Windows使用Git自带bash，不使用缺少发行版的WSL默认bash。运行器建立固定镜像ID、无宿主端口、内部网络及临时MySQL，按完整迁移链运行；源码按Git清单复制进临时构建容器，SQL也从同一容器快照读取，避免SQL和Go来自两个时刻。指定顶层用例必须实际RUN/PASS，读取容器退出码；缺DSN的本机SKIP不算集成通过。可选focus=list只定位HTTP与完成态列表，不能替代默认all。

编译缓存`molin-video-g6-buildcache-908f8ff2ec29-v1`只属于本Goal及固定Go镜像，复用前核对goal/purpose/image三个标签；不接管未标记缓存。缓存仅减少编译，`-count=1`仍重新执行测试。每轮临时数据库、构建容器和内部网络按精确ID回收，缓存保留给本Goal后续复验，不保存业务数据库或凭据。

必须观察实际 RUN/PASS；零匹配、SKIP、编译失败均不能算通过。HTTP 测试使用真实回环 TCP，内容为传输字节夹具，不是可播放 MP4。IAM已经包含临时MySQL权限矩阵和驱动边界故障夹具，JWT/SK也经过真实验证；范围仅限检查点列出的场景，不得写成完整SDK、媒体交付或阶段财务链已通过。

若断流测试发现正文长于第一片或夹入 JSON，检查 Recovery 是否保留中断哨兵。若 IAM 故障注入仍返回允许，检查所有仓储错误是否完整传播；不得通过忽略查询失败使测试变绿。

## 缺陷台账与剩余交付

| 编号 | 级别 | 现象与根因 | 状态 |
|---|---|---|---|
| G6-CONTENT-001 | P1 | Recovery 吞掉流式中断并追加 54 字节 JSON；完整栈回环反例已复现 | 基础检查点独立复验已关闭 |
| G6-CONTENT-002 | P2 | 旧 If-Range 与合法越界范围组合误返回 416 | 基础检查点独立复验已关闭 |
| G6-CONTENT-003 | P2 | 注释将 OpenRange 前置失败误写为任意首片失败 | 基础检查点独立复验已关闭 |
| G6-IAM-001 | P1 | 角色/组权限查询错误被忽略，可返回部分授权集合 | CLOSED_VERIFIED：四类故障、真实MySQL权限期限及全量准入回归通过 |
| G6-ACCESS-002 | P1 | 原RR快照忽略另一连接新提交的deny | CLOSED_VERIFIED：两连接当前读deny及扩大准入矩阵通过 |
| G6-ACCESS-003 | P2 | reinstate被当作active暂停 | 先红后绿，54199实际通过 |
| G6-ACCESS-004 | P2 | 可见性解析器吞数据库故障并误报无权限 | CLOSED_VERIFIED：数据库故障保持503语义，目录/授权专项通过 |
| G6-ACCESS-005 | P1 | 任意类型权益替代指定权益，或错误要求仅资产商品存在配额 | CLOSED_VERIFIED：版本化合同、精确权益及真实MySQL正负矩阵通过 |
| G6-TEST-001 | P2 | 实时挂载源码导致编译读到TDD红绿中间态 | CLOSED_VERIFIED：运行器固定复制源码、输出copy-tree SHA并禁止测试缓存 |
| G6-QUOTE-001 | P2 | service/repository两层Quote错误没有映射，业务拒绝返回500 | 72073真实HTTP复现；7100与31011精确状态、数字码和error类型通过，独立增量复核确认 |
| G6-QUOTE-002 | P1 | 外层RR旧快照漏读唯一键竞争赢家 | 72073双连接及100首次报价复现；共享当前读后7100与31011通过，独立增量复核确认 |
| G6-LIST-001 | P2 | 没有模型授权时跳过主体准入，使未实名返回403而非400/70001 | 39496反例失败；32697精确HTTP错误码通过，提取主体准入后保留完整模型检查 |
| G6-LIST-002 | P2 | 相反排序完成态页面持Task/钱包/下一Task锁成环 | 39496复现页面异常，32697捕获52次1213；固定锁序并删除探针后31011全部100请求通过，独立增量复核确认 |
| G6-RIGHTS-001 | P2 | 政策自身过期或无active版本时历史回执被503覆盖 | 90629复现；89677及14034同边界通过，独立增量审查确认历史可读不等于有效授权 |
| G6-I2V-001 | P1 | access已启用但缺rights依赖时回退旧合同 | 73397重放反例复现；20067失败关闭通过，独立增量复核确认 |
| G6-I2V-002 | P2 | 输入安全删除后HTTP应用预检挡住原账本重放 | CLOSED_VERIFIED：原Job/request不变、正文读取0、Fake提交仍1及inline断连/归属/COMMIT矩阵通过 |
| G6-I2V-003 | P2 | 同owner但错误Quote可关联生成声明 | 73397无唯一键混杂反例复现；消费关联触发器补齐后20067精确1644通过 |
| G6-UPLOAD-001 | P2 | 发布数据库临时故障误转rejected并清理合法对象 | 56971复现，94572及67720恢复/租约围栏通过；独立增量审查确认分类与反例 |
| G6-UPLOAD-TEST-001 | P2 | HTTP解码复用DTO可能沿用前次缺失字段 | 清空DTO、固定八键/null及错误码断言后94572、67720通过；独立增量审查确认 |
| G6-CATALOG-001 | P2 | 已发布视频的工作副本改成Chat/Image，可绕过仅按草稿模态执行的快照保护 | 已改为所有模态读取当前发布身份并隐藏视频模态漂移；独立源码复核关闭，动态结果及范围见catalog专项回执 |

47条本地注册、v1 multipart图生、预算/queued/running准入、下载和全部管理写整改矩阵已经形成本地闭环。当前剩余是最终同源SDK、全量MySQL/race与兼容回归、敏感扫描、证据索引、四类独立复验及Git闭环；真实Provider、MinIO和生产仍未验证。

## 资产生命周期查询（开发与验证中）

### 平台短效下载增量（尚待隔离验证）

`GET /api/token/video-assets/{asset_id}/download-url`无query，当前JWT/SK与G5门禁通过后返回三键`asset_id/download_url/expires_at`。配套`GET /api/token/video-assets/{asset_id}/content?expires=…&signature=…`仍必须携带原Bearer认证，不能匿名分享。地址最长15分钟，进一步受六资产最早到期时间约束；兑换、Range重试或租约续期不能延长地址。两入口不生成Quote、Hold或财务事实。

签名为专用32字节密钥的HMAC-SHA256，绑定域、GET方法、规范路径、原用户/Project/精确Key、版本/hash/大小/角色与期限；内部身份、hash及对象位置不进入URL。`VideoHTTPOptions.DownloadSigningSecret`必须显式注入且禁止JSON序列化；缺失配置503，没有默认密钥或JWT secret兜底。测试仅在内存随机生成临时密钥，未接生产配置。轮换导致旧地址失效，不扩展保留政策。

复用`video_content_service.go`原G5对账、每片授权、2/4并发租约及20MiB/s传输。平台允许content/cover/preview/thumbnail/普通derived，使用服务端video/mp4或image/png/jpeg/webp白名单MIME；审核副本及非普通derived404。v1仍只提供MP4。新增删除意图检查阻止签发/兑换，URL过期后禁止后续分片且写期限不超过URL期限。生命周期can_download在显式装配平台短签名后覆盖JWT和派生物；未配置时仍只反映原v1 SK正文能力。

新文件：service/video_asset_download_service.go、handler/video_asset_download_handler.go；无数据库迁移。关闭回滚为取消装配专用key并关闭相应路由，不删除资产或账单。关闭态红例`e5ef96`已复现404，注册后25826相关原生测试通过；69646真实签发/兑换及JWT吊销断流9项专项已通过，完整时效/并发矩阵仍待后续验证，不得称全阶段验收。

缺陷`G6-ASSET-DOWNLOAD-001`（P1）：95714实际HTTP首片后吊销JWT仍返回完整4054453字节。修复让VideoCaller私有内存credential携带JWT expiry及摘要复验闭包，不保存原Token；初次认证、签发/兑换、事务前后、每片及写前复验，外部吊销依赖受30秒/JWT期限上界约束。URL及写deadline不晚于JWT、六资产期限和租约。69646同一吊销测试转绿；新增自然到期与依赖故障由97564加强验证，独立关闭回执另列，不替代完整下载或G6验收。

第27个局部入口`GET /api/token/video-assets/{asset_id}/lifecycle`面向当前归属主体，只读低敏元数据，不申请下载租约、访问Store或写财务。Task、Request、Project与来源Key共同限定归属；审核副本不是用户交付资产，固定404。根资产parent_asset_id=null，子资产父ID只能是同Task/Request的content。保护、删除元数据可见不等于使用媒体许可，不扩大保留期限。

源码为service/video_asset_lifecycle_service.go及handler/video_asset_lifecycle_handler.go，复用原G5对账、权限和资产表，无新迁移。DTO固定21键，清单见full-api-design第14.0V6节。当前可下载能力仅覆盖已装配的v1 SK content；平台JWT与缩略图下载是仍需完成的完整G6要求，不是永久禁用决定。退回旧二十六路由注册即可关闭本入口，无需删除任何业务事实。

缺陷`G6-LIFECYCLE-001`（P2）：末尾只复验根资产期限，子资产跨期限时可能误报can_download=true。修复在最终授权后读完G5已锁定六资产，再取新时钟逐条复验，读取错误或数量异常失败关闭。旧43678/91278屏障存在DATETIME精度歧义，不作为可靠关闭证据；改用数据库读回期限后，24757去除修复的负向对照Expiry明确FAIL，保留修复的42475与29308明确PASS。29308生命周期全3项及Linux race通过，包含16张关键表前后完整行快照、零Store调用、精确21键、null与归属、保全/争议/隔离/撤权、真实delete_failed及恢复。隔离夹具通过原G4审核拒绝链生成，争议保留开启和解决历史，不改写已有审核结论或关闭SQL守卫。独立关闭回执见证据目录；本切片通过不替代完整G6验收，父关系损坏、数据库故障及完整平台下载仍待完成。

## 媒体删除（开发与验证中）

在原25个局部入口基础上新增`DELETE /v1/videos/{video_id}`，媒体删除切片为第26个入口，后续生命周期为第27个；尚未接bootstrap，不表示部署开放。只允许当前Project SK、Idempotency-Key和无正文/query。运行中或待对账返回409/video_not_deletable_while_running，并用Link指向平台取消入口；该DELETE不触发取消、退款、Provider或重新结算。

`service/video_media_delete_service.go`与000087实现两阶段：先持久化任务级隐藏意图、固定原目标计划并将交付资产推进deleting；再同步删除和核对墓碑，成功后才提交completed。失败/取消且无产物也须通过原G5金融门禁并记录任务墓碑，不能因资产表为空就伪造成功。公共retrieve/list隐藏已接受删除的Job；平台账本只有确认完成才报告media_deleted，原三轴与财务事实保留。

普通目标为content/cover/preview/thumbnail/普通derived五角色；moderation_copy保留正文、元数据和原期限。任一角色隔离、保全、争议都拒绝普通删除。计划hash不代替真实性：执行和完成重放核对实际六角色、五删一留、父子关系及原归属；SQL INSERT额外校验视频/Key/终态，成功任务不得用空计划伪造completed。

准备阶段内部30秒期限约束持锁Head；执行阶段使用受控同步删除，完成前重读保留副本hash/大小。对象已删而数据库确认失败时保持原隐藏意图，只能沿原快照恢复；不得重新选目标或返回虚假deleted=true。成功响应精确为id/object=video.deleted/deleted=true，不表示审核副本或所有个人数据已清除。

98168基础HTTP及关闭态通过；42781增强五墓碑、审核副本保留、确认失败后恢复的旧快照通过。当前新增审核副本附带丢失、完成确认回滚及阻塞Head期限用例仍在验证。完整保存引用协调、并发删除/下载竞争、平台资产入口和SDK仍未完成，不能把本节视为G6验收。

## 用户任务取消（本地增量）

`DELETE /api/token/video-tasks/{task_id}`与`DELETE /api/token/video-tasks/by-video/{video_id}`调用同一`VideoHTTPService.CancelTask`。仅接收现有JWT或Project SK与16—128字节Idempotency-Key，不接收正文、query、reason、金额或Provider取消结果。客户端不传版本，仍由原Task锁与CAS保证状态迁移。管理端的MFA/reason规则不由此豁免。

两路径共用`user_id + project_id + cancel + key`命名空间，目标为同一Task公开ID，不因路径或Key另建取消命令；资源归属和来源Key仍逐次检查，JWT不能接管SK任务。同键异任务409/40901/idempotency_conflict；未认证401、未实名400/70001、撤权403、未知/越权404；财务事实不完整或依赖故障503/video_cancellation_unavailable，不能当作已接受取消。

返回25字段任务详情加`cancel_requested_at`（可null）、`cancellation_result`、`idempotent`，共28字段。取消本身不产生媒体删除意图或部分删除事实。金额仍为八位Decimal字符串或null。X-Molin-Request-ID指向原业务request_id，HTTP追踪由X-Request-ID提供。

| 已锁定事实 | 处理与响应 |
|---|---|
| reserved/queued且严格证明从未提交 | 原G5事务取消、释放、Usage/Outbox/输入租约闭合，200/cancelled |
| submitting至处理中或pending_reconcile | 只追加原取消意图，202/cancel_requested，不代表Provider接受或退款 |
| 原取消命令重放 | 当前权限复验，返回原命令和当前事实，不重复释放 |
| 已成功/失败/取消/过期的终态新请求 | 200/already_terminal明确无操作，不改终态、不退款、不删媒体 |

该HTTP层没有Provider依赖。后续原执行链仍按G5处理取消拒绝、不支持、未知及迟到成功；`/v1 DELETE`仍是媒体删除，不等同本入口。

000086只记录不可变幂等回执，与原G5写入同一事务。G5公开CancelBeforeSubmit保留原自主事务；G6私有调用不再嵌套重试，仅最外层重启完整事务。25398故障注入发现原实现遇1213整笔回滚后返回成功却丢失回执，修复后86729要求回执创建尝试2次、最终1条且对账通过。T2V/I2V各100取消、HTTP矩阵、在途提交、末尾写失败回滚及错Key SQL1644均已局部通过；最终原G5兼容、完整阶段和Git门禁仍待核验。

## 回滚边界

### 输入留存与清理回执增量

首次发布ready时才起算既有7天未绑定留存，不改变原处理命令24小时截止，也不续期历史完成重放。同一输入绑定多个任务时，必须等待所有安全终态并取最晚租约释放加7天与输入期限的较晚值。保全拒绝后正文必须仍存在；真正清理独立导入副本后，来源图片保持原hash，原DELETE返回严格六字段200。完整财务行摘要证明清理未新增或改写钱包、请求、Quote、Hold、流水、Usage、钱包关联和Outbox。

隔离运行77875通过当前39项必选顶层测试、Linux race及1—84迁移/保留式回退复验；99739通过全Go测试、vet和mod verify。见[留存清理检查点](./evidence/video-gateway-vid-g6-input-cleanup-retention-checkpoint.json)。这些证据只绑定新增content之前的源码，不用于宣称后续内容读取已通过；虚拟时钟验证边界，不冒充真实等待7天。

### 私有内容读取增量与未完成边界

`GET /v1/videos/{video_id}/content`只接受当前Project SK，由`VideoHTTPOptions.ContentStore`显式装配只读私有对象边界。缺依赖503；未交付、对账非零、删除/过期/隔离及保全404。正常下载不调用Provider、不执行Settle/Deliver、不写财务。仅content主MP4可返回，未知variant400。

首次请求和每个至多1MiB分片在原G5锁序下核对当前主体/操作权限、全部财务事实和六资产安全事实；固定asset ID、version、SHA256、大小和服务端位置。Head必须与账本一致；短读、超读、关闭失败均不能返回片段。片段先在事务内缓冲，存储等待后再次验证权限和媒体时限；提交结束才交给传输层，钱包锁不覆盖慢客户端写入。已发送字节无法收回，后续片段必须重新授权。真实对象存储必须满足同Ref不可变契约，Fake验证不等于任意真实实现可用。

下载并发2/4与20MiB/s限制已进入本地实现，完整验证状态见下节；大对象中途撤权/删除竞争、完整故障矩阵和可播放MP4浏览器拖动仍未完成。锁定双SDK已覆盖200/206/416、ETag/If-Range及内容hash，但不能替代浏览器seek或完整content验收。P2 `G6-CONTENT-001`：应用错误分支曾写503后再次追加默认500正文；47d667复现，单次写入修复后9bb60f通过，42007实际HTTP故障单JSON复验通过并经独立增量确认，见content检查点。

### 下载并发、续约与速率（本地增量）

后续真实媒体增量使用锁定FFmpeg本地合成的4MiB可播放MP4，通过原Fake/G5结算交付和HTTP多片读取。电影mvhd与轨道mdhd时基不同导致原探测器误报1fps，已修正轨道时基、0/1版本、完整整数CFR采样表与累计媒体时长，并独立计数视频轨道。测试夹具与工具锁见`tests/fixtures/video-gateway-vid-g6`；生产二进制不嵌入夹具。

本地静态浏览器已解码、实际拖动至3秒并播放至5秒结束，移动视口390px无横向溢出；这仅是同一MP4可播放性证据，不冒充Project SK网关浏览器端到端。大媒体第二片失败的业务专项55397已初步通过，新增解析器负向矩阵后的回归与独立确认另记；撤权/删除固定窗口、真实慢连接、SDK及全部G6范围仍待完成。

功能面向持当前Project SK的下载者，入口仍为原content路由，没有产品UI变化。GetContent在第一次Store Head前取得数据库共享名额，用户最多2、Project最多4；超限429 `video_download_concurrency_exceeded`且不触碰Store。名额跨Key、跨Task及应用实例共享，不另起Redis服务。默认单用户拥有Project，用户2路会先于Project4路触发，不能将这类HTTP用例称为已覆盖第五路Project边界。

核心文件为`service/video_download_lease.go`和000085迁移。`VideoContent.Close`必须由调用方在所有成功、错误、取消和panic路径调用，Handler使用defer；释放失败不伪装成功，遗留名额由原60秒工程TTL保护。`OpenRange`先续约再进入原content事务，`BeforeWrite`在每片写出前检查有效CAS租约；取得名额不代替权限、资产安全和G5对账。过期/已释放令牌不能复活，重复释放不能影响新连接。

申请与续约按相同User→Project范围锁串行，防止未提交续约跨旧TTL后被补位。P1 `G6-DOWNLOAD-001`由独立审查发现、77066旧实现真实复现第三名额；修复增加续约范围锁。增强用例使用数据库时钟、同事务连接ID与performance_schema实际scope锁等待，不以固定sleep充当正确性证据；修复后的动态结果另存检查点。

传输层每连接共享20MiB/s、最多1MiB突发桶，空闲积累不会形成无限突发；限速等待在Task/钱包事务外，取消后不再打开下一片。写deadline取当前租约截止与30秒超时的较早值。675aca复现4MiB约14ms发送及写期限超租约，修复后8c90e8通过。44261仅证明当时普通100申请2/98、基础HTTP与速率子集，不覆盖后来发现的续约窗口。

尚需补真实慢连接超时与释放、COMMIT未知、Project层独立限额矩阵、可播放大MP4多片撤权/删除竞争、完整财务不变和SDK/browser；本节不是完整下载能力验收。

当前已有G6的000078至000086本地迁移，没有部署或bootstrap装配。应用回退仍保持视频关闭，不删除G5请求、Quote、Task、输入租约、Usage、Outbox、钱包、回调、资产、权利接受、声明、上传、来源导入、删除申请、清理确认、下载操作性记录或取消命令。down均保留结构与事实；不得用回滚清账、释放未知Hold、替换原输入或恢复失效同意。关闭Goal时必须单独核对PR/main、精确源码与验收状态，不进入G7。

## 上一轮本地候选验证（已失效）

2026-09-01 的统一门禁曾形成一个本地候选，但随后独立QA、产品、Standards和Spec均发现未闭P1/P2；该候选和原SOURCE_STATE已经失效，不能用于提交、PR或验收。下列运行事实仅保留为历史回执，受后续源码影响的项目必须重新执行。

- `verify-video-gateway-vid-g6.sh` 默认 `all` 在一次性MySQL 8、内部网络和Linux race下通过，迁移最新版本为000109；执行器逐项要求顶层测试实际RUN/PASS，零匹配、SKIP或包失败均不能通过。
- 原43条明确路由及4条支撑路由共47条已接入同一VideoCommand、G3任务/资产/事件、G5财务与G4治理账本；新增支撑为临时内容兑换、两条长期读取及Project授权管理，没有建立平行视频、财务或资产账本。
- 锁定Python `openai==2.45.0`和TypeScript `openai@6.39.0`通过真实loopback HTTP、SK HMAC、临时MySQL、T2V/I2V、查询/列表、Range、删除及账单保留；外部Provider与对象存储仍为合成边界。
- 浏览器实际解码本地5秒MP4、seek至3秒并继续播放，390px视口无横向溢出；该证据只证明媒体与Range可播放性，不冒充真实Provider、MinIO或生产浏览器链。
- `go test ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff`、四个视频核心包Linux race、289个本阶段Go文件gofmt和`git diff --check`通过；562个变更/新增文本文件高置信敏感扫描0发现。
- 图片网关IMG-G6真实临时MySQL/race兼容回归通过；视频G5完整1→77迁移、重复up、保留式down/re-up、财务与Legacy Chat兼容回归通过。
- 真实Provider请求、真实Provider Key、真实钱包写入、真实用户资金、调账、测试服写入和生产操作均为0；RabbitMQ、Redis、MinIO、Bifrost视频数据面和Outbox Dispatcher保持关闭。

独立终审后的第一项整改已完成本地运行态准入：G6 HTTP Repository Ledger在queued→submitting时通过000109门闩统计原Task账本，冻结用户1、Project2、模型2；容量输家保持queued且Provider调用为0。专项真实临时MySQL/Linux race的用户、Project、模型、并发唯一赢家、取消/提交互斥及Fake完整执行均通过。其余QA/PM/Standards/Spec缺陷仍在整改，不能据此恢复候选状态。

本阶段回滚只关闭视频路由、专用装配和默认开关，不删除或覆盖任何已形成的请求、Quote、Hold、Usage、Task、事件、输入、输出、回调、审计或财务事实。VID-G7不得自动开始。

## Git规模例外

仓库Git规范原则上要求小PR；本阶段由项目负责人明确授权并要求“当前Goal创建或能够证明归属于当前Goal的唯一VID-G6 PR”，且G6的47条路由、000078—000109迁移、同一任务/资产/财务账本和统一验收证据不可拆成多个可独立合并的阶段而不破坏同源门禁。因此本次使用一个VID-G6 PR作为显式阶段例外。例外不允许混入VID-G7、部署、真实Provider/资金或其他产品改动；仍须单一精确HEAD、完整staged diff、四轴独立审查、Ready CI和普通合并，不允许force、admin或绕过分支保护。

## 最终同源本地候选（2026-09-01）

本节取代本文及所有VID-G6子合同中“开发中、待补、局部验证、上一轮候选、完整阶段未验收”等过程性状态，但不改写这些历史红灯及修复轨迹。当前冻结源码副本及SOURCE_STATE精确值统一见`docs/evidence/video-gateway-vid-g6-local-verification.json`与`video-gateway-vid-g6-source-state.json`，主合同不重复硬编码易漂移哈希。

- `verify-video-gateway-vid-g6.sh`的`all`在同一容器源码副本、一次性MySQL 8、000001→000109和Linux race下完整通过；精确耗时记录在最终本地验证回执，必需测试逐项RUN/PASS，零匹配与SKIP不能替代必需项。
- 新增运行态用户1、Project2、模型2裁决；回调额外submitted夹具仅在测试构建复用真实G5账本绕开另有专测的容量裁决，Task、Provider绑定、回调、事件和财务仍为真实实现。
- 锁定Python `openai==2.45.0`与TypeScript `openai@6.39.0`真实loopback HTTP通过；VID-G5完整迁移/财务兼容和IMG-G6 HTTP兼容均通过。
- `go test ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff`、全部G6变更Go文件gofmt、`git diff --check`与变更文件高风险凭据模式扫描通过。
- 47条本地路由及完整合同矩阵已进入最终候选；默认仍未接bootstrap，缺依赖503，不能静默降级到Fake。真实Provider、真实Key、真实钱包/用户资金/调账、MinIO、RabbitMQ、Redis、Bifrost数据面、测试服、生产及VID-G7均未运行。

当前仅剩新SOURCE_STATE下QA、PM、Standards、Spec独立终审，以及通过后精确暂存、中文提交、唯一PR、Ready CI、普通合并和main包含性核验。完成这些门禁前不得把本地候选写成已合并、已部署、生产或商业可用。
