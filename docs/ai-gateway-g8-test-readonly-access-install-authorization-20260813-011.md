# G8 测试服只读入口 011 交互 sudo 安装授权清单

> 状态：`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。本清单只冻结候选与未来执行边界；当前未授权、未执行 `--local-check`、SFTP、交互 SSH、sudo 认证、root 安装或远端 self-test。

## 1. 精确目标与冻结资产

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- Drop 入口：`pc@8.130.9.163:10003`；不门禁物理 hostname、machine-id 或云实例身份。
- 部署根：`/home/pc/molin`。
- source commit：`099c38ed62ccd62c3c5a3b6811f1369d7f0d3084`。
- source tree：`c2d1252a05d031d842549345128fa7a1ffe53dc8`。
- transport：`DROP_SSH_INTERACTIVE_SUDO`；physical identity：`NOT_APPLICABLE`。
- Windows 候选目录：`D:\molingproject\g8-artifacts\CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- 011 暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- root-only：`/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。

候选必须恰含 `SHA256SUMS`、`ai-gateway-reconcile`、`g8-test-readonly-audit`、`manifest.env`、`molin-g8-test-readonly-audit.sudoers` 五个普通非链接文件：

- Windows 候选回执：`15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f`。
- Linux CI 临时回执：`8bb8efcd789c87af28b8495a9841b95934e50d145e8d40a5eed70cd32565b963`。
- 审计器：`308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`。
- sudoers：`1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f`。
- 对账器：`37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1`，大小 `13066129` 字节。
- root 安装器：`0cfa83a25ab0624ca7f5a475ce718c2cd338d985531cd9e42674cacfe2032b3f`，大小 `7990` 字节。
- 人工命令资产：`a636cea3d0d78631968ff747c208288ff88cf656fa847e6e1e7f5c37271fe415`，大小 `12521` 字节。
- 暂存包装器：当前候选 SHA `ef9d712af26af98688c23b5db28fdcc5452d6272ef1f118ca8d60beeb59c181c`；合并后必须重新计算并同步，漂移即停止。
- 命令生成器：当前候选 SHA `603f415ffff874a6c465a8f6777b73c9cc0afb18cda67242cb0fc0ea73acb3f1`；合并后必须重新计算并同步，漂移即停止。
- 冻结 010 helper：`4fb920e32574c640685ddd9bed919485473dc54873d157a409c1adf987b3ab6a`。

## 2. 未来需再次批准的唯一执行顺序

1. 执行一次 011 包装器 `--local-check`；只核验五文件、manifest、回执、known_hosts、密钥对和本地稳定性，不联网。
2. 完整通过后执行同一包装器正式模式一次；只启动一次 SFTP，独占创建 011 暂存并上传五文件，零重试，不启动 SSH 或 sudo。
3. 操作者使用命令资产中的固定 OpenSSH 参数打开一次带 TTY 的交互 SSH，会话内先执行固定非特权预检。
4. 预检通过后只执行一次 `sudo -k -v`；密码仅由 sudo 从当前 TTY 读取，不得复制、记录、回传或写入任何参数、stdin、脚本、环境变量、日志、文档、提交或 PR。
5. 认证成功后粘贴完整冻结命令；它只以 `sudo -n /bin/bash -ceu` 在新建 root-only 目录 no-clobber 写入、复核并执行 root 安装器。
6. 安装器从 011 暂存重新验证并 no-clobber 复制五文件到 root-only，再检查父链和 live 目标不存在，独占安装两个工具与一个 sudoers 文件。
7. 安装后精确验证 owner/mode/SHA/size、candidate/live 两次 `visudo`、`sudo -n -l -U pc` 的固定命令范围以及 `pc` 不属于 Docker 组。
8. 全部通过后，在同一 `pc` 会话仅执行一次 `sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test`，随后退出。

## 3. 影响、回滚和停止条件

- 授权上限：未来独立批准后本地检查 1 次、SFTP 1 次、交互 SSH 1 次、`sudo -k -v` 1 次、root 安装 1 次、固定 self-test 1 次；全部零重试。
- 创建面：一个 `pc:pc:0700` 暂存、一个 `root:root:0700` root-only 目录、可能新建的 `/usr/local/libexec/molin`、两个工具和一个 sudoers 文件。
- 三个 live 目标均以 no-clobber 同一文件描述符创建并逐项登记。预存目标不得覆盖、删除或修改。
- 失败时只逆序删除本次登记的新 live 目标；sudoers 优先删除并重新校验 `/etc/sudoers`。仅本次新建且已证明为空的工具父目录允许精确 `rmdir`。
- 暂存和失败后的 root-only 目录保留取证；清理必须使用新 ChangeId 和独立授权。
- 任一 ChangeId、端点、登录用户、路径、五文件、manifest、回执、摘要、大小、known_hosts、密钥对、父链、属主、权限、stderr、返回码、输出契约、sudo 认证、`visudo`、sudo 范围、Docker 组或 self-test 不符立即停止。

本清单不授权业务 HTTP、数据库/Redis/RabbitMQ/队列读取或修改、服务重启、环境变量或业务配置修改、生产连接、付费上游、真实通知或客户灰度。请求与费用固定为业务 `0`、上游 `0`、费用上限 `0 CNY`。`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 未完成。
