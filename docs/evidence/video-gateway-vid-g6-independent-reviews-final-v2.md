# VID-G6 最终独立验收回执 v2

绑定SOURCE_STATE：`2644f9a29b34176fdd8acbdc8c993c150d9f96658a9210eec1192dffeded46d9`

绑定源码提交：`f1aaa20d5a1a1c1da1976d11601de08be690cf24`

绑定容器副本：`5a2769740f149c42c1da2cba7122604bd56197f9990d551b0ad9cbcc250233c3`

```text
QA_ACCEPTANCE=PASS
PM_CONFIRMATION=PASS
STANDARDS_REVIEW=PASS
SPEC_REVIEW=PASS
DEV_CODE_REVIEW=PASS
P0=0
P1=0
P2=0
```

## 共同证据

- 409项manifest逐文件原始字节SHA-256匹配，SOURCE_STATE可独立复算。
- G6 all使用MySQL 8、000001→000109及Linux race，service 1232.352秒，224个必需条目/216个唯一顶层测试全部RUN/PASS。
- 锁定Python `openai==2.45.0`、TypeScript `openai@6.39.0`真实loopback HTTP；VID-G5与IMG-G6兼容通过。
- 47条路由、R01—R20、G7停止边界及零真实Provider/资金/测试服/生产操作保持不变。

## CI分类修复复核

- PR #422旧HEAD `23b4f5ca6865b16ea11026a46693960071e02e0b`的Ready run `33515822504`因Git复制检测renameLimit warning进入ClassificationError；不是视频业务测试失败。
- 分类器保留`--find-copies-harder`并使用有限`-l2000`，不采用无限`-l0`。
- Git返回非零或任何stderr仍抛`ClassificationError`，超过有限上限继续失败关闭，不接受不完整路径集。
- 分类单测43项包含C100源/目标、有限上限、stderr warning与非零退出；Ready合同10项、Draft选择14项、Draft runner 5项全部通过。
- 使用原PR精确base/head重新分类exit 0、`CI_CHANGE_SCOPE=PASS`、`full=true`并启用全部Ready门禁。

## Git事实边界

独立审查时PR #422仍为`OPEN_OLD_HEAD`，远端HEAD为`23b4f5ca...`，本地修复尚未提交/推送。审查通过后CI修复源码已提交为`f1aaa20d...`，但本回执生成时仍未push；新HEAD Ready CI尚未发生，因此不能合并。

旧`video-gateway-vid-g6-independent-reviews-final.md`绑定`7e03...`并已明确STALE，不能替代本回执。
