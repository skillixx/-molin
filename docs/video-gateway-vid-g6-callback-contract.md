# VID-G6 内部回调合同（开发与验证中）

## 功能与边界

`POST /api/internal/ai/provider-callbacks/{provider_code}`接收已验签的Provider状态事件，复用原G3 Callback、Task/Event及G4/G5账本。用户JWT或Project SK不能替代回调签名。当前仅显式启用`fake-native-async`的隔离工程协议，不是Runware真实Webhook兼容声明，不请求Provider、不抓取公网内容、不直接结算或交付。

`RegisterVideoInternalRoutes`独立注册；bootstrap尚未装配。交通开关默认关闭，缺应用、专用32字节secret或Fake显式许可时503，不使用默认secret或JWT密钥回退。

## 签名参考

必须是UTF-8 JSON、规范路径、无查询参数。三个头均为单值：`X-Molin-Callback-Timestamp`、`X-Molin-Callback-Nonce`、`X-Molin-Callback-Signature`。

时间戳是规范十进制Unix秒，拒绝前导零、正号、空格及小数；按接收Unix整数秒比较，允许过去300秒至未来30秒，边界包含。nonce是32字节随机数的64个小写hex字符；签名是专用secret计算的HMAC-SHA256小写hex。规范串按下列顺序以LF分隔，末尾不追加LF：

```text
molin-video-callback-v1
POST
/api/internal/ai/provider-callbacks/fake-native-async
<timestamp>
<nonce>
<原始body字节的SHA-256小写hex>
```

必须使用原始body摘要，不将JSON重新序列化后验签。方法、路径、时间、nonce以及body中的任务/事件身份均被覆盖。正文最大64KiB，精确五键：provider_task_id、external_event_id、video_id、status、progress；拒绝未知、重复、缺失、null、尾随JSON和大小写别名。ID为1—128字符规范公开ID，Provider任务额外要求taskUUID-前缀。status沿用processing/succeeded/failed/cancelled/unknown；progress为0—100整数，暂不把Provider进度当作交付证明。

这些签名细节由产品工程角色确认属于本阶段无费用工程冻结，不是新的商业、法律或真实用户授权。

## 去重与ACK

- 原事件唯一键仍是provider_code + provider_task_id + external_event_id。
- 无效签名不写nonce、不抢占原Callback事件键。
- nonce只保存摘要，绑定完整规范请求摘要和原Callback事件ID。同nonce更换时间戳、路径或body均冲突。
- 同一事件可用新时间戳、新nonce重试，原body摘要必须相同；同event异body409。
- 原任务及Provider引用由数据库再次绑定，不能相信客户端owner或signature_status。
- nonce与原Callback、Task/Event状态处理处于同一事务。数据库提交结果未知时不猜测成功，重试从原事实恢复。

成功返回HTTP200，JSON精确三个bool：accepted、applied、replayed。accepted表示合法事件已持久化处理；applied表示该事件曾成功应用，不表示本次重放再次推进；安全忽略可以为true/false/false。ACK不返回内部任务ID、存储位置、Provider正文、Prompt、签名或财务金额。

| 情形 | HTTP |
|---|---|
| 格式、重复头、字段或路径歧义 | 400 |
| 不支持的内容类型 | 415 |
| 签名或时间窗失败 | 401 |
| 未知Provider、任务或错绑 | 404 |
| nonce或事件正文冲突 | 409 |
| 关闭、缺依赖、持久化结果暂不可确认 | 503 |
| 已应用、重放、安全忽略 | 200 |

## 开发说明与当前缺口

`video_callback_verifier.go`实现有界原文验签与严格五字段解析；`video_callback_service.go`在原应用上调用一次G4账本桥接，禁止再调用Gateway.HandleCallback重复推进；`video_callback_handler.go`负责严格头、正文与低敏ACK；`video_callback_route.go`单独装配内部入口。

迁移000093只增加`ai_video_callback_nonces`，外键绑定已验签、已处理且归属完整的原Callback事件；禁止更新和删除。它不是新的任务、事件或财务账本。回滚关闭接收入口并保留原事实，不重新Submit或覆盖历史。

验签固定向量经.NET HMAC独立计算，并由Go测试验证签名范围、时间边界、篡改、JSON歧义及普通JSON不可序列化；关闭路由先编译红例后通过。真实HTTP/MySQL验收正在进行，不能用原生跳过数据库的结果冒充集成通过。

独立接入审查发现的三类问题均已补真实反例：55332复现迟到failed覆盖fetching；63201复现跨任务同外部事件号及128字符事件号503；3542复现已应用事件重放再次安排人工核对事件。修复在原G4桥接持锁后限制回调来源，只允许submitted/processing推进；其他事件仍原三元去重并保存ignored，不删除G3通用失败路径。内部TaskEvent采用`vid_g4_cb_v2_`加完整三元组长度前缀摘要，77字符并与旧命名域分离。G5对账只在本次新应用事件时执行，不因历史Applied=true再次执行。

首个回调直接成功的反例42223也已复现；修复在同一事务沿submitted→processing→fetching两次原CAS迁移，补齐事件但不跳过矩阵。原已存在Callback（包括历史ignored）不被重新解释。16000的验签、真实HTTP和关闭态三项专项已通过，包含首次100竞争及既有100重放、乱序/终态、直接成功、人工核对后的原ACK重放；它不覆盖之后新增的全部故障测试，也不是全阶段验收。

后续还须完成所有写点回滚、提交未知、无效签名抢占、100并发、跨任务同外部事件号、完整乱序/终态、金融和租约不变，以及精确源码独立验收。

最新增量：85483九项专项全部实际RUN/PASS，schema93与Linux race通过，service21.721秒。已覆盖前述基础HTTP、nonce写入后完整回滚、真实COMMIT丢确认恢复、旧RR历史ignored保护，以及旧G4门面与内存账本的三项兼容用例。旧RR缺口84216曾实际把submitted推进processing并新增事件，现使用实体当前读修复。旧G4门面的版本冲突已有重读逻辑，真实SQL组合用例通过，未为未复现的问题修改Gateway。完整HTTP负向、跨Task nonce竞争、更多故障写点、I2V租约及全阶段兼容/独立验收仍未完成，不能将本次九项通过称作完整G6通过。

相关：[G6完整合同](./video-gateway-vid-g6-http-project-sk-contract.md)、[API总表](./full-api-design.md)。
