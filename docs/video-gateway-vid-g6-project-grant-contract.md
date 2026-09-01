# VID-G6 Project视频模型授权管理合同（开发中）

## 接口与角色

`POST /api/admin/token/video-project-grants`由管理员JWT/MFA及`ai_gateway:model_manage`保护。只维护现有`ai_project_model_capability_grants`，不创建平行权限账本。

请求需要Idempotency-Key，严格JSON恰好五字段：

- action：grant或revoke。
- project_id：目标Project。
- model：已发布视频逻辑模型代码。
- version_no：首次grant为0；后续必须等于当前grant版本。
- reason：1—256个Unicode字符，使用Project/action/model独立AES-GCM AAD。

响应固定project_id、model、status、version_no、idempotent，不回显owner、reason、密文或内部ID。

## 业务与事务规则

- 操作者来自JWT，客户端不能提交actor/checker/owner/capability。
- 首次grant创建active/version1；revoke和重新grant每次version+1。相反当前状态、旧版本和同键异意图返回409。
- 同actor/action/key只有一个不可变命令；同键100并发只形成一个授权事实和一对前后审计。
- 事务先预授权，再预读owner并按用户ID排序锁定操作者与Project owner，之后锁Project、已发布模型和grant；锁后重验owner，避免跨管理员/Project锁环或归属漂移。
- grant要求Project、owner、模型和发布合同当前有效；owner由Project数据库事实确定。
- revoke是失败关闭动作：Project、owner或模型已经停用时仍允许撤销，不能要求先恢复准入。
- 命令、加密原因、原grant变更及前后审计同事务；首次写后重读完整命令，核对module/target审计归属、信封、解密原因、规范结果摘要和强类型结果，再末尾复验当前管理员权限/MFA。
- 不修改Key、Quote、Hold、钱包、Usage、Outbox、任务或Provider。

## 数据库和代码

- `service/video_project_grant.go`：CAS、幂等、锁序、授权事实与审计。
- `handler/video_project_grant_handler.go`：严格请求与当前JWT。
- `video_admin_route.go`：显式注册单一管理入口；本地视频注册数由46增至47。
- `service/video_admin_reason.go`：Project grant独立原因AAD。
- 106迁移新增不可变`ai_video_project_grant_commands`，关联原Project、操作者和前后审计；SQL约束结果对象、摘要格式、key版本、12字节nonce、密文及原因长度。触发器禁止UPDATE/DELETE；down保留事实。

## 测试与边界

project-grant专项使用真实JWT/MFA和临时MySQL，覆盖首次grant 100并发、异意图、未知actor、revoke/旧CAS/regrant、归属、命令/审计数量、停用态撤销、撤权后重放、Project SK拒绝、财务快照不变，并回归Project Key、视频准入和目录。

尚未完成Project Key签发/轮换/吊销的持久化幂等和响应丢失恢复；因此完整Key生命周期及VID-G6仍未通过。

回滚只关闭入口，不删除grant、命令、原因和审计。撤销必须保留为原授权表的状态/version事实，不能物理删除记录。
