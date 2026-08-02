# DirectMail Phase 4 测试验收报告（QA/PM 已附负责人豁免通过）

## 报告状态

| 项目 | 当前值 |
|---|---|
| 证据日期 | 2026-08-02 |
| 测试环境 | 墨灵测试环境 |
| 当前结论 | QA/PM 已附负责人豁免关闭 Phase 4 |
| Phase 4 状态 | `passed_with_project_owner_waivers` |
| 未关闭缺陷 | P0=0、P1=0、P2=0 |

QA 与 PM 均已确认附负责人豁免通过；两项签署都不把四项负责人豁免改写为技术 PASS。`qa_phase4_report` 与 `pm_phase4_signoff` 均已关闭。

## 门禁汇总

- 已关闭 19 项，其中 13 项具有技术 PASS 证据，4 项为项目负责人豁免且未技术验证，QA 与 PM 两项为附负责人豁免通过。
- 待关闭门禁：0 项。
- 000055/000056 已在独立 MySQL 8 容器完成 full、partial、down 隔离矩阵，94 个目标全部通过；主库未修改，临时容器和 Stage 已清理，禁止重复执行。
- Redis unknown fresh cycle 已真实完成并精确清理，禁止重复执行或再次重启 Redis。
- 运行时六表面日志门禁、安全扫描和凭据回收已经关闭。

### 关闭门禁证据索引

| 门禁 ID | 结论 | 可复核证据 |
|---|---|---|
| `runtime_six_surface_scan` | 技术 PASS | 验收主文档 §17.2.1 最终同窗口记录：六面 6/6、findings=0、window/deployment 均绑定 |
| `credential_cleanup` | 技术 PASS | 验收主文档 §17.2.1 最终凭据收尾：临时账号为 0、捕获目录不存在、敏感命名文件不存在 |
| `redis_lease` | 技术 PASS | 验收主文档 §17.2.1：真实 `TestEmailRedisLeaseIntegration` 明确执行且未 SKIP |
| `redis_history_cleanup` | 技术 PASS | 验收主文档 §17.2.1：2 条日志、1 条白名单、1 条模板按精确归属清理 |
| `redis_history_postcheck` | 技术 PASS | 验收主文档 §17.2.1：identity diagnostic 与独立 postcheck-only 均返回 PASS |
| `redis_unknown_restart_cycle` | 技术 PASS | 验收主文档 §17.2.5：phase1、唯一 Redis 重启、phase2、cleanup_verified 完整通过 |
| `ram_read_actions` | 技术 PASS | 验收主文档 §17.2.1：`QueryTemplateByParam`、`DescTemplate` 两个真实只读 Action 通过 |
| `ram_effective_permissions` | 负责人豁免，未技术验证 | 验收主文档 §17.2.5 与 §17.2.6：有效策略、信任链和显式 Deny 未形成独立证据 |
| `five_scene_real_replay_expiry` | 负责人豁免，未技术验证 | 验收主文档 §17.2.5 与 §17.2.6：真实重放和过期矩阵未执行 |
| `migration_000057_cycle` | 技术 PASS | 验收主文档 §17.2.1：独立 MySQL 周期完成；授权一次却执行两次的操作偏差单独保留 |
| `migration_000055_matrix` | 技术 PASS | 验收主文档 §17.2.5：独立 MySQL 8 full、partial、down 全矩阵通过 |
| `migration_000056_matrix` | 技术 PASS | 验收主文档 §17.2.5：独立 MySQL 8 full、partial、down 全矩阵通过 |
| `frontend_scope_dod` | 技术 PASS | 验收主文档 §17.2.1：前端范围 DoD 关卡 0 至 4 已完成书面确认 |
| `real_role_responsive_matrix` | 技术 PASS | 验收主文档 §17.2.1：四角色在 1440、768、390 三宽度共 12 项通过 |
| `deployed_frontend_error_state` | 技术 PASS | 验收主文档 §17.2.1：部署构建的受控 503 三宽度错误态通过，写请求为 0 |
| `template_send_real_fault_matrix` | 负责人豁免，未技术验证 | 验收主文档 §17.2.6：项目负责人明确本轮不执行真实故障矩阵 |
| `five_business_flow_e2e` | 负责人豁免，未技术验证 | 验收主文档 §17.2.6：项目负责人明确本轮不执行五业务流真实外发 E2E |
| `qa_phase4_report` | 附负责人豁免通过 | 本报告的 QA 书面结论，确认 13 项技术 PASS、4 项负责人豁免和 P0/P1/P2=0 |
| `pm_phase4_signoff` | 附负责人豁免通过 | 本报告的 PM 书面结论，确认业务范围、QA 结论、四项风险和 Phase 5 边界 |

## 项目负责人豁免

以下四项统一保持 `waived_by_project_owner_not_verified`，不得写成技术验证通过：

1. RAM 有效策略、角色信任链和显式 Deny 证据。
2. 五场景真实重放与过期矩阵。
3. 模板测试发送真实故障矩阵，包括重放、并发、unknown、冷却和白名单。
4. register、login、reset_password、bind_email、admin_verify 五业务流真实外发 E2E。

其中第 3、4 项由项目负责人于 2026-08-02 明确决定本轮不执行真实外发验收。该决定只关闭本轮执行要求，不证明供应商故障路径、真实收件、重放、过期或完整业务闭环已经通过。

## QA 签署记录

- [x] 确认机器清单中的 17 个前置关闭项与证据一致。
- [x] 确认 4 个负责人豁免均明确标注“未技术验证”。
- [x] 确认 P0、P1、P2 未关闭缺陷均为 0。
- [x] 确认 `accepted` 仍只表示供应商受理，不表示人工收件或最终送达。
- [x] QA 结论：附负责人豁免通过。

QA 书面结论：已复核 DirectMail Phase 4 机器清单、验收报告、13 项技术 PASS 证据、4 项负责人豁免边界及 P0/P1/P2=0；确认 `accepted` 仅表示供应商受理，4 项豁免均不代表技术验证通过；允许关闭 `qa_phase4_report` 并提交产品经理最终签署。

## 签署顺序

| 角色 | 当前状态 | 下一动作 |
|---|---|---|
| 测试工程师（QA） | 附负责人豁免通过 | `qa_phase4_report` 已关闭 |
| 产品经理（PM） | 附负责人豁免通过 | `pm_phase4_signoff` 已关闭 |

## PM 最终签署记录

- [x] 确认交付范围是 DirectMail 邮件模板与邮箱验证码 Phase 4 测试环境验收，不包含 Phase 5 或生产上线批准。
- [x] 确认 13 项技术 PASS、QA 附负责人豁免通过及 P0/P1/P2=0 满足本轮业务验收输入。
- [x] 确认四项 `waived_by_project_owner_not_verified` 风险必须持续保留，不能改写为技术 PASS 或生产可用。
- [x] 确认供应商 `accepted` 不等于人工收件或最终送达。
- [x] PM 结论：附负责人豁免关闭 Phase 4。

PM 书面结论：已复核 DirectMail Phase 4 的业务范围、QA 附负责人豁免通过结论、13 项技术 PASS、4 项未技术验证的负责人豁免及 P0/P1/P2=0；确认 `accepted` 不等于最终送达，四项豁免风险必须保留在发布说明中。PM 同意附负责人豁免关闭 Phase 4，但该结论不代表生产环境验证或批准 Phase 5 上线。

## 发布边界

Phase 4 已按附负责人豁免关闭；Phase 5 和生产上线仍未批准。后续发布说明必须保留四项未验证风险，不得改写为生产可用证据；开始 Phase 5 前仍须按生产灰度门禁另行审批和验证。
