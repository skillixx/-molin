# G8 Drop 暂存只读取证 008 执行记录

## 1. 执行边界

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008`
- Drop SSH 入口：`pc@8.130.9.163:10003`
- 部署根：`/home/pc/molin`
- 固定暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`
- 执行基线：`02ca66343c6e858db10a6c6c85b5e94ba159cff7`
- 执行时 008 包装器 SHA-256：`57b63037306b8c5ba564132986d6ddf5dfc659a22d10118fbd3470ec15146365`
- 冻结 004 helper SHA-256：`599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`
- 消费后 008 包装器 SHA-256：`1498fdaa5c0117e3ed231aa611a126b685dc9d27f8ef271f431c4b683e25aaac`
- 业务请求、上游请求、费用：`0 / 0 / 0 CNY`

用户授权一次本地检查和一次只读 SSH，零重试；禁止上传、下载、删除、sudo、数据库/队列/日志/监控/备份读取、业务 HTTP 和生产连接。

## 2. 本地前置与执行次数

执行前确认工作树与 `origin/main` 均精确指向 `02ca66343c6e858db10a6c6c85b5e94ba159cff7`，工作树干净，包装器、helper、known_hosts、ED25519 私钥和公钥均存在且冻结摘要匹配。

首次本地编排命令因 PowerShell 参数传递错误，只启动了一个未携带脚本参数、等待标准输入的本地 Python 进程；超时后确认未留下本次 Python 进程。该过程没有加载 008 包装器、没有执行 `--local-check`、没有创建 SSH 子进程，因此不计入授权次数。随后未重试该错误编排方式。

实际执行次数：

- 008 `--local-check`：1 次，退出码 0，固定输出 `G8_TEST_READONLY_DROP_STAGING_EVIDENCE_LOCAL_CHECK=PASS`，stderr 为空。
- 008 正式只读 SSH：1 次，退出码 0，stderr 为空。
- SSH 重试：0 次。

## 3. 固定低敏结果

正式调用只返回以下固定低敏结果：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE=PASS
staging_state=ABSENT
staging_integrity=NOT_APPLICABLE
staging_mismatch_reason=NONE
business_requests=0 upstream_requests=0 cost_cny=0
```

该结果只证明固定 003 暂存目录不存在，将其状态从 `UNKNOWN` 收敛为 `ABSENT`。没有读取 hostname、machine-id、实例元数据、CMDB、业务文件正文、数据库、队列、日志、监控或备份；没有执行 SFTP/SCP、上传、下载、删除、sudo、HTTP、上游调用或生产连接。

只读 SSH 可能由系统自动产生 sshd/journald/audit 访问日志或文件 atime，本轮没有权限读取这些系统副作用，因此不能宣称远端绝对零写入。

## 4. 停止与后续边界

结果满足 `ABSENT / NOT_APPLICABLE / NONE` 后立即停止，未执行清理，也不需要为该固定路径执行清理。008 已消费，仓库普通入口和 `--self-test` 均必须在 helper、身份材料读取和联网前固定返回 `change_id_consumed`，禁止重放。

该结果不证明测试服 API、MySQL、Redis、RabbitMQ、Bifrost、监控、备份、账务对账或只读审计入口已经恢复。若继续准备安装，只能使用新的 ChangeId 重新冻结安装候选、制品回执、影响、回滚和停止条件，并在工程门禁完成后另行等待用户授权。

`G8_ENGINEERING_READY` 保持；生产部署、真实付费调用、真实通知、客户灰度和四周商业观察仍未执行，`G8_COMMERCIAL_ACCEPTED` 继续保持未完成。

## 5. 仓库收口证据

- 最终执行证据 HEAD：`f0d726ad5d347dd1f35f2ebec2e118f0093e958f`
- CI run：`31661245959`，12/12 SUCCESS，包含 G8 生产就绪、Linux 无网络 19/19、真实后端浏览器验收与必选汇总。
- 独立评审：代码安全/Standards、QA、产品/规格均为 P0=0、P1=0、P2=0。
- PR：`#356`，按 merge commit 合并。
- merge commit：`6b2a1fa438dbad2e7d0a15b33d4c8c0d8ff8b7be`。

上述仓库证据只证明 008 执行事实、消费门禁和文档已合入主干，不扩大任何测试服、生产或商业授权。
