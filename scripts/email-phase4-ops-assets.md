# DirectMail Phase 4 运行时投影运维资产

本文说明测试 API 日志采集、恢复及两个前端容器完整部署树导出工具。脚本不访问数据库、Redis、邮件供应商或 Git；本轮只完成离线实现，未连接测试机，也未执行任何进程或容器操作。

## 测试 API 日志采集与恢复

`run-email-phase4-api-log-capture.ps1` 默认关闭。真实执行同时要求 `-Execute`、固定确认短语和人工生成的 32 位小写十六进制 `CaptureId`。远端 payload 只接受测试机上唯一同时满足以下条件的进程：

- 唯一监听 8080；
- 系统内唯一名为 `molin-api` 的可执行文件；
- 进程 UID 等于执行用户；
- 原 stdout 与 stderr 均为 `/dev/null`。

采集前会在唯一新 stage 中以 0600 保存 `/proc` 原始 `environ`、`cmdline`、状态文件和空日志，并冻结 PID/starttime、UID、cwd、二进制路径、设备号、inode、大小和 SHA-256。环境快照若包含重复键会在停止进程前失败；恢复通过 `execve` 传入全部键值，并从新进程 `/proc/<pid>/environ` 逐键逐值复核。该保证是“完整键值字节语义等价”，不声称 `envp` 项目排序与原始字节流完全一致。完整 cmdline 原始字节也必须一致。

完成二次身份核验后只发送 `SIGTERM` 并等待自然退出，绝不升级信号。采集进程使用上述环境语义、原 argv、原二进制和 cwd 重启，stdout/stderr 合并写入唯一 `application.safe-source.log`。状态机在每个停止、启动和恢复阶段记录固定状态；任一新进程失败或后续状态落盘失败，都先按已知 PID/starttime 精确收敛该进程，再允许恢复原服务。payload 仅把自己直接 `fork` 的 PID/starttime 登记为子进程归属，只有该集合中的 PID 才会调用 `waitpid(WNOHANG)` 回收 zombie；原 API、其他会话启动的采集 API 等非子进程只观察 `/proc`，绝不误用 `waitpid`。无法确认收敛时禁止再次启动，安全摘要只报告低基数服务状态并保留 stage。

恢复操作再次绑定 state 中的 PID/starttime，只发送 `SIGTERM`。采集进程完全退出后，日志封闭为 0400，之后才使用原环境、原 argv、原二进制和 cwd 恢复 `/dev/null`。首次恢复启动失败时先收敛已知失败 PID，再进行一次恢复尝试；仍失败则保留状态与日志并返回安全失败摘要。日志不会自动删除或裁剪。

两个 PowerShell 启动器要求 payload 为严格 UTF-8、无 BOM/NUL、仅 LF。Windows PowerShell 5.1 不使用 `StandardInput` 的 StreamWriter/BaseStream，而是先把已复核原始字节写入仅当前身份可访问的随机临时文件，再由 `Start-Process -RedirectStandardInput` 让操作系统把该文件句柄直接作为 stdin；进程结束后只删除本次三个固定临时文件。SelfTest 会通过同一路径启动本地 Python 字节回显子进程，独立核对 stdin SHA-256、长度和包含空格的 argv，证明没有 BOM、转码或参数漂移。SSH 总等待上限为 120 秒，stdout 只接受不超过 512 字节的一条完整固定 schema，且操作 ID 必须与调用参数一致；stderr、额外行、字段增删、顺序变化或退出码不匹配都会关闭，原始输出不会回显。

```powershell
# 仅验证本地启动器，不访问测试机。
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\run-email-phase4-api-log-capture.ps1 -SelfTest

# 双门禁示例；正式执行前必须另行获得运行授权，本轮禁止执行。
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\run-email-phase4-api-log-capture.ps1 `
  -Action Capture -CaptureId 0123456789abcdef0123456789abcdef `
  -Execute -Confirm I_CONFIRM_PHASE4_API_LOG_CAPTURE

powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\run-email-phase4-api-log-capture.ps1 `
  -Action Restore -CaptureId 0123456789abcdef0123456789abcdef `
  -Execute -Confirm I_CONFIRM_PHASE4_API_LOG_RESTORE
```

## 前端完整部署 manifest

`run-email-phase4-frontend-export.ps1` 同样默认关闭。远端 payload 只检查固定容器 `molin-admin`、`molin-user` 和固定目录 `/usr/share/nginx/html`，使用 `docker inspect` 与 `docker cp ... -` 只读流式导出，不在容器内执行命令或写文件。payload 总时限为 90 秒，启动器总等待上限为 120 秒。

每个 tar 成员在落盘前拒绝绝对路径、`..`、`.env*`、重复路径、symlink、hardlink、设备节点、超深或超限树；成员读取必须恰好达到声明大小，所有文件写入均循环处理短写。目标必须是唯一新 stage；失败 stage 保留，不自动删除。导出后要求完整树同时具备 `index.html`、`assets/*.js|mjs` 和 `assets/*.css`，并按 `phase4_runtime_source_projection.py` 相同的 LIFO 遍历和摘要字节格式独立计算 `tree_sha256/file_count/byte_count`。工具绑定准备器 SHA-256 `2bc04f38c2e5073b5fe390c83394f16acc46b0c6b353834a848eec5487f606ab`；每个容器导出后单独复核，两个导出全部完成后再统一复核两容器 ID 与 image SHA。

payload 不再依赖远端仓库内的 `tests/email` 路径。启动器第三参数固定传入已验证 stage companion：`/home/pc/molin-runtime/phase4-ops-linux-0346ff54/tests/email/phase4_runtime_source_projection.py`，payload 同时把该绝对路径作为唯一允许值，禁止调用者替换。执行 Docker 读取前，90 秒总门禁会用 strict resolve 与 `O_NOFOLLOW` 文件描述符确认它是非 symlink 普通文件、属主为当前 UID、权限精确为 0400 或 0600、大小为 62893 字节，并前后绑定 device/inode、复核固定 SHA；缺失、错摘要、symlink、路径注入或身份漂移均关闭。成功摘要必须带 `projection_bound=true`。本资产只消费该 companion，不负责远端上传或创建。

生成的 `admin.manifest.json` 与 `user.manifest.json` 权限固定为 0600，且只包含 `role`、`tree_sha256`、`file_count`、`byte_count`、`container_or_image_digest` 五个键。导出树关闭为目录 0555、文件 0444，供投影准备器只读消费。

```powershell
# 仅验证本地启动器，不访问测试机。
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\run-email-phase4-frontend-export.ps1 -SelfTest

# 双门禁示例；正式执行前必须另行获得运行授权，本轮禁止执行。
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\run-email-phase4-frontend-export.ps1 `
  -ExportId 0123456789abcdef0123456789abcdef `
  -Execute -Confirm I_CONFIRM_PHASE4_FRONTEND_READONLY_EXPORT
```

## 离线契约

普通模式与优化模式必须分别执行；二者都只运行默认关闭门禁、纯函数夹具和危险能力扫描，不访问网络、Docker、数据库或凭据。攻击夹具覆盖重复环境键、内核短写、capture/restore 启动失败后的已知 PID 收敛、capture/restore 状态落盘失败、launch 超时、日志封闭模式、裁剪 tar、容器漂移及准备器 SHA 绑定。POSIX 环境还覆盖 companion 缺失、错 SHA、symlink、路径注入、owner 不匹配、错误大小/截断、打开后的 mode 漂移，以及用同内容合法文件原子替换路径造成的真实 fd/path inode 漂移；错误大小夹具会把摘要函数伪装为固定 SHA，确认大小门禁仍在摘要读取前拒绝。所有身份攻击只操作当前 `TemporaryDirectory` 内的文件并收敛为固定关闭分类。随后 contract 额外调用真实准备器算法交叉验证树摘要，并真实执行一个本地临时子进程的 `fork → SIGTERM → zombie → waitpid(WNOHANG)` 回收夹具；该夹具只操作 contract 自己直接 fork 的 PID。Windows 输出会明确标记 `projection_crosscheck=skipped_nonposix` 和 `zombie_reap=skipped_nonposix`，不能替代 Linux/CI 结果。

最新 Linux 全新 stage 证据必须保留：normal 契约曾在真实 projection 交叉验证处失败，原因是 contract 创建的临时前端树仍带所有者写权限，真实 `frontend_identity` 按 `frontend_contract` 正确拒绝；同一轮启动脚本使用 `set -e`，因此 `-O` 未执行。该失败属于 contract 夹具错误，不是 projection 缺陷，禁止放宽 projection。候选修复会在调用真实 projection 前把完整树封闭为目录 0555、文件 0444并逐项复核；`close_tree` 本身也位于权限恢复的 `try/finally` 内，即使中途 `chmod` 失败，finally 也只在树仍真实存在、不是 symlink 且解析后位于当前 `TemporaryDirectory` 时，才把本次树恢复为目录 0700、文件 0600。故障注入夹具会令第二次 `chmod` 失败，并确认树外只读哨兵未改变；路径逃逸或 symlink 会关闭失败。新的 Linux normal/`-O` 实机复验完成前，仍不得声称 POSIX 契约通过。

POSIX zombie 夹具不创建或依赖独立临时目录，只操作 contract 自己直接 fork 的子进程和 `/proc/<pid>`；共享的 `TemporaryDirectory` 根会独立核验为 0700，因此不存在把可写前端树误交给 projection 的同类问题。

```powershell
python -B scripts\email-phase4-ops-assets-contract.py
python -O -B scripts\email-phase4-ops-assets-contract.py
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\run-email-phase4-api-log-capture.ps1 -SelfTest
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts\run-email-phase4-frontend-export.ps1 -SelfTest
```

离线通过只证明脚本契约，不代表远端日志已形成、前端部署树已导出、六表面投影已通过或 Phase 4 已验收。
