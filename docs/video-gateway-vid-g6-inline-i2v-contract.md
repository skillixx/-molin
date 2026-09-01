# VID-G6 OpenAI multipart图生视频合同（开发中）

## 功能边界

`POST /v1/videos`无`input_reference`时继续执行文生视频；携带且只携带一张PNG/JPEG上传文件时进入图生视频。接口拒绝`image_url`、data URL、Base64字段、任意`file_id`、重复文件和未知part。Prompt与图片正文仅在请求进程内使用，不进入普通响应、日志、MQ、Outbox或财务字段。

本入口仍只接受Project SK与单值Idempotency-Key。图生视频复用Project所有者对当前合成权利政策的有效接受事实，不增加自定义multipart同意字段，也不允许Codex代替真实用户接受政策。

## 服务端受控输入链

Handler只做有界multipart读取和字段白名单，不接收bucket、object_key、URL或签名参数。读取正文前按用户预留请求字节预算，未知长度按最大请求计入；累计超过64MiB返回429。正文最大10MiB且只保留一份进程内`[]byte`，服务不做第二次完整复制，并再次核对扩展名、声明/探测MIME、大小和SHA-256。应用层要求显式装配`VideoInlineUploadStore`；缺失时503，不向预签URL自调用。

schema108只扩展原`ai_video_upload_controls`插入守卫，使其同时接受`platform_presigned`和`openai_inline_multipart`；不创建平行上传账本。inline会话使用独立版本化指纹和服务端生成的`inline/<user>/<project>/<session>`对象键，平台预签上传继续使用原v1指纹，旧重放兼容不变。

任何UploadSession形成前先只读复验模型、固定规格、Prompt、Prompt审核、当前Project权利及既有生成命令operation；无权利或T2V→I2V同键冲突时输入事实为0。服务端写入原件后复用原Create→Complete链完成不可变封存、图片解码、规范化、安全审核、InputAsset发布和完整性复核；随后把唯一ready InputAsset交给原VideoCommand、G5 Quote/Hold/Task/TaskInput事务，最终入口再次复验完整权限、权利、输入和财务门禁。公开响应仍是HTTP 200标准Video Job，`prompt=null`，不返回UploadSession、InputAsset或对象位置。

## 幂等和并发

上传创建/完成键由外部生成幂等键做域分离SHA-256得到，不保存原Idempotency-Key。`PutOriginal`执行条件不可变写：同Target/size/hash/正文重复写成功，任何差异冲突，Seal后同内容重放也不能创建新对象身份或版本。相同键与相同文件重放复用原会话和输入；相同键不同文件、Prompt或规格冲突。有效`verifying`租约只等待；临时失败把租约收口后，原键可接管同一会话，旧worker受version/lease围栏阻止发布。

真实临时MySQL的`inline-i2v`专项使用100个同时起跑的loopback HTTP请求，验证每个意图只有一个UploadSession、InputAsset、TaskInput、Quote、Hold和Task，并验证无权利及跨模式零输入、无文件T2V、空文件、伪扩展、外部URL字段及重复文件拒绝。还覆盖原件已写但回包未知、Seal临时失败、Seal成功回包丢失，以及Complete后政策换版导致最终拒绝时零Video Request/Task/Hold并建立`pending_delete`凭据。

终审整改新增真实TCP用例：6个Content-Length接近10MiB的请求完成鉴权后阻塞multipart读取，第7个在读取正文前返回429；主动断开前6个连接后全部Handler退出，UploadSession、InputAsset和Video Task仍为0。最新专项副本SHA-256为`ae766c92f8075ac5568196b01ee7de92af67bf696d2283359265fa51a5d39b21`。

## 尚未闭合

终审整改已补跨用户/Project/Key、Complete后到生成事务前断连及生成COMMIT确认未知；确认丢失后原键恢复原Job，Session/Input/Request/Task/Hold唯一且对象只写一次。最新inline完整组合副本SHA-256为`66ee69584f6dedc5b89196c042eb987d0b96c1c6b3d60ef74a5476c0bd6114ab`，COMMIT精确专项后续副本为`affab42574dc9573db7cce5b92acf6d29fa0edafc89943b027d6ded377ab9965`。仍需随最终SOURCE_STATE统一重跑和独立复核。确定拒绝只将输入置为`pending_delete`，物理对象继续遵守既定7天保留边界；未知财务结果不擅自删除。当前仅为关闭态本地Fake对象存储验证，真实Provider、真实对象存储、测试服和生产均未运行。

回滚schema108只恢复禁止新inline控制记录的插入守卫，保留已有UploadSession、InputAsset、Task、财务和审计事实；不得删除事实后重新生成或绕过当前授权。
