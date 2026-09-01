# VID-G6 最终本地证据索引

## 绑定

- BASE_COMMIT：`52563ba450c6d488456137162580022deb06acc8`
- SOURCE_COMMIT：`4de1d59a86c47866b8cec62019ab498b4e76d47a`（当前本地源码提交，尚未推送）
- BUSINESS_SOURCE_COMMIT：`bf4a84ffdbccfa77112efc1736689508c794f9c6`
- SOURCE_STATE_ID：`f7831697a58aab29d1084330de44a3eb65ce9c243d0dd08236ab1803d9722a54`
- 最终容器副本SHA-256：`78582a80abf1e6a821ebcf8bd9b44645de0ea589096cea9ad167d96a8779d7f6`
- 分支：`feature/video-gateway-vid-g6-http-project-sk-contract`

## 权威回执

| 证据 | 用途 | 当前状态 |
|---|---|---|
| `video-gateway-vid-g6-source-state.json` | 413项文件原始字节哈希与可复算SOURCE_STATE | 两类CI失败本地修复候选 |
| `video-gateway-vid-g6-requirements.json` | 47路由与R01—R22逐项矩阵 | R01—R20 PASS；R21等待新SOURCE_STATE终审；R22待Git/CI/合并 |
| `video-gateway-vid-g6-local-verification.json` | 全量、SDK、兼容、CI分类、通用门禁与零真实操作 | CI分类修复后本地PASS，待独立终审 |
| `video-gateway-vid-g6-defects-final.md` | 首轮审查与最终全量缺陷关闭轨迹 | OPEN P0/P1/P2候选均为0 |
| `video-gateway-vid-g6-review-remediation-progress.json` | 首轮P1/P2整改专项哈希 | 历史专项均PASS |
| `video-gateway-vid-g6-independent-reviews-final-v2.md` | 上一SOURCE_STATE五轴独立终审 | STALE_AFTER_SENSITIVE_SCANNER_FIX |
| `video-gateway-vid-g6-independent-reviews-final-v3.md` | 当前SOURCE_STATE五轴独立终审 | QA/PM/Standards/Spec/DEV全部PASS，P0/P1/P2=0 |

## 最终验证

- G6 all：一次性MySQL 8，000001→000109，Linux race，service 1237.281秒，必需测试全部RUN/PASS。
- SDK：Python `openai==2.45.0`、TypeScript `openai@6.39.0`，真实loopback HTTP与临时MySQL；Provider/Store为合成边界。
- 兼容：VID-G5完整1→77、down/re-up、财务与Chat兼容PASS；IMG-G6 HTTP/MySQL/race PASS。
- 通用：`go test ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff`、变更Go文件gofmt、Bash语法、diff与高风险凭据模式扫描PASS。
- 浏览器：本地5秒可播放MP4解码、seek至3秒并继续播放，390px无横向溢出；不冒充真实Provider/MinIO/生产浏览器链。
- CI分类：大PR的Git复制检测固定有限`-l2000`，分类单测43项及其余Draft/Ready合同29项通过；stderr warning与非零Git退出均失败关闭。原PR精确base/head从ClassificationError转为PASS并启用全部Ready门禁。
- 共享敏感扫描：ASCII字母数字边界保留独立手机号检测并排除hex摘要内数字片段；6项自测及178当前文件、584已提交路径、595历史blob、596历史事件扫描PASS，findings=0。
- G6客户专项：基线迁移严格停在000064，再显式执行65/66；请求夹具时间固定在汇总窗口，失败诊断遮蔽临时口令；4项合同测试及真实临时MySQL 8专项PASS。

## 当前Git与PR事实

- PR：`#422`，状态`OPEN_CURRENT_FAILED_HEAD`。
- 当前远端PR HEAD：`781c6cb8826392dc8b6674dc13bed572ecf98093`。
- 当前Ready CI：run `33524057389`，分类器PASS；文档、阶段5、G7、G8四处扫描因同一摘要误报FAIL；后端G6隔离步骤另因专项迁移循环和日期夹具漂移FAIL。
- 当前本地两类修复：共享扫描器边界、G6客户专项迁移截止与固定时间夹具均已本地复验；`LOCAL_FIX_COMMITTED=YES`、`LOCAL_FIX_PUSHED=NO`。

## 安全边界

`REAL_PROVIDER_REQUESTS=0`、`REAL_PROVIDER_KEYS=0`、`REAL_WALLET_WRITES=0`、`REAL_USER_FUNDS=0`、`REAL_ADJUSTMENTS=0`、`PROVIDER_COST_CNY=0`、`TEST_SERVER_WRITES=0`、`PRODUCTION_OPERATIONS=0`、`VID_G7_STARTED=NO`。

## 后续门禁

SOURCE_STATE `f7831697a58aab29d1084330de44a3eb65ce9c243d0dd08236ab1803d9722a54`下QA、PM、Standards、Spec及DEV Review全部PASS且P0/P1/P2=0。下一步提交、push并等待精确新HEAD的Ready完整CI。
