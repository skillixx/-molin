# AI 网关 G8 测试服低敏 SSH 传输诊断 005 执行记录

## 1. 执行结论

- ChangeId：`CHG-G8-TEST-READONLY-TRANSPORT-DIAG-20260812-005`。
- 目标：`pc@8.130.9.163:10003`。
- 本地检查：唯一一次执行，结果 `PASS`。
- 正式只读 SSH：唯一一次执行，结果 `PASS`。
- 重试：`0`。
- 业务请求 / 上游请求 / 费用：`0 / 0 / 0 CNY`。
- 005 已消费，禁止再次执行。

## 2. 低敏结果

```text
ssh_exit_class=ZERO
stdout_contract=EXACT
stdout_bytes=39
stdout_sha256=e76ff5fe44e778c54ced87cd38a604443bce0ced0f627ddcb20c061e1a8afa51
stderr_state=EMPTY
stderr_lines=0
stderr_bytes=0
stderr_sha256=NONE
diagnostic=PASS
```

上述结果没有保存或输出远端错误正文。SSH 可能由系统自动记录 sshd、journald 或审计日志；本轮未获授权读取或删除这些日志。

## 3. 证据边界

本次只证明固定 OpenSSH、公钥身份、严格 known_hosts 与远端 `/usr/bin/python3 -I -` 固定标记程序可用。远端程序未读取暂存目录、部署文件、身份数据库、日志或业务数据，因此不能证明 003 暂存目录存在、不存在或内容完整；003 暂存状态仍为 `UNKNOWN`。

本次未执行 SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、Docker、数据库、队列、服务控制、HTTP、生产连接、真实上游、通知或客户灰度。继续关闭暂存 `UNKNOWN` 必须使用新的 ChangeId、完成独立工程门禁并取得用户再次明确授权。
