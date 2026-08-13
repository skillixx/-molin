# G8 Drop 暂存只读取证 008 授权清单

> 状态：`CONSUMED_ABSENT`。用户已独立批准并完成唯一一次本地检查和唯一一次只读 SSH；固定 003 暂存目录结果为 `ABSENT / NOT_APPLICABLE / NONE`。008 及本文全部执行命令均已消费并作废，禁止重放。

## 1. 变更标识与精确目标

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008`
- Drop SSH 入口：`pc@8.130.9.163:10003`
- 部署根：`/home/pc/molin`
- 目标暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`
- 业务请求上限：0
- 上游请求上限：0
- 费用上限：0 CNY

Drop 地址只代表固定传输端点，不代表底层物理主机身份。008 禁止读取或验证 hostname、`/etc/machine-id`、实例元数据或 CMDB；严格 known_hosts 与本地 ED25519 密钥只用于验证 Drop SSH 端点和客户端身份。

## 2. 冻结资产

- 008 包装器：`infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py`
- 008 包装器 SHA-256：`57b63037306b8c5ba564132986d6ddf5dfc659a22d10118fbd3470ec15146365`
- 冻结 004 helper SHA-256：`599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`
- 远端只接受登录用户 `pc`、部署根真实路径与 `pc:pc` 元数据；部署根必须具备属主 `0700` 必需位且组/其他不可写，暂存目录必须精确 `0700`。随后只检查固定暂存 basename 和固定五文件摘要/大小。

工程证据绑定 PR #353 最终 HEAD `59cc103862e710e56749c7e1875a734783e4d754`、CI run `31658983454` 的 12/12 SUCCESS、独立代码安全/QA/产品规格 P0/P1/P2=0，以及 merge commit `670b39dd316a53af7c7baa639c9822b1a65994aa`。这些证据只允许把状态收敛为等待用户批准，不等于已经执行本地检查或 SSH。

## 3. 已消费的历史命令摘要

本次授权实际只允许并完成：

1. 使用固定 ChangeId、known_hosts、私钥和公钥执行一次 `--local-check`；该步骤不得创建 SSH 子进程。
2. 在完全相同参数下移除 `--local-check`，执行一次正式只读 SSH；`ConnectionAttempts=1`，不得重试。

禁止 SFTP/SCP、上传、下载、删除、sudo、数据库/队列/日志/监控/备份读取、业务 HTTP、上游调用和任何生产连接。不得在文档、日志或聊天中粘贴私钥内容。

上述命令仅用于历史审计，现已全部作废。不得依据本文、旧候选、旧回执或本次结果再次执行本地检查或 SSH。

## 4. 预期结果与停止条件

- `ABSENT / NOT_APPLICABLE / NONE`：只证明固定暂存目录不存在；立即停止，后续动作另立 ChangeId。
- `PRESENT / PASS / NONE`：只证明固定五文件完整匹配；立即停止，清理或安装另立 ChangeId。
- `PRESENT / MISMATCH / <固定原因>`：退出码 3，立即停止；禁止清理、覆盖、续传或安装。
- 任一身份材料、Drop 端点、登录用户、部署根路径/属主/权限、helper 摘要、九键输出、stderr、超限、超时或返回码不符：失败关闭并立即停止。

## 5. 影响与回滚

本次只读 SSH 可能产生 sshd/journald/audit 访问日志或文件 atime；没有修改应用资产，因此没有应用层回滚。固定暂存目录已证实不存在，未执行也无需执行清理。任何安装、sudoers 修改、服务重启、生产操作、真实付费调用、告警通知或客户灰度均不在本清单授权范围。

## 6. 当前边界

007 的 `READABLE_MISMATCH` 是已保留的历史事实，但 machine-id 门禁不适用于 Drop 映射入口，不再作为当前测试服运行态 P1。008 的唯一正式结果已把固定 003 暂存状态从 `UNKNOWN` 收敛为 `ABSENT`；这只关闭暂存残留阻塞，不证明 API、数据库、Bifrost、监控、账务或安装后的只读入口可用。后续安装候选必须使用新 ChangeId、重新冻结并取得独立授权。`G8_ENGINEERING_READY` 保持；`G8_COMMERCIAL_ACCEPTED` 未完成。
