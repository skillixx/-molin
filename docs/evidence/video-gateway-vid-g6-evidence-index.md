# VID-G6 最终本地证据索引

## 绑定

- BASE_COMMIT：`52563ba450c6d488456137162580022deb06acc8`
- SOURCE_COMMIT：`52563ba450c6d488456137162580022deb06acc8`（提交前工作树）
- SOURCE_STATE_ID：`7e03bb3ef60c2eb61bdec03854467262065221da2535174635a1f41b23a49a20`
- 最终容器副本SHA-256：`b9593d3d3d9a42b8e07844ec6ab838f37dc06e5eee1909f79efe2215759193c2`
- 分支：`feature/video-gateway-vid-g6-http-project-sk-contract`

## 权威回执

| 证据 | 用途 | 当前状态 |
|---|---|---|
| `video-gateway-vid-g6-source-state.json` | 407项文件原始字节哈希与可复算SOURCE_STATE | FINAL_LOCAL_CANDIDATE |
| `video-gateway-vid-g6-requirements.json` | 47路由与R01—R22逐项矩阵 | R01—R20 PASS；R21独立终审PASS并等待Git/CI；R22待Git/CI/合并 |
| `video-gateway-vid-g6-local-verification.json` | 全量、SDK、兼容、通用门禁与零真实操作 | PASS_LOCAL_FINAL_CANDIDATE |
| `video-gateway-vid-g6-defects-final.md` | 首轮审查与最终全量缺陷关闭轨迹 | OPEN P0/P1/P2候选均为0 |
| `video-gateway-vid-g6-review-remediation-progress.json` | 首轮P1/P2整改专项哈希 | 历史专项均PASS |

## 最终验证

- G6 all：一次性MySQL 8，000001→000109，Linux race，service 1230.686秒，必需测试全部RUN/PASS。
- SDK：Python `openai==2.45.0`、TypeScript `openai@6.39.0`，真实loopback HTTP与临时MySQL；Provider/Store为合成边界。
- 兼容：VID-G5完整1→77、down/re-up、财务与Chat兼容PASS；IMG-G6 HTTP/MySQL/race PASS。
- 通用：`go test ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff`、变更Go文件gofmt、Bash语法、diff与高风险凭据模式扫描PASS。
- 浏览器：本地5秒可播放MP4解码、seek至3秒并继续播放，390px无横向溢出；不冒充真实Provider/MinIO/生产浏览器链。

## 安全边界

`REAL_PROVIDER_REQUESTS=0`、`REAL_PROVIDER_KEYS=0`、`REAL_WALLET_WRITES=0`、`REAL_USER_FUNDS=0`、`REAL_ADJUSTMENTS=0`、`PROVIDER_COST_CNY=0`、`TEST_SERVER_WRITES=0`、`PRODUCTION_OPERATIONS=0`、`VID_G7_STARTED=NO`。

## 后续门禁

新SOURCE_STATE下QA、PM、Standards、Spec及DEV Review均已独立PASS并确认P0/P1/P2为0。下一步为精确暂存、中文提交、推送唯一PR、等待Ready完整CI、锁定HEAD普通合并并fresh fetch验证main包含性。PR/CI/merge证据只能在真实发生后追加，当前不得预填。
