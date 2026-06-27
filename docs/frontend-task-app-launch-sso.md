# 前端任务：进入应用阶段二（SSO 一次性票据）

> 适用：`web/user-console`（用户控制台）。后端 PR #295 已合并 main 并上线测试服。
> 本任务把「进入应用」从**阶段一（直接打开 access_url）**升级为**阶段二（先换一次性票据，携票据跳转）**，让应用方可信识别用户身份（免登 SSO）。

---

## 一、为什么改 / 改什么

**阶段一现状**：点「进入应用」→ 直接 `window.open(access_url)`。应用方拿不到「来访的是哪个平台用户」。

**阶段二目标**：点「进入应用」→ 先调 `POST /api/apps/{appId}/launch` 拿 `{access_url, launch_ticket}` → `window.open(access_url + '?ticket=' + launch_ticket)`。应用后端再用票据换身份。

> 票据：60s 有效、一次性、绑定 user/app/product。前端只负责「换票 + 携票跳转」，不解析票据内容。

---

## 二、新增接口（后端已就绪）

```
POST /api/apps/{appId}/launch        （需登录，自动带 Bearer Token）
```

- `appId` = **应用 ID（applications.id）**，不是商品 ID、不是资产 ID。各页面如何拿到见 §四。
- 请求体：无（应用 ID 走路径）。
- 成功响应 `data`：
  ```json
  { "access_url": "https://app.example.com", "launch_ticket": "lt_xxx", "expires_in": 60 }
  ```
- 失败（HTTP 4xx，message 可直接展示）：
  | code | 含义 | 前端处理 |
  |---|---|---|
  | `40003` | 无该应用使用权（未购买/未开通，或资产非 active） | 提示「请先购买或开通后再进入」 |
  | `40400` | 应用不存在 / 未上架 / 未配置访问入口 | 提示「该应用暂未开放访问入口」 |

> `access_url` 由 launch 响应直接返回，**阶段二不再需要单独调 `GET /api/marketplace/apps/{id}` 拿 access_url**（少一跳）。

---

## 三、API 封装（`src/api/app.ts` 加一个函数）

```ts
import http from './http'
import type { MarketplaceApp } from '@/types/app'

export function getMarketplaceApp(id: number) {
  return http.get<unknown, MarketplaceApp>(`/marketplace/apps/${id}`)
}

// 阶段二：换取一次性进入票据
export interface LaunchTicket {
  access_url: string
  launch_ticket: string
  expires_in: number
}
export function launchApp(appId: number) {
  return http.post<unknown, LaunchTicket>(`/apps/${appId}/launch`)
}
```

> 注意：`http` 拦截器已自动解包 `data` 并注入 Bearer Token；402/40x 的 message 也已统一弹错。各页面只需处理「拿到票据 → 跳转」和「业务码 40003/40400 的友好提示」。

---

## 四、三处改动（把「直接开 access_url」换成「换票 + 携票跳转」）

公共跳转逻辑（建议抽一个小工具函数复用）：

```ts
import { launchApp as apiLaunchApp } from '@/api/app'
import { ElMessage } from 'element-plus'

// appId = applications.id
async function openAppById(appId: number) {
  try {
    const { access_url, launch_ticket } = await apiLaunchApp(appId)
    const sep = access_url.includes('?') ? '&' : '?'
    window.open(`${access_url}${sep}ticket=${encodeURIComponent(launch_ticket)}`, '_blank', 'noopener,noreferrer')
  } catch (e: any) {
    const code = e?.response?.data?.code
    if (code === 40003) ElMessage.warning('请先购买或开通后再进入')
    else if (code === 40400) ElMessage.warning('该应用暂未开放访问入口')
    // 其它错误 http 拦截器已弹，无需重复
  }
}
```

### 4.1 `src/views/assets/AssetListView.vue`（我的资产 — 优先级最高）

当前 `launchApp(row)` 两跳：`getProductDetail` 拿 `business_ref_id` → `getMarketplaceApp` 拿 `access_url` → 直接 open。

**改为**：第一跳拿 `business_ref_id`（即 appId）后，直接调 `openAppById(business_ref_id)`，不再调 `getMarketplaceApp`、不再读 `app.access_url`：

```ts
async function launchApp(row: unknown) {
  const asset = row as UserAsset
  launchLoadingAssetId.value = asset.id
  try {
    const { product } = await getProductDetail(asset.product_id)
    if (!product.business_ref_id) {
      ElMessage.warning('该应用未配置访问地址')
      return
    }
    await openAppById(product.business_ref_id)   // 阶段二：换票 + 携票跳转
  } finally {
    launchLoadingAssetId.value = null
  }
}
```

> 我的资产页的资产本就是用户已购、active 的，launch 必然成功，是 SSO 的主路径。

### 4.2 `src/views/app/AppDetailView.vue`（应用详情）

路由 `/apps/:id` 的 `id` 就是 **applications.id**，`app.value.id` 即 appId。

把点击处理（约 28–29 行的 `window.open(app.value.access_url, ...)`）改为：

```ts
async function onLaunch() {
  if (!app.value) return
  await openAppById(app.value.id)
}
```

按钮可见性维持现状（`v-if="app.access_url"`）即可；非持有者点击会得到 40003 友好提示。

### 4.3 `src/views/marketplace/ProductDetailView.vue`（商品详情）

`appDetail` 来自 `getMarketplaceApp(business_ref_id)`，`appDetail.value.id` 即 appId。

把点击处理（约 55–56 行）改为：

```ts
async function onLaunch() {
  if (!appDetail.value) return
  await openAppById(appDetail.value.id)
}
```

> 商品详情页面向「可能还没买」的浏览用户，点击若未持有会返回 40003 → 提示先购买。若产品上希望非持有者**不显示**该按钮，可另行加「是否已购」判断控制 `v-if`（可选，不在本任务必做范围）。

---

## 五、验收标准

- [ ] `src/api/app.ts` 新增 `launchApp(appId)`，POST `/api/apps/{appId}/launch`，返回 `{access_url, launch_ticket, expires_in}`。
- [ ] 三处「进入应用」按钮点击后：先调 launch，再 `window.open(access_url + '?ticket=' + launch_ticket)`，**URL 带 `?ticket=`**。
- [ ] 我的资产页：持有 active 资产的应用，点击能携票跳转成功。
- [ ] 无使用权时（如商品详情页未购用户）点击 → 提示「请先购买或开通」，不抛红错、不跳转。
- [ ] 未配置访问入口（40400）→ 提示「该应用暂未开放访问入口」。
- [ ] `window.open` 保留 `'noopener,noreferrer'`。
- [ ] ticket 仅出现在跳转 URL 的 query，**不要打印到 console / 不要落日志**。
- [ ] `access_url` 已带 query 时用 `&ticket=` 拼接（用上面的 `sep` 逻辑），不要写死 `?`。

---

## 六、对接注意

- launch 接口的 `appId` 是 **applications.id**：我的资产页用 `product.business_ref_id`，应用/商品详情页用 `app.id`/`appDetail.id`。传错（如传成 product_id）会 404/40400。
- 阶段二不改变阶段一的「按钮显隐」逻辑（仍按 access_url 是否存在控制），只改「点击后的动作」。
- 阶段一直接打开方式可整体替换，无需保留两套；后端使用权校验已集中在 launch 接口。
- 测试服已部署后端阶段二接口；如本机联调走 dev 代理到测试服即可。
