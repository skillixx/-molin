# 前端对接说明 — 放开「http / 内网 IP」外呼地址校验（自建局域网）

> 责任范围：本文档由后端（Claude）出具，仅说明**接口契约与校验口径**，不实现 Vue 页面代码。
> 前端校验放开由 Codex/前端实现。
> 关联后端改动：PR #309（已合并 main，commit `85d2b46`），测试服已置 `TRUST_INTERNAL_OUTBOUND=true` 并生效。
> 关联设计文档：`docs/feature-allow-http-ip-outbound.md`。

---

## 1. 背景与结论

平台支持无 https、无域名的**局域网自建部署**。后端已新增环境变量开关 `TRUST_INTERNAL_OUTBOUND`：

- **测试服已开启**（`=true`），后端现在接受 `http://` 与内网/IP 主机（如 `http://192.168.20.16:8080`）。
- 但**前端表单仍有「必须 https」的硬校验**，会在提交前就拦下 http/IP 地址，导致后端虽放开、页面仍报错提交不了。

**前端需做的事**：把下列表单的「必须 https」前端校验，放宽为「允许 `http://` 或 `https://` + 允许 IP/端口主机」，与后端口径对齐。

> 说明：是否「仅在可信环境显示 http 选项」「用开关控制前端文案」由前端按 UI 需要决定，后端不强制。最简做法是直接放宽校验规则（后端默认 `false` 的生产环境仍会兜底拒绝 http，所以前端放宽不会削弱生产安全）。

---

## 2. 需要放开的前端位置（共 4 处）

| # | 页面/表单 | 文件:行（现状硬校验） | 字段 |
|---|---|---|---|
| 1 | 工作台 — 插件配置 endpoint_url | `web/admin-console/src/views/token/WorkbenchConfigView.vue:737`（`startsWith('https://')`）+ `:246` placeholder | `endpoint_url` |
| 2 | 工作台 — MCP server endpoint_url | `web/admin-console/src/views/token/McpServerListView.vue:255`（`startsWith('https://')`）+ `:72` placeholder | `endpoint_url` |
| 3 | 应用管理 — 访问入口 access_url | `web/admin-console/src/views/app/AppManageView.vue:298`（`startsWith('https://')`）+ `:545` placeholder | `access_url` |
| 4 | 商品管理 — 访问地址 | `web/admin-console/src/views/product/ProductListView.vue:189`（「访问地址必须以 https:// 开头」）| （请前端确认该字段是否同源 access_url，若是同样放开） |

> 第 1、3、2 处的输入框 placeholder（「必须是公网 https 地址」/「https://your-app.com」）也建议同步改为示意可填 IP/http，避免误导。

---

## 3. 对应后端接口契约（字段名、路径）

放开校验后，前端提交的请求体字段、接口路径均**不变**，只是值域放宽。

### 3.1 插件 endpoint_url
- `POST /api/admin/plugins`、`PATCH /api/admin/plugins/{id}`
- 权限：`plugin:manage`
- 请求体字段：`endpoint_url`（string）

### 3.2 MCP server endpoint_url
- `POST /api/admin/mcp-servers`、`PATCH /api/admin/mcp-servers/{id}`
- 配置后可调 `POST /api/admin/mcp-servers/{id}/discover` 拉取工具
- 权限：`plugin:manage`
- 请求体字段：`endpoint_url`（string）

### 3.3 应用 access_url
- `POST /api/admin/apps`、`PATCH /api/admin/apps/{id}`
- 权限：`app:manage`
- 请求体字段：`access_url`（string，可空=清空入口）

---

## 4. 前端校验规则（放宽后的口径，与后端一致）

放行：
- scheme 为 `http://` **或** `https://`（大小写不敏感）
- 主机可为**域名**或**IP**，可带**端口**（如 `192.168.20.16:8080`、`10.0.0.5`、`localhost:3000`）
- 路径/查询可选

仍然拒绝（前端可保留这几条，与后端一致，提升体验）：
- 空字符串：
  - 插件/MCP `endpoint_url`：必填，空则拒。
  - 应用 `access_url`：**空串合法**（视为清空入口），不要把空串当错误。
- 危险 scheme：`javascript:`、`data:`、`ftp:` 等一律拒（只放 http/https）。
- 缺少主机：如 `http://`（scheme 后无 host）拒。
- 长度：`access_url` 上限 512 字符（插件/MCP 无此前端硬限）。

参考校验思路（前端自行实现，仅示意正则/逻辑，不强制写法）：
```
允许： ^https?://[^\s/$.?#].[^\s]*$    （http 或 https 开头 + 非空 host）
危险 scheme： 命中 ^(javascript|data|ftp|file|vbscript): → 拒
access_url 额外： 空串直接放行；非空再走上面规则；长度 ≤ 512
```

> 注意：前端正则只是体验优化。**真正的安全边界在后端**——生产环境（`TRUST_INTERNAL_OUTBOUND=false`）后端仍会拒绝 http/内网，所以前端放宽不会让生产环境变得不安全。

---

## 5. 后端错误返回（前端可据此提示，放开后基本不会再触发）

放开校验前若前端误放过、或在 `false` 环境提交，后端会返回校验错误（HTTP 400，统一 `{code,message}` 结构），`message` 可能为：

| 来源 | message | 含义 |
|---|---|---|
| 插件/MCP（SSRF 校验） | `仅允许 https` | 环境未开开关却提交了 http |
| 插件/MCP | `不允许指向内网/回环地址` / `不允许指向内网/本机` | 环境未开开关却提交了内网/IP |
| 插件/MCP | `缺少主机名` / `URL 不能为空` | host 缺失 / 空 |
| 插件/MCP | `域名 X 不在白名单内` | 运维配了 `PLUGIN_DOMAIN_WHITELIST`，主机不在白名单（白名单优先级高于开关） |
| 应用 access_url | `access_url 必须以 https:// 开头` | 环境未开开关却提交了 http |
| 应用 access_url | `access_url 长度不能超过 512` | 超长 |

前端直接透传 `message` 即可，无需自行翻译。

---

## 6. 联调验收点

测试服（已开 `TRUST_INTERNAL_OUTBOUND=true`）前端放开后：

- [ ] 插件 `endpoint_url = http://192.168.20.16:8080/...` 能提交、保存成功
- [ ] MCP `endpoint_url = http://192.168.20.16:8080/mcp` 能提交、保存并 discover 成功
- [ ] 应用 `access_url = http://192.168.20.16:3000` 能提交、保存成功
- [ ] 应用 `access_url` 留空仍能提交（清空入口，不报错）
- [ ] `javascript:alert(1)` 前端即拦（仍拒危险 scheme）
- [ ] 公网 `https://...` 地址行为不变（回归）
