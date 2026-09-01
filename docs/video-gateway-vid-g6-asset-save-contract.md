# VID-G6 用户资产保存：工程合同与实现进度

## 范围与当前状态

这是完整VID-G6的一部分，不是单独缩小后的阶段目标。保存已作为第30个局部入口注册，但未接bootstrap，默认无显式保存配置则503。已接入真实预占、复制计划、用户资产/事件/额度结转与原媒体删除保护；43262的12项专项通过。提交前跨期限、完整保存/删除竞争、各写点回滚与清理闭环仍待完成，不能把局部测试视为保存或G6完整验收。

接口仅接受content根路径与Idempotency-Key，无body/query。首次实际创建用户资产返回201，完成重放返回200，固定七键asset_id、video_id、request_id、user_asset_id、status、size_bytes、idempotent；X-Molin-Request-ID保留原业务请求。错误输入400、越权404、容量不足409/video_storage_capacity_exceeded、状态冲突409/video_save_conflict、依赖不可用503/video_save_unavailable。

依据：`multimodal-ai-gateway-implementation-plan.md`第24.4/24.7节及视频G6阶段合同。独立产品角色`vid_g6_g5_gate`确认以下关闭态工程范围，不代替商业、法务或真实用户授权。

## 功能合同

- 用户以content根调用`POST /api/token/video-assets/{asset_id}/save`，保存一条视频的五个交付角色：content、cover、preview、thumbnail、ordinary derived。审核副本不复制。
- 当前用户、Project、精确来源Key、实名、模型许可、内容安全和原G5结算/对账必须有效。JWT还必须保持原凭据有效。
- 新长期资产使用原`user_assets`，每次创建写`asset_events`。原生成请求、Quote、Usage、钱包和成本不变，不再收费或请求Provider。
- 服务端复制到独立长期区域，不移动原临时对象，不允许客户端指定bucket、object_key或URL。
- 保存成功后允许按原规则删除临时结果，独立长期副本和容量占用保留。保存处理中须阻止原媒体清理；删除先取得执行权时保存失败关闭。
- 同一个视频即便使用多个幂等键，也只形成一个长期用户资产；重放必须重新检查当前权限和原归属。
- 第一版长期资产不自动过期，仅用于本阶段合成策略；不能解释为真实商业永久保存承诺。
- 无有效宽限配置时，已到期、expiring、deleting、delete_failed或已接受删除意图的媒体均不能被保存复活。不臆造默认宽限小时数。

## 容量与配置

必须显式提供存储商品、权益类型/单位、模型保存许可、策略版本、用户/Project/全局容量上限及总容量告警阈值，缺失配置关闭入口。商品必须来自当前用户实际关联的存储权益及父资产，不能随意采用模型商品ID。

实际容量以五份副本的总字节计数；`user_entitlements`权益单位仅接受明确的`bytes`、十进制`GB=1000000000 bytes`或`GiB=1073741824 bytes`。按DECIMAL(18,6)向上取整，防止小文件占用被截为零；不作为模型价格或收费。失败且尚未确认清理的对象仍占预留，不可凭普通Head错误释放额度。

`VideoHTTPOptions.AssetSave`显式提供Store及`VideoAssetSavePolicy`，构造时校验并复制模型许可列表，缺失时仅关闭保存能力。容量预占复用原权益Repository，实际总容量达到告警阈值仅输出固定低敏告警，不代表完整生产容量监控已交付。

## 实现结构与下一事务步骤

- `video/saved_copy.go`：Fake服务端不可变复制。原目标独立、相同目标幂等、冲突字节拒绝、目标墓碑不复活；长期区域使用既定`ai-user-assets`。
- `service/video_asset_save_policy.go`：转存Store能力与显式策略类型、GB/GiB/bytes精确换算。
- `000088_video_asset_save_coordination`：只保存协调事实，down保留事实。三表为范围锁`ai_video_asset_save_scopes`、唯一Task转存计划`ai_video_asset_saves`、用户/Project幂等命令`ai_video_asset_save_commands`。
- 协调记录包含原Task/Request/User/Project/Key、存储权益/商品、策略版本、总字节和权益预留量、不可变五对象计划/hash、CAS版本、状态及已保存user_asset关联。不包含Prompt、Provider正文或凭据。
- 计划必须固定原五资产的ID、版本、角色、hash/大小、安全规格摘要及独立目标；服务层须重新核对实际资产树，数据库JSON长度检查不能证明计划正确或对象复制成功。

当前两阶段实现：原Task锁及G5财务/资产门禁 → 删除意图与已保存状态 → 全局/用户/Project容量及存储权益锁 → 原子预占并持久化复制计划与命令；随后在原Task锁覆盖的第二事务内按固定目标复制并确认 → 再验权限/安全/额度 → 原子创建user_asset、asset_event、保存关联及容量结转。调用总期限30秒；失败已复制对象仍归属于首阶段计划和reserved容量。不能把原生内存锁或全Mock作为数据库互斥证明。

代码为`video_asset_save_service.go`、`video_asset_save_complete.go`、`video_asset_save_capacity.go`及`video_asset_save_models.go`；复用原Asset/Event/Entitlement Repository，不新增生成财务流程。MySQL规范化JSON后先严格解码再规范编码验hash，禁止用原始JSON字节比较误拒绝合法计划。

当前000088状态为copying、copy_failed、completed、cleanup_pending、aborted，所有迁移须CAS且计划不可替换。cleanup/aborted已接入内部服务并具备局部故障/COMMIT确认丢失恢复证据；关联[长期读取](./video-gateway-vid-g6-saved-read-contract.md)验证中，仍不得凭表存在宣称完整覆盖。

## 必须补齐的删除协调

`mediaDeleteProtection`已接入保存协调表：copying/copy_failed/cleanup_pending阻止原媒体删除；completed必须通过保存Store核验长期用户资产关联、创建事件和五份目标，才允许删除原临时结果，不删除长期对象或退还容量。长期Store独立配置，不能用源Store的同名对象作证明；缺依赖时失败关闭。全并发竞争与cleanup_pending/aborted终态恢复仍待验证。

## 未发布保存的清理恢复

`CleanupVideoAssetSave`是fixture用途限定的内部恢复方法，不注册HTTP、不启动G7 Worker。仅在未发布且无匹配用户资产时，根据原Source或存储权益/父资产已到期的事实写入cleanup_pending。网络错误、撤权、暂停或配置变化本身不触发销毁；保全、争议、隔离和待对账仍失败关闭。

000089在原协调行增加清理策略版本、原因、资格时刻、开始/完成时刻和目标摘要。首次意图必须由SQL核验真实到期实体，资格时刻不得凭空捏造；意图一旦接受不可变，之后不因权益续期改写历史授权。只删除原五目标，包括尚未创建的目标也须建立不可复活标记，绝不删除原视频、审核副本或completed副本。

全部目标确认后，原子释放原quota_reserved、向原存储父资产追加`video_save_aborted`事件并CAS为aborted。权益可已到期，但归属、单位、原预占必须一致。重放检查目标标记、摘要及唯一事件，不重复释放。删除/确认失败或完成写失败保留意图和预占；真实COMMIT成功但确认丢失时从完成事实恢复。

迟到Copy测试在旧保存事务取消后，让已接受复制继续使用Background尝试落地；仍被目标标记拒绝，不只是依赖调用context取消。该证明只适用于当前Fake/同步边界，不能冒称真实异步存储验证。

## 已执行与未执行证据

- 复制能力缺失红测7434ff；实现后发现长期区域尚未纳入Fake删除合法引用，1730d8失败；补明确长期zone和规范路径后视频包5938ee通过。
- 100并发相同目标复制和不同来源冲突测试598dbb通过；原临时对象删除后仍能读取长期副本。
- 容量换算缺失红测ee6ced，补精确单位与向上取整后36990通过。
- 32462初始4项基础回归通过，但未执行协调INSERT。新增真实正例后31811复现1054：触发器误读Task的计费/交付字段；改为Request字段后61134的5项基础及真实INSERT正反例通过Linux race，schema88。错Key、错存储商品、提前completed均被1644拒绝。
- 11084保存10项专项通过：HTTP、重放、原结果删除后长期保留、分离Store与影子对象、部分复制失败恢复、财务整行不变。43262加强12项通过，含100个不同幂等键同视频只创建一个长期资产/一次容量结转，以及用户/Project/全局/权益容量分别拒绝且不触发复制。
- 6710已验证最后写入跨source/entitlement/JWT期限时回滚完成事务。88698清理8项专项通过，含删除/确认/数据库完成写失败恢复、保全/匹配或completed资产保护、迟到Copy、SQL伪过期资格拒绝及真实COMMIT先成功再丢确认恢复；不将UPDATE失败称为COMMIT未知。
- 仍待完成：清理后同Task发起新的保存尝试、保存发布本身各写点及COMMIT未知、跨Task容量竞争、更多保存/删除/保护并发、长期资产读取。上述局部范围不能替代完整保存或G6验收。
- 长期读取暴露DATETIME(0)开始时间舍入问题：91884实际超前327ms。保存现在仅对立即生效StartedAt向下取秒；40252确定性反例转绿，其他事件时刻及读侧未来开始拒绝不变。这不是改变业务保留期限。
- 当前仅本地/合成环境；真实Provider、Key、钱包资金、费用、共享测试服务器及生产操作均为0。

## 回滚边界

入口只在独立本机测试注册，未完成前不接bootstrap或部署。应用回滚必须关闭保存及依赖保存状态的媒体删除/清理，或使用兼容当前协调结构的版本；已有计划、容量占用、用户资产及复制目标关联均保留。不得DROP协调表、删除用户资产、释放尚有对象的预留，或恢复已删除对象。完整G6及Git门禁未通过前不提交、推送、建PR或合并。

000091增量改变了原单尝试回滚边界：当前协调表通过原public_id承载多次历史尝试，旧键固定指旧尝试，新键经重新准入后才可产生后继；旧aborted行与全部墓碑不变。详见[新尝试合同](./video-gateway-vid-g6-save-reattempt-contract.md)。一旦存在多个尝试，禁止只关闭Save就运行旧版媒体DELETE；必须同时关闭依赖保存状态的删除/清理，或使用兼容新结构的版本。当前仅局部服务通过，100并发及历史迁移继续验证。
