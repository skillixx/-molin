# 前端任务单：presenton 回退后同步与本地清理（给 Codex）

> **给 Codex 的一句话指令**：presenton 应用市场已整体下线，请把本地同步到最新 `main`，删除/回退本地所有 presenton（应用市场）前端改动；注意 MCP server 与 Agent 分类前端**已经合并进 main**，不要重复开发。

---

## 一、背景

应需求，presenton 应用市场深度二开集成已**全面回退下线**。`main` 已更新到最新（以 `origin/main` 最新提交为准），涉及以下已合并 PR：

| PR | 内容 | 对前端的影响 |
|---|---|---|
| **#265** | 从 main 移除 presenton（后端模块、子模块 `services/presenton`、迁移 000052、config/bootstrap 注册、相关文档） | presenton 后端入口/反代已不存在，前端任何 presenton 页面都将无接口可用 |
| **#266** | 第三阶段 **MCP server 前端 + Agent 分类前端**已合并 main | ⚠️ **已在 main 里，请勿重复开发**，`pull` 后即获得 |
| **#267** | README 开发进度更新 | 无 |

> 说明：presenton 的前端页面（应用市场入口、PptX 应用页等）此前一直是**未提交的本地改动**，从未合并进 main。因此 main 上不存在 presenton 前端代码，你只需要清理**自己本地**残留的这部分改动。

---

## 二、需要你执行的操作

### 步骤 1：同步到最新 main

```bash
git fetch origin
git checkout main && git pull --ff-only origin main   # 同步到 origin/main 最新提交
```

### 步骤 2：删除 presenton（应用市场）新增文件

这些文件只存在于本地、从未合并，直接删除：

```bash
rm -f web/user-console/src/views/app/MyAppsView.vue \
      web/user-console/src/views/app/PresentonAppView.vue \
      web/user-console/src/api/app.ts \
      web/user-console/src/types/app.ts
rmdir web/user-console/src/views/app 2>/dev/null
```

### 步骤 3：回退掺入 presenton 片段的文件到 main 版本

下列文件本身要保留，但其中夹带的 presenton 片段需回退（用 main 版本覆盖即可）：

```bash
git checkout main -- \
  web/user-console/src/router/index.ts \
  web/user-console/src/views/marketplace/MarketplaceView.vue \
  web/user-console/vite.config.ts \
  web/user-console/src/components/layout/TopNav.vue \
  web/user-console/src/views/overview/OverviewView.vue
```

各文件中要去掉的 presenton 片段对照：

| 文件 | presenton 片段（应去除） |
|---|---|
| `router/index.ts` | `apps` 与 `apps/presenton` 两条路由（`MyAppsView`/`PresentonAppView`） |
| `views/marketplace/MarketplaceView.vue` | `presentonProduct`/`presentonMarketLink` 计算属性、`featured-app` 推荐卡区块及其样式 |
| `vite.config.ts` | `/app/presenton` 同源代理配置 |
| `components/layout/TopNav.vue` | 顶部导航与下拉菜单的「我的应用」(`/apps`) 入口 |
| `views/overview/OverviewView.vue` | 总览页 quickLinks 的「我的应用」(`/apps`) 卡片 |

### 步骤 4：清理本地 presenton 相关分支（如有）

```bash
git branch | grep -i presenton    # 列出后用 git branch -D <分支名> 逐个删除
```

---

## 三、必须保留（已在 main，勿删）

第三阶段正式功能，`pull` 后即在 main 中，**不要删除也不要重新实现**：

- **MCP server 前端**：`web/admin-console/src/views/token/McpServerListView.vue`，以及 admin 的 `api/token.ts`、`types/token.ts`、`router/index.ts`、`components/layout/SideMenu.vue`、`views/token/WorkbenchConfigView.vue` 中的 MCP server 相关代码
- **Agent 分类前端**：user-console 的 `api/token.ts`（`listAgentCategories`）、`types/token.ts`（`AgentCategory`/`visible_scope`）、`views/agent/AgentWorkbenchView.vue` 的分类逻辑

---

## 四、完成自检

```bash
git status                 # 应为干净工作区
grep -ri presenton web/    # 应无任何结果
```

两项都满足即清理完成。两端（admin-console / user-console）`npm run build` 能正常通过即可。有接口或对接疑问随时找后端（Claude）确认。
