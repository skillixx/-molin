# VID-G6 视频生成预算准入合同（开发中）

## 权威账本与事务

G6 HTTP复用G4 `ai_budget_policies`、`ai_budget_overrides`和`ai_budget_reservations`，覆盖Project与Project SK日/月预算；不再使用旧`ai_projects.monthly_budget`统计作为HTTP预算权威。旧G5内部构造器保持原兼容行为，只有`NewVideoHTTPService`显式装配预算组件。

`ReserveBudgetTx`使用调用方G5事务与同一连接，内部嵌套只建立SAVEPOINT。Request、Quote消费、预算预留、钱包Hold、Task/Input、Payload、Event及Outbox任一失败均整体回滚；queued容量拒绝同样回滚预算。

预算策略锁取得后才调用视频注入时钟，统一转换为UTC，再按Project timezone计算日/月账期和24小时到期，避免跨本地午夜/月界等待记入旧周期。非法timezone及SQL错误保留底层错误链，对外稳定503。

disabled不创建预算预留；soft形成预留与阈值事实但不拒绝；hard在已结算与held合计超过上限时返回HTTP429/code42920/type`budget_limit_exceeded`。等于上限允许，Project与API Key策略同时适用。

## 生命周期

钱包取消释放、明确失败释放和成功结算均在原财务事务内调用`SyncBudgetFromRequestTx`。预算状态随原`AIRequest.billing_status`同步为released或settled；缺少预算预留时为兼容no-op。同步失败会回滚钱包、Request、预算和事件，不调用Provider。

## 当前证据和边界

schema109预算专项验证Project hard 0.49拒绝、0.50成功、disabled、API Key hard/soft、同键重放唯一、非UTC时钟expiry/released_at及100并发唯一0.50预算槽位。成功结算、Provider明确失败释放和预算后置故障三个子测试实际RUN/PASS。终审整改进一步验证Asia/Shanghai本地月末前后daily/monthly周期切换、同新周期拒绝，以及完整生成事务真实提交后确认丢失时原键恢复唯一Budget/Request/Task/Hold；最新副本SHA-256为`14d1421543c20cdc9fd984ebfee4faeb6dc894dda722c40172645b2d9ca83ecd`。

仍需把补偿恢复和跨周期锁等待纳入最终统一审查；本合同不代表真实资金、生产预算或商业开放。
