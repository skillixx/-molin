# G8 Drop 最小只读入口安装 015 消费记录

## 1. 当前状态

`CONSUMED_LOCAL_PATH_ERROR_DOWNSTREAM_UNKNOWN / REMOTE_NOT_AUTHORIZED`

015 曾在精确工程门禁、主线合并和用户独立授权后进入唯一执行窗口。人工粘贴冻结本地段时，Windows 可信路径正则发生运行时错误；该错误在 Windows PowerShell 5.1 默认策略下属于非终止错误，现有终端记录不能证明后续身份材料读取或 SSH 是否发生。连接上限为 1、重试固定为 0，因此无论后续实际到达何处，015 都已消费并禁止重放。

## 2. 冻结身份与原候选

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-015`。
- 冻结来源：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- 工程 HEAD：`440b0e3cda32e7feec3bae55005229529ff035c3`。
- CI run：`31771604235`，当时适用 Ready 门禁成功。
- main merge commit：`102e833b1975d56100faf4d8e4add0150a3edc39`。
- 原安装器 Git blob：`9465 / ed2af4cbd7d102d120d9b2af59b0f60867c83eb79c655c01b45332455617829e`。
- 原生成器 Git blob：`15629 / 5e1adc70bf5de967afa01400b7c1b358fff47649096d56c4c08d3f92ebc255a8`。
- 原冻结双段命令：`22796 / fc6b095b5167c6cf65cd049b04a4033274a90a44011963014fcadcbf502b917a`。

这些摘要只用于历史审计，不重新授权旧生成物。当前仓库中的 015 生成器和安装器均为固定消费墓碑。

## 3. 唯一执行结果

- 本地可信路径表达式：`ERROR_OBSERVED / POWERSHELL_REGEX_TRAILING_ESCAPE`。
- 身份材料读取：`UNKNOWN`。
- SSH 会话：`UNKNOWN_WITHIN_APPROVED_MAX_1`；不得解释为可重试额度。
- 远端安装段与 sudo 认证：`NOT_EVIDENCED / UNKNOWN`。
- root-only 副本与 live 文件：`UNKNOWN`；没有成功证据。
- 业务请求、上游请求、费用：未获授权且无发生证据，但本记录不以终端缺少输出证明绝对为 0。
- 四项成功标志：`PREFLIGHT_015`、审计器 self-test、`INSTALL_015`、`POSTCHECK_015` 均未产生。

事后单独执行的 `Write-Host "G8_015_LOCAL_EXIT=$LASTEXITCODE"` 显示 `0`。`Write-Host` 不会自行重置 `$LASTEXITCODE`，该值可能来自此前任一原生程序，包括 `ssh-keygen` 或 `ssh`；由于没有在冻结步骤边界即时捕获并绑定，既不能证明 015 成功，也不能证明 SSH 未启动。

## 4. 停止与后续边界

015 不得再次生成、粘贴或执行，不得借原用户批准连接测试服。当前不得擅自清理或回滚任何潜在远端状态；016 在任何 sudo 提示前必须以只读预检重新确认 root-only/live 目标不存在，出现既有目标即停止并另行申请只读取证或清理授权。既有 011 暂存不得修改。

修复必须使用新 ChangeId、重新完成离线测试、精确 HEAD CI、独立代码安全/QA/产品复评、PR 合并和合并后 Git blob 复核，并再次取得用户明确授权。016 仅为新的工程候选，不继承 015 执行授权。
