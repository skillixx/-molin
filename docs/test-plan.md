# 测试计划

## 1. 测试策略

```text
单元测试（开发者负责）
  - 每个 service 方法都有对应单元测试
  - 覆盖率目标：核心业务模块 > 70%
  - 工具：Go testing 标准库 + testify

集成测试（开发者负责）
  - 测试完整 HTTP 请求链路
  - 使用测试数据库（molin_test）
  - 工具：net/http/httptest

接口测试（测试/产品负责）
  - 测试所有 API 接口
  - 工具：curl 或 Postman / Bruno

功能验收测试（测试/产品负责）
  - 每周验收，测试完整业务流程
  - 手动操作 UI 验证

安全测试（开发者 + 产品共同执行）
  - 权限绕过测试
  - 并发扣费测试
  - 幂等性测试
```

## 2. 后端单元测试文件位置

每个模块测试文件与被测文件放在同一目录：

```text
server/internal/modules/auth/
  service/
    auth_service.go
    auth_service_test.go        -- 注册、登录、退出、刷新 Token 单元测试

server/internal/modules/iam/
  service/
    iam_service.go
    iam_service_test.go         -- 权限计算优先级测试

server/internal/modules/billing/
  service/
    wallet_service.go
    wallet_service_test.go      -- 扣费事务、余额不足、乐观锁冲突测试
    payment_service.go
    payment_service_test.go     -- 支付回调幂等测试

server/internal/modules/product/
  service/
    pricing_service_test.go     -- 价格优先级：会员价 > 角色价 > 默认价

server/internal/modules/finance_consumer/
  service/
    consumer_service_test.go    -- 消费事件幂等测试

server/internal/modules/asset/
  service/
    asset_service_test.go
    entitlement_service_test.go -- 权益原子消耗测试（并发）
```

## 3. 接口测试用例

### 3.1 认证模块

**原有接口：**

| 用例 | 接口 | 输入 | 期望结果 |
|---|---|---|---|
| 邮箱注册成功 | POST /api/auth/register/email | 正确邮箱、密码、验证码 | 201，返回 access_token |
| 重复邮箱注册 | POST /api/auth/register/email | 已注册邮箱 | 409，code=40900 |
| 验证码错误 | POST /api/auth/register/email | 错误验证码 | 400，code=40000 |
| 邮箱登录成功 | POST /api/auth/login/email | 正确邮箱、密码 | 200，返回 token 对 |
| 密码错误 | POST /api/auth/login/email | 错误密码 | 401，code=40001，message=`邮箱或密码错误` |
| 退出登录 | POST /api/auth/logout | refresh_token | 200，再次刷新返回 401 |
| 刷新令牌 | POST /api/auth/refresh | 有效 refresh_token | 200，新 access_token |
| 用吊销的 Token 刷新 | POST /api/auth/refresh | 已退出的 refresh_token | 401 |
| 验证码限流 | POST /api/auth/verification-codes/email | 连续 11 次 | 第 11 次返回 429 |

**邮箱验证码登录 Phase 1 delta（待 QA/PM 签署，尚未执行实现验收）：**

| 用例 | 操作/输入 | 期望结果 |
|---|---|---|
| 密码登录保持不变 | 调用 `POST /api/auth/login/email` | 继续按邮箱+密码登录，不被新端点替换 |
| 严格 Body | 调用 `POST /api/auth/login/email/code`，Body 仅含合法 `email`、`code` | 200，响应字段与既有 `LoginResp` 完全一致 |
| 缺失、空值或类型错误 | 缺少 `email`/`code`、传空值或错误 JSON 类型 | `400/40000「请求参数错误」` |
| 拒绝额外字段 | 在 `email`、`code` 外附带 `scene`、`password` 或任意字段 | `400/40000「请求参数错误」` |
| 验证码资格 | 分别准备 scene 非 login、pending/failed、已使用、已过期、邮箱或验证码不匹配的记录 | 均返回 `400/40000「验证码错误或已过期」`，不签发 Token |
| 原子消费 | 对同一条 accepted、未使用、未过期且匹配的 login 验证码并发提交 2 次 | 验证与条件更新 `used_at` 在同一事务中完成；恰好 1 次成功，另 1 次返回 `400/40000「验证码错误或已过期」` |
| 未注册邮箱 | 使用未注册邮箱与任意验证码 | `404/40404` |
| 禁用账号 | 使用禁用账号与有效 login 验证码 | `403/40003`，验证码不被消费 |
| D-16 跨方式累计 | 同一邮箱先密码失败 4 次，再验证码登录失败 1 次 | 第 5 次失败触发 15 分钟锁定；后续登录返回 `423/42901「登录失败次数过多，请15分钟后重试」` |
| 锁定期间保护 | 锁定期间提交有效 login 验证码 | 返回 `423/42901`，不消费验证码、不创建会话 |
| 成功清除 D-16 | 失败未达锁定阈值后，分别用密码登录或验证码登录成功 | 任一方式成功均清除该邮箱失败计数；后续失败重新计数 |
| 锁定到期 | 推进时间超过 15 分钟后提交有效凭据 | 允许重新登录，不再返回 42901 |
| 双维度发码限流 | 从同一 IP 向不同邮箱发码超过 10 次/分钟；再从不同 IP 向同一规范化邮箱发码超过 10 次/分钟 | 两种情况第 11 次均返回 `429/42900`；邮箱维度只使用 HMAC 标识，不记录完整邮箱 |
| 会话共存 | 账号已有有效会话，再通过邮箱验证码登录 | 创建新会话；既有会话仍有效，不被吊销 |
| 管理员 MFA 不绕过 | 管理员用邮箱验证码取得普通登录 Token 后直接访问要求双重认证的管理接口 | 仍返回 `403/40003`；普通登录不设置或刷新管理员手机/邮箱 MFA 状态 |
| 数据结构边界 | 审查 delta 数据设计 | 复用 `verification_codes` 的 `scene=login` 与既有会话表，不新增 schema 或 migration |

> 上表只是 Phase 1 delta 的可执行验收口径。QA 与产品经理尚未在设计评审文档签署，且本轮未执行 Go、数据库、Redis、DirectMail、前端或 E2E 验证，不得据此声称接口已实现或验收通过。

**★ 统一注册（POST /api/auth/register）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 统一注册成功（手机+邮箱双OTP） | 正确手机/邮箱/密码/双验证码 | 201，返回 token 对，phone_verified/email_verified=true |
| 手机号重复 | 已注册手机号 | 409，code=40900 |
| 邮箱重复 | 已注册邮箱 | 409，code=40900 |
| 用户名重复 | 已存在用户名 | 409，code=40900 |
| 手机验证码错误 | 错误 phone_code | 400，code=40000 |
| 邮箱验证码错误 | 错误 email_code | 400，code=40000 |
| 用户名过短（1位） | username="a" | 400 |
| 用户名过长（33位） | username 超长 | 400 |
| 用户名含非法字符 | username 含空格/特殊符号 | 400 |

**★ OTP 密码重置（POST /api/auth/password/reset）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 手机 OTP 重置成功 | 正确手机号、验证码、新密码 | 200；旧密码无法登录；新密码可登录 |
| 邮箱 OTP 重置成功 | 正确邮箱、验证码、新密码 | 200；旧密码无法登录 |
| 重置后旧 Refresh Token 失效 | 使用旧 refresh_token 刷新 | 401（全部会话已吊销） |
| 验证码错误 | 错误 code | 400，code=40000 |
| 不存在的手机/邮箱 | 未注册账号 | 400 |
| 非法 target_type | target_type="wechat" | 400 |

**★ 修改用户名（PATCH /api/me/username）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 修改成功 | 合法新用户名 | 200；GET /api/me 返回新用户名 |
| 用户名重复 | 已存在用户名 | 409，code=40900 |
| 用户名非法 | 含特殊字符 | 400 |
| 无 Token | 无 Authorization 头 | 401 |

**★ 修改手机号（PATCH /api/me/phone）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 修改成功 | 新手机号 + 正确验证码（scene=bind_phone） | 200；phone_verified=true |
| 验证码错误 | 错误 code | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |

**★ 修改邮箱（PATCH /api/me/email）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 修改成功 | 新邮箱 + 正确验证码（scene=bind_email） | 200；email_verified=true |
| 验证码错误 | 错误 code | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |

**★ 管理员手机双重认证（POST /api/admin/auth/verify-phone）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 认证成功 | 管理员 Token + 正确验证码（scene=admin_verify） | 200；admin_phone_verified=true |
| 验证码错误 | 正确 Token + 错误验证码 | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |
| 普通用户访问 | 无 user:manage 权限的 Token | 403，code=40003 |

**★ 管理员邮箱双重认证（POST /api/admin/auth/verify-email）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 认证成功 | 管理员 Token + 手机已认证 + 正确邮箱验证码 | 200；admin_email_verified=true |
| 验证码错误 | 正确 Token + 错误验证码 | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |
| 普通用户访问 | 无 user:manage 权限的 Token | 403，code=40003 |

### 3.1.1 DirectMail 邮件模板管理与 OTP 发送（Phase 1 契约门禁，Phase 2 未验收）

> Phase 0 仅核对外部资质、模板与 RAM 准备证据；Phase 1 冻结可执行契约，并以 `docs/aliyun-directmail-email-template-phase1-design-review.md §15` 的 QA/PM 书面记录为唯一出口；Phase 2 才验收 Go、migration、真实 MySQL/Redis、DirectMail、前端与 E2E。既有实现、MySQL 和真实发送材料全部是 Phase 2 待复验输入，不能倒置 Phase 1 门禁。本轮只确认协议与 Redis 锁原语，Go 集成未验收。

**Phase 1 出口：** QA 必须书面确认下列用例可执行、断言完整，产品经理必须书面确认五场景、MFA、错误文案和 accepted 展示语义；两项均写入指定记录且无阻断项后才可进入 Phase 2。

**verification_codes PM B 全停机 migration：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 新库迁移 | 空库从000001连续迁移到000055 | version=55/dirty=0；code保留为VARCHAR(64) NULL并新增code_hash；五张邮件业务表、五场景、四权限及一张migration-only ownership技术表完整 |
| 旧库迁移 | 已验收54/dirty=0执行000055 up | 结构、数据失效、CHECK、索引和seed完整；五业务表+一技术表边界正确，无半成品DDL |
| down回54 | 55/dirty=0按全停机顺序执行down | version=54/dirty=0；删除code_hash并保留code VARCHAR(64) NULL；五业务表和ownership技术表均按预期删除，预存权限/admin绑定保留，旧应用健康 |
| 64 位 hash | 邮箱和手机各生成新 OTP | code_hash 完整保存 64 位小写 hex，校验使用 code_hash，无截断 |
| 历史歧义行 | 准备 16 位截断值后执行维护窗口迁移 | 不把旧值当明文再 hash；行被安全失效，不能通过校验 |
| 历史与手机兼容 | 准备过期邮箱/手机历史行并迁移；再发新手机码 | 所有历史行统一 `send_status=failed`、`accepted_at=NULL`、过期且已使用；新手机显式 accepted 且可正常一次性校验 |
| 历史邮箱不可关联迁移 | 准备同邮箱多条历史行并执行维护窗迁移 | 全部 failed/过期/已使用且 target_value 清空；每行 target_hash 各不相同且不使用应用HMAC密钥，masked 为统一占位；仅新email写真实HMAC |
| 新邮件默认安全 | 创建邮件 OTP | 业务代码显式写 pending；未获供应商受理前不可校验 |
| 邮箱最小存储 | 创建并校验邮件 OTP | verification_codes.target_value 为 null，仅 target_hash/target_masked 非空；校验按 HMAC 命中，库中不存在完整邮箱 |
| 手机兼容存储 | 创建并校验手机 OTP | 继续使用 target_value，target_hash/target_masked 为 null，不受邮件列改造影响 |
| CHECK否定写入 | 对状态、场景、provider和条件约束写入非法组合 | 全部被数据库拒绝，合法数据不受影响 |
| seed/version/dirty | 重跑seed并模拟migration中断 | seed幂等；半完成结构禁止force；人工恢复完整后version/dirty与实际schema一致 |
| 代表性备份恢复（Phase 2 待复验历史输入） | 54 状态库单事务备份并恢复到新隔离库后执行 up；55 状态库备份并恢复到新隔离库后执行 down | 历史预期为迁移与回滚结构正确；本轮未重跑，不代表通过 |
| partial-up/down（Phase 2 待复验历史输入） | 依次在 up 16 个、down 15 个断点注入失败，并各跑 1 次无注入基线 | 历史结果仅作复验线索；Phase 2 必须重新留存断点与最终结构证据 |
| 结构统计口径（Phase 2 待复验历史输入） | 查询 CHECK、外键及 `(table_name,index_name)` | 历史统计仅作复验基线；本轮不确认当前结构通过 |
| 000020 历史兼容修复（Phase 2 待复验历史输入） | 使用真实golang-migrate在MySQL 8.4.10执行空库1→55、19→20→19→20、v20→55不重放、55→54→55；对测试服务器MySQL 8.0.46只读审计 | 历史材料待 Phase 2 重跑，不代表本轮通过 |
| 55→56 部署门禁 | 按全停机流程从 54/dirty=0 发布 | 停止邮箱/手机 OTP 发码、OTP 校验、注册、登录流量→等待 10 分钟→停止全部 auth/API 实例→备份并验证可恢复→000055 up；必须先断言 version=55/dirty=0、五业务表、000055 ownership、五场景、四权限及 admin 绑定完整，再执行 000056 up；随后断言 version=56/dirty=0、bootstrap 权限/admin 绑定、000056 ownership 与空 receipt 表完整，才可部署新应用；禁止滚动部署和新旧共存 |
| 56→57 UTC 秒级结构门禁 | 在停机维护窗核对 000055/000056 结构后执行 000057 up | 精确断言旧结构与专用备份表不存在；创建 manifest 后仅备份非零小数秒行，证明 expected_count、主键、原值、秒值及非时间指纹完整；只更新这些行的 created_at 到秒并复验，再修改三列。0/1/多行均成功且 version=57/dirty=0，备份表保留供 down |
| 000057 离线资产 | 不连接数据库，静态读取 up/down SQL | 严格白名单仅新增专用备份表的精确 CREATE/INSERT/UPDATE/DROP；去注释规范化后的 up 16 条、down 15 条完整语句与顺序冻结 SHA-256，当前 down 文件 SHA-256 为 `EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB`。每个语句边界及每个断言均覆盖删除、替换、注释伪装和重排故障注入；模型覆盖0/1/多行归一恢复、备份缺行、源回执缺失和孤儿备份失败。四道关键证据门禁必须由备份反向 LEFT JOIN receipt，校验 manifest、expected_count、COUNT(r.id) 与 r.id IS NULL；down 删除备份必须位于最终门禁之后。结构攻击覆盖额外列、列顺序/类型/unsigned/可空/默认值/extra/排序规则、引擎、表排序规则、主键和三项 CHECK 名称/表达式；另要求 statistics 派生表投影 non_unique，三项 CHECK 比较仅窄化规范反斜杠单引号，并拒绝全量删除反斜杠。字符集 introducer 只允许显式移除 `_utf8mb4` 与 MySQL 8.0.46 实际返回的 `_latin1`；fixture 必须覆盖三个实际 `_latin1` clause、既有 `_utf8mb4`、转义引号、白名单外前缀保留和 non_unique。该证据仅证明资产结构与模型，不证明 MySQL 方言或运行时语义 |
| 000057 隔离执行资产 | 不连接数据库，静态检查 `run-000057-container-backup-restore-cycle.sh` | `bash -n` 通过；Up/Down SHA 与当前文件一致，Down 固定为 `EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB`。目标 schema 在启动预检阶段只读取一次系统随机 UUID，严格规范为 `molin_restore_57_reverify_<32位小写十六进制>` 后立即 readonly 冻结，禁止复用旧 dirty1 目标且不输出随机目标名。`work_dir` 精确冻结为只读预检确认不存在的 `/root/molin-000057-container-cycle-3263e5469732436c910dd22f894d647b`，只能在 readonly 定义处出现一次且不能被覆盖；禁止复用已存在的 `/root/molin-000057-container-cycle`。静态断言继续冻结源库 schema56/dirty0、单事务 dump、目标不存在、仅目标写、Up→Down→Up、全量/稳定表快照及最终源未写入门禁，并拒绝 DROP DATABASE/SCHEMA、目录清理、强制修复和源库写 SQL。当前脚本 SHA-256 为 `6C43A8E82440A7A939FDD66EA375EB56BF4BD8DEFE1A6F0ADD1F1FF0EA9290F8`；该检查不证明真实数据库周期已执行 |
| 000057 down | 从完整 schema57 回滚到 schema56 | 要求备份表六列精确结构、InnoDB/utf8mb4_0900_ai_ci、receipt_id 单列非前缀主键、三项已启用 CHECK，以及完整 manifest、expected_count、备份行、当前秒值和非时间指纹；statistics 派生表必须投影 non_unique，CHECK_CLAUSE 只允许把反斜杠单引号替换为单引号，并只按 `_utf8mb4`、`_latin1` 明确白名单移除字符集 introducer，不能删除其他反斜杠或下划线标识符。先扩回 DATETIME(3)，按PK恢复原毫秒并验证，再恢复默认值，最后删除备份表。0行 manifest 同样可逆；缺行、孤儿、篡改、重复 down 或未知 partial 均失败关闭且不得擅自删备份。本次不连接数据库；后续仍须在授权 MySQL 8.0.46 隔离环境重跑完整 up/down，确认运行时 CHECK_CLAUSE 规范化文本与门禁一致 |
| A 类回滚 | 000056 从未执行，从 schema55 回滚 | 完成停流、等待 10 分钟、停实例、备份恢复验证和外部引用检查后，执行原 000055 down；断言 version=54/dirty=0、五业务表与 000055 ownership 已按规则删除、预存权限/admin 绑定保留、旧 `code VARCHAR(64) NULL` 保留 |
| B 类回滚 | 000056 已执行且 receipt 行数为 0 | 完成停流、等待 10 分钟、停实例、备份恢复验证，并断言无成功 receipt、无未知角色/用户覆盖/分组等引用；先执行 000056 down，断言 version=55/dirty=0、000056 ownership/空 receipt 表及本迁移创建项已精确清理，再执行 000055 down；最终断言与 A 类相同 |
| C 类应用回滚 | 存在成功 bootstrap receipt | 关闭 bootstrap、撤除 Secret/CIDR并部署与 schema55+56 兼容的回滚应用；不执行 000056 down 或 000055 down；断言 version=56/dirty=0，receipt、模板镜像、`admin_verify` 绑定及两张 ownership 证据完整，普通邮件链路保持可追溯；全程禁止 force |
| C 类高风险例外 | 业务明确要求回到 schema55 之前 | 必须另立高风险变更单，先停止相关流量与实例、关闭 bootstrap 并撤除 Secret/CIDR，完成数据库备份及隔离恢复验证，留存覆盖 receipt/镜像/绑定/权限归属的不可变审计证据，取得 QA、产品经理、运维联合批准；盘点并经审批解除权限、角色、用户覆盖、分组、receipt、镜像和绑定等全部引用后，先执行 000056 down并断言 version=55/dirty=0及 000056 对象精确清理，再执行 000055 down并断言 version=54/dirty=0及 000055 对象精确清理；任一前置或断言失败立即停止，全程禁止 force |

**000055 权限 ownership 真实 MySQL 用例：**

`migration_000055_permission_ownership` 仅是 migration-only 技术表，不属于五张邮件业务表；以下用例必须在真实 MySQL 8 执行。

| 用例 | 预置/故障注入 | 期望结果 |
|---|---|---|
| 四种预存组合 | 分别验证四权限全新、权限预存但 admin 绑定缺失、权限和 admin 绑定均预存、四个权限混合上述状态 | ownership 恰好四行并准确记录两个 created 标志；up 只补缺项并回填最终 ID；down 只删 created=1，所有预存权限与绑定保留 |
| 权限元数据冲突 | 预存同 code 但 name/resource/action 任一不符 | up 在写 ownership/seed 前 fail-closed，不覆盖或删除冲突记录 |
| 未知角色引用 | 给本 migration 创建的权限增加非 admin role_permissions 后执行 down | down fail-closed，ownership 与业务结构保留供恢复，不误删引用 |
| 用户覆盖引用 | 给本 migration 创建的权限增加 user_permission_overrides 后执行 down | down fail-closed，不删除权限或 ownership |
| 分组权限引用 | 给本 migration 创建的权限增加 group_permissions 后执行 down | down fail-closed，不删除权限或 ownership |
| partial-up 五业务表创建故障 | 在五张业务表每个创建点分别中断 | information_schema 能定位最后完成对象；按矩阵逆序清理或补齐，禁止 force 半成品 |
| partial-up ownership 取证故障 | 在技术表创建、四行写入期间分别中断 | 取证不完整时从备份恢复，不猜测 created 标志；五业务表与技术表边界可独立核对 |
| partial-up seed/关联故障 | 在补权限、补 admin 绑定、回填权限 ID、回填关联 ID、两类写后强断言处分别中断 | 仅补缺项；ownership 与实际 ID/created 标志一致，强断言全部通过后才允许 version=55/dirty=0 |
| partial-down 引用阻断 | 分别注入三类未知引用后触发 down | 在任何删除前 fail-closed；清除未知引用并复核后才可重新执行精确清理 |
| partial-down 精确清理故障 | 在 admin 绑定删除、权限删除、写后断言、ownership 删除、五业务表逆序删除、verification 清理处分别中断 | 按 information_schema/ownership 恢复矩阵续作；写后断言前不得删技术表；最终五业务表+一技术表均清理且预存权限保留 |

**固定场景与变量映射：**

| 用例 | 输入/操作 | 期望结果 |
|---|---|---|
| 五场景种子完整 | 查询 `/api/admin/email/scenes` | D-95 返回且恰含 register/login/reset_password/bind_email/admin_verify |
| 非法第六场景 | 请求 `/api/admin/email/scenes/other` | 400/40000，不创建绑定 |
| 平台字段映射 | 发送任一场景 OTP | 服务端从冻结镜像 `TemplateText` 本地渲染 `Code` 与 `ExpireMinutes`；Adapter 收到最终 `HtmlBody`，不接收未渲染变量 |
| SingleSendMail 正文参数 | 捕获生产 Adapter 的 RPC 表单 | 必须包含 `Subject` 与非空 `HtmlBody`，不得包含 `Template.TemplateId`、`Template.TemplateData` 或 `TextBody`；TemplateId 仍保留在绑定与发送日志中 |
| 本地渲染语法与普通花括号 | 分别使用 `{Code}`、`${Code}`、`{{ Code }}` 及对应有效期变量，并在同一正文放置 CSS、JSON 和 HTML 邻接内容 | 只替换两个固定变量；CSS、JSON 普通花括号与 HTML 结构保持不变，不把 JSON 键名误判为变量 |
| 模板正文失败关闭 | 分别使用空正文、缺变量、大小写错误、畸形/嵌套/尾随花括号、额外变量、渲染后残留变量 | `422/51001「邮件模板变量不完整」`；不创建 pending 验证码/发送日志，Adapter 增量 0 |
| 主题与正文边界 | 主题分别为 100/101 个 Unicode 字符，HtmlBody 分别为 80 KiB/80 KiB+1 个 UTF-8 字节，并覆盖空值和非法 UTF-8 | 精确边界允许；超过边界或非法内容失败关闭且不发起 HTTP 请求 |
| 映射不可覆盖 | 管理端尝试提交自定义 variable_mapping | 400/40000，仍保持 code→Code、expire_minutes→ExpireMinutes |
| TemplateId 动态解析 | 连续把场景绑定切换到两个不同模板并发送 | Adapter 使用当前 DB 绑定的 TemplateId；代码/配置中无场景 TemplateId 常量 |

**OTP 供应商受理与认证安全：**

| 用例 | 供应商结果 | 期望结果 |
|---|---|---|
| 内部 pending | 验证码或测试发送占位已落库、供应商尚未返回 | OTP 不可校验；测试发送重试返回 40900且调用数仍为1；GET日志、筛选和summary均不公开/统计pending |
| 明确受理 | DirectMail 同步返回成功 | 原子置 `send_status=accepted`、写 accepted_at 与 accepted 日志；验证码 10 分钟内可校验一次 |
| 发送失败 | 供应商返回失败 | `send_status=failed`，发送日志 status=failed，验证码不可校验 |
| 供应商拒绝 | 分别返回白名单 Code 与未知 Code，并覆盖 4xx/5xx | send_status=failed；failure_reason 仅为 `provider_rejected_{固定类别}_{HTTP状态族}`，未知归 other；验证码不可校验；不出现原始 Code/Message/raw/正文/OTP/完整邮箱/凭据/字段值 |
| 调用超时 | 客户端超时且结果不确定 | send_status=failed，日志 status=failed；失败关闭，验证码不可校验 |
| 受理日志字段 | DirectMail 正式发送成功 | 保存场景、脱敏邮箱、TemplateId、业务请求号、阿里云 RequestId、accepted 和提交时间，不保存供应商原始响应 |
| 上海进程 UTC 写入 | 进程时区固定 `Asia/Shanghai`，创建 pending、明确 accepted、明确 failed 和手机 accepted 验证码 | MySQL DATETIME 参数保留 UTC 墙上时间，不额外加八小时；扫描后全部按 UTC 比较 |
| 旧 failed 冷却到期 | 准备部署前 failed/unknown 行，其 expires_at 或 submitted_at+10分钟恰等于当前 UTC 秒值 | 恰到边界不再阻断新请求；边界前一秒仍阻断，不使用 DATETIME 无法保存的纳秒，不执行 migration 或历史数据改写 |
| accepted UTC 原子消费 | 在上海进程下准备 accepted、未使用且 expires_at 比当前 UTC 晚一秒的验证码，再准备恰到边界记录 | 前者恰好一次消费并以 UTC 秒值写 used_at；边界记录固定拒绝；pending/failed 始终不可消费 |
| Phase 1 范围否定 | 检查接口、数据表和管理页面契约 | 无投递回执 Webhook、最终送达、打开率、点击率字段或统计接口；accepted 只显示“供应商已受理发送请求” |

**五场景真实发送、校验/消费、重放与过期矩阵（Phase 2 必须逐行执行）：**

每行均使用独立脱敏测试账号与已审核绑定，记录发送前后 `email_adapter_calls_total{operation="send_mail",scene,result}` 快照；
首次发送调用次数必须恰增 1，前置拒绝、校验、消费、重放和过期步骤不得再次调用 Adapter。不得记录完整邮箱或 OTP。

| scene | 真实发送与 accepted | 对应校验/消费 | 重放断言 | 过期断言 |
|---|---|---|---|---|
| `register` | 调公开发码端点，响应 `{sent:true,expires_in:600}`，日志 accepted 且界面文案“供应商已受理发送请求” | 用该码完成统一注册，验证码 `used_at` 非空且账号只创建一次 | 同码再次注册返回 `400/40000「验证码错误或已过期」`；冷却窗口同发码请求不再次外呼 | 新发码后推进超过 10 分钟，注册返回 `400/40000「验证码错误或已过期」` |
| `login` | 调公开发码端点并确认 accepted | 调用 `POST /api/auth/login/email/code` 用该码完成邮箱验证码登录，验证码验证与置 `used_at` 原子完成且只消费一次 | 同码再次登录返回 `400/40000「验证码错误或已过期」`；发码幂等重放不再次外呼 | 超过 10 分钟登录失败且不签发 Token |
| `reset_password` | 调公开发码端点并确认 accepted | 用该码重置密码，验证码消费且既有会话按认证契约吊销 | 同码再次重置返回 `400/40000「验证码错误或已过期」`；发码重放不再次外呼 | 超过 10 分钟不能改密，原密码状态不变 |
| `bind_email` | 登录态调用换绑邮箱发码端点并确认 accepted | 用该码完成 `PATCH /api/me/email`，验证码消费且仅绑定目标邮箱 | 同码再次换绑返回 `400/40000「验证码错误或已过期」`；发码重放不再次外呼 | 超过 10 分钟不能修改邮箱 |
| `admin_verify` | 具备管理权限且手机验证有效的管理员调用专属邮箱发码并确认 accepted | 用该码完成邮箱二次认证，`admin_email_verified=true` 且码被消费 | 同码再次验证返回 `400/40000「验证码错误或已过期」`；发码重放不再次外呼 | 超过 10 分钟不能完成 MFA，邮件管理接口仍返回 `403/40003「请先完成管理员双重认证」` |

**模板同步原子性、幂等与 missing：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 完整同步 | 供应商多页全部成功 | 单事务 upsert，计数正确，未出现模板标 missing |
| 中途分页失败 | 第 N 页报错 | 本地模板、版本、missing 标记全部不变；同步记录 failed |
| 详情读取失败 | 列表成功但某 DescTemplate 失败 | 整批回滚，不产生半新半旧镜像 |
| 供应商模板消失 | 上次存在、本次完整同步未出现 | missing=true，首次写 missing_since；绑定保留但所有发送被阻断 |
| 模板重新出现 | 后续完整同步再次出现 | missing=false、missing_since=null；local_enabled 保持原值 |
| 同 key 同请求重试 | 重复 POST sync | 返回原 run，idempotent=true，不重复同步 |
| 同 key 不同请求 | 复用 key 但请求指纹不同 | 409/40900 |
| 跨管理员全局 scope | 管理员 A/B 复用相同 key+method+path+provider | 两者命中同一 run；唯一键为 global scope+key_hash，不按管理员拆分 |
| 两个不同 key 并发同步 | 首个仍 running | 第二个 409/40900，不并发写镜像 |
| 进程中断的 running | started_at 已超过 5 分钟 | 下一次同步前将旧记录收敛为 failed/completed_at，之后允许新同步，不永久阻塞 |
| 同key陈旧同步 | 同key命中超过5分钟的running且已取得同一sync lease | 先写attempt审计并收敛failed，再以idempotent=true返回原失败，不启动新同步 |
| 上海同步时间往返 | `Asia/Shanghai` 进程创建 running，分别成功 apply、失败收敛并重新扫描 | started_at/created_at/completed_at 保持原 UTC 秒值，不增加八小时；成功、失败和列表返回一致 |
| 相同模板连续同步 | `Asia/Shanghai` 进程下数据库扫描的 `provider_created_at` 与供应商返回值墙钟相同，连续执行两次完整同步 | 两次均计为 unchanged；模板 version 保持不变，不因 Local 与 UTC location 不同误判更新 |
| running 秒级 stale 边界 | started_at 恰等于当前 UTC 秒值减5分钟，再准备早1秒的记录 | 恰等于边界不收敛；早1秒且取得同一 sync lease 后收敛 failed，completed_at 为当前 UTC 秒值 |
| 陈旧候选仍持lease | started_at超5分钟但原执行者持续续租 | 重放无法取得同一sync lease，返回409；run保持running且镜像不变 |
| 同步事务fencing | 事务开始或最终更新时run已非running | RowsAffected守卫触发，事务内增改/missing全部回滚 |

**版本锁、权限与 D-95：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 场景绑定乐观锁 | 两管理员读取 version=3，先后保存 | 首个成功到 version=4；第二个 409/40900，不覆盖 |
| 邮件概览字段 | GET `/api/admin/email/summary` | 精确包含 template_total、approved_count、local_enabled_count、unbound_scene_count、submitted_today_count、failed_today_count、last_synced_at；前六项为整数，last_synced_at 可 null |
| 上海今日边界 | 在北京时间 00:00 前后分别插入 pending/accepted/failed 日志 | submitted 仅含 accepted+failed，pending 不计；failed 只含 failed，不使用滚动24小时 |
| 最近同步可空 | 无 succeeded run，再新增 succeeded run | 首次为 null；成功后等于最近 succeeded.completed_at，failed/running 不覆盖 |
| 模板启停 | PATCH `/api/admin/email/templates/{id}/status` 带 local_enabled+version | 成功 version+1、概览刷新；停用立即阻断已有绑定正式发送与模板测试 |
| 模板启停并发 | 两管理员提交相同旧 version | 仅一人成功，另一人 409/40900；同步不覆盖 local_enabled |
| 白名单撤销乐观锁 | 使用旧 version 删除 | 409/40900，记录保持不变 |
| 列表结构 | 模板/场景/同步/白名单/发送日志列表 | data 顶层均为 `{items,page,page_size,total}`，无 list/pagination |
| 只读权限 | 仅 email:template:view | 可 GET，不可绑定/同步/测试/维护白名单 |
| 细分写权限 | 分别只授 `email:template:manage` / `email:template:sync` / `email:template:test` | 只能执行对应能力，其他写接口 403/40003 |
| 四权限 MFA | 分别只授 view/manage/sync/test 且手机或邮箱任一认证缺失/过期 | 对应 GET/写接口全部固定 `403/40003「请先完成管理员双重认证」`；手机+邮箱均有效后才按权限继续判定 |
| 权限缓存失效 | 新增/移除四个权限码 | 立即生效，无需等待 5 分钟 |

**首次配置 `admin_verify` bootstrap delta：**

以下矩阵仅针对 `POST /api/internal/email/bootstrap/admin-verify`；普通 13 个 `/api/admin/email/*` 接口继续执行既有完整 MFA 与四权限矩阵。

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 默认关闭 | 未配置或关闭 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`，访问任意方法 | 路由未注册且返回 404；响应、ready 与日志不泄露配置状态 |
| enabled 严格解析 | 分别令配置键缺失，或注入 true/false、大小写变体、空字符串、1/yes/on/任意文本 | 仅字面 true/false 大小写变体合法；只有配置键缺失时默认 false；显式空字符串或任何其他值均启动失败 |
| 配置失败关闭 | enabled=true，逐项令 TOKEN/ALLOWED_IPS/TRUSTED_PROXY_IPS 缺失、空值、弱占位、Token 少于 8 种不同原始字节、非法 CIDR，或注入 `0.0.0.0/0`、`::/0` 及等价零前缀 | 每一项均使应用启动失败；不得只降级 ready；Token 校验复用内部 Token 的客观基线实现但配置值独立；不得从 metrics/公开邮件配置读取、合并或回退 |
| CIDR 地址族全覆盖矩阵 | 分别在 ALLOWED_IPS、TRUSTED_PROXY_IPS 中测试单条 `0.0.0.0/0`、单条 `::/0`、等价零前缀，以及组合 `0.0.0.0/1,128.0.0.0/1`、`::/1,8000::/1`；另测有重叠但未覆盖完整地址族的组合，并测试 allowed 与 trusted-proxy 各覆盖一部分但只有跨列表合并才全覆盖 | 每个列表独立规范化并求 CIDR 地址并集；任一列表覆盖完整 IPv4 或 IPv6 地址族均启动失败；未覆盖完整地址族的重叠组合允许；两个不同语义列表不跨表合并判定 |
| Bootstrap 两列表 CIDR 等价与重叠 | allowed 配置 `10.0.0.0/24`、trusted-proxy 分别配置等价写法 `10.0.0.1/24` 和不同前缀 `10.0.0.0/25`；再交换两列表测试 IPv4/IPv6 | 规范化后完全相同的 CIDR 条目使应用启动失败；不同前缀仅部分重叠且各列表自身并集未覆盖完整地址族时允许启动 |
| Token 与平台 CIDR 独立性 | Bootstrap Token 与已配置 `INTERNAL_API_TOKEN` 完全相等；分别令 bootstrap CIDR 与平台既有 INTERNAL/TRUSTED 列表完全相同、部分重叠或不重叠 | Token 相等必须启动失败且比较不记录值；bootstrap CIDR 与平台既有列表独立显式配置后允许同值或重叠，同一 Nginx 代理网段可合理重复；不得把该允许规则误用于 bootstrap allowed 与 bootstrap trusted-proxy 之间的规范化同条目 |
| 网络双闸 | 错 Token、重复 Token Header、来源不在 allowlist、伪造 XFF、trusted proxy 缺失/重复/非法 X-Real-IP | 固定 `403/40003「无权限」`；无审计、数据库或 Adapter 副作用 |
| Bootstrap Token 边界 | Header 缺失、空、重复、逗号多值、错误 | 全部精确 `403/40003「无权限」`；Authorization 异常仍按标准401，Idempotency-Key异常仍为400 |
| JWT 边界 | 无 Authorization、无效/过期、已吊销、账号封禁 | 依次精确返回 `401/40001「未登录」`、`401/40001「token 无效或已过期」`、`401/40001「token 已失效，请重新登录」`、`401/40101「账号已被封禁」` |
| 手机 MFA/权限 | 无 `email:template:bootstrap`；手机 MFA 缺失、刚过期、时间戳在未来；expireHours=0 永不过期边界；配置 expireHours<0 | 无权限为 `403/40003「无权限」`；缺失、过期或未来手机 MFA 为 `403/40003「请先完成手机号认证」`，attempt 审计、Adapter、数据库增量均为 0；expireHours=0 只豁免过期不豁免未来时间；expireHours<0 无论 bootstrap 是否启用均启动失败 |
| 普通用户动态获权 | 非 admin 普通用户通过角色/分组/用户 allow override 得到 bootstrap 权限 | 仍为 `403/40003「无权限」`，不写 attempt、不 Describe |
| 权限隔离 | 仅有 bootstrap 权限访问普通 13 接口；仅有原四权限调用 bootstrap | 均拒绝；bootstrap 权限不继承 view/manage/sync/test，普通权限不隐含 bootstrap |
| 方法与媒体类型 | enabled=true 时 GET/HEAD/PUT；Content-Type 缺失、非 JSON、非法 charset | 方法为 `405/40000「请求方法不允许」` 且 Allow=POST；媒体类型为 `415/40000「请求参数错误」`；无业务副作用 |
| Authorization Header | 缺失、空、重复、逗号多值、格式错误 | 标准401；排除Bootstrap Token与Idempotency-Key |
| Idempotency-Key/Body | key 异常；或 `provider_template_id` 为空、全零、65 字节及以上、含非数字、正负号、小数、指数、首尾/内部空白；或 Body 其他严格校验失败 | `provider_template_id` 仅允许 1-64 字节 ASCII 十进制正整数；所有非法输入均为 `400/40000「请求参数错误」`，attempt 审计、Adapter、数据库增量均为 0；排除 Bootstrap Token |
| 精确供应商查询与指标 | 合法请求；对照官方 DescTemplate 文档 | 详情只采用 `RequestId/CreateTime/TemplateSubject/TemplateStatus/TemplateName/TemplateText`；QueryTemplateByParam 列表 `TemplateId/TemplateName/TemplateStatus/CreateTime` 另行处理且不得混用；只调用目标 DescribeTemplate 一次；`operation=describe_template,scene=template_sync` 的 accepted/failed/timeout 恰增一次，不新增序列，不调用 create/modify/delete/send |
| 模板名称 | ProviderTemplate.Name 分别为精确值、大小写变化、首尾空白、其他名称 | 只有 `molin_admin_verify_code_v1` 成功；其他均 `409/40900「邮件模板名称不符合管理员认证约定」` |
| 模板状态/变量 | 分别返回 draft/pending/rejected/未知状态、缺 Code、缺 ExpireMinutes、变量大小写错误 | 按 SSOT 精确 409 文案或 `422/51001「邮件模板变量不完整」`；binding/receipt 不变，send_mail 增量0 |
| 供应商错误 | 不存在、明确上游失败、超时/结果未知 | 分别 `404/40400「邮件资源不存在」`、`502/51002「邮件上游调用失败」`、`502/51002「供应商响应未知，请稍后重试」` |
| DescTemplate 不存在码严格白名单 | 分别返回精确白名单 Code、白名单 Code 加后缀、含 template/notfound/notexist 片段的未知 Code | 仅精确白名单映射 404；伪装与未知 Code 固定通用 502 且安全类别 other，不返回原始 Code/Message |
| 不同 key 并发 Describe | 两个不同 key 同时首次调用 | 允许各自只读 Describe，指标各增一次；写阶段 `SELECT ... FOR UPDATE` 串行，只有一个提交，另一请求409且不覆盖 |
| 相同 key 幂等预检 | 成功后用相同 key+fingerprint 重放 | 直接从 receipt 返回 idempotent=true，不写 attempt、不再 Describe，指标增量0 |
| 同admin同key首次并发 | 两请求均已Describe后竞争行锁 | 第二个匹配completed_by/key hash/fingerprint，返回原成功且idempotent=true |
| 跨admin同key | A成功后B同key或两者并发 | B固定409已完成，不泄露A；admin_id纳入HMAC作用域与fingerprint |
| 行锁/CAS | binding 已非 NULL/false/version1；或并发事务等待行锁后发现 receipt | 初始态 CAS 或 receipt 复查返回409；不能覆盖现有配置；bootstrap 并发控制仅依赖数据库事务、行锁和唯一约束 |
| 成功原子提交 | Name 精确、approved、变量完整 | 镜像 local_enabled、admin_verify CAS 绑定、成功 receipt 与 result 审计同事务；result 审计固定 `target_type=email_admin_verify_bootstrap_receipt`、`target_id=receipt` 内部十进制 ID，不得写供应商 TemplateId/管理员 ID/scene；返回最小字段，不写 MFA 时间戳、不发邮件 |
| 上海 bootstrap 时间往返 | `Asia/Shanghai` 进程执行模板镜像、admin_verify 绑定与成功 receipt 的写入准备并模拟数据库扫描 | provider_created_at/last_synced_at/created_at/updated_at 保持原 UTC 秒级墙钟，不增加八小时；receipt 重放返回同一 UTC 创建时间 |
| 并发唯一 | 多管理员、多 key 并发首次调用 | 最多一个成功；其余 `409/40900`，最终仅一个 scope receipt，不覆盖成功绑定 |
| 同 key 幂等 | 相同 key+指纹重放 | 返回原成功语义且 idempotent=true，不再次调用供应商或写第二条审计结果 |
| key 指纹冲突 | 相同 key 改 provider_template_id | `409/40900`，不改模板、绑定或 receipt |
| attempt 审计失败 | 注入 audit 写失败 | `500/50000「系统内部错误」`；不发起外呼，Describe 指标增量0，无业务写入 |
| result 审计失败 | Describe 成功后令事务内 result 审计写失败 | `500/50000「系统内部错误」`；镜像/绑定/receipt 全回滚，不得复用普通 best-effort result 语义 |
| 失败可重试 | 上游失败或数据库事务失败后使用合规新请求重试 | 失败无成功 receipt/半绑定；attempt 审计存在；条件恢复后允许一次成功 |
| 已完成关闭 | receipt 已存在后任意新 key、操作者或模板 | `409/40900「管理员邮箱认证场景已完成首次配置」`，Adapter 增量 0 |
| 审计与敏感扫描 | 检查 attempt/result、receipt、日志、响应、trace/metrics | 可关联操作者、时间、固定 scope 与安全摘要；无原 Token、Idempotency-Key、邮箱、OTP、模板正文或供应商原始响应 |
| 正常 MFA 闭环 | bootstrap 成功后发送 admin_verify 邮件并 verify-email | 必须先有有效手机 MFA；真实邮件 accepted 后才能校验；成功后普通 13 接口才通过完整 MFA |
| 普通接口回归 | 对 13 个邮件接口执行无 Token、无完整 MFA、无权限矩阵 | 路径/Body/四权限不变；邮件无权限精确为 `403/40003「无权限」`，不出现 `「无操作权限」` |
| 成功后运维收口 | 移除 enabled/token 并重启 | bootstrap 路径恢复 404；普通 13 接口与已绑定 admin_verify 不受影响 |
| 000056 up ownership | admin 0/1/2 行；权限缺失、预存精确、预存元数据冲突；admin 绑定缺失/预存 | 仅 admin=1 且无冲突成功；ownership 一行准确记录 created 标志并回填最终 ID |
| 000056 partial-up/down | 在每个 DDL/DML/断言边界中断 | information_schema/ownership/permission/binding/receipt 与断点一致；盲目重跑被阻断；备份恢复或获批前向补缺可收敛 |
| 000056 down | 无 receipt；已有 receipt；新增权限存在未知角色/用户覆盖/分组引用 | 仅无 receipt、无未知引用时按 ownership 精确 down并保留预存项；其他均在删除前失败关闭且禁止 force |

**模板变量完整性与隔离 HTML 预览：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 缺 Code 绑定 | 模板只有 ExpireMinutes，执行绑定或 enabled=true | 422/51001，不保存绑定/启用 |
| 缺 ExpireMinutes 启用 | 模板只有 Code，启用 local_enabled | 422/51001，version 不变 |
| 缺变量安全停用 | 已启用模板缺任一变量，提交 local_enabled=false+version | 允许停用并 version+1，正式/测试发送立即阻断 |
| 缺变量模板测试 | 对缺任一变量模板调用 test-send | 422/51001，供应商调用次数 0 |
| 缺变量正式发送 | 已有历史绑定模板同步后缺变量，发送五场景任一 OTP | 422/51001，前置校验阶段不创建验证码/发送日志，供应商调用次数 0 |
| 大小写错误 | 变量为 code/expire_minutes | 视为缺失，422/51001 |
| HTML 脚本 XSS | 模板含 script、onerror、javascript URL | sandbox 预览无脚本执行，管理主文档未被修改 |
| HTML 表单/跳转 | 模板含 form、target=_top、meta refresh、window.open | 不能提交表单、顶层跳转或打开弹窗 |
| HTML 网络外带 | 模板含外部 img/font/iframe URL | CSP 阻断外部网络请求，仅允许 data 图片和内联样式 |

**正式 OTP 服务端幂等与统一响应：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 五入口 scope | 分别触发 register/login/reset_password/bind_email/admin_verify | scope 分别含公开入口+target_hmac，或认证态 user/admin ID+target_hmac，不要求客户端 Idempotency-Key Header |
| 同冷却窗口重放 | 同入口、scene、目标和指纹重复请求 | 复用 business_request_no，sent=true、expires_in 按原 expires_at 递减；不生成新码、不重置有效期、不重复调用供应商 |
| pending 并发重放 | 首请求仍在调用供应商时并发相同 scope+指纹 | 409/40900「邮件正在发送，请稍后重试」，供应商调用总数仍为 1 |
| 外呼未知持久化墓碑 | 外呼期间丢锁且响应未知/超时 | 同事务把原 send_logs pending→failed、reason=`provider_outcome_unknown` 并保留 scope；OTP 保留 expires_at 且同事务置 failed；test expires_at 保持NULL；本次 Adapter 已调用，不断言增量0 |
| unknown 原请求/旧 key | 原请求返回后以同一 Idempotency-Key 重放 | 均返回 `502/51002「供应商响应未知，请在验证码过期后重试」`；重放 `idempotent=true`，不再次外呼 |
| cooldown_until 派生 | 分别制造 purpose=otp/purpose=test unknown failed | OTP cooldown_until=expires_at；test cooldown_until=submitted_at+10分钟且 expires_at=NULL；不新增列 |
| unknown 墓碑期新 key | cooldown_until 前改用新 key 请求同 scope | `409/40900「邮件发送结果确认中，请在验证码过期后重试」`，Adapter 增量0；Redis 重启/键丢失后仍被数据库记录阻断 |
| unknown 到期行为 | cooldown_until 后分别使用旧 key、新 key | 到期后仅新 key 可重新发送；旧 key 仍重放原 502/51002 |
| 普通邮件动作审计语义（不含 bootstrap） | attempt失败；或动作后result失败 | attempt失败不执行动作；result失败产生可观测告警但已生效动作仍返回成功；bootstrap 另按上表强事务 result 失败全回滚 |
| failed 重放 | 首次供应商失败后在冷却窗口重试 | 返回原安全错误，不生成新码、不重复调用供应商 |
| 指纹冲突 | 同业务请求号对应 template/binding_version 等不同指纹 | 409/40900，不调用供应商 |
| 新冷却窗口 | 冷却结束后再次明确发码 | 生成新 business_request_no，可创建新验证码和发送日志 |
| send_logs 内部落地与公开过滤 | 正式 OTP accepted/failed 及模板测试 pending/accepted/failed | 内部三态字段完整；公开 GET 只返回 accepted/failed，pending 过滤参数被拒绝且 summary 不统计 |
| 统一发码响应 | 调用公开 email/phone、换绑 email/phone、管理员 email/phone 发码 | 成功 data 均为 `{sent:true,expires_in:600}` |
| 生产禁 code | APP_ENV trim+小写后为 production，无论调试开关取值 | 响应所有层级均无 code，日志/审计/持久化/telemetry 无明文 OTP |
| 环境声明失败关闭 | APP_ENV 缺失、空白、staging 或其他未列出的值，且开启 Mock/调试开关 | 服务不得启用 Mock，响应不得返回 code；配置对象的默认值不能替代显式环境声明 |
| 非生产既有调试边界 | APP_ENV 被显式设置，且 trim+小写后精确为 local/development/dev/test/testing | 仅当原调试开关 trim 后精确等于小写 `true` 时可额外出现 data.code；前端不读取、展示或记录 |
| 调试开关严格解析 | 安全非生产环境下分别配置大写、混合大小写、数字、单字母、yes/on、空白或其他宽松布尔别名 | 均视为关闭且响应无 code；不得复用通用宽松布尔解析 |

**测试邮箱白名单、幂等与脱敏：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 白名单命中 | 向 active 白名单邮箱测试发送 | 可进入供应商调用，响应只回脱敏邮箱 |
| 非白名单 | 向未配置邮箱测试发送 | 固定 400/40000，供应商调用次数为 0 |
| 白名单新增响应 | POST 合法邮箱 | HTTP 201；data 字段/类型固定，status=active、version 为整数且无完整邮箱 |
| 白名单撤销响应 | DELETE 带正确 version | HTTP 200；data 字段/类型固定，status=revoked、version 递增、revoked_at 非空 |
| 白名单物理清理 | revoked 满 30 天并运行清理任务 | 通过 status+revoked_at 索引删除整行，审计仍保留脱敏操作事实；可重新新增同邮箱 |
| 大小写/空格规范化 | 同一邮箱不同大小写/首尾空格重复添加 | 命中同一 HMAC，返回 409/40900，不重复记录 |
| 模板测试路径 | POST `/api/admin/email/templates/{id}/test-send` | 使用路径模板解析 TemplateId，以 SingleSendMail 发送；不要求模板已绑定场景 |
| 测试发送锁 scope | 同管理员、平台模板、场景、规范化邮箱分别使用不同 Idempotency-Key | 固定 scope=`admin-email-template-test:admin:{admin_id}:template:{platform_template_id}:scene:{scene}:recipient:{recipient_hmac}`；仅调用一次 Adapter |
| 锁 scope 维度隔离 | 分别改变管理员/平台模板/场景/规范化邮箱任一维度 | 不竞争同一把锁；Redis key 仅为 scope HMAC 摘要，不含 Idempotency-Key 或完整邮箱 |
| 测试发送 accepted 重放 | 首次 accepted 后以同 key、模板、场景、邮箱重试 | 返回同一 HTTP 200 accepted 结果，idempotent=true，供应商总调用一次 |
| 测试发送明确 rejected | 供应商明确拒绝 | `WHERE id=? AND status='pending'` 唯一收敛 failed，返回安全 502/51002，不返回 HTTP 200/status=failed |
| 测试发送响应未知 | 供应商超时或响应未知 | 原 pending 行条件更新 failed、reason=`provider_outcome_unknown` 并保留 scope；expires_at 仍为NULL，cooldown_until=submitted_at+10分钟，返回指定 unknown 502/51002 |
| 测试发送 failed 重放 | 首次 failed 后以同 key、同请求重试 | 返回同一安全 502/51002 错误信封，供应商总调用一次，不生成第二条日志 |
| 测试发送同 key 换请求 | 同 key、不同模板、邮箱或场景 | 409/40900 |
| 发送日志 accepted 可空性 | GET `/api/admin/email/send-logs` accepted 行 | D-95；provider_request_id 非空、failure_reason=null，其余固定字段类型正确 |
| 发送日志 failed 可空性 | 查询 failed 行 | failure_reason 非空、provider_request_id 可 null，其余 NOT NULL 字段全部可落地 |
| 同步记录可空性 | 查询 running/succeeded/failed 三类 run | running 的 completed/error 均 null；succeeded 仅 completed 非 null；failed 的 completed/error 均非 null |
| 敏感信息扫描 | 合法请求入参含 email/code 后检查输出面 | 请求处理内存允许必要明文；响应、日志、审计、持久化、telemetry 无验证码、完整邮箱、AccessKey、TemplateData、供应商原始响应 |

**数据库枚举与清理约束：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 非法枚举 | 直接写非法 provider/provider_status/scene/sync status/白名单 status/send status/purpose | MySQL 8 CHECK 拒绝 |
| missing 时间一致性 | 写 missing=1+null 或 missing=0+非 null | CHECK 拒绝；清理查询使用 missing+missing_since 索引 |
| 白名单状态一致性 | active+revoked_at 或 revoked+null | CHECK 拒绝；清理查询使用 status+revoked_at 索引 |
| 发送日志条件空值 | accepted 无 RequestId/有 failure_reason，或 failed 无 failure_reason | CHECK 拒绝 |
| 同步记录条件空值 | running 带完成时间、failed 缺错误字段 | CHECK 拒绝 |

**生产/Mock Adapter 边界与所有发送前置条件：**

| 用例 | 环境/缺失条件 | 期望结果 |
|---|---|---|
| 生产误配 Mock | APP_ENV=production 且选择 Mock | 503/51003，失败关闭，不产生 accepted 验证码 |
| 非生产显式 Mock | 非生产且显式启用 | 可用于单元/集成测试；结果明确标记 mock，不能计入真实发送验收 |
| 凭据缺失 | AccessKey/Region/发信地址任一为空 | 503/51003，失败关闭，无供应商请求 |
| 邮件资源不存在 | 路径模板或邮件资源不存在 | `404/40400「邮件资源不存在」`，Adapter 调用次数增量 0 |
| 场景无绑定 | scene 无模板 | `409/40900「邮件场景未绑定模板」`，验证码不可用，Adapter 增量 0 |
| 绑定停用 | enabled=false | `409/40900「邮件场景已停用」`，验证码不可用，Adapter 增量 0 |
| 模板本地停用 | local_enabled=false | `409/40900「邮件模板已停用」`，Adapter 增量 0 |
| 模板 draft | provider_status=draft | `409/40900「邮件模板尚未提交审核」`，Adapter 增量 0 |
| 模板 pending | provider_status=pending | `409/40900「邮件模板正在审核」`，Adapter 增量 0 |
| 模板 rejected | provider_status=rejected | `409/40900「邮件模板审核未通过」`，Adapter 增量 0 |
| 模板 missing | missing=true | `409/40900「邮件模板在供应商侧不存在」`，Adapter 增量 0 |
| 模板变量缺失 | 缺 Code 或 ExpireMinutes | `422/51001「邮件模板变量不完整」`，Adapter 增量 0 |
| bind_email 来源越权 | 服务层受控 fixture 注入非当前登录用户此次换绑流程目标 | `403/40003「无权向该邮箱发送验证码」`，Adapter 增量 0 |
| admin_verify 来源越权 | 服务层受控 fixture 注入非当前管理员已绑定邮箱 | `403/40003「无权向该邮箱发送验证码」`，Adapter 增量 0 |
| admin_verify 黑盒多余字段 | 管理员邮箱发码端点提交额外 email 字段 | `400/40000「请求参数错误」`，Adapter 增量 0 |
| 限流 | 同 IP 连续超过验证码限额 | 429/42900，超限请求不落可用验证码 |
| 账号维度限流 | 跨多个 IP 对同一规范化邮箱/同一 user_id/admin_id 超过 10 次/分钟 | `429/42900「请求频率超限」`，账号键不含完整邮箱，超限请求不外呼 |

**Redis 发布必需锁原语：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 原子加锁 | 并发执行 `SET key token NX PX ttl` | 仅一个所有者成功；同步 TTL=30秒，OTP/测试 TTL=15秒 |
| 所有权续租 | token 匹配/不匹配分别执行 Lua compare+PEXPIRE | 仅匹配者续租；间隔不超过 TTL 三分之一 |
| 所有权释放 | token 匹配/不匹配分别执行 Lua compare+DEL | 仅匹配者删除；非所有者不得删除并产生告警 |
| 外呼前丢锁 | 外呼前使锁过期或被新 token 获取 | 停止后续动作，邮件不外呼；同步镜像事务回滚 |
| 外呼中丢锁且明确响应 | Adapter 返回明确 accepted/rejected 前使锁丢失 | `WHERE id=? AND status='pending'` 条件更新恰一行，唯一收敛 accepted/failed；accepted 仅来自明确 accepted，发送日志无长期 pending；Adapter 已调用 |
| 外呼中丢锁且未知响应 | Adapter 超时/响应未知且锁丢失 | 同事务持久化 unknown failed 与 OTP failed；按旧/new key规则响应；Adapter 已调用，不断言增量0 |
| 未取得锁/外呼前 Redis 故障 | 连接、加锁失败，或外呼前续租/所有权检查失败 | `503/51003「邮件发送服务未就绪」`，Adapter 调用次数增量0，不降级数据库锁 |
| 外呼后 Redis 故障 | Adapter 已开始后续租/所有权检查失败 | 不返回503；按明确响应 fencing 或未知结果持久化墓碑收敛 |
| Redis 重启防绕过 | 持久化 unknown failed 后重启 Redis/删除锁 key，再用新 key 请求同 scope | 取得新锁后查询 DB 命中未过期 unknown failed，返回409/40900，Adapter 增量0 |
| fencing 竞争失败 | 两执行者竞争更新同一 pending 行 | 仅 `WHERE id=? AND status='pending'` 影响1行；失败者读取已有终态并原样返回，不覆盖 |
| Go 集成边界 | 审查本轮证据 | 仅协议和锁原语可通过；未完成真实 Redis 集成测试前，Go 集成保持“未验收” |

**DirectMail RAM 否定权限测试（必须用最小权限测试账号）：**

| 显式 Deny action | 被测流程 | 期望结果 |
|---|---|---|
| `dm:QueryTemplateByParam` | 模板同步 | 502/51002，同步 failed，本地镜像与 missing 不变 |
| `dm:DescTemplate` | 模板同步详情 | 502/51002，整批回滚，不保存不完整模板 |
| `dm:SingleSendMail` | 正式 OTP 与后台模板测试 | 502/51002，验证码保持不可用，模板测试不误报 accepted |

| RAM 组合 | QueryTemplateByParam | DescTemplate | SingleSendMail | Create/Modify/DeleteTemplate | 期望 |
|---|---:|---:|---:|---:|---|
| 最小 Allow 基线 | Allow | Allow | Allow | Deny | 同步与发送可进入业务流程；应用轨迹不调用写模板 action |
| 否定查询列表 | Deny | Allow | Allow | Deny | 同步 `502/51002`，镜像不变 |
| 否定查询详情 | Allow | Deny | Allow | Deny | 同步 `502/51002`，整批回滚 |
| 否定发送 | Allow | Allow | Deny | Deny | 正式/测试发送 `502/51002`，OTP 不可用且不误报 accepted |
| 越权写模板探测 | Allow | Allow | Allow | 分别直接探测均 Deny | 三个写 action 全部被拒绝，应用调用计数均为 0 |

最小权限账号的 Allow 必须严格只有上述三个 action。另直接探测 `dm:CreateTemplate`、`dm:ModifyTemplate`、
`dm:DeleteTemplate`，三者都必须被拒绝；应用运行轨迹不得调用这三个供应商写模板 action。

所有 RAM 否定用例还需断言：响应/日志不含 AccessKey、完整邮箱、验证码、供应商原始响应；审计只记录 action、
内部目标 ID、脱敏对象和归一化结果。

**公开邮件发码来源 IP Phase 1 delta（待 QA/PM 签署，十项测试矩阵）：**

以下每项均须同时断言最终来源 IP、HTTP/code/message、IP 与账号限流计数、验证码/发送记录、发送锁、邮件服务入口及 Adapter 调用增量；所有前置拒绝均不得记录完整来源 Header。

| 编号 | 用例 | 配置/请求 | 期望结果 |
|---:|---|---|---|
| 1 | 空配置直连 | `TRUSTED_PROXY_IPS` 为空，从可解析的 `RemoteAddr` 直连并伪造全部来源 Header | 仅用 `RemoteAddr`，忽略 `X-Real-IP`/XFF/Forwarded；正常进入后续限流与业务判定 |
| 2 | 合法精确 IP/CIDR | 配置带项目周围空白的 IPv4、IPv6、精确 IP 与 CIDR，分别从命中/不命中地址请求 | 每项 trim 后正确解析；仅命中项视为 trusted，且不得复用 `INTERNAL_TRUSTED_PROXY_IPS` |
| 3 | 非法配置 | 分别配置空项、非法 IP、非法 CIDR、带 IPv6 zone 的地址 | 应用启动失败或 `/api/ready` 不通过，不承载公开邮件发码；不得静默忽略坏项或回退直连模式 |
| 4 | 非 trusted 伪造 | `RemoteAddr` 不命中 trusted，分别注入允许 IP 的 `X-Real-IP`、XFF、Forwarded | 始终只用 `RemoteAddr`；所有来源 Header 不改变 IP 限流或安全结果 |
| 5 | trusted 合法单值 | `RemoteAddr` 命中 trusted，Nginx 覆盖一个合法单值 `X-Real-IP` | 使用该单值作为最终来源 IP，进入后续 IP/账号限流与业务服务 |
| 6 | trusted 缺失/空值 | trusted 请求不带 `X-Real-IP` 或只带空值 | `403/40003「无权限」`；不计数、不写验证码/发送记录、不取锁、不入服务、Adapter 增量 0 |
| 7 | trusted 非法 IP | trusted 请求携带不可解析或带 IPv6 zone 的 `X-Real-IP` | `403/40003「无权限」`，全部副作用与 Adapter 增量为 0 |
| 8 | trusted 多值 Header | trusted 请求分别携带逗号多值或两个重复 `X-Real-IP` Header | `403/40003「无权限」`，不得选择首值/末值，全部副作用与 Adapter 增量为 0 |
| 9 | XFF 与 Nginx 边界 | trusted 请求携带冲突 XFF/Forwarded；检查 Nginx 转发配置与应用判定 | Nginx 删除 XFF/Forwarded、覆盖而非追加 `X-Real-IP=$remote_addr`；应用永不读取 XFF，结果只由单值 X-Real-IP 决定 |
| 10 | 运行时解析器不可用 | 注入来源解析器不可用/无法安全判定故障 | `503/51003「邮件发送服务未就绪」`；不计数、不写记录、不取锁、不入服务、Adapter 增量 0 |

> 该矩阵只冻结 Phase 1 可执行验收口径，尚待 QA/PM 书面签署。本轮未执行 Go、Nginx、限流器、DirectMail 或 E2E，不得据此声称实现通过。

**内部邮件 Adapter 指标 Phase 1 metrics delta（待 QA/PM 复签，尚未执行实现验收）：**

| 用例 | 操作 | 期望结果 |
|---|---|---|
| 成功抓取 | 从允许 IP 发起 `GET /api/internal/metrics`，携带正确 `X-Internal-Token` | HTTP 200；Content-Type 为 Prometheus text 0.0.4；同时有 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff` |
| 方法白名单 | 分别发送 HEAD、POST、PUT、PATCH、DELETE、OPTIONS | 均为 405 且 `Allow: GET`，不返回指标正文 |
| Token 缺失或错误 | 从允许 IP 分别不带、带空值、带错误 `X-Internal-Token` | 均为 `403/40003「无权限」`，响应不可区分具体原因 |
| Token 客观强度 | 依次配置少于 32 个 UTF-8 字节、含首尾空白，以及不同大小写的 `REPLACE_WITH_INTERNAL_API_TOKEN`、`CHANGE_ME`、`CHANGEME`、`DEFAULT`、`SECRET`、`TEST` | 全部失败关闭且不得承载指标抓取；不做 trim 后接受，不降级为无 Token 或只校验 IP |
| Token 合法生成 | 用 CSPRNG 生成至少 32 个随机字节，分别 base64、hex 编码后注入 | 两种非空值均通过配置校验；配置、请求 Token 与完整 Header 不出现在任何日志、审计、指标或错误响应 |
| 常量时间原始字节比较 | 审查实现，并用不同首字符/末字符、大小写变化、首尾空白请求值重复请求 | 不 trim 请求或配置，按原始 UTF-8 字节常量时间比较；所有不匹配响应状态、code、message 与外观一致，不暴露匹配位置 |
| IP 未命中 | 携带正确 Token 从白名单外连接源 IP 请求 | `403/40003「无权限」`，不提示 IP 白名单失败 |
| IP/CIDR 配置解析 | 两个列表分别配置逗号分隔的 IPv4、IPv6、CIDR，项目周围带空白 | 每项 trim 后解析，精确 IP 与 CIDR 均可正确命中 |
| IP 配置失败关闭 | 分别令 `INTERNAL_ALLOWED_IPS` 或 `INTERNAL_TRUSTED_PROXY_IPS` 缺失、整个列表空、含空项、非法 IP 或非法 CIDR | 任一情况 metrics 均失败关闭或就绪失败；不能降级为允许全部、本机默认、忽略 trusted proxy 或只校验 Token |
| 双闸组合 | 依次测试正确/错误 Token × 允许/拒绝 IP 四种组合 | 仅正确 Token 且 IP 命中时成功；其余三种完全一致返回 `403/40003「无权限」` |
| 启动零值 | 全新进程未调用 Adapter 时抓取 | 只输出 21 个合法时间序列，值全部为 0，并包含该指标族的 HELP/TYPE |
| 封闭 operation/scene | 解析全部样本 label | `query_templates`/`describe_template` 只配 `template_sync`；`send_mail` 只配五个固定 scene；不得出现交叉非法组合 |
| 封闭 result | 触发 accepted、failed、timeout | 只出现 `accepted/failed/timeout`，对应合法序列各自恰增 1 |
| 单调计数 | 同进程连续触发多次合法 Adapter 调用并多次抓取 | 所有序列只增不减；前置拒绝与幂等重放不增加 Adapter 调用计数 |
| 重启语义 | 产生非零计数后重启进程并再次抓取 | 允许全部序列重置为 0，不要求跨重启持久化 |
| 指标族白名单 | 解析完整响应 | 除 `email_adapter_calls_total` 的 HELP/TYPE 与样本外没有任何其他指标族，包括 Go runtime、process、HTTP、数据库或 Redis 指标 |
| label 敏感扫描 | 扫描全部 label 名和值 | 无邮箱、邮箱 HMAC、OTP、用户/管理员 ID、request_id、业务请求号、RequestId、TemplateId、错误原文、IP、Token 或其他高基数/敏感值 |
| 直连伪造来源头 | 从非 trusted proxy 的拒绝 IP 直连，注入允许 IP 的 `X-Real-IP`、`X-Forwarded-For`、`Forwarded` | 应用只按 `RemoteAddr` 判定并返回 `403/40003「无权限」`；伪造头不能改变结果 |
| 非可信代理 | 请求经未列入 `INTERNAL_TRUSTED_PROXY_IPS` 的代理到达，携带正确 Token 与允许 IP 的 `X-Real-IP` | 仍只用代理的 `RemoteAddr`；若其未命中 allowed 则返回 `403/40003` |
| 可信代理单值 | `RemoteAddr` 命中 trusted proxy，代理删除 XFF/Forwarded 并覆盖一个合法、命中 allowed 的 `X-Real-IP` | 双闸通过并返回 200；应用使用该单值作为最终来源 IP |
| 可信代理头失败 | `RemoteAddr` 命中 trusted proxy，但 `X-Real-IP` 缺失、空值、非法 IP、逗号多值或重复多 Header | 全部返回 `403/40003「无权限」`，不回退到代理 `RemoteAddr` 或 XFF |
| 应用不读 XFF | trusted proxy 请求分别只带 XFF、带冲突 XFF 与合法 X-Real-IP | 只带 XFF 时 403；有合法单值 X-Real-IP 时结果只由其决定，XFF 内容完全无效 |
| 反代网络边界 | 从非监控网络与监控网络分别请求反代路径，并检查转发头 | 仅监控网络可到达；反代删除 XFF/Forwarded、覆盖而非追加单值 `X-Real-IP`；应用仍执行 Token+来源 IP 双闸 |

> 上表已补齐 QA 阻断对应的可执行验收口径，尚待 QA/PM 复签。本轮未执行 Go、反向代理、监控系统或 E2E 验证，不得据此声称指标端点已实现或通过。

**审计、告警、Adapter 计数与敏感扫描：**

| 用例 | 观测点 | 期望结果 |
|---|---|---|
| Adapter 首次调用 | `email_adapter_calls_total{operation,scene,result}` | 首次真实调用恰增 1；label 仅含固定枚举，不含邮箱、RequestId 或错误原文 |
| 前置拒绝/幂等重放 | 调用前后指标快照 | Adapter 调用次数增量 0 |
| attempt 审计失败 | audit_logs 写入故障 | 动作不执行并告警 |
| result 审计失败 | 动作已生效后注入故障 | 成功响应不反转为 500，同时产生 warning 告警 |
| 锁所有权异常 | 续租/释放 token 不匹配 | 产生 critical 告警，字段仅含 request_id/action/scene/内部ID/result/error_code/occurred_at |
| 失败率告警 | 同 scene 5分钟至少10次且 failed 比例超过20% | 产生 critical 告警，不包含敏感字段 |
| 敏感扫描 | HTTP 响应、stdout/stderr/集中日志、audit_logs、邮件表与 verification_codes、指标/trace/event、前端 console/埋点/持久缓存 | 无完整邮箱、OTP、AccessKey、TemplateData、锁 token 或供应商原始响应；合法请求体仅在受控内存短暂存在 |

### 3.2 实名认证

| 用例 | 接口 | 期望结果 |
|---|---|---|
| 提交实名认证 | POST /api/identity/verifications | 200，status=pending |
| 重复提交 | POST /api/identity/verifications | 400，审核中不可重复提交 |
| 未实名购买商品 | POST /api/products/:id/purchase | 400，code=70001 |
| 审核通过 | PATCH /api/admin/identity-verifications/:id/review | 200，用户 real_name_status=verified |
| 审核拒绝 | PATCH /api/admin/identity-verifications/:id/review | 200，用户可重新提交 |

### 3.3 商品与购买

| 用例 | 接口 | 期望结果 |
|---|---|---|
| 用户查看商品列表 | GET /api/products | 只返回该用户角色 can_view=true 的商品 |
| 普通用户买普通应用 | POST /api/products/:id/purchase | 200，扣费+生成资产 |
| VIP 用户买有角色价商品 | POST /api/products/:id/purchase | 按角色价扣费 |
| 会员用户买会员价商品 | POST /api/products/:id/purchase | 按会员价扣费 |
| 余额不足 | POST /api/products/:id/purchase | 400，code=60001 |
| 无购买权限 | POST /api/products/:id/purchase | 403，code=40003 |
| 重复购买（同 Idempotency-Key）| POST /api/products/:id/purchase | 200，返回原订单（不重复扣费） |
| 缺少 Idempotency-Key | POST /api/products/:id/purchase | 400，code=40000 |

### 3.4 钱包与充值

| 用例 | 接口 | 期望结果 |
|---|---|---|
| 查看余额 | GET /api/wallet | 返回当前余额 |
| 创建充值订单 | POST /api/recharge/orders | 200，返回 pay_url |
| 支付回调处理 | POST /api/payments/notify/wechat | 200，钱包余额增加 |
| 重复回调 | POST /api/payments/notify/wechat | 200（幂等），余额不重复增加 |
| 签名错误的回调 | POST /api/payments/notify/wechat | 400，余额不变 |

### 3.5 权限控制

| 用例 | 期望结果 |
|---|---|
| 无 token 访问需要登录的接口 | 401，code=40001 |
| 普通用户访问管理员接口 | 403，code=40003 |
| 管理员给用户添加 deny 权限后，用户无法访问对应接口 | 403 |
| 管理员给用户移除 deny 权限后，用户恢复访问 | 200 |
| 修改角色权限后，缓存失效，新权限立即生效 | 修改后立即生效，不需等 5 分钟 |
| 封禁用户后其 Token 立即失效 | 401 |

## 4. 并发与安全测试

### 4.1 并发扣费测试（必须通过）

```text
场景：用户余额 100 元，同时发起 10 个并发请求各扣 20 元
期望：只有 5 个请求成功，剩余 5 个返回余额不足（60001）
方法：使用 wrk 或 ab 工具，或 Go 并发测试
```

```go
// server/internal/modules/billing/service/wallet_service_test.go
func TestConcurrentDeduct(t *testing.T) {
    // 初始化余额 100
    // 10 个 goroutine 同时扣 20
    // 断言：成功次数 = 5，最终余额 = 0，无负数
}
```

### 4.2 幂等性测试

```text
场景：同一 Idempotency-Key 并发发送 5 次购买请求
期望：只生成 1 个订单，只扣费 1 次，只生成 1 个资产
```

### 4.3 权限绕过测试

```text
场景 1：伪造 JWT（修改 payload 后不更新签名），访问需要登录的接口
期望：401

场景 2：使用普通用户 Token 请求 /api/admin/* 接口
期望：403

场景 3：修改 URL 中的 :id 访问他人资产（如 GET /api/my/assets/999）
期望：404 或 403（不能看到他人数据）
```

## 5. 每周验收 Checklist

### Week 1–2 验收标准

```text
□ 管理员可以用邮箱登录管理后台
□ 管理员可以创建角色（platform_admin / normal_user）
□ 管理员可以创建普通用户并分配角色
□ 用户可以用邮箱注册（验证码 → 注册 → 登录）
□ 用户可以用手机号注册
□ 用户可以提交实名认证
□ 管理员可以审核通过实名认证
□ 未实名用户购买商品返回 70001
□ 退出登录后原 Token 不可用
```

### Week 3 验收标准（核心购买闭环）

```text
□ 管理员后台创建商品 → 配置套餐 → 配置价格 → 配置角色权限
□ 用户控制台看到可购买商品
□ 用户余额充值（支付回调模拟）
□ 用户购买商品 → 扣费 → 生成订单 → 生成资产
□ 用户在「我的资产」看到已购买资产
□ 管理员后台看到订单记录
□ 管理员后台看到钱包流水
□ 同一请求重复发送不重复扣费（幂等测试）
□ 余额不足时返回正确错误码
□ 10 并发扣费不出现负余额
```

### Week 4 验收标准

```text
□ 会员用户购买商品按会员价扣费
□ 会员专属商品非会员用户不可购买
□ 管理员发布公告，用户端可见
□ 管理员创建帮助文档，用户端可搜索
□ 帮助文档按可见范围正确过滤
```

## 6. 测试环境数据初始化

测试开始前需要初始化以下基础数据：

```sql
-- 初始化角色
INSERT INTO roles (code, name) VALUES
  ('platform_admin', '平台管理员'),
  ('finance_admin',  '财务管理员'),
  ('ops_admin',      '运维管理员'),
  ('normal_user',    '普通用户'),
  ('vip_user',       'VIP 用户');

-- 初始化平台管理员账号（密码：Admin@123456，bcrypt hash 替换）
INSERT INTO users (email, email_verified, password_hash, real_name_status, status)
  VALUES ('admin@molin.io', 1, '$2a$10$...', 'verified', 'active');

-- 给管理员分配角色
INSERT INTO user_roles (user_id, role_id)
  VALUES (1, (SELECT id FROM roles WHERE code = 'platform_admin'));
```

测试数据初始化脚本：`scripts/seed_test_data.sh`（运维负责创建）。

## 7. 缺陷管理

- 缺陷在 Git Issues 中跟踪。
- 优先级：P0（生产阻断）/ P1（核心功能缺陷）/ P2（一般缺陷）/ P3（体验问题）。
- P0、P1 缺陷必须在下一个迭代前修复，不得上线。
- 每个缺陷 Issue 必须包含：复现步骤、期望结果、实际结果、截图或日志。
