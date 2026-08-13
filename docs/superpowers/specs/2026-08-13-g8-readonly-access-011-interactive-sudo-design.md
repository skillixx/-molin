# G8 Drop 只读入口 011 交互 sudo 安装方案设计

## 1. 决策与授权边界

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010` 已完成一次本地检查、一次只读 SSH 和一次原子 SFTP，随后在本地 root 启动器参数构造阶段停止。010 已消费；其暂存只保留取证，011 不读取、不复制、不删除，也不得恢复 010 的任何入口。

新的 ChangeId 为 `CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。已知事实是：`pc@8.130.9.163:10003` 公钥登录成功，`pc` 属于 `sudo` 组，但 `sudo -n -l` 返回需要密码。011 因而采用用户批准的方案 A：操作者只在唯一一次交互 SSH 会话的终端中向 `sudo -k -v` 输入密码，密码不得进入参数、stdin、脚本、环境变量、日志、文档、候选、提交或 PR。

本轮仅允许仓库设计、实现、构建、测试、CI、评审和 PR。禁止运行 011 `--local-check`、SFTP、交互 SSH、`sudo -v`、root 安装或远端 self-test；禁止连接测试服或生产环境。

## 2. 方案选择

采用“先认证、再执行冻结安装命令”的单会话模型：

1. 本地离线检查新的 011 五文件候选；
2. 未来另获执行授权后，使用一次原子 SFTP 新建并上传 011 暂存；
3. 随后只打开一次带 TTY 的交互 SSH，会话内先完成非特权预检；
4. 操作者执行一次 `sudo -k -v` 并仅在终端输入密码；
5. 认证成功后以 `sudo -n /bin/bash -ceu` 执行冻结的单一安装命令；
6. 安装成功后仍在同一个 `pc` 会话执行一次固定 `sudo -n ... --self-test`；
7. 任一门禁失败立即退出该会话，零重试。

不采用直接进入交互 root shell后逐条执行：该方式容易漏步骤，无法稳定绑定输入与结果。不采用临时 NOPASSWD sudoers：它会在验证前扩大权限边界。

## 3. 冻结来源、候选与路径

011 从创建设计分支时的精确 `origin/main` 冻结 source commit `099c38ed62ccd62c3c5a3b6811f1369d7f0d3084` 和 source tree `c2d1252a05d031d842549345128fa7a1ffe53dc8`，并在候选生成器、manifest、测试和授权清单中保持一致。冻结目标为：

- Drop 入口：`pc@8.130.9.163:10003`；
- 部署根：`/home/pc/molin`；
- transport：`DROP_SSH_INTERACTIVE_SUDO`；
- physical host identity：`NOT_APPLICABLE`；
- 011 暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`；
- 011 root-only 目录：`/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。

候选仍必须恰含五个文件：

1. `SHA256SUMS`；
2. `ai-gateway-reconcile`；
3. `g8-test-readonly-audit`；
4. `manifest.env`；
5. `molin-g8-test-readonly-audit.sudoers`。

单一安装命令是独立执行资产，不混入五文件候选或候选回执。仓库单独保存可审查的 root 安装脚本和由它生成的人工复制命令，并分别冻结 SHA-256。人工复制命令只包含固定低敏路径、摘要和 Base64 编码的安装脚本，不包含凭据或环境变量值。

## 4. 本地生成与防重放

候选生成器必须：

- 将 010 保持在 consumed 集合，并锁定其 Windows/Linux 历史回执；
- 将 011 设为唯一活动候选，绑定新 ChangeId、source commit/tree、transport 和新暂存路径；
- 使用同一冻结源码连续构建两次对账器并比较摘要；
- 只创建新的绝对且不存在的输出目录；
- 生成新的 manifest、`SHA256SUMS` 和 Windows/Linux 候选回执；
- 不连接测试服，不读取 010 暂存。

011 正式额度一旦执行，无论成功或失败，都必须转为 consumed。消费后，生成器不得持久重建 011；本地检查、暂存、交互命令生成和 self-test 入口必须在候选、身份材料或网络读取前固定返回 `change_id_consumed`。仅允许在系统临时目录复现历史回执。

## 5. 暂存阶段

011 使用独立暂存包装器，仅承担离线检查和未来单次 SFTP，不启动 SSH，也不处理密码：

- `--local-check` 只验证五文件白名单、manifest、候选回执、known_hosts、密钥对、固定指纹和本地证据稳定性；
- 正式暂存模式只启动一次 SFTP，`ConnectionAttempts=1`、零重试；
- 显式使用系统 SFTP、`-F none`、`IdentitiesOnly=yes`、固定 `UserKnownHostsFile` 与 `IdentityFile`；
- 禁用密码、键盘交互、Agent、转发和调用方配置；
- 首条 SFTP 命令以独占 `mkdir` 创建 011 暂存，已存在即停止且不覆盖；
- SFTP 只读取随机本地临时目录中的五文件快照；
- 调用前后复核原候选、身份材料和快照；任一漂移、非零、stderr、超时或输出异常立即失败关闭。

010 暂存路径不得出现在 011 SFTP batch 或远端读取路径中。

## 6. 唯一交互 SSH 会话

工程门禁与后续独立用户执行授权全部通过后，操作者使用固定命令打开一次交互会话：

- 系统 OpenSSH 绝对路径；
- `-F none`、`-tt`、`IdentitiesOnly=yes`、`ConnectionAttempts=1`；
- 固定 known_hosts、原始 ED25519 私钥和 `pc@8.130.9.163:10003`；
- 禁 Agent、X11、端口转发和本地命令；
- 不在命令行附带密码或 root 命令。

进入会话后先执行冻结的非特权预检块，只允许读取：`id`、`sudo -n -l` 的预期密码态、部署根与 011 暂存的低敏元数据、五文件类型/属主/权限/大小/摘要、三个 live 目标是否不存在、父目录链元数据。不得读取 hostname、machine-id、数据库、Redis、RabbitMQ、队列、日志、监控、备份、环境变量或业务数据。

预检通过后执行一次：

```bash
sudo -k -v
```

密码由 `sudo` 直接从当前 TTY 读取。操作者不得复制、记录或回传密码。非零、额外 stderr、重复提示或认证失败立即结束会话，禁止第二次尝试。

## 7. 单一失败关闭安装命令

认证成功后，操作者粘贴由仓库工具生成的完整命令。该命令使用：

命令外形固定为 `sudo -n /bin/bash -ceu` 加单引号包围的 bootstrap，并使用名称固定为 `G8_011_INSTALL_B64` 的 quoted heredoc 传入 root 安装脚本 Base64。生成器必须输出完整可执行文本，不允许保留模板变量、占位符或要求操作者编辑。

`sudo -n` 只消费同一会话刚建立的 sudo 时间戳，不再次读取密码。固定 bootstrap 必须：

1. 设置可信 PATH、`umask 077`，清除 `BASH_ENV`、`ENV`、`CDPATH`、`PYTHONPATH`、`PYTHONHOME`；
2. 原子独占创建 011 root-only 目录，要求不存在、非链接、`root:root:0700`；
3. 以 no-clobber 同一文件描述符写入 Base64 解码后的 root 安装脚本；
4. 关闭描述符后复核脚本为普通非链接文件、`root:root:0700`、固定大小和 SHA-256；
5. 只执行 root-only 副本，绝不执行 `pc` 可写路径中的脚本；
6. bootstrap 任一步失败时，不执行安装脚本，并保留 root-only 目录用于取证。

root 安装脚本必须是单一、无参数、固定 ChangeId 的失败关闭程序，并完成：

- 再次核验当前 EUID=0、011 暂存真实路径、普通目录、非链接、`pc:pc:0700`；
- 核验暂存恰含五文件，逐项为普通非链接文件，manifest、回执、四项摘要和对账器大小精确匹配；
- 将五文件逐项 no-clobber 复制到 root-only 目录，并在 root-only 副本重新完成同样核验；
- 对 root-only sudoers 副本执行精确 `visudo -cf /root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011/molin-g8-test-readonly-audit.sudoers`；
- 逐级核验 `/usr`、`/usr/local`、`/usr/local/libexec`、已有 `/usr/local/libexec/molin`、`/etc`、`/etc/sudoers.d` 为非链接、root-owned、组/其他不可写目录；
- 仅当 `/usr/local/libexec/molin` 缺失时以 `root:root:0755` 原子创建并登记；
- 安装前再次确认三个 live 目标不存在；
- 三个 live 目标分别使用 `set -o noclobber` 与同一文件描述符独占创建，逐项登记；
- 安装后复核普通文件、非链接、`root:root`、精确权限、三个 SHA-256 和对账器大小；
- 精确执行 `visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit`；
- 核对 `sudo -n -l -U pc` 只新增固定审计器 self-test/只读审计契约，不授予 shell、通配符、SETENV、Docker 或其他命令；
- 核对 `pc` 不属于 Docker 组；
- 只输出固定低敏阶段枚举，不输出 sudo 列表原文、路径异常正文或凭据。

脚本使用 EXIT trap。失败时只逆序删除本次已登记的新建 live 目标；sudoers 优先删除并对 `/etc/sudoers` 重新执行 `visudo -cf`。本次新建的 `/usr/local/libexec/molin` 仅在两个工具均已回滚且目录再次证明为空时精确 `rmdir`。预存目标绝不删除、覆盖或修改。011 暂存和失败后的 root-only 目录保留取证，清理必须使用新 ChangeId 和独立授权。

## 8. 固定 self-test 与会话结束

只有安装脚本输出固定 PASS 且所有门禁通过后，操作者才可在同一 `pc` 会话执行一次：

```bash
sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test
```

禁止直接执行、附加参数或运行真实审计。self-test 必须固定低敏输出、退出码 0、stderr 为空。随后立即退出 SSH 会话，不执行清理或其他 sudo 命令。

## 9. 测试与工程门禁

采用 TDD，至少覆盖：

1. 010 始终 consumed，历史回执不漂移，011 是唯一活动候选；
2. 011 五文件、manifest、transport、source commit/tree 和平台回执精确匹配；
3. 暂存包装器 local-check 不联网，正式模式仅一次 SFTP，不包含 SSH 或 sudo；
4. SFTP batch 只引用 011，且 010 路径静态和动态均不可达；
5. 交互命令固定一次 `sudo -k -v`、随后只使用 `sudo -n`，不出现 `-S`、askpass、密码变量、密码 stdin 或凭据日志；
6. Base64 解码脚本在 root-only 目录独占创建，摘要/大小不符不执行；
7. root 安装脚本对预存 live 目标失败且内容不变；
8. 部分安装失败只逆序删除本次新建目标，预存目标不删除；
9. sudoers candidate/live 两次 `visudo`、精确 sudo 范围和 Docker 组门禁；
10. 密码认证失败、sudo 时间戳不可用、非零、stderr、输出漂移均立即停止，且无重试；
11. consumed 状态覆盖所有 011 入口并发生在本地材料或网络读取前；
12. Windows 本地测试、Linux `--network none` 动态 shell 测试、`py_compile`、`bash -n`、Actionlint、敏感扫描和 `git diff --check` 全部通过。

测试不得连接测试服，不得调用真实 sudo，不得读取真实密码，不得将真实私钥内容载入输出。动态 root/no-clobber/回滚测试只在隔离临时目录或无网络容器内使用测试身份和假目标。

## 10. 授权清单与商业边界

工程门禁通过后生成独立 011 安装授权清单，冻结 ChangeId、source commit/tree、五文件、四项摘要、候选回执、对账器大小、暂存包装器/root 安装脚本/人工复制命令/helper SHA-256、固定交互 SSH 命令、影响、回滚和停止条件。

在精确 PR HEAD 的测试、适用 CI、独立代码安全评审、QA、产品/规格验收和 merge commit 全部完成前，状态只能是 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。合并后只能收敛为 `PENDING_USER_APPROVAL`，仍需用户再次明确批准才可执行本地检查、SFTP、交互 SSH、sudo 认证、安装或 self-test。

业务请求、上游请求和费用上限均为 `0 / 0 / 0 CNY`。生产部署、真实付费调用、真实通知、客户灰度和四周商业观察不在 011 授权范围。`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 继续未完成。图片、音频、视频、多模态异步任务、对象存储生命周期、GPU、Agent、Skills 和公开自助支付继续排除。
