# G8 Drop 暂存只读取证 008 授权清单

> 状态：`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。本文仅冻结候选，不构成连接测试服务的授权；下列执行命令当前全部禁止运行。

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
- 008 包装器 SHA-256：`a2a9f7cbb0ca306c1ff67f08d911f609a805dce44509b4dbfcab26d881a70cb6`
- 冻结 004 helper SHA-256：`599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`
- 远端只接受登录用户 `pc`、部署根真实路径与 `pc:pc` 元数据；部署根必须具备属主 `0700` 必需位且组/其他不可写，暂存目录必须精确 `0700`。随后只检查固定暂存 basename 和固定五文件摘要/大小。

## 3. 授权后才可使用的命令摘要

授权执行时仅允许：

1. 使用固定 ChangeId、known_hosts、私钥和公钥执行一次 `--local-check`；该步骤不得创建 SSH 子进程。
2. 在完全相同参数下移除 `--local-check`，执行一次正式只读 SSH；`ConnectionAttempts=1`，不得重试。

禁止 SFTP/SCP、上传、下载、删除、sudo、数据库/队列/日志/监控/备份读取、业务 HTTP、上游调用和任何生产连接。不得在文档、日志或聊天中粘贴私钥内容。

## 4. 预期结果与停止条件

- `ABSENT / NOT_APPLICABLE / NONE`：只证明固定暂存目录不存在；立即停止，后续动作另立 ChangeId。
- `PRESENT / PASS / NONE`：只证明固定五文件完整匹配；立即停止，清理或安装另立 ChangeId。
- `PRESENT / MISMATCH / <固定原因>`：退出码 3，立即停止；禁止清理、覆盖、续传或安装。
- 任一身份材料、Drop 端点、登录用户、部署根路径/属主/权限、helper 摘要、九键输出、stderr、超限、超时或返回码不符：失败关闭并立即停止。

## 5. 影响与回滚

候选本身只修改仓库。未来若单独获准执行，唯一远端影响是一次只读 SSH 可能产生 sshd/journald/audit 访问日志或文件 atime；不修改应用资产，因此没有应用层回滚。任何清理、安装、sudoers 修改、服务重启、生产操作、真实付费调用、告警通知或客户灰度均不在本清单授权范围。

## 6. 当前边界

007 的 `READABLE_MISMATCH` 是已保留的历史事实，但 machine-id 门禁不适用于 Drop 映射入口，不再作为本候选的测试服运行态 P1。003 暂存仍为 `UNKNOWN`，只有未来独立批准并实际执行 008 后，才能按三态结果收敛。`G8_ENGINEERING_READY` 保持；`G8_COMMERCIAL_ACCEPTED` 未完成。
