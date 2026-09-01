# VID-G6 最终本地证据索引

## 绑定

- BASE_COMMIT：`52563ba450c6d488456137162580022deb06acc8`
- SOURCE_COMMIT：`f1aaa20d5a1a1c1da1976d11601de08be690cf24`（CI分类有限上限修复源码提交）
- BUSINESS_SOURCE_COMMIT：`bf4a84ffdbccfa77112efc1736689508c794f9c6`
- SOURCE_STATE_ID：`2644f9a29b34176fdd8acbdc8c993c150d9f96658a9210eec1192dffeded46d9`
- 最终容器副本SHA-256：`5a2769740f149c42c1da2cba7122604bd56197f9990d551b0ad9cbcc250233c3`
- 分支：`feature/video-gateway-vid-g6-http-project-sk-contract`

## 权威回执

| 证据 | 用途 | 当前状态 |
|---|---|---|
| `video-gateway-vid-g6-source-state.json` | 409项文件原始字节哈希与可复算SOURCE_STATE | CI分类修复候选 |
| `video-gateway-vid-g6-requirements.json` | 47路由与R01—R22逐项矩阵 | R01—R20 PASS；R21等待新SOURCE_STATE终审；R22待Git/CI/合并 |
| `video-gateway-vid-g6-local-verification.json` | 全量、SDK、兼容、CI分类、通用门禁与零真实操作 | CI分类修复后本地PASS，待独立终审 |
| `video-gateway-vid-g6-defects-final.md` | 首轮审查与最终全量缺陷关闭轨迹 | OPEN P0/P1/P2候选均为0 |
| `video-gateway-vid-g6-review-remediation-progress.json` | 首轮P1/P2整改专项哈希 | 历史专项均PASS |
| `video-gateway-vid-g6-independent-reviews-final-v2.md` | 最终SOURCE_STATE五轴独立终审 | QA/PM/Standards/Spec/DEV全部PASS，P0/P1/P2=0 |

## 最终验证

- G6 all：一次性MySQL 8，000001→000109，Linux race，service 1232.352秒，必需测试全部RUN/PASS。
- SDK：Python `openai==2.45.0`、TypeScript `openai@6.39.0`，真实loopback HTTP与临时MySQL；Provider/Store为合成边界。
- 兼容：VID-G5完整1→77、down/re-up、财务与Chat兼容PASS；IMG-G6 HTTP/MySQL/race PASS。
- 通用：`go test ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff`、变更Go文件gofmt、Bash语法、diff与高风险凭据模式扫描PASS。
- 浏览器：本地5秒可播放MP4解码、seek至3秒并继续播放，390px无横向溢出；不冒充真实Provider/MinIO/生产浏览器链。
- CI分类：大PR的Git复制检测固定有限`-l2000`，分类单测43项及其余Draft/Ready合同29项通过；stderr warning与非零Git退出均失败关闭。原PR精确base/head从ClassificationError转为PASS并启用全部Ready门禁。

## 当前Git与PR事实

- PR：`#422`，状态`OPEN_OLD_HEAD`。
- 当前远端PR HEAD：`23b4f5ca6865b16ea11026a46693960071e02e0b`。
- 首次Ready CI：run `33515822504`，分类器FAIL，依赖门禁SKIP，必选汇总FAIL。
- 当前本地有限上限修复：`LOCAL_FIX_COMMITTED=YES`、`LOCAL_FIX_PUSHED=NO`，源码提交`f1aaa20d5a1a1c1da1976d11601de08be690cf24`。

## 安全边界

`REAL_PROVIDER_REQUESTS=0`、`REAL_PROVIDER_KEYS=0`、`REAL_WALLET_WRITES=0`、`REAL_USER_FUNDS=0`、`REAL_ADJUSTMENTS=0`、`PROVIDER_COST_CNY=0`、`TEST_SERVER_WRITES=0`、`PRODUCTION_OPERATIONS=0`、`VID_G7_STARTED=NO`。

## 后续门禁

SOURCE_STATE `2644f9a29b34176fdd8acbdc8c993c150d9f96658a9210eec1192dffeded46d9`下QA、PM、Standards、Spec及DEV Review已全部PASS。下一步提交本证据、普通push更新唯一PR并等待新HEAD的Ready完整CI。PR/CI/merge事实只能在真实发生后更新，当前不得冒称新CI已启动或通过。
