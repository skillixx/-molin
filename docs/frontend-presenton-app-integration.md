# presenton 应用前端对接说明（给 Codex）

> 范围：用户控制台「我的应用」里打开「PPT 生成器(presenton)」的前端对接。
> 本文只给**接口契约 + iframe 嵌入要点**，页面实现由前端负责。
> 后端实现见墨灵网关 D1/D2（`server/internal/modules/presenton`，PR #258）。

---

## 1. 整体流程（前端视角）

```
「我的应用」点【打开 PPT 生成器】
  → GET /api/app/presenton/open（带登录态）
    → 拿到 data.embed_url
      → 用 embed_url 作为 <iframe> 的 src（或新标签页打开）
        → presenton 编辑器在 iframe 内加载，用户开始用
```

前端**只调一个接口** `/api/app/presenton/open`，拿到 `embed_url` 后塞进 iframe 即可。
后续 presenton 的所有请求由墨灵反代自动处理（注入身份、计费、隔离），前端无需参与。

---

## 2. 接口契约：`GET /api/app/presenton/open`

- **鉴权**：登录态（与其它用户端接口一致，带 JWT；沿用现有 axios 鉴权封装）。
- **入参**：`model`（可选，query）——用户在模型下拉所选的 `code`（取自下方 `/models`）；不传则用后端默认模型。
- **方法**：GET。

### 成功响应 `200`

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "embed_url": "https://<网关域名>/app/presenton/launch?ticket=<一次性票据>",
    "expires_in_seconds": 300
  }
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `data.embed_url` | string | **直接作为 iframe 的 src**。指向墨灵反代的 launch 入口，内含一次性票据。 |
| `data.expires_in_seconds` | number | 票据有效期（秒，默认 300）。**拿到后尽快加载 iframe**，过期需重新调本接口。 |

### 错误响应

| HTTP | code | message | 前端处理 |
|---|---|---|---|
| 401 | 40001 | 未登录 | 跳登录（一般由 axios 拦截器统一处理） |
| 403 | 40300 | 未开通 PPT 生成器，请先购买 | **引导去应用市场购买/开通**该应用 |
| 400 | 40000 | 所选模型不可用 | 提示重选（正常下拉只给合法值，一般不触发） |
| 500 | 50000 | 打开失败 | 提示「打开失败，请稍后重试」 |

> 错误体结构同其它接口：`{ "code": <非0>, "message": "<中文提示>", "data": null }`。

---

## 2.5 接口契约：`GET /api/app/presenton/models`（模型下拉数据）

- **鉴权**：登录态（同上）。**入参**：无。**方法**：GET。
- **作用**：返回 presenton 可用模型白名单（运维经 `PRESENTON_ALLOWED_MODELS` 配置，只含支持 json_schema 结构化输出、能正常出片的模型），供前端「打开」前的模型下拉。

```json
{ "code": 0, "message": "ok", "data": { "items": [ { "code": "GPT-4o", "name": "GPT-4o" } ] } }
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `data.items[].code` | string | 模型标识，**选中后作为 `/open` 的 `model` 参数**。 |
| `data.items[].name` | string | 展示名（下拉显示用，v1 与 code 相同）。 |

> `items` 可能为空（运维未配白名单）→ 此时不显示下拉，直接用默认模型打开（`/open` 不带 `model`）。

---

## 3. iframe 嵌入要点

### 3.1 基本用法
- `embed_url` 直接给 `<iframe :src="embedUrl">`。
- launch 会下发会话 cookie 并 302 重定向到反代根，presenton 编辑器随即在 iframe 内加载——**前端不用处理重定向，浏览器自动完成**。

### 3.2 票据 vs 会话（影响"何时调接口"）
- **票据**（embed_url 里的）是**一次性 + 短期（默认 5 分钟）**：点【打开】时**才**调 `/open`，别提前预取或缓存复用。
- 加载成功后，墨灵反代会建一个**更长的会话**（默认 2 小时，cookie 维持），编辑期间无需再调接口。
- 会话过期后 iframe 内请求会返回 401 → 体验上建议：用户**重新点【打开】**重走一遍 `/open`。

### 3.3 安全（前端不碰密钥）
- `embed_url` 里只有票据，**没有任何模型 key**；用户的 token_gateway key 全程在后端，浏览器拿不到。前端无需也不应尝试获取。

### 3.4 跨域 / CSP（需与运维确认，影响能否内嵌）
- 若**网关域名与控制台同源**：iframe 直接可用，cookie 为 SameSite=Lax，无额外要求。
- 若**网关域名与控制台跨源**：
  - 反代会话 cookie 在生产（https）下为 `SameSite=None; Secure`，支持跨源 iframe；
  - 需运维在 presenton/反代侧配置 CSP `frame-ancestors` 允许控制台域名，否则 iframe 会被浏览器拦截（控制台报 `Refused to frame`）。
  - 这两项是**运维配置**，前端若遇 iframe 空白/被拦，先排查这里，不是前端代码问题。

### 3.5 加载态与失败兜底
- iframe 加载前显示 loading；可监听 `iframe.onload` 收起 loading。
- `/open` 返回 403 时**不要**渲染 iframe，直接展示「未开通」引导。

---

## 4. 最小调用示例（仅示意接口契约，非页面实现）

```js
// 点击「打开」时调用；sk/JWT 走现有 axios 实例的鉴权封装
async function openPresenton() {
  const { data } = await api.get('/api/app/presenton/open') // axios 已解包则按你们封装取
  // data = { embed_url, expires_in_seconds }
  return data.embed_url // 赋给 iframe 的 src
}
// 403 → 引导购买；401 → 登录；其余 → 失败提示
```

> 注：返回外层 `{code,message,data}` 的解包按你们 axios 拦截器既有约定处理（与其它接口一致）。

---

## 5. 待运维侧就绪的依赖（前端联调前确认）

- presenton 内网已部署，墨灵配置 `PRESENTON_INTERNAL_BASE_URL`（否则 `/open` 能返回 embed_url，但 iframe 打开 D2 反代会 404/不可用）。
- `PRESENTON_GATEWAY_BASE_URL` 已配（embed_url 的域名来源）。
- 跨源场景的 CSP `frame-ancestors` 已放行控制台域名。

---

## 6. 一句话总结

> 前端 = **调 `/api/app/presenton/open` → 把 `data.embed_url` 塞进 iframe**。
> 鉴权/计费/多租户隔离全在后端，前端只管「打开按钮 + iframe + 403 引导购买」。
