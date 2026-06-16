# 墨灵系统 Agent 总览

## 使用原则

本目录定义墨灵项目的系统级 Agent。每个 Agent 只在自己的职责边界内工作，跨角色需求必须通过接口文档、任务单或评审意见交接，禁止越权直接修改其他角色负责的业务代码。

所有 Agent 必须遵守：

- 开发前先确认当前分支；如在 `main`，先创建语义清晰的 feature 分支。
- 每次输出前先确认本次内容是否属于自己的职责范围。
- 代码注释、提交说明、PR 说明、评审意见必须使用中文。
- 写代码时同步补充必要且详细的中文注释，说明关键逻辑、数据流、状态变化、异常处理和接口调用意图。
- 接口字段、错误码、分页结构以 `docs/full-api-design.md`、`docs/frontend-api-reference.md` 和对应模块权威设计文档为准。
- 钱包扣费、订单状态流转、实名隐私、权限逻辑、资产生成、按量计费、幂等处理属于高风险内容，必须人工复核。

## Agent 清单

| Agent | 角色定位 | 文档 |
|---|---|---|
| 后端甲开发工程师 | auth / identity / iam / audit | `docs/agents/backend-a.md` |
| 后端乙开发工程师 | product / order / billing / finance_consumer | `docs/agents/backend-b.md` |
| 后端丙开发工程师 | asset / membership / app / content / provision | `docs/agents/backend-c.md` |
| 前端甲开发工程师 | 管理后台 `web/admin-console` | `docs/agents/frontend-a.md` |
| 前端乙开发工程师 | 用户控制台 `web/user-console` | `docs/agents/frontend-b.md` |
| 测试工程师 | 接口测试、功能测试、验收测试 | `docs/agents/qa.md` |
| 运维工程师 | infra、环境、CI/CD、部署 | `docs/agents/devops.md` |
| 产品经理 | 需求确认、代码合并、业务验收 | `docs/agents/pm.md` |

## 协作流程

```text
产品经理确认阶段范围
  -> 对应开发 Agent 创建 feature 分支并实现
  -> 开发 Agent 自测并补充中文功能/开发文档
  -> 测试工程师执行功能验收并输出测试结论
  -> 产品经理进行业务确认和 PR 合并
  -> 运维工程师按部署 Checklist 发布测试或生产环境
```

## 阶段门禁

每个阶段完成后必须先通过测试工程师验收和产品经理确认，才能进入下一阶段开发。若用户要求进入下一阶段，Agent 必须先询问当前阶段的测试验收和产品确认是否完成；未完成不得推进。
