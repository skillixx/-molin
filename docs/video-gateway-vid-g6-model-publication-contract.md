# VID-G6 受控视频模型发布合同（开发中）

## 功能范围

视频模型沿用现有发布、下架和回滚URL，由专用管理处理器接管：

- POST `/api/admin/token/models/{id}/publish`
- POST `/api/admin/token/models/{id}/unpublish`
- POST `/api/admin/token/models/{id}/rollback`

请求需要管理员JWT/MFA、`ai_gateway:model_manage`、Idempotency-Key和严格JSON。发布/下架恰好`{version_no,reason}`；回滚增加`target_version_no`。reason使用模型动作独立AES-GCM AAD，不写入原发布Reason明文；原发布Reason只保存低敏命令引用。

## 发布规则

- 模型、草稿状态、价格版本及SKU在同一事务锁定；草稿version和实际摘要必须一致。
- 显式配置仅接受具体FakeAsyncVideoAdapter、`fake-native-async`、Runware合同`RUNWARE_RUNWAY_GEN4_5_TASKUUID_5S`和`runway:1@2`；缺依赖或模型映射失败关闭。
- 发布重新验证七键合同、可见范围、两份健康静态文档，以及合同中每个operation的5秒/1280x720/16:9/24fps/无音频合成价格。
- 发布快照写回原`ai_model_release_versions`，增加内部`video_execution`：schema/purpose/driver/provider_contract/provider_model/config_version/price_version_id及每个operation的价格快照摘要。视频快照不使用ChannelID或UpstreamModel冒充Bifrost就绪。
- 全局publication guard串行所有视频模型发布动作，再检查其他active默认模型；不能两个模型同时成为默认。
- 发布首次201、原键重放200；下架200；回滚基于历史视频版本重新使用当前native配置和当前价格创建新版本，首次201、重放200。
- 下架保留release_version_no与历史版本。发布、下架和回滚各使草稿管理version递增。
- 最后复验管理员资格及价格期限；Provider Submit始终为0，且不创建Quote/Hold、授权、钱包、Usage或Outbox。

## 兼容与关闭策略

旧G5 handler/service/repository均识别视频身份并拒绝旧Bifrost发布、下架、回滚旁路。Chat仍走原Bifrost健康路由与发布规则。105迁移只扩展原模型命令动作并增加单行协调锁，不建立平行模型账本；down保留发布、命令、原因和审计事实。

能力需显式注入ModelDrafts、ModelPublishing、原因密钥和真实JWT认证器；bootstrap尚未装配。Fake发布通过不表示真实Runware、Bifrost、存储、生产或商业可用。

## 当前测试与剩余范围

model-publication专项验证发布→下架→拒绝无native旧版本回滚→受控回滚、各动作重放、发布快照、低敏Reason、原财务不变、Submit=0及旧G5写旁路。终审整改进一步覆盖发布事务真实COMMIT确认丢失幂等恢复、命令末尾SQL失败全事务回滚、权限deny、MFA过期，以及两个真实视频模型/价格/草稿同时声明default_model=true时全局guard一胜一冲突。当前schema恢复矩阵副本SHA-256为`1b25271d58d2b857f9546e7b778a756512bfa21bc930ccf2c3e8fedc4ad33927`，默认模型并发副本为`7c49adb40344090276ac347076c41d670b611a738c3c5b218a7a8d1bf8bc9e28`；77号旧Chat G5发布全部子用例通过。

仍需把密文/审计篡改和完整原应用装配纳入最终同源全量及独立复核；当前切片不能单独签完整模型管理或G6通过。

回滚仅关闭专用处理器，不删除任何模型、发布、命令、原因、价格或财务事实；禁止回退到可写视频的旧G5路径。
