# VID-G6 Project SK显式视频能力合同（开发中）

## 功能

`POST /api/token/projects/{project_id}/keys`新增`video_generate_allowed`布尔字段，响应及`GET .../keys`列表回显该字段。旧请求省略时为false；数据库78号迁移的默认值仍为0，不从scope=all、模型发布或Project创建自动继承。

显式开启必须同时满足：

- scope_mode=allowlist，不能使用all或legacy_all。
- model_codes至少包含一个当前active、已发布且发布快照具备video.generate及合法七键合同的视频模型。
- 每个视频模型均存在同User/Project的active `ai_project_model_capability_grants`。
- 模型目录指针及其active release的published_at都不晚于当前UTC时间；未来发布不能提前配置后自动生效。
- 所有模型仍通过已发布可见性检查；Chat/Image原范围保持。

预检后，仓储在签发事务内重新锁定模型、发布快照和Project grant，再写API Key、scope和审计。授权撤销与签发竞争只能有一个先提交；不能留下越权视频Key。

`POST .../keys/{key_id}/rotate`从数据库锁定的旧Key及锁定scope重建新Key，只保留新生成的prefix/hash；名称、计费模式、来源、scope模式、期限、Project归属和视频能力均不相信事务外对象。非Project普通SK失败关闭。轮换重新校验scope及Project grant；失败不创建新Key、不吊销旧Key。false永不升级为true，true也不能在授权撤销后继续轮换。

完整Secret仍只在首次签发或成功轮换响应返回，列表不返回Secret或hash。审计摘要只记录scope、公开模型代码、到期时间和能力布尔值，不记录Secret。

## 代码与兼容

- `auth/model/api_key.go`映射既有`video_generate_allowed`列，普通JSON禁止直接输出，由ProjectKeyView显式回显。
- `service/project_service.go`处理输入、视图、预检、审计和轮换继承。
- `repository/g2_repository.go`执行事务内当前读及锁定校验。
- `handler/project_handler.go`使用严格JSON，拒绝未知、大小写别名及重复字段。
- `catalog_service.go`对视频使用发布快照可见性，不以未发布工作副本决定签发。

旧77号schema没有该列时，仓储仅允许false能力并显式省略新列，保留历史Chat测试；true失败关闭。最新105号schema实际持久化并读取该列。

## 验收边界

project-key专项验证：true签发、真实HMAC认证、列表不泄露Secret、未来模型/release拒绝且Key/scope/audit零新增、grant撤销后失败且无新Key、恢复后轮换、事务外all/空scope/放宽期限篡改无效、非Project旧Key拒绝、旧Key仍false、all和隐式视频scope拒绝、大小写别名拒绝，以及原准入19项矩阵和三个Chat Key单测。外部Provider、钱包和测试服务器写入均为0。

Project视频模型grant和[视频Key持久化幂等](./video-gateway-vid-g6-project-key-idempotency-contract.md)已完成局部实现。仍需COMMIT未知、全部写点故障和最终阶段验收，因此不能标记完整Key管理或VID-G6通过。

## 回滚

回退时关闭true签发能力，不清除已存在Key、scope、审计或grant。不能把true改为false伪装撤销；正式撤销仍使用原Key吊销和Project grant状态事实。
