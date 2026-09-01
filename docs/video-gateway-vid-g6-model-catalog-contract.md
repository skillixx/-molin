# VID-G6 视频模型公开目录合同

## 功能与使用角色

`GET /api/token/models?modality=video` 向登录用户和持 Project SK 的调用方展示已发布视频模型，新增明确的 `capability="video.generate"` 与 `supported_operations`。客户端必须按操作数组判断文生视频、图生视频，不从 `modality` 猜测。仍使用 D-95 `{items,page,page_size,total}`，不是 Videos Job 的游标分页。

本增量没有产品前端页面、Provider请求或部署。它不是完整G6验收，也未完成后台七键视频合同的编辑/发布、Key授权管理或生成资源准入。

## 业务规则

- 视频目录必须同时有当前 active 模型、非零发布版本、已生效发布时间和该版本的 active 发布事实。
- 展示名、厂商、说明、文档、可见范围、商品要求和支持操作从同一发布快照读取。工作副本修改不提前改变公开内容。
- 已发布视频的工作副本改成Chat/Image时隐藏该条目，不允许通过切换查询模态绕过发布隔离。
- 缺失、损坏、未来生效、已退役或身份不匹配的快照不进入列表；内部七键视频合同依然执行既有严格解析。
- Project SK 使用当前视频准入检查：用户/Project/Key、实名、显式视频位、模型scope、Project模型授权、IAM、发布状态及权益。专用依赖缺失时不展示视频，不套用仅支持Chat的历史检查。
- JWT目录沿用用户可见性展示规则，展示不等于允许生成；实际生成仍必须指定或解析Project并经过全部准入、权利声明和财务门禁。
- 公开条目不返回渠道、上游、商品、完整视频资格合同或快照原文。
- Chat/Image条目保持已有字段集合；`/v1/models` 仍沿用既有仅Chat的兼容行为。

## 开发结构

| 文件 | 职责 |
|---|---|
| `repository/token_model_repo.go` | 单次关联读取当前发布身份与有效快照；各模态查询均防止视频草稿的模态漂移旁路 |
| `service/video_catalog.go` | 校验快照身份、视频能力、七键合同并生成公开投影 |
| `service/catalog_service.go` | 已发布可见性过滤后分页；显式注入并复用视频准入 |
| `handler/model_handler.go` | SK按模态使用对应权限检查，过滤后重算D-95总数 |
| `dto/model_dto.go` | 仅视频增加两个可选公开字段 |
| `module.go` | 注入只读视频资格服务，不注册生成路由或启动后台任务 |

以上路径均位于 `server/internal/modules/token_gateway/`。使用既有 `token_models`、`ai_model_release_versions`、身份与授权表；本目录增量不增加表，也不创建报价、钱包、Task或Outbox。

## 测试与回滚边界

`service/video_catalog_http_mysql_test.go` 使用真实临时MySQL、真实SK哈希验证和loopback HTTP。验证公开字段、草稿不泄露、快照缺项/未来/退役、撤销Key视频位/Project授权/模型scope/实名、缺依赖关闭和既有图片字段兼容。专项同时运行历史 `/v1/models` 四项测试。运行脚本焦点为 `catalog`，不得把SKIP或零匹配当通过。

本实现仍需纳入最终源码冻结、完整Chat/Image回归及独立门禁。回滚只能回滚代码或关闭视频目录的专用依赖，不修改或删除发布、任务、资产和财务历史。不能通过回滚把未发布视频草稿重新暴露为已发布能力。
