# AI 网关 G8 测试服只读入口安装尝试记录（011）

## 1. 变更身份与结论

| 项目 | 结果 |
|---|---|
| ChangeId | `CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011` |
| Drop SSH 目标 | `pc@8.130.9.163:10003` |
| 执行日期 | 2026-08-13 |
| 结论 | `STOPPED_AT_STAGING_CALL_STAGING_UNKNOWN` |
| 本地 `--local-check` | 包装器退出码 `0`、stderr 为空、固定输出 `G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE_LOCAL_CHECK=PASS`，执行 `1 / 1` |
| 正式暂存包装器调用 | 执行 `1 / 1`，退出码 `2`、stderr 为空、固定输出 `G8_TEST_READONLY_ACCESS_STAGE_DROP_INTERACTIVE=FAILED reason=invalid_request` |
| SFTP / 远端暂存 | 低敏失败结果不能区分 SFTP 是否启动、远端目录是否创建或五文件是否部分上传，统一保持 `UNKNOWN` |
| 交互 SSH / sudo 认证 / root 安装 / self-test | 均未执行 |
| 业务请求 / 上游请求 / 费用 | `0 / 0 / 0 CNY` |

正式暂存包装器在唯一一次调用中命中停止条件后立即结束；没有重试、补传、诊断性 SSH、交互 SSH、sudo、root 安装或 self-test。011 及其候选、回执、授权和历史命令现已消费，禁止通过修复本地环境后重放。

## 2. 已确认的本地事实

1. 执行时包装器 SHA-256 为 `6faa85b19cbac0dcd4099185168fef577317278cfa48ea65cc3a7efffe64ea85`，与合并后授权清单一致。
2. 候选目录恰好包含五个冻结文件；`SHA256SUMS` 回执为 `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f`。来源提交、源码树、审计器、sudoers、对账器摘要和对账器大小均通过唯一一次本地检查。
3. 固定 `known_hosts`、同目录 ED25519 密钥对、批准的服务端和客户端公钥指纹以及本地材料稳定性均在唯一一次 `--local-check` 中通过。没有输出 known_hosts 正文、公钥正文、私钥、密码、Token 或环境变量值。
4. 本地检查包装器本身退出码为 `0` 且输出契约正确；外层本地编排器曾错误地按正式模式标记校验该行并在本地退出，但没有再次运行包装器，也没有联网，不改变 `1 / 1 PASS` 的包装器事实。
5. 唯一正式暂存包装器调用只返回低敏 `invalid_request`。该结果不能证明失败发生在本地校验、SFTP 启动、远端独占建目录或文件上传的哪个阶段，因此不得把 011 暂存登记为存在、不存在、完整或已清理。
6. 因正式暂存阶段未形成可接受证据，按授权清单立即停止，后续交互 SSH、`sudo -k -v`、root-only 复制、live 安装、`visudo`、sudo 精确范围、Docker 组和固定 self-test 全部为 `0 / 1`。

## 3. 防重放与回滚边界

- 包装器、人工命令生成器的所有入口均在 helper、候选、身份材料或网络读取前固定返回 `change_id_consumed`。
- 候选生成器不再提供活动候选；011 只能在系统临时目录使用 `--verify-consumed-candidate` 复现历史 Windows/Linux 回执，完成后自动销毁。
- 消费态包装器 SHA-256 为 `859dab407654bc9e833bd5bf9bd0d18af04609656b464796aa8a51510610a03e`，大小 `7731` 字节；消费态人工命令生成器 SHA-256 为 `c0cdd1426325d7f93239c099ca66c73ab3bd941b8e58017544a8b69022bc40ec`，大小 `11716` 字节。
- 没有进入交互 SSH 或 root 通道，没有创建 root-only、live 工具或 sudoers 目标，因此没有 live 目标需要回滚。
- 远端 011 暂存可能不存在、部分存在或完整存在；当前授权禁止再次连接确认，也禁止删除。诊断或清理必须使用新 ChangeId、重新完成工程门禁并取得独立用户授权。

## 4. 残余状态与后续门禁

- 测试服 API 停止、既有运行态 P1=3，以及数据库、Bifrost、监控、备份和账务 `UNKNOWN` 均未关闭。
- 若继续只读入口安装，必须先以新 ChangeId 对 011 暂存的存在性和完整性做只读取证；不得复用 011 候选、回执、授权或命令。
- 只有暂存状态经独立门禁关闭后，才允许另行设计清理或新安装候选；任何清理、上传、安装、sudo 或 self-test 均需要新的明确授权。
- 本次结果不授权业务 HTTP、数据库/队列访问、服务重启、生产连接、真实付费调用、真实通知或客户灰度。`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 继续未完成。

## 5. 仓库合并证据

- 防重放与执行证据精确 HEAD 为 `03d53a4dfb22510808b1723d04b69172fce07450`，固定基线为 `380af658998e21ead0b2b076a3b2a38b4f785280`。
- CI run `31689994630` 精确绑定该 HEAD，状态为 `completed / success`，12/12 作业全部 SUCCESS，包含 G8 生产就绪、真实后端浏览器和必选门禁汇总。
- 独立代码安全、QA、产品/规格均在该精确 HEAD 给出 `P0=0 / P1=0 / P2=0`。
- PR #367 已使用 merge commit `eba0116ad5e790a4d29e32bc68d3151d9d22dc06` 合入主干；双亲依次为固定基线和精确功能 HEAD，merge tree 与功能 HEAD tree 均为 `8430d1869dc60aaa6332fdc1c40ed3c3e167e445`，未使用 squash 且无内容漂移。
- 远端功能分支 `feature/backend-d-ai-gateway-g8-readonly-access-011-attempt-evidence` 已删除。本节只收口仓库工程证据，不授权再次连接测试服、确认或清理暂存、安装或生产操作。
