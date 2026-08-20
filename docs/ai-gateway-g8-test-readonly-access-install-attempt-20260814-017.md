# G8 Drop 最小只读入口安装 017 执行记录

## 1. 固定结果

`CONSUMED_LOCAL_GATE_FAILED_SSH_REACHABILITY_UNKNOWN`

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017`。
- 用户授权：已明确批准固定 017 清单的一次执行；不授权业务请求、上游请求或费用动作。
- 冻结双段命令：大小 `25862`，SHA-256 `6acc63972cb779eea18df49dcaec271c7d50223000d96f2a1c1d57364d4cc98e`；生成后再次核对一致。
- 唯一人工本地段：返回 `G8_TEST_READONLY_ACCESS_017_LOCAL_GATE=FAILED reason=local_gate_failed`，随后退出码 `2`。
- SSH 启动与连接：`UNKNOWN / 最多 1`；现有低敏输出无法区分 SSH 前门禁异常与 `ssh.exe` 非零返回。
- 远端安装段：`0/1`；用户没有粘贴第二段，也没有进入 sudo 密码响应。
- sudo、安装器与 post-check：`0 / 0 / 0`。
- 业务请求、上游请求、费用：`0 / 0 / 0 CNY`。
- 重试：`0`；017 按失败关闭规则消费并禁止重放。

墓碑化后四个普通文件的冻结摘要如下，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob |
|---|---:|---|---|
| `infra/scripts/g8-test-readonly-access-install-017.sh` | 182 | `4013529da1e7e9c9a883aa1f9cc77f7dfa194b913976bac342aee03955c4bffc` | `9b689b7c2cbff2f2ab678d4501630751eb87bdeb` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-017-command.py` | 410 | `fcc8ed7c6ed503fa0b4ee4108516d7c52550580b59dbebfe5b9eb191507e9ec9` | `887ff92ba6cbd2cc1d9387f724c453a073e44738` |
| `infra/scripts/test_g8_test_readonly_access_install_017.py` | 1230 | `7c1dd8a6c8b4c6095bf694f32e8c502387a6190d2403954022f2fc8e14efcd97` | `c54c5ae45526bd6aa6f08f1c8835999e9c909b01` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_017_command.py` | 1444 | `c8055c0d48566d5f7ecef2e2208e1226ece91baab3426b76e4b59f3de3ff1abc` | `bce0a4842056823ebc79c2e2275dff98706b4ba9` |

## 2. 可绑定证据

用户在可见 Windows PowerShell 中完整执行冻结命令第一段。控制流进入外层 `catch`，仅输出固定低敏结果：

```text
G8_TEST_READONLY_ACCESS_017_LOCAL_GATE=FAILED reason=local_gate_failed
```

随后最终门禁执行 `exit 2`，终端进程以代码 `2` 退出。未出现 `G8_TEST_READONLY_ACCESS_PREFLIGHT_017=PASS`、审计器 self-test、`INSTALL_017=PASS` 或 `POSTCHECK_017=PASS`；用户没有看到或响应 sudo 密码提示，也没有粘贴冻结远端第二段。

事后只运行不含 SSH 的本地同构探针：它逐项复用系统目录、固定文件流式摘要、known_hosts、主机指纹、公私钥配对、客户端指纹与二次材料摘要门禁，并在唯一 SSH 调用前截断；结果为退出码 `0`。这证明取证时本地 SSH 前门禁可通过，但不能倒推唯一人工执行当时没有瞬时漂移，也不能证明当时是否已启动 `ssh.exe`。

本机 Security 4688 与 Sysmon 进程创建日志不可用；执行后没有存活的 `ssh.exe` 进程。Prefetch 只有早于本次执行窗口的历史记录，不能作为本次是否启动 SSH 的确定证据。由于冻结命令把 SSH 前异常和 SSH 非零返回统一收敛为同一低敏结果，现有本地证据无法可靠绑定 SSH 调用次数为 0。

## 3. 影响边界

本次只执行 Windows 本地第一段；远端 here-doc 第二段从未粘贴，因此固定预检、`sudo -k -v`、root-only 017 副本、live 文件、sudoers、self-test 与 post-check 均未执行。未确认测试服安装成功，也没有证据表明 live 入口发生变化。若 SSH 曾短暂启动，最多可能产生客户端和服务端连接审计记录；不把该可能性表述为已连接或远端零触达。

017 清单明确规定首次 SSH、sudo、安装器或 post-check 任一步成功或失败均立即消费且重试为 0。由于无法反证唯一 SSH 调用是否已到达，必须按更严格边界把 017 视为已消费；不得重新打开终端、重复粘贴第一段或使用历史生成文件重放。

## 4. 后续门禁

017 生成器和安装器现均为固定 `change_id_consumed` 墓碑，在参数解析、材料读取和联网之前退出。任何继续诊断或安装都必须使用新的 ChangeId，先修复“SSH 前失败与 SSH 返回失败不可区分”的低敏阶段证据，完成本地测试、CI、独立代码安全/QA/产品复评、主线合并和合并后冻结摘要复核，再取得用户独立精确授权。

本记录不授权 SSH、sudo、安装、清理 root-only 副本、运行态审计、部署、服务操作、数据库/队列操作、业务请求、付费上游、真实通知、客户流量或生产动作。

## 5. 消费归档工程集成与合并后复核

017 消费归档由 PR `#386` 完成工程集成：最终 HEAD 为 `48489d5cc2e29d4a89b6ed9da2e0104ebdea3066`，CI run `31807824354` 为 `completed/success`，`CI 必选门禁汇总` 成功；代码安全、QA、产品/规格独立复评均为 P0/P1/P2/P3=`0/0/0/0`。PR 使用 merge commit `6c198580a22d60c70271ee0f059b3a614b3821f6` 合入 main，父提交依次为旧 main `05a5b6e5a73037c1b77703154314448626aefe0c` 与工程 HEAD `48489d5cc2e29d4a89b6ed9da2e0104ebdea3066`；远端工程分支已删除。

从合并后 `origin/main` 的原始 Git blob 独立复核第 1 节四个墓碑文件，大小、SHA-256、Git blob 与表中冻结值逐项一致，CRLF 计数均为 0。该复核只证明消费归档已进入主干且历史入口不可重放，不证明测试服已安装，也不授权 018 的 SSH、sudo、安装或任何运行态动作。
