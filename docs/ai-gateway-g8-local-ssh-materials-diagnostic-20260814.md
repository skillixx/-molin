# G8 本地 SSH 材料诊断记录（2026-08-14）

> 状态：首次 `FAILED / known_hosts_unavailable`，缺陷修复候选复测 `PASS`。本记录只描述无 ChangeId 本地诊断，不构成 013 SSH、测试服连接、上传、清理、安装、部署或运行态审计授权。

## 1. 执行范围

- 独立工作树：`D:\molingproject\molin-gateway-013-diagnostic`
- 精确提交：`c56a0e8b0f357d4f27520b60333abd43abdd6f1d`
- 诊断脚本：`infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py`
- 冻结大小：`14615`
- 冻结 SHA-256：`aa1bb957e9b950eed263424a0b1e104695f68cad076e33b74fe5b70e54b320ed`
- 执行前复核：大小与 SHA-256 均匹配。

## 2. 低敏结果

```text
G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=known_hosts_unavailable
```

该枚举只表示本地受信主机记录不可用或不符合冻结契约，不输出也不能推断实际主机指纹、私钥、公钥或文件内容。

## 3. 影响与边界

- 网络连接：0。
- 013 正式 SSH：0，未消耗。
- SFTP/SCP/上传/下载：0。
- 测试服与生产修改：0。
- 业务请求、上游请求、费用：`0 / 0 / 0 CNY`。

目标 Drop 端点的 ED25519 主机记录已经通过既有可信独立渠道核对且保持不变；不得修改信任库、自动接受未知主机密钥或关闭主机校验来绕过。修复后可以重复运行本地诊断，只有固定结果为 `PASS`，并且新 014 完成工程门禁与用户再次明确批准，才允许执行一次只读 SSH。

## 4. 根因与修复候选复测

低敏分解确认：目标端点存在唯一 ED25519 记录，固定指纹匹配；失败不是主机记录缺失或漂移。根因是 Windows 分支给系统 `ssh-keygen.exe` 的最小环境只保留 `SystemRoot`，遗漏系统 OpenSSH 实际需要的 `PROGRAMDATA`，工具因此在 stderr 为空时返回非零并被收敛为 `known_hosts_unavailable`。

修复候选从 Windows 系统 API 获取 Windows 目录和公共应用数据目录，仅把这两个可信本地绝对路径传给系统 OpenSSH；调用方伪造的 `SystemRoot`、`PROGRAMDATA`、UNC、相对路径、代理、PATH 或其他环境变量均不继承。验证结果：

- Windows 定向单测：诊断器 18 项通过、1 项按平台跳过；013 墓碑入口 1 项通过；014 包装器 23 项通过、6 项 POSIX 动态语义按平台跳过。
- Linux 断网门禁：诊断器 17 项通过、2 项 Windows 路径语义按平台跳过；013 墓碑入口 1/1、014 包装器 29/29 全部通过。
- 离线 self-test：`PASS`。
- 真实本地材料诊断：`G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=PASS`。
- 修复候选诊断器大小：`15833`。
- 修复候选诊断器 SHA-256：`3382b66c289c08b54ad36abc78969983ce89a89b7216e84c23b31aec6e34cadf`。
- 旧 013 已墓碑化；新的 014 包装器摘要记录在独立 014 工程候选清单中。
- 跨平台检出通过 `.gitattributes` 固定相关安全脚本为 LF，确保 Windows CI 与 Git 原始对象使用同一冻结字节摘要。

PR #377 最终工程 HEAD `477a2d3da9b672c1fbc8b792de5eef7a4ed29af1` 的 CI run `31762838448` 已完成并成功，原生 Windows G8 门禁、Linux 断网动态测试所在 G8 主门禁及 `CI 必选门禁汇总` 均为 SUCCESS；同一 HEAD 的独立安全、QA、产品/规格复评均为 P0/P1/P2=0。PR 已按 merge commit `3c9c5cf489b28e45b789c114243e45936a0d81d2` 合入 main，远端功能分支已删除；合并后从原始 Git blob 复核诊断器与 014 的大小、SHA-256 和 LF 均未漂移。2026-08-13 的旧 013 冻结清单继续失效且可执行入口已墓碑化。不得使用本次本地 PASS 或工程合并直接联网；014 当前为 `PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`，只有用户对独立清单再次明确批准后才可执行一次。
