# 测试报告 — 后端丙 #168 会员权益公开端点 + M2/M9 内联等级名

- **日期**：2026-06-18
- **被测对象**：PR #168（`GET /api/memberships/{id}/benefits` 公开权益端点 + M2/M9 内联 `level_code`/`level_name`）
- **环境**：测试服 `8.130.9.163:8080`（部署分支 main，molin-api md5=`1e402fc`），MySQL `127.0.0.1:13306` 库 `molin`
- **被测契约**：`docs/frontend-api-reference.md` §11.1b / §11.2 / §11.5
- **测试脚本**：`tests/test_membership_benefits_inline.py`
- **统计**：总用例 **22** / 通过 **20** / 失败 **2**（同一根因 P3，详见 D-MBI-01；**#168 新增能力本身全部通过**）

## 逐项结果

### §11.1b `GET /api/memberships/{id}/benefits`（公开端点，6/6 通过）

| 用例 | 结果 | 佐证 |
|---|---|---|
| (a) 无 token 公开访问 | PASS | HTTP 200 `code:0`，等级A(id=20) 返回 items |
| (b) 仅返回 active 权益 | PASS | 等级A 建 active(id=5)+inactive(id=6)，仅返回 `items_ids=[5]` |
| (b) 权益对象字段齐全 + 值正确 | PASS | 含 id/level_id/benefit_type/benefit_value/status/created_at/updated_at；`level_id=20,status=active,type=discount` |
| (c) 不存在 level_id | PASS | `GET /999999999/benefits` → HTTP 404 `{code:40400,message:"会员等级不存在"}` |
| (d) inactive 等级（含 active 权益） | PASS | 等级D 置 inactive 后 → HTTP 404 `code:40400`（不泄露未上架等级） |
| (e) active 等级无 active 权益 | PASS | 等级E 无权益 → HTTP 200 `items:[]`；等级F 权益全 inactive → `items:[]` |

### §11.2 `GET /api/my/membership` 内联等级名（4/5 通过）

| 用例 | 结果 | 佐证 |
|---|---|---|
| 无会员用户 → null | PASS | admin 未开会员 → `{membership:null}` |
| 有会员 → 返回会员对象 | PASS | user(456) 开通等级A → 返回会员对象 |
| 保留 level_id | PASS | `level_id=20` |
| 新增 level_code/level_name 正确 | PASS | `level_code=mbi_benefA_96973436`，`level_name=内联测试等级benefA`（与等级一致） |
| 原字段全保留 | FAIL | 缺 `asset_id`（DB 该字段为 NULL，被 omitempty 省略）；id/user_id/status/started_at/expires_at 均在。见 D-MBI-01 |

### §11.5 `GET /api/admin/user-memberships` 内联 + 批量映射（10/11 通过）

| 用例 | 结果 | 佐证 |
|---|---|---|
| 无 token | PASS | HTTP 401 `code:40001` |
| 无 membership:view 普通用户 | PASS | HTTP 403 `code:40003` |
| 扁平分页结构 | PASS | `{items,page=1,page_size=20,total=2}` |
| 同页 level_id=A 等级名不串味 | PASS | `code=mbi_benefA_96973436 / name=内联测试等级benefA` |
| 同页 level_id=G 等级名不串味 | PASS | `code=mbi_m9G_96979571 / name=内联测试等级m9G`（同页两个不同 level_id 各自正确，间接佐证批量映射无 N+1/串味） |
| items 含 created_at/updated_at + 原字段 | FAIL | 缺 `asset_id`（同 D-MBI-01）；created_at/updated_at/level_code/level_name 均在 |
| 缺失等级 → 等级名留空不报错 | PASS | DB 造 level_id=988888888 会员 → HTTP 200，该项 `level_code:"" level_name:""`，接口不报错 |

## 缺陷清单

### [会员][P3] D-MBI-01 — M2/M9 会员对象在 asset_id 为 NULL 时省略该 key（与契约示例不一致）

- **优先级**：P3（契约一致性问题，非 #168 引入，不阻断）
- **复现**：(1) 用 M10 `POST /api/admin/user-memberships` 开通会员（不带 asset_id，DB asset_id=NULL）；(2) 调 `GET /api/my/membership` 或 `GET /api/admin/user-memberships`
- **期望**：契约 §11.2/§11.5 示例含 `"asset_id": 2`，即 key 恒在（null 时应为 `"asset_id": null`）
- **实际**：asset_id 为 NULL 时整个 key 被省略
- **根因**：DTO `MembershipResponse.AssetID` / `AdminUserMembershipResponse.AssetID` 使用 `json:"asset_id,omitempty"`，指针零值（nil）时省略
- **影响**：前端按 `data.membership.asset_id` 取值得 undefined（与 null 行为一致，通常可兜底），但与契约文本严格比对不符。**与 #168 无关**——#168 仅新增 level_code/level_name，未改原序列化行为
- **处理**：已于 PR #169 修复（去掉 `omitempty`，key 恒在、空值返 `null`，并补序列化单测 + 文档说明），已合并 main（merge commit `8d481b8`）

## 数据清理核对

仅清理本脚本自造数据，全部按**精确主键** DELETE（无 LIKE/模式匹配批量删除）：

- 自造主键：users=[455,456]、levels=[20,21,22,23,24]、benefits=[5,6,7,8]、memberships=[24,25,26]（及对应 user_roles/user_sessions）
- 清理后计数核对：`user_memberships=0, membership_benefits=0, users=0, membership_levels=0` → **全部=0，无残留**
- 既有业务数据（等级 id=4/5 等）未触碰，未改 .env、未重启服务

## 签字结论

**部分通过 → #168 建议关闭（已合并）。**

- #168 三项新增能力（§11.1b 公开权益端点的过滤 / 404 防泄露 / 空集语义、§11.2 与 §11.5 内联 level_code/level_name、批量映射无串味、缺失等级留空容错、鉴权 401/403）**全部按契约通过**。
- 唯一 2 项 FAIL 为 `asset_id` 在 NULL 时省略 key 的既有序列化行为，与 #168 无关，定级 P3，**已由 PR #169 闭环修复**。

> 测试工程师 QA 签字，2026-06-18。D-MBI-01 已随 #169 修复并合并，建议关闭。
