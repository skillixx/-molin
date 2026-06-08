# P1 缺陷修复复测报告

**复测日期**：2026-06-08
**测试环境**：测试服务器 8.130.9.163:8080（API 二进制 `molin-api`，6 月 8 日 15:26 部署）
**关联提交**：开发修复 commit `7bc0765`，合并 `480e661`；DB migration `000013` 已执行
**测试方式**：自包含测试账号方案（自行注册新账号，绑定系统已有 `admin` 角色）

## 测试账号

- 邮箱：`qa_p1verify_1780904259@molin.io`
- user_id：`80`
- 通过 `INSERT IGNORE INTO user_roles (user_id, role_id) VALUES (80, <admin角色id>)` 绑定系统已存在的 `admin` 角色（未新增/修改任何 permissions、role_permissions 数据）
- 验证后角色查询：`user_id=80 -> role_code=admin`

## 验证结果汇总

| 编号 | 验证项 | 期望结果 | 实际结果 | 结论 |
|---|---|---|---|---|
| 1 | `GET /api/admin/products` 不再 403 | 200 | **200**，返回商品列表（21 条记录），及 `GET /api/admin/products/21` 返回详情 200 | 通过 |
| 2 | `GET /api/admin/orders` 不再 403 | 200 | **200**，返回订单列表（71 条记录） | 通过 |
| 3 | `POST /api/recharge/orders` 校验 `payment_method` 枚举值 | 见下表 | 全部符合预期 | 通过 |

### 验证项 3 明细（payment_method 枚举校验）

| 输入 | 期望 | 实际状态码 | 实际响应 |
|---|---|---|---|
| `payment_method: "alipay"` | 201 | **201** | `{"code":0,"message":"ok","data":{"order_id":72,"pay_url":"/api/simulate-pay?order_no=ORD20260608XC8K7F3G&amount=10"}}` |
| `payment_method: "wechat"` | 201 | **201** | `{"code":0,"message":"ok","data":{"order_id":73,"pay_url":"/api/simulate-pay?order_no=ORD20260608CM0F4M6C&amount=10"}}` |
| `payment_method: "bitcoin"`（非法值） | 400，提示"不支持的支付方式" | **400** | `{"code":40000,"message":"不支持的支付方式: bitcoin，仅支持 wechat / alipay","data":null}` |
| 缺省 `payment_method` | 400 | **400** | `{"code":40000,"message":"不支持的支付方式: ，仅支持 wechat / alipay","data":null}` |
| `payment_method: ""`（空字符串） | 400 | **400** | `{"code":40000,"message":"不支持的支付方式: ，仅支持 wechat / alipay","data":null}` |
| 旧字段名 `provider`（无 `payment_method`，sanity check） | 400 | **400** | `{"code":40000,"message":"不支持的支付方式: ，仅支持 wechat / alipay","data":null}`（确认服务端确实读取新字段名 `payment_method`，旧字段名被忽略而触发缺省校验） |

## 关键响应片段（原始 curl 输出）

```
=== 1) /api/admin/products → 200 ===
HTTP_STATUS:200
{"code":0,"message":"ok","data":{"list":[...21 条记录...],"pagination":{"page":1,"page_size":20,"total":21}}}

=== /api/admin/products/21 → 200 ===
HTTP_STATUS:200
{"code":0,"message":"ok","data":{"id":21,"product_type":"saas","product_code":"qa_fullapi_prod_1780901339",...}}

=== 2) /api/admin/orders → 200 ===
HTTP_STATUS:200
{"code":0,"message":"ok","data":{"list":[...71 条记录...],"pagination":{"page":1,"page_size":20,"total":71}}}

=== 3) payment_method=bitcoin → 400 ===
HTTP_STATUS:400
{"code":40000,"message":"不支持的支付方式: bitcoin，仅支持 wechat / alipay","data":null}
```

## 数据库验证（确认权限码已 seed 并绑定）

```
permissions 表：id=15 product:view（商品查看），id=16 order:list（订单列表）
role_permissions：admin -> product:view ✅，admin -> order:list ✅
```

测试账号 user_id=80 通过 `user_roles` 绑定 `admin` 角色后，登录获取的 access_token 即可正常访问上述两个原本返回 403 的管理端接口，证实权限链路（角色 → 权限码 → 路由中间件校验）已打通。

## 结论

**3 项 P1 缺陷修复全部验证通过，未发现遗留问题。**

1. `GET /api/admin/products`（含详情接口）：admin 角色已可正常访问，返回 200。
2. `GET /api/admin/orders`：admin 角色已可正常访问，返回 200。
3. `POST /api/recharge/orders` 的 `payment_method` 枚举校验已生效：合法值（`alipay`/`wechat`）返回 201 并生成订单及支付链接；非法值（`bitcoin`）、缺省、空字符串均正确返回 400 及"不支持的支付方式"提示；新字段名 `payment_method` 已生效（旧字段名 `provider` 不再被识别）。

## 建议

可将本轮验证结果纳入下一份周测试报告的"已修复缺陷复测"小节，建议关闭对应的 3 个 P1 Issue。
