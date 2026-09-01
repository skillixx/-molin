# VID-G6 受控视频模型草稿合同（开发中）

## 功能与接口

具备`ai_gateway:model_manage`且管理员JWT/MFA有效的操作者，可创建或整体替换视频模型工作副本。复用`token_models`和既有管理URL；保存不发布、不改变发布快照、不授予Project/Key权限，也不调用Provider。

- `POST /api/admin/token/models`：新草稿，version_no=0，首次201，重放200。
- `PATCH /api/admin/token/models/{id}`：已受控草稿，version_no为当前版本，成功200并递增。
- 两者需要Idempotency-Key和严格UTF-8 JSON，不接受query或Content-Encoding。
- 顶层恰好`version_no`、`reason`、`video_definition`三个字段；定义是整体替换，不是遗漏字段的默认合并。

video_definition必须显式包含15字段：logical_model_code、display_name、provider_name、description、video_contract、product_id、intro_url、docs_url、quick_start_url、docs_url_health_status、quick_start_url_health_status、visible_scope、group_ids、group_roles、role_codes。

只有description、product_id及三个URL可null；集合显式[]，scope为all/groups/roles。video_contract仍是完整七键，更新不可改模型代码。文档URL拒绝userinfo、query和fragment，不保存带凭据/签名的能力URL。

成功data包含model_id、version_no、release_version_no、video_definition、idempotent。响应no-store，不回显reason、密文、nonce、原因摘要或密钥。

## 核心规则

- 身份来自真实JWT，禁止客户端actor/checker或Project SK；事务开始和完成前复验当前权限与MFA。
- 同actor/action/key对应一个不可变命令；异定义、原因或版本409。create与update命名空间分离。
- 模型工作副本、版本围栏、命令和前后审计同事务；更新比较version及实际模型摘要，旁路漂移冲突。
- 原因采用专用AES-GCM和模型/动作独立AAD。重放先验证密文与原审计，再比较意图，原因不进入普通字段。
- JSON摘要保留整数精度，MySQL键序变化不造成误冲突，超过2^53的相邻版本不折叠。
- 旧Create/Update不允许写视频；旧Delete锁行判断视频身份并稳定拒绝，包括历史无引用草稿。非视频仍按原权限执行。

## 开发结构

代码位于`server/internal/modules/token_gateway/`：

- service/video_model_draft.go：定义校验、事务、CAS、幂等和审计。
- service/video_admin_reason.go：独立模型AAD，不改变旧任务/资产域。
- handler/video_model_draft_handler.go：严格三字段命令和15字段定义。
- handler/video_model_dispatch.go、model_handler.go：同一URL按video_definition分派。
- route.go：可选注入视频处理器；非视频分支仍必须有原token:manage。

103迁移位于server/migrations：ai_video_model_draft_states保存原模型版本/摘要；ai_video_model_draft_commands保存不可变命令、原因信封、结果及审计引用，不是平行模型真相源。命令禁止UPDATE/DELETE，状态必须递增，down保留事实。

## 关闭策略、测试与剩余范围

只有显式注入ModelDrafts选项、专用原因密钥及真实JWT认证器才能开放，bootstrap尚未装配。只在本机HTTP和一次性MySQL测试，不降级为恒成功Mock。

model-draft专项覆盖创建、100并发重放、更新/陈旧CAS、大小写/重复/未知字段、定义遗漏、SK拒绝、撤权重放拒绝、原财务事实不变、AAD隔离及旧删除边界。首轮结果不覆盖之后的完整定义修复，须按最终源码回执判断。

已增加下节的详情及接管实现，尚未完成原应用完整装配矩阵、发布/回滚/下架、默认模型唯一性、全部故障及多管理员并发。不能签完整模型管理或G6通过。

回滚仅关闭可选管理处理器，不删除模型、版本、命令、原因和审计。新模型为inactive，更新不改release指针，禁止借旧CRUD绕过门禁。

## 草稿详情与历史接管增量

`GET /api/admin/token/models/{id}?view=video_draft`要求当前管理员JWT/MFA及model_manage。只读返回model_id、version_no、release_version_no、managed、source_sha256、video_definition、redacted_fields七字段，带no-store。显式view才分派新视图；无关有效query仍沿用旧详情，非视频写权限不扩大。

已受控草稿验证实际模型摘要与状态围栏，返回当前version及source_sha256=null。未受控历史模型返回version0及原始状态摘要；读取不自动创建状态、命令、合同或发布。历史不安全URL和非法合同不回显原文，通过redacted_fields标记；原值仍进入源摘要，不改写数据库。

接管使用原PATCH路径，顶层除原三个字段外必须增加source_sha256，version_no=0。管理员显式提交完整合法定义，服务在模型锁内验证：摘要仍一致、没有状态围栏、没有历史草稿命令。摘要包括原模型ID、配置快照、排序、发布指针、时间及操作者字段，期间变化返回409。接管建立version1，保留原模型ID、状态及发布指针；不是模型重新发布。

104迁移为原命令增加可空source_sha256，仅update/version0允许且必须提供；同一摘要进入命令及前后审计。接管指纹额外绑定源摘要，既有普通创建/更新指纹不变。旧键重放原结果，新键不能重复接管已受控草稿；撤权后读取或重放均拒绝。
