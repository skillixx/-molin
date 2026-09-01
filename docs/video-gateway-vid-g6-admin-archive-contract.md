# VID-G6 归档重试HTTP合同（开发与验证中）

## 功能与入口

`POST /api/admin/token/video-tasks/{task_id}/archive-retry`恢复原视频任务的受控归档过程，不重新生成。只接受管理员JWT、有效手机/邮箱MFA及`ai_gateway:task_manage`；不接受Project SK替代管理身份。默认关闭且未接bootstrap，没有产品前端页面。

依赖必须显式提供原因专用加密器、只含Name/OpenContent的原内容读取器、探测、审核、标识、支持不可变写入和代次检查的Store及服务端对象定位器。本阶段只允许fake-native-async，不接真实Provider、MinIO或Bifrost视频数据面，不静默创建Fake依赖。

## 请求与响应

必须单值`Idempotency-Key`（16—128字节），UTF-8 JSON仅允许：

```json
{"reason":"核对原任务后恢复归档","version_no":12}
```

拒绝query、编码正文、未知/重复字段、非法UTF-8、0或越界版本。reason去首尾空白后1—256字符、不超过1024字节，无控制字符。客户端不能指定phase、Provider ID、URL、对象位置、围栏代次或令牌。

响应七字段：command_id、task_id、request_id、status、execution_status、version_no、idempotent。`X-Molin-Request-ID`始终是原业务请求。HTTP202/running仅表示已有管理操作在进行；HTTP200/completed表示本次归档操作已安全完成，HTTP200/unknown表示原尝试需要核对。它们不是OpenAI Video Job状态，也不代表已结算或可交付。

所有状态下重放均重新校验当前管理员、原目标/版本/原因密文及前后审计。同键同意图不再次执行媒体IO；已unknown的历史回执不被后续成功覆盖。需要再次恢复时，另用当前Task版本和新键发起明确的新命令，仍只处理原Task与原Provider结果。

## 归档与持久事务

1. 当前授权、原Task/Request/owner/Key/Provider绑定与成功成本事实验证。
2. 从原资产和状态推导起点；原用户/Key停用不授予用户新权限，但不阻止管理员履行原任务恢复。
3. 前审计、原Task围栏认领和running命令同事务提交。
4. 事务外运行已验证执行器，操作前后校验身份/围栏，固定对象不可覆盖，旧代次写回拒绝。
5. 最终六角色安全、对象/hash/大小、原计量与时长校验通过后，将原Task成功、围栏释放、后审计和completed命令同事务提交；最后再次鉴权和检查期限。

prepared/running不能冒充完成。准备事务重试会清空ORM查询对象，避免旧自增ID漏读同键赢家；完整审计在成功事务中以FOR SHARE锁读，不等成功提交后才发现异常。外部执行不进入数据库重试闭包。

000100增加`ai_video_admin_archive_commands`，引用原Task/Request/owner/Key，冻结原版本、Provider绑定摘要、归档代次、起始phase、原因AES-GCM、执行期限和审计引用。running/version1只能变成completed或unknown/version2；每Task最多一个running命令。完成回执不可UPDATE/DELETE，不建立第二套视频或财务账本。

## 失败与安全事实

媒体确认失败等未知结果保留unknown回执，将有效旧围栏下的执行中任务安全转为pending_reconcile，复用原G5创建待核对/Outbox事实，再退让围栏；不会在HTTP中退款、扣费或重新抓取来做财务补偿。失去旧证明或代次已被接管时，不改新代次任务。

明确审核拒绝保留原资产rejected/quarantined及对象位置。只有原Task真正处于moderating或labeling时才按原G5约束产生对应失败来源；原pending_reconcile不会伪装成这些阶段来制造退款依据。已有未知命令始终保留，输入租约仍等待原财务与安全条件闭合。

真实failed/cancelled/expired不复活；已有隔离、保全、争议、删除、过期或Provider冲突拒绝新恢复。当前0/1/6资产集合有受控恢复路径，损坏的部分SQL资产集合拒绝，不能覆盖原事实强行补齐。

## 测试与当前边界

默认关闭测试先404、注册后503；ORM映射测试曾捕获私有嵌入字段导致id缺失，修复后验证主键/归属/密文/审计列完整映射。ORM导出别名仅为映射需要，不是公开响应DTO。

28140首批7项通过（service 53.731秒），复制树`e04fc5433905a002f0f1d5b4ef84aa613ed65bdc557d0b0b6e301007d68d4930`，不覆盖后来事务主键复用与审计锁读修复。35281修复批7项通过（service 55.465秒），复制树`bbdbfbca3a1a0d54e8098ecbf4ff235c47b39413962f36b853e43c01523b2731`，覆盖T2V/I2V实际HTTP、停用主体管理、同键零重复媒体读取，以及I2V Head失败→unknown/pending→新命令恢复，旧命令和资金事实保持。schema100与Linux race通过。

后续新增正常/pending安全拒绝及原失败来源验证。终审整改进一步覆盖100同键并发与最终completed事务COMMIT确认丢失：一次完整归档固定三阶段内容读取，100请求仍只形成一条命令/两条审计和一套媒体IO；提交确认丢失由最外层读取已提交事实恢复，后续显式重放不再执行媒体IO。权限/MFA时效、审计篡改和最终完整异常矩阵仍随统一全量复核。

92020安全收口批3项全部RUN/PASS、无SKIP，schema100/Linux race通过，service 28.677秒。其中正常/pending安全拒绝7.89秒、HTTP成功/Head失败恢复12.92秒、最终权限到期回滚6.77秒；复制树SHA256为`204a534bdc8034fb4b28aa22251126f73fa93443b3ce3d6fa9539e73da7a4c8f`。独立工程确认本次失败来源、无退款/不释放输入租约及原G5核对路径没有新增确定问题，但完整阶段未验收。

最新归档恢复聚焦批全部RUN/PASS、无SKIP，schema109/Linux race通过，复制树SHA256为`ad3ebbf4a83f2ce43cd70bbecf9021b84336a93875e204cb41be2d67f23b4572`。已修复的准备事务主键复用和审计核验顺序仍由既有专项覆盖；该切片不替代最终SOURCE_STATE和四轴验收。

## 回滚边界

关闭归档路由和依赖；down保留命令、围栏、TaskEvent、资产、安全结论和财务事实。不得自动清账、覆盖旧unknown、删除媒体、回退状态或释放未知资金。当前无共享环境部署、真实调用或Git提交，不进入G7。
