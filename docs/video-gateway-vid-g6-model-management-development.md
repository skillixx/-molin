# VID-G6 视频模型管理开发文档（进行中）

## 功能目标与当前边界

管理员应能沿用现有模型目录的创建、编辑、详情、发布与回滚路径，维护七键视频合同。工作副本修改不影响当前有效快照，发布不能自动授予Project或Key视频资格。

共享解析器、存储、草稿读写/接管已完成局部验证；现新增[受控发布、下架和回滚](./video-gateway-vid-g6-model-publication-contract.md)，使用原发布版本表，不伪造Bifrost就绪。完整故障和并发矩阵仍未完成，局部通过不是完整模型管理验收。公开目录见[目录合同](./video-gateway-vid-g6-model-catalog-contract.md)。

## 已实现的基础

- `server/internal/modules/token_gateway/model/video_model_contract.go`：唯一七键解析器，拒绝缺项、重复键、未知键、商业用途及矛盾的商品/权益要求。
- `server/internal/modules/token_gateway/service/video_model_contract.go`：保留类型入口和`ErrVideoAccessUnavailable`分类，避免改变原HTTP准入语义。
- `server/internal/modules/token_gateway/model/token_model.go`：`VideoContractJSON`映射原`token_models.video_contract_json`；发布快照保留显式false/null/空数组并校验完整合同。非视频不得夹带视频合同。
- `server/migrations/000102_video_model_contract_draft.up.sql`：新增nullable JSON列；旧记录保持NULL，不猜测授权。CHECK限制非NULL值只能是video模型的JSON对象。
- 对应down保留数据，不删除已配置合同或历史事实。

## 独立产品核对确定的接入规则

本轮产品角色核对是工程建议，不是完整PM验收或新的商业批准。

1. 继续使用既有模型管理路径，不另建视频模型账本。
2. 写入区分“未传不修改”和“显式null或非法合同拒绝”；检查数据库原模态，防止改写或省略请求模态绕过权限。
3. 视频写入口校验`ai_gateway:model_manage`、真实管理员JWT/MFA、reason、CAS、Idempotency-Key，并在完成前复验资格。
4. reason采用G6专用加密方案；普通发布记录只保存低敏引用，不沿用旧发布流程的明文Reason。
5. 发布原子绑定草稿版本、合同摘要、前后审计和发布指针；同键异草稿冲突，重放不新增版本。默认模型唯一性须有事务或数据库保障。
6. Chat/Bifrost分支不变；视频单独验证显式Fake native async执行映射、版本、5秒规格、支持操作、合成价格和文档。不能伪造healthy Bifrost渠道或简单跳过所有路由校验。
7. 专用发布依赖未装配时失败关闭。发布只形成工程目录版本，不开启真实流量、不调用Provider、不批准价格政策。

## 测试与剩余任务

快照测试先红后绿：七键保留；缺项、重复键、商业用途及跨模态拒绝；非视频不增加空字段。`TestVideoG6ModelContractPersistenceMySQL`验证原仓储读写、MySQL JSON/CHECK、失败更新不变及Chat默认NULL。`model-contract`专项包括原七键服务测试、真实HTTP目录回归和4项旧模型列表测试。

剩余：受控写入事务、严格HTTP DTO、专用native执行证明、加密原因/幂等/审计、CAS与默认模型唯一性、管理员负向与100并发、发布/回滚故障注入、完整兼容与独立验收。上述未完成前不能标记模型管理或G6通过。

## 回滚边界

保留模型、发布、任务、资产、报价与账单事实；回退程序不删除新增合同列的数据。旧代码不使用该列，必须保持视频写入口关闭，不能把缺少视频校验的旧发布路径作为可用回退方案。
