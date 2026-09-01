# VID-G6 锁定SDK互操作夹具

本目录提供客户端验收程序，不创建假HTTP服务、不驱动真实Provider。`TestVideoG6LockedSDKHTTPMySQL`现会装配真实Molin loopback路由、临时MySQL、合成Provider/Store边界及两套完成媒体；父runner负责启动和回收数据库。双SDK真实HTTP已在本地隔离环境执行通过，但不代表真实Provider、MinIO、测试服或生产开放。

## 锁定与依据

- Python：`openai==2.45.0`、`httpx==0.28.1`；全部已解析依赖见`requirements.lock.txt`。
- TypeScript/Node：`openai@6.39.0`，使用`package-lock.json`与`npm ci --ignore-scripts`；Node24原生运行可擦除类型的`.ts`文件。
- 仓库依据：`docs/video-gateway-vid-g0-gate.md`第5节及`docs/video-gateway-openapi-snapshot-v1.yaml`。
- 官方调用形状：[创建](https://developers.openai.com/api/reference/typescript/resources/videos/methods/create)、[TypeScript下载](https://developers.openai.com/api/reference/typescript/resources/videos/methods/download_content)、[Python下载](https://developers.openai.com/api/reference/python/resources/videos/methods/download_content)。这些页面不是Molin规格真相源，不把官方模型和4/8/12秒替换进本地5秒合同。
- Python wheel发布SHA256：`5df105f5f8c9b711fcb9d06d2d3888cebc82506db216484c14a4e53cdf651777`；Node完整性见package-lock。版本不存在或资源方法移除时失败，不静默升级。

## 必须由主测试宿主提供

1. 与脚本处于同一网络命名空间的字面量回环地址：`http://127.0.0.1:端口`或`http://[::1]:端口`。不接受localhost、LAN、公网地址、用户密码URL、基础路径或重定向。
2. 真正Molin路由、真实临时MySQL、真实SK HMAC/用户/Project/Key/权限/权益与G5事务。不得用假成功服务满足这些脚本。
3. 一次性用户与Project；合成SK明文以`sk-molin-g6-fixture-`开头，仅通过`VID_G6_SDK_SK`环境变量注入。前缀是防误配置，不是认证替代；后端仍保存HMAC并走真实校验。
4. 已发布5秒720p视频模型及显式Project/Key授权；I2V当前权利协议接受事实和参考图合成JPEG/PNG。参考图路径必须位于manifest目录之内。
5. 两个不同、一次性、已完成可交付的真实G5夹具任务，分别供Python与TypeScript下载和删除。必须已完成安全、标识、结算、零差额对账；每个MP4建议数KiB，最多8MiB，首box为ftyp，记录精确字节数与SHA256。
6. `GET /api/token/videos/requests/{request_id}`及`/by-video/{video_id}`返回平台Envelope；data包含`billing_facts`列出的稳定原账单事实及布尔`media_deleted`。manifest只列期望事实，不批准收费政策。接口最终DTO改变时须与SSOT对齐后修改断言，不能为了绿测删去保留事实要求。
7. 两个SDK的fixture必须分别重建；删除测试会真实删除指定合成媒体正文，不回滚或删除请求/财务事实。新建T2V/I2V只测试创建与重放，不自动运行Provider或强行改任务状态。
8. 宿主另行提供准确源码hash、隔离证明、Provider计数0、合成钱包状态及测试结束资源回收证据。客户端无法单独证明服务端没有真实Provider调用，因此输出PASS仅指这里实际执行的SDK断言。
9. 本套删除可见性夹具必须保持当前Key列表不超过100条，删除前目标必须实际出现在完整单页，删除后仍要求has_more=false。这样不会把目标落在第二页误报为已隐藏；大规模分页并发另行验收。

生成`fixture.local.json`时参考`fixture.example.json`，必须替换全部占位ID/hash/金额，并准备`reference.local.png`。不要把真实凭据写入该文件；不要直接执行示例。

## 静态与依赖检查

在本目录执行：

```powershell
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.lock.txt
npm.cmd ci --ignore-scripts --no-audit --no-fund
.\.venv\Scripts\python.exe sdk_python.py
node sdk_typescript.ts
node --check sdk_typescript.ts
```

前两个客户端命令不带`--execute`，只验证安装版本并输出`http_contract=NOT_RUN`；它们不发送业务请求。Linux宿主对应使用`.venv/bin/python`。

## 实际验收

仅在宿主证明上述条件成立后，由隔离runner注入合成凭据并设置：

```powershell
$env:VID_G6_SDK_APPROVED = 'ISOLATED_SYNTHETIC_ONLY'
# VID_G6_SDK_SK由父测试进程注入；不要在命令或日志中显示值。
.\.venv\Scripts\python.exe sdk_python.py --execute --fixture fixture.local.json
node sdk_typescript.ts --execute --fixture fixture.local.json
```

仓库级可复现入口：

```powershell
$env:VIDEO_GATEWAY_G6_SDK_APPROVED = 'YES'
.\infra\scripts\verify-video-gateway-vid-g6-sdk.ps1
```

该runner只使用锁定MySQL镜像、宿主回环随机端口和本目录既有锁定依赖；合成SK只通过子进程环境变量传递，临时fixture在测试退出时自动删除。

测试覆盖：

- SDK创建T2V、缺幂等键400、同键同意图重放、异意图409。
- I2V文件multipart及同文件重放；Python通过extra_headers，TypeScript通过request options传Idempotency-Key。
- 原始JSON完整13字段/null与SDK反序列化；HTTP200，业务/HTTP request ID分离。
- retrieve/list及已创建任务可见。
- 真实完成夹具的默认MP4、200/206、ETag/If-Range、Content-Length/Range，非法/越界/多Range416，内容hash。
- SDK删除，retrieve/content404、list隐藏；双向账单查询保留固定事实，只改变媒体删除标记。

安全：Python禁环境代理且每次真实发送验证目标；TypeScript使用原生HTTP及proxyEnv为空的专用Agent实现SDK fetch边界，避免Node24全局环境代理。两端均关闭SDK重试、拒绝重定向、限制媒体读取8MiB、禁止自动读取.env。失败只输出case、异常类和HTTP码，不输出原始异常、响应正文、Prompt、Token或存储位置。首个阶段失败即停止，后续阶段视为NOT_RUN；不要将单个PASS条目汇总成全阶段通过。

不在本目录声明已完成：浏览器实际播放/seek、100并发、服务端账本唯一性SQL、JWT/管理接口、全部越权矩阵、真实Provider、真实MinIO、部署及商业验收。这些必须由其他独立证据覆盖。
