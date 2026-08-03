# 墨灵 AI 网关 G1 Bifrost POC 记录

> 本文件只记录可复核证据。历史部署、Fake 测试、上游鉴权成功和真实推理不得互相替代。

## 1. 基线

| 项目 | 值 |
|---|---|
| 开发分支 | `feature/bifrost-ai-gateway-v2` |
| G1 起始提交 | `1e96584` |
| Bifrost 候选版本 | `maximhq/bifrost:v1.6.6` |
| 逻辑上游 | `bailian`、`openrouter` |
| 公开能力 | 文字 Chat Completions、SSE |

## 2. 自动化契约证据

当前自动化测试覆盖：

- Native/Bifrost 标准公开响应和 Usage 等价。
- 普通响应、完整 HTTP SSE 和 `include_usage`。
- input/output/reasoning/cached/total tokens。
- 401、429、500、HTTP 200 业务错误、非法 JSON、缺少 choices。
- 超时、客户端断开、缺少 `[DONE]` 和流中错误。
- 禁止自动 fallback、内部字段脱敏、SK/JWT/资产门禁。
- 百炼/OpenRouter 配置只能引用受限环境变量。

自动化测试使用 `httptest` Fake 上游，不产生费用，不代表真实供应商验收。

2026-08-03 当前工作树已执行：

```text
go test -count=1 ./...                                      PASS
go vet ./internal/modules/token_gateway/...                 PASS
infra/bifrost/config.json JSON 解析                          PASS
infra/scripts/*.sh Bash 语法（10/10）                       PASS
git diff --check                                            PASS
000060 忽略注释后的破坏性语句、唯一键、外键和索引静态检查  PASS
本 Goal 变更文件敏感模式扫描                                PASS（37 个文件，提交前复核暂存区）
```

`000060` 仍是不会切换现有读写的 Expand Migration；除静态契约和 Go/GORM 字段映射外，本轮已在无端口、tmpfs 数据盘的隔离 MySQL 8 临时容器完成真实验收，未连接项目测试数据库。

## 3. 测试 Linux 历史证据

`docs/bifrost-docker-deployment-guide.md` 保存了 2026-07-30 部署和 2026-08-03 健康复核记录，包括固定镜像、双节点、统一入口、配置摘要、单节点退出和日志敏感扫描。历史记录可证明部署基础存在，但不能代替本轮普通响应、SSE、Usage、错误结构和延迟复验。

## 4. 本轮验收矩阵

| 验收项 | 自动化/Fake | 本轮真实 Linux | 结论 |
|---|---:|---:|---|
| Native/Bifrost 标准响应等价 | 通过 | 不适用 | 工程通过 |
| 百炼普通响应与 Usage | 通过 | 通过 | 关闭 |
| 百炼 SSE、Usage、`[DONE]` | 通过 | 通过 | 关闭 |
| OpenRouter 普通响应与 Usage | 通过 | 通过 | 关闭 |
| OpenRouter SSE、Usage、`[DONE]` | 通过 | 通过 | 关闭 |
| 401/429/500/200 业务错误 | 全部通过 | 真实入口 401/400 通过；其余使用 Fake | 工程关闭 |
| 内部入口缺失/错误/重复 Token | 通过 | 401/401/400，均在上游前拒绝 | 关闭 |
| 单节点退出与恢复 | 不适用 | 两节点分别停止，健康/模型 200，恢复 healthy | 关闭 |
| 配置/镜像回滚 | 不适用 | 旧配置恢复、固定镜像重建、G1 配置重应用通过 | 关闭；未做跨版本降级 |
| 真实 Native/Bifrost 延迟观察 | 不适用 | 两轮 80/80 成功，但 JSON 均未过原门槛 | 永久保留为端到端失败观察 |
| 受控上游 20 组 P95/TTFT 增量 | 脚本已静态通过 | JSON 1.991ms、SSE 1.766ms | 性能硬门关闭，待最终双签 |

### 4.1 2026-08-03 本轮只读环境复核

本轮通过 SSH 只读取测试 Linux 的环境身份、容器状态、配置摘要和密钥存在性，没有上传文件、重启容器或调用真实模型：

```text
HOST=pc-Z790-UD-AX
APP_ENV=test
bifrost-1=running/healthy, maximhq/bifrost:v1.6.6
bifrost-2=running/healthy, maximhq/bifrost:v1.6.6
bifrost-lb=running, nginx:1.27-alpine
LB_HEALTH_HTTP=200
REMOTE_CONFIG_SHA256=7c80855f3a1767e32a2e0776e851997112f86874d3287370245af99898881cee
LOCAL_CONFIG_SHA256=2995f01d7f81776c6bd02a0d5bf0415fa70f7f195d23aa7f371e93dfd00189aa
PROVIDERS=bailian,openrouter
SECRETS_PERMS=600
BAILIAN_API_KEY=present
OPENROUTER_API_KEY=present
BIFROST_INTERNAL_TOKEN=missing
```

Nginx 当前配置只有 `proxy_pass`，未发现内部 Authorization 映射、401 拒绝或转发前清除 Authorization。结论：双节点基础设施健康，但服务器仍运行旧配置；必须先备份当前配置、生成并安全注入内部 Token、部署当前 Nginx 模板和 Bifrost 配置，再执行 G1 真实 POC。以上属于测试环境凭据与配置变更，未在本轮只读检查中擅自执行。

同日再次只读复核：`bifrost-1/2` 仍为 `running/healthy`，统一入口 `/health=200`；负载均衡仍挂载历史 `nginx.conf`。服务器已有 `mysql:8.0` 镜像，可在负责人批准后使用独立临时容器验证 `000060`，无需写入项目测试数据库。

隔离数据库验收使用 `infra/scripts/verify-ai-gateway-migration-000060.sh`。该脚本默认退出码 3；授权后只创建无端口、tmpfs 数据盘、随机精确名称的临时 MySQL 8 容器，验证首次 up、保留结构 down、re-up、版本/dirty、租户外键、预算 CHECK、用量幂等约束，并在独立漂移库确认同名错误索引会失败关闭。退出时删除自己创建的容器，禁止连接项目测试数据库。

### 4.2 2026-08-03 真实验收结果

```text
隔离 MySQL 8：first_up/down_retained/reup/tenant/budget/idempotency/drift_rejected 全部 PASS
当前 Bifrost 配置 SHA256：2995f01d7f81776c6bd02a0d5bf0415fa70f7f195d23aa7f371e93dfd00189aa
内部鉴权：missing=401，wrong=401，duplicate=400（Nginx 协议层拒绝，未进入 Bifrost）
百炼 JSON：HTTP 200，total_tokens=15
百炼 SSE：HTTP 200，total_tokens=15，包含 [DONE]
OpenRouter JSON：HTTP 200，total_tokens=18
OpenRouter SSE：HTTP 200，total_tokens=18，包含 [DONE]
故障演练：分别停止 bifrost-1/2，健康与模型请求均 200，恢复后 healthy
回滚演练：旧配置恢复健康、v1.6.6 固定镜像重建、G1 配置重新应用、内部鉴权 401
最终健康复核：LB=200，两个节点 running/healthy，密钥文件 600，内部 Token 恰一行，容器日志敏感模式命中 0
```

首次性能脚本 20 组、80 次请求全部成功，观察成功率差异为 0；但旧算法使用两个独立 P95 相减，得到 JSON `75.662ms`、SSE `-64.371ms`。JSON 未达到 20ms 门槛，该轮判定失败并永久保留。

第二轮真实百炼采用交替顺序和配对差值，20 组、80 次全部成功，脱敏 TSV 权限 `0600`，SHA256 为 `e6a3e370d94fad927a124ed593fc09997954e7a7ba5ea811ca7ead52501faa3f`。脚本与独立 PowerShell 复算均得到 JSON 配对 P95 `108.676ms`、SSE TTFT 配对 P95 `19.696ms`；JSON 未达到当时有效的 20ms 门槛，因此该轮仍判定失败。单对差值跨度为 JSON `-123.530..123.098ms`、SSE `-186.963..145.993ms`，QA 判定真实推理、供应商排队和公网抖动使该短样本无法可靠归因 Bifrost 自身开销。

QA 与产品经理在受控测试形成结果前批准调整测量口径：真实双上游结果继续作为失败的端到端观察，纯网关增量改用固定响应、固定 Usage 和固定 SSE 分片的本地受控上游测量；样本数、交替顺序、配对公式及 20ms/30ms 门槛不变。已发生的受控编排失败包括 30 秒健康等待不足、宿主机网桥不可达和临时 Provider 私网访问未启用，均在正式样本前失败并自动恢复原配置 SHA256；这些轮次不属于性能通过证据，也不得删除。

### 4.3 受控性能硬门结果

2026-08-03 03:56:42 UTC 在 `pc-Z790-UD-AX` 执行受控性能测试。固定上游源码 SHA256 为 `8e589f08e3ba9df01898026e0b6e8b656c94df84d911efd38912a59a953f81d4`，Bifrost 镜像为 `maximhq/bifrost:v1.6.6`，测试前后正式配置 SHA256 均为 `2995f01d7f81776c6bd02a0d5bf0415fa70f7f195d23aa7f371e93dfd00189aa`。

本次正式样本实际执行的 benchmark SHA256 为 `e2ea94fecca2320801b9fd42ffac199a116cb8e748584bf120a0f13843caef54`，controlled runner SHA256 为 `7cf1d6d0ec547d9bc77be9e5c55af0091f8aa16c6f7c7e7de961ccaafc74d87e`。QA 后续要求补强批准门、主机/配置基线门和异常清理；补强后的仓库版本 SHA256 分别为 `91e5bb7ceea73459885b73f66563f655a56080a01656c24fb0296277cbfe89e1`、`a9bcccb4ab40f7d43da31f11b915aedb56ee101aceef24c5c1ed075837a07319`，未改动样本公式或证据内容。

```text
预热：5 组、20 次，PASS，不计入正式样本
正式样本：20 组、80/80 成功，native_first=10，bifrost_first=10
JSON 配对差值 P95：1.991ms，门槛 20ms，PASS
SSE TTFT 配对差值 P95：1.766ms，门槛 30ms，PASS
证据权限：0600
证据 SHA256：a5e209c06b8e2540119a1c443cca0ed646d9d0891041cc854aa01b10a3a00058
独立 PowerShell 复算：20 行、模式 controlled、两个 P95 与脚本一致
清理：临时容器不存在、19190 端口释放、配置 SHA 恢复、LB=200、两个节点 healthy
```

补强版本另在测试机以候选文件执行失败注入：缺少批准退出码 `3`，错误配置 SHA 退出码 `2`；两种情况均未创建临时容器、未监听 19190、未生成证据文件，正式配置 SHA 保持不变。仓库 10 个 Bash 脚本语法检查全部通过。

脱敏原始样本保存在 `tests/evidence/ai-gateway-g1-controlled-20260803T035642Z.tsv`。该结果只关闭 Bifrost/Nginx 纯附加延迟硬门，不改写两轮真实百炼端到端失败观察，也不代表生产容量或长期 0.1 个百分点成功率证明。

## 5. 真实调用安全门禁

真实验收只能使用测试服务器受限环境文件中的密钥，脚本不得输出 Authorization、完整响应或提示词。任何脚本网络请求都必须在负责人明确授权后执行，因为旧入口可能让原本用于鉴权的探测穿透到真实模型；未授权前只允许执行容器、配置、健康和脚本静态检查。

## 6. 当前结论

G0 已由产品经理采纳 37/37 QA、权限收口和前端证据并签收。G1 的工程契约、隔离 MySQL、双上游真实 JSON/SSE/Usage、内部鉴权、单节点故障、配置回滚和受控固定响应性能硬门均已通过；两轮真实百炼延迟观察继续保留为失败。最终 QA 与产品经理复审均为 `P0=0、P1=0、P2=0`，G0/G1 双签通过。该结论不授权进入 G2，不代表生产可用，不授权合并 `main` 或生产部署。
