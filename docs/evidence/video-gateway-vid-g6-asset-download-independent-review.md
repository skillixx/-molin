# VID-G6 平台短效下载局部独立复核

本回执不是完整下载、完整VID-G6或Git门禁验收。

## 冻结范围

- SOURCE_STATE_ID：`97f6aab52a82faa1f98fc9fb1b5234c44b398449c662327a2c24cdcdae65d80c`。
- 157文件，清单`video-gateway-vid-g6-asset-download-current-source.json`。
- 拷贝树预期`e7dee8af3f995ff69b50e130b853dd696a9bfec57362ce1ccf8b833ee6a33680`。
- 尚未提交，HEAD仍为G5基线`52563ba450c6d488456137162580022deb06acc8`。

## 产品合同

产品角色`vid_g6_g5_gate`确认：最长15分钟同源短地址、原Bearer认证、规范GET与精确归属/版本绑定、平台派生物真实MIME和审核副本404，符合现有SSOT。原43条明确路由之外，配套content兑换也纳入验收；没有新增财务、法律、保留或真实运行授权。

## 独立QA结论

测试角色`vid_g6_contract_audit`只读复算157文件一致，确认`G6-ASSET-DOWNLOAD-001`（P1）可局部关闭。动态证据采用主代理实际工具结果，独立角色没有重跑数据库，不能冒称两次独立运行。

- 95714红例：真实无Key任务/4MiB MP4，在首片后吊销JWT仍完整返回4054453字节，无读取错误。
- 69646修复后9项专项通过，首片吊销转为仅1MiB、UnexpectedEOF及租约释放。
- 97564增强13项真实MySQL/Linux race通过，schema87，service61.576秒；JWT自然到期、吊销依赖故障、合法短效URL受六资产最早期限影响，均实际跨越首片后的失效边界并回收租约。
- JWT/SK与五用户媒体角色、字节/hash/MIME、Range、签名篡改/旧版本/跨路径/HEAD，以及原v1与生命周期局部回归通过。
- 原Token不被内存credential闭包捕获，摘要和复验能力不进入JSON；初次和逐片读取均受30秒/JWT上界限制。15680额外验证长JWT的30秒上界，生产逻辑不变。
- 40972原生全量Go测试、vet、mod verify通过；不以原生SQL SKIP冒充集成验证。

## 未完成项

完整72项G6回归24938仍待终态。跨入口共享并发、删除竞争、完整财务无写入矩阵、SDK及业务浏览器、保存/删除/管理/回调与其余G6范围未全部验收。不得据本局部P1关闭而提前提交、推送、创建PR、合并或进入G7。

真实Provider、Key、资金/钱包写入、费用、共享测试服务器和生产操作均为0。
