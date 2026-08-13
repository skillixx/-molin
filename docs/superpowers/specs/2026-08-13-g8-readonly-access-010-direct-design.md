# G8 Drop 只读入口 010 直连方案设计

## 1. 决策与边界

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009` 的候选生成与本地检查曾通过，但正式包装器在复制 Windows 私钥后因 NTFS ACL 过宽而在 SSH/SFTP 前失败。009 已消费且禁止重放。

后续一次低敏诊断已证明 `pc@8.130.9.163:10003` 可使用现有 ED25519 私钥完成免密码认证。010 因此采用用户批准的直连方案：

- 新 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010`；
- SSH/SFTP 显式引用调用方现有 `id_ed25519`、`id_ed25519.pub` 和固定 `known_hosts`；
- 不复制、不 chmod、不修改现有私钥或其 Windows ACL；
- 禁用密码、键盘交互、Agent、AskPass、转发、TTY 和调用方 SSH 配置；
- `ConnectionAttempts=1`，正式执行零重试；
- 不以物理服务器 hostname、machine-id 或云实例身份作为 Drop 入口门禁。

本轮只允许仓库设计、实现、构建、测试、CI、评审和 PR，不授权运行 `--local-check`、SSH、SFTP、root 安装或远端 sudo self-test。

## 2. 冻结来源

- source commit：`75b1fc4ddb7138495547cec03fa948648de337d7`
- source tree：`53ba990318bc1a036b442d88ff8133d776a453dc`
- Drop 入口：`pc@8.130.9.163:10003`
- 部署根：`/home/pc/molin`
- transport：`DROP_SSH_DIRECT`
- physical host identity：`NOT_APPLICABLE`

候选必须包含且只包含：

1. `SHA256SUMS`
2. `ai-gateway-reconcile`
3. `g8-test-readonly-audit`
4. `manifest.env`
5. `molin-g8-test-readonly-audit.sudoers`

## 3. 本地信任模型

### 3.1 现有身份材料

010 不生成、不复制、不修改身份材料。包装器只接受绝对路径，并在网络调用前完成：

- 普通文件、非链接检查；
- known_hosts 中目标 ED25519 唯一条目和冻结指纹检查；
- 公钥类型与冻结本地公钥指纹检查；
- 固定系统 `ssh-keygen -y` 验证私钥可读且密钥对一致；
- 路径、设备号、inode、mode、大小、mtime、ctime 和 SHA-256 证据冻结。

包装器在 SSH 前、SSH 后/SFTP 前和 SFTP 后重新计算这些证据。任何持久漂移均失败关闭，不输出路径、摘要、密钥正文或工具 stderr。

直连方案接受一个明确残余风险：同一 Windows 用户理论上可在校验与 OpenSSH 打开文件之间做瞬时替换并恢复。该风险由显式固定路径、重复证据复核、禁隐式身份发现、候选独立快照，以及后续 root-only 副本摘要复核共同降低；不得把它描述为绝对不可变。

### 3.2 候选快照

候选五文件仍复制到随机系统临时目录，使用独占创建并在复制后重新验证五文件集合、manifest、回执、各文件摘要和大小。未来获得独立授权后，SFTP 只能读取该候选快照，不读取原候选目录。

身份材料不得进入该临时目录。

### 3.3 复用 helper 的约束

010 可复用已评审 009 包装器中的通用纯函数，但加载前必须验证 helper 为普通非链接文件、前后 inode/大小/时间戳稳定、SHA-256 精确匹配，并复核目标、端口、known_hosts 指纹、部署根和必要函数契约。010 不调用 009 的 `main()`，不恢复其正式额度。

## 4. 远端执行模型（本轮不执行）

未来另获用户授权后，固定顺序为：

1. 一次 `--local-check`；
2. 一次只读 SSH 预检；
3. 一次原子 SFTP 暂存上传；
4. 一次 root-only 副本安装；
5. 一次由 `pc` 非特权会话发起的固定 `sudo -n ... --self-test`。

SSH/SFTP 共用以下约束：

- 固定系统 OpenSSH 绝对路径；
- `-F none`、`BatchMode=yes`、`IdentitiesOnly=yes`；
- 明确 `UserKnownHostsFile` 与 `IdentityFile`；
- `ConnectionAttempts=1`；
- 禁密码、键盘交互、Agent、X11、端口转发、本地命令和 TTY；
- 最小子进程环境；
- stdout/stderr 有界采集；
- 非零返回码、任意 stderr、超限或输出契约漂移立即停止。

远端预检只读取登录用户、组、部署根真实路径与低敏元数据，并检查 010 暂存目录和三个 live 目标不存在。不得读取 hostname、machine-id、数据库、Redis、RabbitMQ、业务队列、日志、监控、备份、环境变量或业务数据。

## 5. 防重放

- 001 至 009 的消费事实、历史回执和失败证据保持不变；
- 010 使用独立脚本、ChangeId、manifest、候选回执、暂存路径和授权清单；
- 010 成为生成器唯一活动候选；
- 010 正式额度一旦执行，无论成功或失败，都必须置为 consumed；
- consumed 后普通、`--self-test`、`--local-check` 和正式入口必须在 helper、候选、身份材料或网络读取前返回固定 `change_id_consumed`；
- 已消费候选只允许在系统临时目录重建并核对冻结回执，不得生成持久安装目录。

## 6. 测试边界

测试以公开 CLI 和候选格式为边界，至少覆盖：

1. 009 继续 consumed，历史 Windows/Linux 回执不漂移；
2. 010 成为唯一活动候选，错误 ChangeId、来源提交和既存输出目录失败关闭；
3. 五文件、manifest、Drop 端点、传输类型和候选回执精确匹配；
4. 包装器不包含身份材料复制、chmod 或 ACL 修改路径；
5. local-check 不调用 SSH/SFTP；
6. 正式路径只向 SSH/SFTP 传递原始身份路径，只向 SFTP 传递候选快照；
7. known_hosts、密钥对、helper 和本地证据漂移失败关闭；
8. 单 SSH、单 SFTP、`ConnectionAttempts=1`、零重试和有界输出；
9. consumed 状态覆盖普通、自检、本地检查和正式入口；
10. Windows 本地测试、Linux `--network none`、`py_compile`、Actionlint、敏感扫描与 `git diff --check` 全部通过。

测试不得连接测试服，不得读取现有私钥正文到输出，不得创建远端文件。

## 7. 安装授权清单

工程门禁通过后必须生成独立 010 安装授权清单，至少冻结：

- ChangeId、source commit/tree；
- 五文件名、四个文件摘要、候选回执和对账器大小；
- 010 包装器 SHA-256、009 helper SHA-256；
- Drop 端点、known_hosts 指纹、本地公钥指纹和部署根；
- 一次 local-check、一次 SSH、一次 SFTP、一次 root 安装、一次 sudo self-test 的精确命令摘要；
- SFTP 暂存、root-only 临时目录、可选父目录和三个 live 目标的影响面；
- no-clobber 独占创建、逐项目登记、失败逆序回滚、预存目标绝不删除；
- visudo、sudo 精确范围、Docker 组和 self-test 停止条件。

在精确 HEAD 的测试、CI、独立安全评审、QA、产品/规格验收和 merge commit 全部完成前，状态只能是 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。合并后仅可收敛到 `PENDING_USER_APPROVAL`，仍需用户再次明确批准才能连接或安装。

## 8. 商业边界

010 只服务于测试服最小只读审计入口准备。业务请求、上游请求和费用上限均为 `0 / 0 / 0 CNY`。

`G8_ENGINEERING_READY` 保持；生产部署、真实付费调用、真实通知、客户灰度和四周商业观察未授权，`G8_COMMERCIAL_ACCEPTED` 继续未完成。图片、音频、视频、多模态异步任务、对象存储生命周期、GPU、Agent、Skills 和公开自助支付不在本阶段范围内。
