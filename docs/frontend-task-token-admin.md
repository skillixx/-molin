# 前端对接任务：管理端「Token 渠道 / 模型」配置页

> 负责人：前端工程师甲（web/admin-console）
> 对应后端：Token 网关 v3（已上线 main + 测试环境）——管理端 `/api/admin/token/*`
> 接口契约：`docs/backend-token-gateway-design.md` §5、本文档

## 一、背景（了解即可）

平台的 AI 对话能力靠「渠道（上游供应商）+ 模型目录」驱动：运营在后台配好渠道（填上游 api_key）和对外模型（关联渠道 + 上游真实模型名），用户端才能选模型对话。目前**只有后端 API，没有管理页**，运营得手敲接口。本任务做管理后台的可视化配置页。

> 权限：所有接口需 **管理员登录 + 双重认证 + `token:manage` 权限**（管理后台已有这套机制，沿用现有 admin 请求封装即可）。

## 二、接口契约

### A. 渠道管理 `/api/admin/token/channels`
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/token/channels` | 列表 [扁平分页 `{items,page,page_size,total}`] |
| POST | `/api/admin/token/channels` | 新建 |
| GET | `/api/admin/token/channels/{id}` | 详情 |
| PATCH | `/api/admin/token/channels/{id}` | 更新 |
| DELETE | `/api/admin/token/channels/{id}` | 删除 |

**列表/详情响应字段（ChannelResp）**：
```json
{ "id":1, "code":"openai", "name":"OpenAI", "type":"openai_compatible",
  "base_url":"https://api.openai.com/v1", "has_api_key":true,
  "status":"active", "priority":0, "created_at":"...", "updated_at":"..." }
```
**新建请求体**：`{ code, name, type?, base_url, api_key_plaintext, status?, priority? }`
**更新请求体**（PATCH，字段可选，只传要改的）：`{ name?, type?, base_url?, api_key_plaintext?, status?, priority? }`

🔴 **api_key 安全约束（重点）**：
- 创建/更新时用 `api_key_plaintext` 传**明文** key；
- 响应**永远不返回 key**，只有 `has_api_key` 布尔表示「是否已配置」；
- 编辑页：key 输入框**留空 = 不修改**（后端 PATCH 收到空/不传不会清空已存 key）；UI 上对已配置渠道显示「已配置（留空不修改）」占位，**不要**尝试回显原 key。

### B. 模型目录管理 `/api/admin/token/models`
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/token/models` | 列表 [扁平分页]，支持 `?status=&modality=` |
| POST/GET{id}/PATCH{id}/DELETE{id} | 同上 | 增删改查 |

**响应字段（ModelResp）**：
```json
{ "id":1, "logical_model_code":"gpt-4o", "display_name":"GPT-4o", "modality":"chat",
  "product_id":5, "channel_id":1, "upstream_model":"gpt-4o",
  "status":"active", "sort_order":0, "created_at":"...", "updated_at":"..." }
```
**新建/更新请求体字段**：`logical_model_code`（对外名，唯一）、`display_name`、`modality`（chat/image/audio/video，空默认 chat）、`channel_id`（路由到哪个渠道）、`upstream_model`（上游真实模型名）、`product_id`（关联 token 商品，计费用）、`status`、`sort_order`。

## 三、要做的页面（web/admin-console）

1. `src/api/token.ts`：渠道 + 模型两组 CRUD 封装（沿用现有 admin axios 实例，参考 `src/api/group.ts`）。
2. 页面（参考现有 `views/group/` 的列表+管理风格）：
   - **渠道管理**：列表（code/name/base_url/has_api_key 状态/status/priority）+ 新建/编辑弹窗 + 删除。编辑时 key 框留空不改。
   - **模型目录管理**：列表 + 新建/编辑（含「渠道」下拉选 channel_id、「关联商品」选 product_id、模态选择、上游模型名）+ 删除。
     - 「渠道」下拉数据来自渠道列表接口；「关联商品」可让运营填 product_id（或下拉 token 类商品，二期）。
3. 路由 + 菜单入口（参考现有分组管理菜单的注册方式），可放在一个「Token 网关」分组下含「渠道」「模型」两个 tab 或两个页面。
4. 类型放 `src/types/`。

## 四、错误处理
- 沿用管理后台统一响应拦截器（透传后端 message）。常见：`400`（参数/code 重复用 409）、`404`（不存在）、`403`（无 token:manage 权限或未双重认证）。
- 渠道 `code` 唯一、模型 `logical_model_code` 唯一，重复时后端返回 409，按 message 提示即可。

## 五、不要做 / 边界
- **绝不回显 / 不尝试获取 api_key 明文**（后端不返回，UI 用 has_api_key + 留空不改）。
- 不碰用户控制台（对话页已由前端乙完成）、不碰后端。
- 不做「测试渠道连通性」按钮（后端暂无该接口，二期再说）。

## 六、验收
- 新建一个渠道（填 base_url + api_key）→ 列表出现、has_api_key=true；编辑时 key 留空保存 → key 不变。
- 新建一个模型（选渠道 + 填上游模型名 + 关联商品）→ 列表出现。
- 删除、唯一冲突提示正常。
