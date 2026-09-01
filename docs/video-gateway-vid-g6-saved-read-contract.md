# VID-G6 长期视频副本读取合同

## 功能与边界

本功能让用户读取已保存到个人存储的独立视频副本。原生成结果自然到期不应使已保存副本失效；通过原媒体删除流程清除后，原 `/v1` 查询返回404，不恢复临时对象。仅自然到期时原Job的公开状态不由本功能证明，仍须按原v1合同另行验收。已完成保存的副本使用独立入口读取。

这是VID-G6资产生命周期范围内的两个配套读取入口，不是原43条明确路由的原文，也不是另建视频账本。完整清单为原43条、临时资产内容兑换1条、本节长期读取2条，共46条。当前独立注册函数32条只供本地验收，尚未接入bootstrap，不代表完整阶段通过。

第一版无自动到期的长期资产仅为已批准的非商业合成夹具策略，不能据此承诺真实用户永久保存。真实Provider、真实资金、共享测试服和生产均不在本次执行范围。

## 接口参考

| 方法与路径 | 用途 | 成功响应 |
|---|---|---|
| `GET /api/token/video-saved-assets/{user_asset_id}/{role}/download-url` | 签发短效本地兑换路径 | 平台Envelope，data为`asset_id/download_url/expires_at` |
| `GET /api/token/video-saved-assets/{user_asset_id}/{role}/content` | 凭原身份及短效签名读取独立副本 | 原始媒体，复用200/206/416与Range传输 |

`user_asset_id`来自保存接口的`user_asset_id`，是规范的正整数十进制字符串，不接受前导零、符号或0。角色仅允许`content/cover/preview/thumbnail/derived`，不开放审核副本。

签发不接受查询参数。兑换恰好接受单值`expires`和`signature`；未知参数、重复参数或非法编码返回400。`expires`为规范Unix秒字符串，`signature`为64位小写十六进制。HEAD不支持，返回405。

签发响应的`asset_id`为UserAsset编号的字符串，不是原`ai_gateway_assets.public_id`。保存响应原有7字段不变。URL只含相对路径、到期秒数与签名，不含内部bucket、object_key、Provider地址或凭据。需要把相对路径接到同一受信API基址，不能当成匿名公网下载链接。

两个入口均接受当前有效JWT或Project SK，但必须对应原保存任务的同用户、Project及精确Key归属。JWT不能读取原SK专属资源；其他Key也不能借用户相同绕过。无认证401，越权404；当前权益不可用403，保存证明或保全冲突409，缺配置或存储不可用使用既有低敏错误。客户端不得自动重试为另一身份。

## 读取准入与完整性

1. 当前用户、Project、Key/JWT、模型、权限与原G5执行/计费/交付事实有效。
2. 保存计划为completed，关联唯一原UserAsset及创建事件，原Task/Request/User/Project/Key一致。
3. 当前存储商品仍为active storage且角色允许使用；原父资产、权益及已保存资产当前有效。
4. 权益类型与单位匹配保存时冻结值；同权益所有completed保存的合计容量不能超过已占用容量，混合类型/单位失败关闭。
5. 原六资产树、安全事实与五份保存计划的hash/规格/父子关系一致。仅允许可证明的原临时到期或原删除流程改变生命周期；保全、争议、隔离继续拒绝。
6. 兑换签名绑定UserAsset、角色、原主体、保存版本、目标位置、hash、大小和期限。临时与长期签名域隔离，不可互换。
7. 下载复用用户2路/Project4路租约和每片最多1MiB的读取器，不设第二套容量。当前准入、签名及名额须先于对象存储访问。
8. 每次有界读取使用保存Store核验五份独立目标；有任一目标缺失，不返回已缓冲媒体。不能用临时Store中同名对象替代。

URL最长15分钟，受当前JWT、Key、存储权益与资产更短期限约束。已有能力期限只能收紧，不能随重放续期。读取后的权限与时效再次检查；读取过程中撤权、过期不能继续获得下一片。

## 开发结构与数据

- `handler/video_saved_read_handler.go`：严格路径/查询、认证及平台错误合同。
- `service/video_saved_read_service.go`：原账本归属、保存证明、当前存储资格和专用HMAC。
- `service/video_content_service.go`：临时与长期共用的租约、快照、分片及明确Store参数。
- `service/video_asset_save_complete.go`：保存证明的数据库记录和独立目标验证。
- `000090_video_saved_entitlement_type`：保存时冻结`storage_entitlement_type`；INSERT核对原权益，UPDATE不可改变，包括大小写漂移。旧行保留NULL，不从当前权益推测历史类型；缺原事实时读取关闭。

读取不改原请求、报价、生成账单、钱包、资产hash/规格或保存容量。下载租约是运行协调记录，可以创建、续约及释放，不能把它们当作业务财务写入。

## 如何运行隔离验收

前提：当前VID-G6工作树、固定digest的Go/MySQL镜像和已核验Goal专属编译缓存。运行器创建一次性内部网络和临时数据库，无宿主端口；外部边界Fake，真实认证和事务不替换。

```powershell
$env:VIDEO_GATEWAY_G6_ISOLATED_MYSQL_APPROVED='YES'
$env:VIDEO_GATEWAY_G6_TEST_FOCUS='saved-read'
& 'C:\Program Files\Git\bin\bash.exe' infra/scripts/verify-video-gateway-vid-g6.sh
```

通过必须同时具备：schema90成功、指定测试实际RUN/PASS、无SKIP、容器测试退出0、运行器整体退出0。仅`REQUIRED_TESTS=PASS`不能覆盖正则额外选中的失败测试。记录输出的源码复制hash；源码变化后旧结果不得冒充当前验证。

当前待复验：共享限流零Store、签名/存储撤权零Store、独立Store丢失、JWT真实HTTP流中撤销、元数据漂移、完整无业务写入矩阵。历史局部通过和未完成项详见VID-G6证据目录，本文不签完整G6验收。

## 局部缺陷台账

| 编号 | 级别 | 已观察事实 | 当前处理 |
|---|---|---|---|
| G6-SAVED-READ-001 | P2 | 44560第三路被限流却先执行对象Head | 拆开数据库证明和对象验证，当前资格/签名/名额前置；40252对应测试PASS，最终源码复验与独立关闭待完成 |
| G6-SAVED-READ-002 | P2 | 91884实际开始时间比保存返回超前327ms；初步误定位保存准入，实际是保存后签发失败 | 新UserAsset的立即生效StartedAt向下取秒，读侧未来时间仍拒绝；40252确定性测试PASS，最终复验待完成 |
| G6-SAVED-READ-003 | P2 | 40252实际注入三处数据库连接错误，错误折叠为403/409 | 仅真实记录缺失映射业务拒绝，其余依赖错误为503；修复后复验中 |

以上不是完整G6的缺陷统计。任何局部关闭均需绑定实际测试和独立复核；数据库故障、误拒绝或测试失败不能用重试绿色代替原因说明。

## 回滚与排查

关闭读取路由或撤下显式依赖，不删除保存计划、原财务、容量结转、用户资产或副本。migration90 down保留历史事实，不用回填猜测绕过类型校验。

404先检查当前身份和原Key归属，不暴露资源是否存在；403检查原存储权益及当前角色；409检查保全、完整保存记录与五目标证明；503检查显式下载secret/保存Store依赖。只记录低敏分类，不记录Token、签名URL或存储位置。

相关：[阶段总合同](./video-gateway-vid-g6-http-project-sk-contract.md)、[保存合同](./video-gateway-vid-g6-asset-save-contract.md)、[需求矩阵](./evidence/video-gateway-vid-g6-requirements.json)。
