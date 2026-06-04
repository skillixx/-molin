---
name: tool-documentation-principle
description: 项目原则：开发者引入或使用某个工具时，必须简要记录该工具的作用和常用命令
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9b292ad9-2e97-4482-a1dc-b29c4ea9b9a2
---

# 工具文档原则

**开发者在项目中用到某个工具时，必须简要写明该工具的作用和常用命令。**

## 记录格式

在对应模块的 CLAUDE.md 或 `docs/tools.md` 中追加：

```markdown
## 工具名称

**作用：** 一句话说明这个工具解决什么问题。

**常用命令：**
```bash
# 最常用的 2–5 条命令，每条加简短注释
tool command        # 说明做什么
tool command --flag # 说明加了这个参数有什么效果
```
```

## 适用范围

- 新引入的 CLI 工具（如 migrate、swag、wire、golangci-lint）
- 新引入的框架或库（如 GORM、Gin、Viper、Element Plus）
- 基础设施工具（如 docker、docker compose、redis-cli、mysql）

## 不需要记录的情况

- git、go、npm 等行业通用基础工具，团队成员默认熟悉
- 已有官方中文文档且命令极简单的（直接贴文档链接即可）

## 示例

```markdown
## golang-migrate

**作用：** 管理数据库 Migration 版本，支持 up/down 回滚，确保各环境数据库结构一致。

**常用命令：**
```bash
migrate -path ./migrations -database "mysql://..." up        # 执行所有未运行的 migration
migrate -path ./migrations -database "mysql://..." up 1      # 只执行下一条 migration
migrate -path ./migrations -database "mysql://..." down 1    # 回滚最近一条 migration
migrate create -ext sql -dir ./migrations -seq add_users     # 新建一对 up/down SQL 文件
```
```

**Why:** 项目成员水平不一，工具记录可以让新人快速上手，也避免老成员忘记不常用的命令时到处查文档。

**How to apply:** 每当 AI 辅助开发时引入或使用一个新工具，自动在对应位置追加工具说明，无需用户提醒。
