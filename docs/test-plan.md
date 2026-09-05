# 测试计划

VID-G7新增[执行租约专项](video-gateway-vid-g7-worker-lease-contract.md)：租约基础、事务回滚、数值边界与输入保护已由124项组合race覆盖。自动10秒心跳、异常退出和到期接管使用独立heartbeat筛选补验；组件测试不能替代完整运行时、多进程kill、Redis/MinIO和完整G7验收。

后续回执首尾围栏由15项Linux race（含全部13项原G5提交测试）及同源31项G7/Broker组合验证。`-Focus receipt`必须实际发现旧测试且全部RUN/PASS，零匹配和SKIP均失败；真实30秒尾部到期与五秒context超时须分别断言，不得混用。

普通财务围栏使用`-Focus financial_fence -LinuxRace`：验证结算/退款的missing/stale拒绝与合法重放、独立补偿授权，以及主事务/新补记事务的四个并行到期子例。`-FinanceRegression`仍要求发现并完整执行原99项G5，不能用新四项专项替代；并行子例须单列耗时，顶层耗时不表示总等待时间。

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

**邮箱验证码登录 Phase 1 delta（历史标题；QA/PM 后续已签署，当前状态以邮件整体验收文档为准）：**

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

> 历史说明（已被后续证据取代）：上表形成时 QA 与产品经理尚未在设计评审文档签署，且未执行 Go、数据库、Redis、DirectMail、前端或 E2E 验证。后续 Phase 1 delta 已签署，部分实现与环境证据也已形成；但该表本身仍不能用于声称 Phase 4 或整体功能通过。

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

### 3.1.1 DirectMail 邮件模板管理与 OTP 发送（历史 Phase 1 契约门禁；当前 Phase 4 已附负责人豁免通过）

> Phase 0 仅核对外部资质、模板与 RAM 准备证据；Phase 1 冻结可执行契约，并以 `docs/aliyun-directmail-email-template-phase1-design-review.md §15` 的 QA/PM 书面记录为唯一出口；Phase 2 才验收 Go、migration、真实 MySQL/Redis、DirectMail、前端与 E2E。既有实现、MySQL 和真实发送材料全部是 Phase 2 待复验输入，不能倒置 Phase 1 门禁。本轮只确认协议与 Redis 锁原语，Go 集成未验收。

> 状态收口（2026-08-02）：000055/000056 独立 MySQL 8 隔离矩阵、000057 技术可逆周期、Redis unknown fresh cycle、真实 Redis lease、真实四角色三宽度、已部署前端受控错误态、运行时六表面扫描和凭据回收均已关闭，禁止重复执行已标记的高风险门禁。RAM 有效权限、五场景真实重放/过期、模板测试发送真实故障矩阵和五业务流真实外发 E2E 均由项目负责人登记为 `waived_by_project_owner_not_verified`，不属于技术 PASS。QA/PM 已附负责人豁免通过，Phase 4 状态为 `passed_with_project_owner_waivers`；该结论不批准 Phase 5 或生产上线。权威状态见 `docs/aliyun-directmail-email-template-feature-acceptance.md §17.2.6`。

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
| 000055 partial-up/down 独立执行资产 | 本地先运行 `tests/email/run_000055_container_partial_matrix_contract.py`；真实执行需独立双门禁，并依次执行 Up 16 点、Down 15 点及各一条无注入基线 | 当前 Up/Down SHA 与 31 行边界清单 SHA 均冻结；schema54、schema55 与两行基线清单还必须通过三个独立外部 SHA 环境门禁，文件、清单和外部冻结值三方一致。基线先拒绝 MySQL 可执行注释/优化器提示，再用 SQL 感知扫描正确处理转义、字符串、普通注释和未闭合输入，最后检查跨 schema 限定名，并拒绝库级 DDL、账号授权和全局配置；normal/`-O`、Bash、默认关闭、单门禁及 27 个攻击模型必须通过。每用例仅从 root:400 的完整基线恢复到运行时 UUID 新隔离库，共保留 33 个目标与 600 证据。Up 覆盖五业务表、ownership 表及1..4行：执行器抽取冻结第27条语句，在唯一终止分号前注入 `ORDER BY spec.code LIMIT 1..4`，并逐点断言精确 permission code 集合及创建标志；同时覆盖权限/绑定补缺与ID回填、两类强断言。Down 覆盖验证码失效/断言、绑定/权限精确删除、写后断言、ownership、五业务表逆序和 verification 四步清理。每点必须核验 information_schema、ownership、权限/绑定及 verification 状态，禁止只看退出码。基础 runner 继续输出 `not_implemented`，只有本独立资产真实执行后才形成组合证据；当前仅离线 PASS，不关闭真实 MySQL 门禁 |
| 000055 partial 最新离线证据 | 只运行独立 partial 资产的静态契约、自检和攻击模型，不连接 MySQL | Up 16、Down 15、基线 2，共 33 个目标；runner SHA `E9EC4C1F...EBA9C9`，boundary SHA `4B5E02DC...18585`，攻击模型 32 项通过。环境预检不再依赖镜像未承诺的 `/usr/bin/wc`，改由既有 awk 精确核验两行 manifest；真实矩阵仍待复验，不得将 000055 partial 改记为已验收 |
| 结构统计口径（Phase 2 待复验历史输入） | 查询 CHECK、外键及 `(table_name,index_name)` | 历史统计仅作复验基线；本轮不确认当前结构通过 |
| 000020 历史兼容修复（Phase 2 待复验历史输入） | 使用真实golang-migrate在MySQL 8.4.10执行空库1→55、19→20→19→20、v20→55不重放、55→54→55；对测试服务器MySQL 8.0.46只读审计 | 历史材料待 Phase 2 重跑，不代表本轮通过 |
| 000055 当前静态契约 | 本地以 normal 与 Python `-O` 两种模式执行 `tests/email/migration_000055_contract.py`，不连接远程或数据库 | 当前 Up SHA `7238522C...1FA3D`、Down SHA `217B8FD...C26EE`；两种模式均 `PASS checks=2161 mutations=16`，并显式拒绝 MySQL executable comment 与 optimizer hint。历史真实执行材料缺少执行时 SHA 绑定，只能间接对应当前资产，不得替代新库、旧库、partial 和 down 的真实 MySQL 门禁 |
| 000055 当前 SHA 隔离矩阵控制器 | 本地执行 `tests/email/run_000055_container_isolation_matrix_contract.py`；真实控制器仅允许在受控 MySQL 容器内以参数和环境双门禁启动 | 本轮 runner SHA 为 `A656E9...A468D`；normal/`-O`、默认关闭、自检、单门禁和 17 个攻击模型通过，QA 复核 P1/P2=0。冻结当前 Up/Down SHA，并分别拒绝硬编码旧隔离库字面量、通过 `mysql_admin` 访问旧库、主库选择、固定目标、完整随机库名输出、option file、schema 删除和基线控制语句。本轮未执行真实 MySQL，partial 固定为 `not_implemented`；离线 PASS 不得记为完整 000055 真实隔离验收 |
| 000056 当前 SHA 隔离矩阵控制器 | 本地执行 `tests/email/run_000056_container_isolation_matrix_contract.py`；真实控制器只允许在受控 MySQL 容器内以参数和环境双门禁启动 | 本轮 runner SHA 为 `D86CA32E...85308`、契约 SHA 为 `BB0A4E9D...2ADA`；normal/`-O`、`bash -n`、SelfTest、默认关闭、单门禁关闭及 20 个攻击模型通过。basic 与 partial 统一使用 schema55/schema56 两行 manifest 并逐项绑定文件 SHA，关闭原同名 manifest 一行/两行互斥阻断。冻结 Up `BC900F...C735`、Down `F42A30...A5C2`；本轮未执行真实 MySQL，partial 固定为 `not_implemented`，离线 PASS 不得记为完整 000056 真实隔离验收 |
| 000056 partial-up/down 独立执行资产 | 本地执行 `tests/email/run_000056_container_partial_matrix_contract.py`；真实执行另需双门禁 | 当前 SQL 实际为 Up 27 条、Down 14 条，逐语句形成 27/14 partial 点，另有 Up/Down 无注入基线各一，共保留 43 个运行时 UUID 新隔离库；不得人为补点。Up 覆盖断言表、14 条前置断言、receipt、ownership 捕获、权限/绑定补缺与两类 ID 回填、4 条写后断言和断言表删除；Down 覆盖断言表、6 条删除前断言、绑定/权限精确删除、2 条删除后断言及三表逆序删除。边界名称、顺序、语句编号和状态共同冻结。schema55、schema56、两行 manifest 使用三个外部 SHA 与文件/清单三方绑定；SQL 感知扫描拒绝可执行注释、优化器提示、跨 schema 限定名和未闭合输入。每点核验 assertion 行、receipt 9列/5索引/2外键/5 CHECK 且0行、ownership flags/ID、bootstrap 权限和唯一 admin 绑定；normal/`-O`、Bash、默认关闭、单门禁及32个攻击模型必须通过。基础 runner 保持 `not_implemented`；当前仅离线，不关闭真实 MySQL 门禁 |
| 000055/000056 六项基线生成器 | 本地仅执行 `email_migration_baseline_generator_contract.py` normal/`-O` 与脚本 `--self-test`；真实生成需新的专项授权 | 冻结 000001→000056 共 56 个 Up migration 集合 SHA `8EB07701...1A82`；只允许本地已存在且 digest/镜像 ID 双绑定的 MySQL 8 镜像，禁止 pull，容器 `network=none`、无端口、只读根文件系统，并在容器内实际执行 `mysql --version` 证明主版本为 8。依次生成 schema54 empty/legacy、schema55、schema56 和两份 manifest；输出目录必须预存为空且 700，发布使用 noclobber，失败仅清理本轮固定输出和精确容器 ID。生成器 SHA `FBAD0D66...EB6F0`、契约 SHA `E967BE59...6B011`，20 个攻击模型通过；本轮 `docker_access=false`、`database_access=false`、`migration_executed=false`、`outputs_created=false` |
| 000055/000056 单次全隔离矩阵编排 | 本地执行 `email_migration_full_isolation_matrix_contract.py` normal/`-O` 及 PowerShell `-SelfTest`；真实模式需独立专项授权 | 控制器冻结 66 个源文件，其中 56 个 Up migration 集合 SHA 为 `8EB07701...1A82`；打包、自校验和 UTF-8 stdin 传输均在本地完成。远端成功路径固定两次 SSH、一次 `scp -O`，只读绑定测试环境 `molin-mysql` 的本地镜像 digest/ID，不连接其数据库；随后顺序创建两个不同时存在的 `network=none`、无端口、只读根文件系统临时 MySQL 8 容器，先生成六项基线，再执行 000055 full 7、partial 33、000056 full 11、partial 43，共 94 个 UUID 隔离目标。全部通过后删除精确临时容器和唯一 Stage；失败删除精确临时容器但保留 Stage，零重试。payload/controller/contract SHA 为 `4FC51DB9...FA401`、`1E002BAA...76A91`、`7769FD36...CE658`，normal/`-O` 32 个攻击模型、PS5 SelfTest 和默认关闭通过；当前未授权、未联网、未访问 Docker、未生成基线、未执行 migration |
| 000056 partial 最新离线证据 | 只运行独立 partial 资产的静态契约、自检和攻击模型，不连接 MySQL | Up 27、Down 14、基线 2，共 43 个目标；runner SHA `1BDAF145...3BFB1`，boundary SHA `7B9E3132...BA4A62`，contract SHA `407456C5...EBFA`，`attack_cases=37`。与 000055 partial 同步移除 `/usr/bin/wc` 绝对路径依赖并保持两行 manifest 精确门禁；尚未真实执行，不得登记通过 |
| 本地隔离矩阵打包资产 | 仅运行 manifest、PowerShell 打包器与 Python contract 的离线自检，不生成持久包 | manifest SHA `231B37EA...D004`、PS1 SHA `F1DBC34E...D5A3`、contract SHA `CA2E2E6C...AB60`；manifest 20 项由 14 项内部资产和 6 项外部基线占位组成，tar 固定 15 项。SelfTest、AST、默认关闭、normal/`-O` 通过，`attack_cases=13`、`output_preservation_cases=4`、`symlink_checks=true`，QA P1/P2=0。P1“预存输出被删除”已通过 `CreateNew` 与 owned flags 修复，只允许处理本轮创建且归属明确的输出。本轮未生成持久包、未上传、未外连、未访问数据库、未执行 migration；真实 MySQL 仍未执行，Phase 4 仍未通过 |
| 55→56 部署门禁 | 按全停机流程从 54/dirty=0 发布 | 停止邮箱/手机 OTP 发码、OTP 校验、注册、登录流量→等待 10 分钟→停止全部 auth/API 实例→备份并验证可恢复→000055 up；必须先断言 version=55/dirty=0、五业务表、000055 ownership、五场景、四权限及 admin 绑定完整，再执行 000056 up；随后断言 version=56/dirty=0、bootstrap 权限/admin 绑定、000056 ownership 与空 receipt 表完整，才可部署新应用；禁止滚动部署和新旧共存 |
| 56→57 UTC 秒级结构门禁 | 在停机维护窗核对 000055/000056 结构后执行 000057 up | 精确断言旧结构与专用备份表不存在；创建 manifest 后仅备份非零小数秒行，证明 expected_count、主键、原值、秒值及非时间指纹完整；只更新这些行的 created_at 到秒并复验，再修改三列。0/1/多行均成功且 version=57/dirty=0，备份表保留供 down |
| 000057 离线资产 | 不连接数据库，静态读取 up/down SQL | 严格白名单仅新增专用备份表的精确 CREATE/INSERT/UPDATE/DROP；去注释规范化后的 up 16 条、down 15 条完整语句与顺序冻结 SHA-256，当前 down 文件 SHA-256 为 `EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB`。每个语句边界及每个断言均覆盖删除、替换、注释伪装和重排故障注入；模型覆盖0/1/多行归一恢复、备份缺行、源回执缺失和孤儿备份失败。四道关键证据门禁必须由备份反向 LEFT JOIN receipt，校验 manifest、expected_count、COUNT(r.id) 与 r.id IS NULL；down 删除备份必须位于最终门禁之后。结构攻击覆盖额外列、列顺序/类型/unsigned/可空/默认值/extra/排序规则、引擎、表排序规则、主键和三项 CHECK 名称/表达式；另要求 statistics 派生表投影 non_unique，三项 CHECK 比较仅窄化规范反斜杠单引号，并拒绝全量删除反斜杠。字符集 introducer 只允许显式移除 `_utf8mb4` 与 MySQL 8.0.46 实际返回的 `_latin1`；fixture 必须覆盖三个实际 `_latin1` clause、既有 `_utf8mb4`、转义引号、白名单外前缀保留和 non_unique。该证据仅证明资产结构与模型，不证明 MySQL 方言或运行时语义 |
| 000057 隔离执行资产 | 不连接数据库，静态检查 `run-000057-container-backup-restore-cycle.sh` | 普通 Python 与 `PYTHONOPTIMIZE=1` 优化模式必须真实执行相同的显式异常校验，且 `bash -n` 通过；全部 `mysql`/`mysqldump` 调用以 `--no-defaults` 为客户端首参数。源库只读证据同时覆盖 helper、直接客户端和管理查询中的 SQL，固定门禁为 schema57/dirty0，并拒绝任何非 `SELECT` 源库调用。使用单事务 dump 恢复到运行时唯一的新隔离库，再按 Down→Up→Down→Up 验证两次毫秒原值恢复、两次秒级归一、三列结构、备份表、receipt 行数、非时间指纹及最终 schema57/dirty0。一次系统 UUID 同时派生随机隔离库和独立运行目录；额外恢复库前缀、旧库语义引用、完整旧隔离库名及随机名称输出均被拒绝。MySQL 失败只返回安全分类、退出码和 stderr 字节长度，不输出原始错误、凭据或业务内容。拒绝 DROP DATABASE/SCHEMA、目录清理、强制修复和源库写 SQL；离线故障注入覆盖 12 类关键门禁破坏。Up SHA 为 `50DCD97A45D8ADCF2F7CAC316B44D942DDB880D4F922B8872CAA34BA01CFC67C`，Down SHA 为 `EE05D166EB874D34A14A0D12FC17EE083CAC28DAFEEAC3772A8C14A4945495BB`，脚本 SHA 为 `D3A4B8A318D101640BFC130A482ECE423D61B63F63DC36DF6E89D497A7AF83A6`；该检查不证明真实数据库周期已执行 |
| 000057 真实隔离周期 | 测试主库只读门禁通过后，在运行时唯一新隔离库执行 Down→Up→Down→Up | 2026-07-30 技术周期实际成功两次：执行前主库 57/0、69 张 InnoDB 表、无 DDL/锁，隔离库 2；两个新目标最终均为 57/0、69 表、backup 表 1、receipt 精度 0、marker 600，稳定摘要均为 `D41910...B237A`。两次 dump 摘要分别为 `D6696C...B479E`、`9E1242...A6DBC`，后者与对应操作员输出一致；后验隔离库 4、主库仍 57/0、周期进程 0、health/ready 200。技术门禁通过；“授权一次、实际执行两次”作为正式操作偏差登记。用户确认两个新隔离库和证据冻结保留至 Phase 4 验收结束，不清理、复用或再执行 |
| 000057 额外资产保留与后续处置 | Phase 4 验收结束前冻结保留本次两个新隔离库与运行证据 | 保留资产必须从 Redis unknown 墓碑测试的准备、恢复点及 cleanup 全部目标中排除，不构成其只读准备前置。Phase 4 结束后如需清理，必须重新取得独立破坏性操作授权；从受保护证据只读解析精确目标，逐一核对来源、57/0、69 表、marker 和 dump 摘要，数据库和运行目录分别列出并分别确认。禁止记录完整随机库名，禁止前缀、通配符、模式或模糊删除，禁止触碰原有两个隔离库和测试主库 |
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
| 000056 当前静态契约 | 本地以 normal 与 Python `-O` 两种模式执行当前迁移静态契约，不连接远程或数据库 | 两种模式均 `PASS 92`；当前 Up SHA `BC900F...C735`、Down SHA `F42A30...A5C2`。四个真实数据库门禁仍为 SKIP，不得以静态 PASS 替代隔离 MySQL 运行证据 |

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
| Redis unknown 远程只读准备 | 单次 SSH 核对 API、容器、主库、UTC、既有状态、unknown 日志、模板/白名单、Redis 只读状态及 000057 排除边界 | 2026-07-30 本次仅执行一次 SSH、未重试且无写入/重启/清理；API 单实例 health/ready 200、MySQL/Redis 唯一、主库57/0、UTC偏差≤5秒、旧状态文件600安全、两条owned unknown日志同scope、模板/白名单各1、Redis只读命令及两个000057证据目录元数据通过。但 `cycle_exclusion` 聚合失败、最终摘要未输出，两个隔离schema存在及排除状态、精确phase、run_id变化、锁key EXISTS和孤儿目录数量未确认；总门禁未通过。PowerShell stdin UTF-8 BOM 导致首行 `set -u` 未生效，当前runner不得冻结。须新授权修复BOM和只读聚合后仅重跑只读对账，不进入phase1、Redis重启或phase2 |
| Redis unknown 替代只读运行器 | 本地执行 PowerShell SelfTest、Python 静态契约和攻击模型，不启动 SSH | Windows PowerShell 5.1 先把无 BOM payload 写入本轮权限受限临时文件，再由模块限定 `Start-Process -RedirectStandardInput` 把文件句柄交给固定 SSH；禁止使用默认 StandardInput StreamWriter/BaseStream，stdout/stderr 使用独立临时文件，120 秒超时只终止精确子进程，finally 精确清理。远端 `set -Eeuo pipefail` 后使用 `shopt -qo` 自检，不依赖 `set -o` 的 locale/文本布局，不使用嵌套 `sh -c`；失败路径先耗尽剩余 stdin 后再输出固定摘要。仅允许 GET health/ready 且状态必须精确为 200、MySQL SELECT、Redis PING/INFO/精确 EXISTS、受限 stat/find；历史状态文件和孤儿目录按冻结的 32hex 路径精确纳入，近似名排除；000057 dump 在 schema 查询前显式拒绝符号链接；线上测试 API 必须 `APP_ENV=test` 且已退出 Mock，成功摘要固定 `live_adapter_mock=false`，独立 phase1/phase2 测试进程仍须显式 Mock，持久化模板和发送日志 provider 固定为 `aliyun_directmail` 并核验 provider TemplateId、审核和变量兼容状态；两个完成证据目标必须是互异 32hex、非主库且不进入状态或 Redis 目标。隔离库存在性使用容器内 root 固定 COUNT SELECT 通道，不增加 GRANT、不把 root 凭据传出容器；应用账号不可见但 root 可见 2/2 属于预期权限模型。失败正则以命名组提取 stage，并使用 `\\r?\\n?\\z` 绝对结尾；只有完整白名单单行、stderr 为空且分类为 `remote_gate_failed` 时安全 JSON 才带 stage；未知 stage、额外行、双 LF、双 CRLF 或任意 stderr 均不带 stage。`email_unknown_remote_readonly_gate_contract.py` 已修复旧 `cases=18` 摘要兼容问题，normal 与 Python `-O` 两种模式均通过，固定为 `attack_cases=43`；配套 PowerShell SelfTest 通过，并固定声明 `external_access=false`、`database_access=false`、`redis_access=false`、`ssh_started=false`。真实 Windows 重定向探针继续断言子进程收到首字节 115、无 BOM、早期失败 stderr 为空；文件元数据攻击模型拒绝中文、英文及其他 locale 类型文本。本地结果只证明替代资产，不代表真实 cleanup 或新 Redis 周期通过 |
| Redis unknown 正式只读准备 | 严格一次且不重试执行冻结远程只读门禁 | PASS：API 1，health/ready true，live adapter 非 Mock；MySQL/Redis 各 1，schema57/dirty0、时钟通过；state safe 且 phase1_created；primary/unexpected/scope/template/allowlist=1/1/2/1/1；Redis PING、run_id_changed，lock_exists=0；orphan=0；cycle evidence/valid/schema/excluded=2/2/2/2；writes/restart/cleanup=false。下一步仅在人工授权后精确 cleanup 历史夹具，不得清理或修改 000057 隔离资产 |
| Redis unknown 历史夹具 cleanup 编排状态 | 获得专项授权后按 `metadata→cleanup→postcheck` 严格顺序执行，任一阶段失败不得继续 | 历史 metadata 失败及本地 runner 修复记录保留。本轮最新授权执行在 metadata 阶段以 `metadata_exit_nonzero` 安全失败，`cleanup_started=false`、`postcheck_started=false`，且未重试；因此未形成真实 cleanup 通过证据，也不得启动后续新 Redis 周期 |
| Redis unknown recovery 只读预检复核 | cleanup 授权失败后，仅以 recovery preflight diagnostic 重新核对历史夹具状态，不执行清理或复用原授权 | 本地 SelfTest `cases=34`；远程严格单次 SSH 成功。结果为 schema57/dirty0、`migration_rows=1`，两条日志、一条白名单和一条模板均存在，全部字段归属与摘要匹配；`writes=false`、`backup=false`、`cleanup=false`、`restarts=false`、`retries=0`。前次 `metadata_exit_nonzero` 未证明状态失效，当前只读门禁已重新通过；原 cleanup 授权已失败且不得重试，cleanup/postcheck 仍未完成，Phase 4 仍未通过 |
| Redis unknown cleanup metadata 根因与修复 | 保留修复前正式执行记录，仅用远端只读诊断定位 metadata 失败原因；不得自动重试 cleanup | 修复前正式流程唯一一次在 `metadata_exit_nonzero` 停止，cleanup/postcheck 均未启动。随后一次远端只读诊断 PASS：`mysql_identity_count=1`、`mysql_compose_label_count=0`，state/recovery/binary/cycle/snapshot 门禁全部通过，证明根因为正式 metadata 误依赖 Compose label。运维改为用 `ID|Image|Name` 唯一识别，修复脚本 SHA `A9DC...E073`；本地 AST=0、SelfTest=33、LocalPreflight=9、两个 payload `bash -n` 通过，QA P1/P2=0。真实 cleanup 仍未执行，须取得新专项授权后才能依次执行 cleanup 和独立 postcheck；Phase 4 仍未通过 |
| Redis unknown cleanup 与独立 postcheck 最新结果 | 新专项流程严格执行一次；cleanup 成功后只启动一次独立只读 postcheck，任一失败均不重试 | metadata 与 cleanup 严格摘要均通过；两条日志、一条白名单和一条模板已精确清理，状态文件已移除，恢复点和两套 000057 资产保留，Redis 键未删除。独立只读 postcheck 随后失败，外层仅留下 `stage=postcheck classification=postcheck_failed`。纯本地诊断确认外层 `catch` 吞掉子 runner 白名单分类并已最小修复：只有固定 JSON、退出码、stderr、分类和远端 stage 全部合约匹配才传播；授权流程 SelfTest 36/36、postcheck SelfTest 56/56、LocalPreflight 9/9、AST、`bash -n` 和 `git diff --check` 均通过，且无外部访问。原失败的真实根因因原始摘要已清理而仍未知；当前只允许在新授权下单次执行独立只读 postcheck，严禁再次 cleanup |
| Redis unknown postcheck-only 最新入口结果 | 用户新授权后，仅从正式入口执行一次 postcheck-only；入口失败不得重试或复用授权 | Windows PowerShell 5.1 空数组折叠导致 SSH 前 `local_gate_failed`；`metadata_ssh=0`、`postcheck=0`、`retries=0`，未访问远端，未执行 metadata、cleanup 或真实 postcheck。授权已消费且不得复用。共享 `Initialize-RunFiles` 已修复根因，新 runner SHA `9F524238...BE29`；本地自检 24/24、预检 3/3，QA P1/P2=0。真实 postcheck 仍未执行，须取得新的单次只读授权；历史 cleanup 保持通过且严禁再次执行 |
| Redis unknown 第二次 postcheck-only 结果 | 再次取得新授权后，仅执行 postcheck-only；失败不得重试或复用授权 | `metadata_ssh=1`、`postcheck_child=1`、`retries=0`，结果 `postcheck_failed`，cleanup 未调用。纯本地确认 PowerShell `-File` 把两个数组参数按 `hash1 hash2` 位置展开并触发 `PositionalParameterNotFound`，已修复为两个命名 scalar 参数。三个 SelfTest 为 56/56、25/25、37/37，preflight 为 3/3、9/9，QA P1/P2=0；新 runner SHA `5E69D2E6...5C85`。授权已消费；真实 postcheck 仍未完成，须取得新的单次只读授权，严禁再次 cleanup |
| Redis unknown 第三次 postcheck-only 结果 | 新授权下再次执行 postcheck-only；失败不得重试或复用授权 | `metadata_ssh=1`、`postcheck=1`、`retries=0`、`cleanup=0`，精确失败 `recovery_gate`。metadata 静态差异为漏检 `/home/pc` 与 `/home/pc/molin` 父链 owner、符号链接及 group/other writable，已把相同门禁前移对齐且未放宽。SelfTest 29/29、Preflight 3/3，QA P1/P2=0；新 runner SHA `BE0217D5...17C9`。远端具体不满足层级未知；下一步只能新授权单次执行只读父链诊断，不得直接重试 postcheck。任何 `chmod`/`chown` 必须另行授权；严禁再次 cleanup |
| Redis unknown identity diagnostic 与 postcheck-only 最终结果 | 修复 recovery trailer parser 后，各严格执行一次，不重试、不调用 cleanup | identity diagnostic 返回 `parser_pass=true`、`classification=pass`、`candidate_unique=true`、`file_identity=true`；独立 postcheck-only 返回 `status=pass`、`stage=complete`、`metadata_ssh_attempts=1`、`postcheck_calls=1`、`retries=0`。parser 严格支持 variable-width、2..8 个空格及数值范围白名单，未放宽身份或父链门禁；本地 postcheck 58/58、postcheck-only 29/29、identity 70/70，QA P1=0/P2=0。历史 cleanup 的 2 日志+1 白名单+1 模板精确删除已核验且不得重跑。该结果不关闭新 Redis 周期、RAM 有效权限或最终 QA/PM；`accepted` 只表示供应商受理，不等于人工收件或最终送达 |
| 本轮离线总回归及后续真实门禁 | 本地静态契约、攻击模型、内存模型、auth 测试和敏感扫描不连接外部环境；获批的独立高风险门禁另行按单次边界执行 | `go test ./...` 与 `go vet ./...` 通过；运行时六表面扫描正式关闭。Redis unknown fresh cycle 与 000055/000056 的 94 目标独立 MySQL 8 full/partial/down 矩阵均已真实通过并标记禁止重复。RAM 有效权限、五场景真实重放/过期、模板测试发送真实故障矩阵和五业务流真实外发 E2E 由项目负责人豁免但未独立验证；QA/PM 已附负责人豁免通过，Phase 4 已关闭 |
| Redis 最新只读身份预检 | 只读取 API 进程地址配置、health/ready、容器列表、Redis PING 与两端 `INFO server`，不输出地址或 run_id | API 1、health/ready 200、MySQL/Redis 容器各 1、PING 通过、Bootstrap 全方法 404；API 配置目标与唯一 `molin-redis` 容器的 run_id 相同。该结果证明重启目标身份唯一，也证明重启会影响当前 API；未取得专项重启及 phase1 数据库夹具写入授权前不得继续，不构成重启周期通过证据 |
| 000055/000056 最新执行输入预检 | 运行四套 runner normal/`-O` 契约、打包器 SelfTest 与 normal/`-O` 契约，并只读检查测试 MySQL 容器资产目录 | basic 攻击模型为 17/19，partial 为 27/32，打包契约 `attack_cases=13`、`output_preservation_cases=4`，全部通过且 `database_access=false`、`migration_executed=false`。容器内四个正式资产目录均不存在，六项外部 schema/manifest 基线尚未交付；当前同时缺 migration 授权和受控输入，不得执行或记为真实 MySQL 通过 |
| 五场景与模板发送最新离线复验 | 当前 Python 参考矩阵分别以 normal/`-O` 执行，并从当前 Go 源码一次性构建测试二进制执行 `TestPhase4*`；不注入集成开关 | Python 两种模式均为 9/9，Go 顶层 Phase 4 测试为 8/8 并 PASS；一次性源码、二进制、构建目录和本地归档均已回收。该结果不连接数据库、Redis 或 DirectMail，不替代真实重放、过期、并发、unknown、冷却和五业务流 E2E |
| Phase 4 剩余门禁机器清单 | 读取 `phase4_remaining_gates.json` 并以 normal/`-O` 执行失败关闭契约 | 2026-08-02 固定 13 个 closed、6 个 open，QA/PM `not_signable`。RAM 与五场景重放/过期为负责人豁免。Redis unknown fresh cycle 已用冻结 ELF/payload 和原生 `scp.exe -O --` 完整通过：phase1 tombstone/Adapter1、唯一 Redis 重启 run_id 改变、phase2 双 key 阻断/Adapter0、cleanup 行数0及 state/ELF/Stage 不存在、API health/ready 通过；标记不可重复。migration 仍待真实隔离 MySQL 8 验收 |
| 前端最新本地复验 | 在当前工作树执行两端静态检查、契约测试和构建，不部署、不启动真实浏览器、不访问外部服务 | 分支 `feature/aliyun-email-template-management`、HEAD `87161414...`。管理端 `type-check`/`lint`/`build` PASS，邮件 11 + 管理员 MFA 7 + outbound 4 = 22/22；用户端 `type-check`/`lint`/`build` PASS，邮箱 OTP 15/15。仅有既有 Vite chunk、dynamic-import、模块类型 warning，无错误。只与本地 `origin/main` `288599f0...` merge-base 对账，未 `fetch`，最新远端 main Gate 0 仍未通过；该证据不关闭部署、真实浏览器、真实 E2E 或 Phase 4 门禁 |
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

**RAM 当前证据收口（取代上述缺参真实探针的权限判定方式）：** 官方已确认 `SingleSendMail` 无 `DryRun`，缺参请求不能证明 Allow/Deny。后端甲已修复 `directmail_ram_probe_test.go`，真实探针只调用 QueryTemplateByParam、DescTemplate 两个 read action；SingleSendMail、CreateTemplate、ModifyTemplate、DeleteTemplate 等副作用 action 无法安全构造，旧 Deny 用例在进入 Adapter 前失败关闭。四个官方权限码必须按完整字符串精确匹配，未知码不猜测、不扩展白名单。专项 `go test`、`go vet` 均 PASS，QA 复核 P1/P2=0。2026-07-31 以当前源码 SHA `987D8859...F953` 构建的一次性探针完成离线安全测试和两个真实 read action，结果为 `RAM_PROBE PASS mode=minimum_allow reads=true`；没有发信、模板写入、数据库或 Redis 副作用，一次性源码包、二进制、构建目录和本地归档均已回收。最终验收仍应组合既有真实 `accepted`、有效策略快照，以及 RAM 权限审计或既有 `RequestId` 的 OpenAPI Troubleshoot 诊断；Chrome 权限审计因本机原生通信缺失暂未完成，当前尚缺权限审计/RequestId 证据，故状态为 `PARTIAL / BLOCKED_BY_AUTH`。该补证无需新增真实邮件，也不得构造有副作用的真实请求。

所有 RAM 否定用例还需断言：响应/日志不含 AccessKey、完整邮箱、验证码、供应商原始响应；审计只记录 action、
内部目标 ID、脱敏对象和归一化结果。

**公开邮件发码来源 IP Phase 1 delta（历史标题；QA/PM 后续已签署，十项测试矩阵保留）：**

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

> 历史说明（已被后续证据取代）：该矩阵形成时尚待 QA/PM 书面签署，也未执行 Go、Nginx、限流器、DirectMail 或 E2E。后续 Phase 1 delta 已签署且 Nginx 来源边界已有真实证据；该历史矩阵仍不能替代 Phase 4 总门禁。

**内部邮件 Adapter 指标 Phase 1 metrics delta（历史标题；QA/PM 后续已签署，运行证据已补充）：**

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

> 历史说明（已被后续证据取代）：上表形成时尚待 QA/PM 复签，也未执行 Go、反向代理、监控系统或 E2E。后续 Phase 1 metrics delta 已签署，测试环境 Token+IP 双闸、21 个固定低基数序列、Prometheus target UP 与三条告警规则已有真实证据；该子门禁通过不代表 Phase 4 通过。

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

### 3.6 阿里云短信与短信模板管理

> 本节是短信功能的正式测试策略，契约基线为 `docs/full-api-design.md §3.19`、
> `docs/frontend-api-reference.md §五之二` 和 `docs/database-schema-design.md §3.1.1`。
> 阶段 1 已进入开发验证；状态列区分本地单元证据和仍待隔离 MySQL/QA 执行的项目。任何 Mock 结果都不得声明为阿里云受理或真实手机收件。
> 旧公开发码端点及 `target` 字段的清理范围见 `docs/sms-template-management-test-cleanup.md`。

#### 3.6.1 阶段 1：数据迁移与关闭态发送链路

| 编号 | 测试项 | 测试方法 | 期望结果 | 当前状态 |
|---|---|---|---|---|
| SMS-S1-01 | 统一验证码结构兼容 | migration 升级、降级及边界值测试 | 复用 `000055` 的 `code_hash` 64 字符；`000058` 不覆盖或删除邮件基础字段 | 隔离 MySQL 8.0.46 up/down 通过（2026-08-03） |
| SMS-S1-02 | 全新数据库顺序迁移 | 从空库顺序执行 `000001` 到 `000058` | 邮件与短信结构同时存在，无重复列、索引或 CHECK | 71 张表全量迁移通过（2026-08-03） |
| SMS-S1-03 | 新手机验证码状态机 | 使用供应商 Mock 分别返回受理、拒绝、超时和网络错误 | 新记录先为 `pending`；仅受理后转为 `accepted`；失败转为 `failed` | 本地单测通过 |
| SMS-S1-04 | 手机发送失败不可校验 | Mock 返回失败后提交正确验证码 | `failed`、`pending` 或过期的手机记录均无法通过校验，且不可被原子消费 | 本地单测及隔离 MySQL 用例通过 |
| SMS-S1-05 | 邮箱回归 | 执行注册、登录、重置密码、换绑邮箱和管理员邮箱验证 | 邮箱继续使用 DirectMail 独立 Sender 和 `accepted/failed` 状态，单次消费行为不受影响 | 服务/仓储单测与原始隔离 HTTP 回归通过 |
| SMS-S1-06 | 五场景模板选择 | 使用数据库 fixture 配置 `register/login/reset_password/bind_phone/admin_verify` | 每个场景只读取自身数据库绑定，不读取 `SMS_TEMPLATE_CODE_*`，不串用模板 | 本地 fixture + Mock 通过 |
| SMS-S1-07 | 三张短信表与仓储约束 | migration 升降级、唯一索引和外键/并发测试 | `sms_templates`、`sms_scene_bindings`、`sms_send_logs` 可升降级；模板编码、场景和业务请求标识满足唯一性约束 | 仓储单测及隔离 MySQL 约束/并发通过（2026-08-03） |
| SMS-S1-08 | 功能开关关闭 | 保持 `SMS_ENABLED=false` 调用全部手机发码入口 | 返回 `503/50300`；不调用真实阿里云；不产生可校验手机验证码；邮箱链路仍可用 | 服务与错误映射单测通过；HTTP 全入口待 QA |
| SMS-S1-08A | 测试场景白名单 | `SMS_ENABLED=true`、`SMS_TEST_MODE=true`，手机号在白名单但场景不在 `SMS_TEST_SCENE_ALLOWLIST` | 在模板查询、OTP 创建、发送日志和供应商调用前返回短信不可用；白名单内 `login` 可继续 | 配置与 Dispatcher 单测通过；测试服长期登录放行待独立部署授权 |
| SMS-S1-09 | 配置 fail-closed | 分别缺失供应商、AccessKey、签名、端点、HMAC 密钥或场景绑定 | 启动失败或拒绝短信提交，不回退 Mock、固定验证码或明文验证码响应 | 本地单测通过 |
| SMS-S1-10 | 敏感信息 | 扫描响应、应用日志、审计日志和数据库 | 不出现验证码明文、完整手机号、AccessKey、请求签名原文或完整供应商响应；手机号仅保留脱敏值和独立 HMAC | 模型/响应单测通过；运行态与数据库扫描待 QA |

阶段 1 只允许使用仓储 fixture 和供应商 Mock 验证内部行为。Mock 通过不能证明阿里云账号、签名、模板或网络可用，也不能证明真实手机收到短信。

#### 3.6.2 阶段 2：管理接口、真实阿里云受理与安全测试

九个管理接口必须逐项覆盖：

> 开发及验证快照（2026-08-03）：九条路由及最小权限映射、九接口 401/403/MFA 矩阵、模板适配器、同步失败零写入与总截止、同步去重、模板启停 CAS、固定五场景、同版本场景冲突、测试发送并发单外呼、幂等隔离与冲突、白名单、双维限流失败关闭、审计/响应脱敏、全库 Go 测试、vet、依赖校验和敏感扫描已通过。PR #315 提交 `34b69a4` 的 GitHub Actions 运行 #375 已在 MySQL 8、Redis 7 和 Linux race 环境通过；隔离测试服也已完成模板同步、五场景绑定、管理测试发送和五入口真实发码/收件。下表仍保留独立 QA HTTP 与业务 E2E 验收，自动化及开发侧真实验证不能替代验收签字。

| 编号 | 方法与路径 | 权限 | 核心检查 | 当前状态 |
|---|---|---|---|---|
| SMS-A01 | `GET /api/admin/sms/summary` | `sms:template:view` | 统计口径正确；从未同步时 `last_synced_at=null`；无客户端多页聚合假设 | 自动化通过；QA HTTP 待验收 |
| SMS-A02 | `GET /api/admin/sms/templates` | `sms:template:view` | 筛选、边界分页、空列表及 D-95 `{items,page,page_size,total}` | 自动化通过；QA HTTP 待验收 |
| SMS-A03 | `GET /api/admin/sms/templates/{id}` | `sms:template:view` | 完整字段、可空字段、404/40400 和敏感信息边界 | 自动化通过；QA HTTP 待验收 |
| SMS-A04 | `POST /api/admin/sms/templates/sync` | `sms:template:sync` | 无 body；幂等计数；供应商失败无部分写；后端总截止 10 秒 | 自动化及真实阿里云重复同步通过；QA HTTP 待复核 |
| SMS-A05 | `GET /api/admin/sms/scenes` | `sms:template:view` | 固定五场景、D-95；未绑定字段为 `null`、`enabled=false`、`version=0` | 自动化通过；QA HTTP 待验收 |
| SMS-A06 | `PUT /api/admin/sms/scenes/{scene}` | `sms:template:manage` | 只接收 `template_id/enabled/version`；不接收 `sign_name`；版本冲突返回 409/40900；同一模板绑定另一启用场景返回 409/40900，停用历史共用绑定允许整改 | 自动化通过；QA HTTP 待验收 |
| SMS-A07 | `PATCH /api/admin/sms/templates/{id}/status` | `sms:template:manage` | 乐观锁、审核状态约束及有效绑定阻止停用 | 自动化通过；QA HTTP 待验收 |
| SMS-A08 | `POST /api/admin/sms/templates/{id}/test-send` | `sms:template:test` | 白名单、场景绑定、`Idempotency-Key`、双维度限流和受理语义 | 自动化通过；真实发送已受理且原白名单手机确认收件；QA HTTP 待复核 |
| SMS-A09 | `GET /api/admin/sms/send-logs` | `sms:template:view` | D-95、筛选、可空字段、RFC3339 闭区间、开始不晚于结束、最大 31 天及脱敏 | 自动化通过；QA HTTP 待验收 |

模板同步、启用、绑定和运行时选模均须拒绝含额外变量的模板，只允许变量集合精确为 `code`。四类管理写操作的请求审计失败必须在业务调用前返回 `500/50000` 且零副作用；业务完成后的结果审计失败必须记录安全 warning 并返回真实业务结果，不得用 500 诱导客户端重复改绑或重复发送。

四个权限必须分别创建最小权限管理员测试，不能只用超级管理员覆盖：

| 权限测试 | 期望结果 | 当前状态 |
|---|---|---|
| 无 Token 调用任一短信管理接口 | `401/40001` | 自动化通过；QA 待复核 |
| 已登录但未完成管理员双重认证 | `403/40031` | 自动化通过；QA 待复核 |
| 仅有 `sms:template:view` | 只允许 A01/A02/A03/A05/A09；写接口返回 `403/40003` | 自动化通过；QA 待复核 |
| 仅追加 `sms:template:manage` | 允许 A06/A07，不获得同步和测试发送能力 | 自动化通过；QA 待复核 |
| 仅追加 `sms:template:sync` | 只新增 A04 能力 | 自动化通过；QA 待复核 |
| 仅追加 `sms:template:test` | 只新增 A08 能力 | 自动化通过；QA 待复核 |
| 权限 seed 重复执行 | 不产生重复权限或重复角色绑定 | MySQL 8 CI 通过；QA 待复核 |

同步、绑定和测试发送必须补充以下并发与幂等用例：

- 同一阿里云模板连续同步和并发同步，最终仅有一条 `(provider, template_code)` 快照，计数稳定，不重复创建。
- 同步中途超时、阿里云拒绝或网络异常返回 `502/50200`，本次不写入部分快照；不得把旧快照误报为本次同步成功。
- 两个管理员持同一场景版本并发改绑，最多一个成功，另一个返回 `409/40900`；失败方重新读取最新版本后才能重试。
- 测试发送缺少 `Idempotency-Key` 返回 `400/40000`；同一管理员、相同 Key 和相同请求体串行或并发重试，只调用一次阿里云并返回首次 `business_request_id`。
- 相同管理员复用同一 Key 但修改手机号、模板或场景，返回 `409/40900`；不同管理员使用相同 Key 互不串单。
- 测试手机号不在白名单返回 `400/40000`；白名单为空时全拒；完整手机号不得持久化或出现在日志中。
- 测试发送的 `scene` 必须属于目标模板当前已启用的 `bound_scenes`；未绑定、绑定停用、模板未审核通过或本地停用时不得提交。
- 按管理员和手机号两个维度分别触发限流，返回 `429/42900`；HTTP `Retry-After` 与 `data.retry_after_seconds` 一致；幂等重放不消耗新的限流次数。
- `sign_name` 只允许来自 `SMS_ALIYUN_SIGN_NAME`；请求体注入签名字段不能改变绑定或发送签名。
- 阿里云拒绝、签名错误、模板错误、账户异常、超时及网络错误统一清洗并映射为安全错误，不泄露原始供应商响应。

#### 3.6.3 阶段 4：五场景全链路回归与证据分级

| 场景 | 发码入口 | 后续业务 | 必须验证 | 当前状态 |
|---|---|---|---|---|
| `register` | `POST /api/auth/verification-codes/phone`，body 使用 `phone/scene` | 统一注册 | 独立注册模板、正确签名、验证码可单次消费 | 本地 HTTP+Mock 与隔离 MySQL/Redis CI 通过 |
| `login` | `POST /api/auth/verification-codes/phone`，body 使用 `phone/scene` | 手机验证码登录 | 独立登录模板，不可与注册验证码串用 | 本地 HTTP+Mock 与隔离 MySQL/Redis CI 通过 |
| `reset_password` | `POST /api/auth/verification-codes/phone`，body 使用 `phone/scene` | 重置密码 | 独立重置模板、成功后旧会话失效 | 本地 HTTP+Mock 与隔离 MySQL/Redis CI 通过 |
| `bind_phone` | `POST /api/me/verification-codes/phone`，body 仅含新 `phone` | 换绑手机号 | 必须登录；公开端点传该 scene 被拒；成功后手机号更新 | 本地 HTTP+Mock 与隔离 MySQL/Redis CI 通过 |
| `admin_verify` | `POST /api/admin/auth/verification-codes/phone`，无 body | 管理员手机双重认证 | 发往当前管理员绑定手机号；公开端点传该 scene 被拒 | 本地 HTTP+Mock 与隔离 MySQL/Redis CI 通过 |

阶段 4 新增以下统一安全回归：手机号与场景 60 秒 Redis 原子冷却、手机号与场景十分钟最多五次校验尝试且
并发请求不能越过硬边界、新验证码受理或成功消费后清除旧计数、Redis 故障失败关闭、同一验证码 MySQL 并发消费
恰好一次成功、七类供应商错误安全归一化、公开手机发码/密码重置使用可信来源解析器且轮换伪造 XFF 仍共享同一
IP 限流桶、两个控制台开发代理默认本机。
本机缺少隔离 MySQL/Redis 且 Windows Go 为 `CGO_ENABLED=0`，因此真实数据库、真实 Redis 与 `-race` 结果由
PR #320 GitHub Actions run `30891459024` 提供并已通过；没有用本地跳过替代 CI。阶段 4 未获真实短信授权，
不连接阿里云、不发送短信。

邮箱注册、登录、重置密码、`POST /api/me/verification-codes/email` 换绑邮箱及
`POST /api/admin/auth/verification-codes/email` 管理员邮箱验证必须全量回归，证明短信改造未将邮箱接入短信适配器，也未错误要求 `send_status=sent`。

验收证据必须分层记录，禁止统一写成“短信发送成功”：

| 证据层级 | 可以证明 | 不能证明 |
|---|---|---|
| 单元测试、fixture、Mock | 参数组装、模板选择、状态机、错误映射、幂等与本地分支 | 阿里云账号、签名、模板、网络或真实收件可用 |
| 阿里云返回 `Code=OK` / `submit_status=accepted` | 本次请求被阿里云受理并获得可追踪请求标识 | 运营商已送达或用户已收到 |
| 白名单真实手机收件记录 | 指定手机收到本次正确签名、场景文案和验证码 | 全量用户送达率及长期稳定性 |

真实链路验收必须保存脱敏的时间、场景、模板编码、`business_request_id`、供应商请求标识和收件确认，禁止保存验证码、完整手机号或密钥。只有外部准备项已核验、`SMS_TEST_MODE=true`、白名单非空且获得测试授权后，才允许执行真实阿里云提交和收件验证。

> 2026-08-04 五模板窗口共新增 6 条 `Code=OK/accepted`：管理测试 1 条、OTP 5 条，覆盖五个业务入口且失败 0。用户确认收到 6 条，并确认统一签名和六条文案正确。窗口结束后已恢复 `SMS_ENABLED=false`、`SMS_TEST_MODE=true` 和原白名单，健康检查为 200。历史 7 条受理记录继续保留为前次验证证据，但不与本轮计数混算。

> 独立复验结论：五独立模板、五场景绑定、真实收件、统一签名和文案证据已关闭历史单模板 P1；`79ac4d0` 三项 CI、隔离部署和九 API 独立 HTTP 已关闭部署态 P2。阶段 2 独立 QA、产品经理和正式代码评审均已通过，P0=0、P1=0、P2=0、P3=0；最终文档 `dd5df2d` 已提交，PR #315 已合并为 `main@9e50ee1`。前三个公开业务验证码消费 E2E 转入阶段 4。

#### 3.6.4 阶段 5：灰度部署、反向代理与观察

阶段 5 不新增业务 API。测试必须按“关闭态部署 → 代理来源链 → 白名单 Canary → 生产关闭态部署 → 生产
Canary → 开关与观察”顺序执行，任一层未通过不得跳级。

| 编号 | 测试项 | 期望结果 | 当前状态 |
|---|---|---|---|
| SMS-P5-01 | 主线、工作区和阶段 4 门禁 | `origin/main` 包含 PR #320；独立工作树无重叠改动 | 通过 |
| SMS-P5-02 | 测试服关闭态基线 | `SMS_ENABLED=false`、`SMS_TEST_MODE=true`，health 200，凭据只核验存在性 | 只读通过 |
| SMS-P5-03 | Nginx 来源头 | 实际配置覆盖 X-Real-IP，删除 XFF/Forwarded | 静态和测试服只读通过 |
| SMS-P5-04 | 应用可信代理 | 固定代理网段进入 `TRUSTED_PROXY_IPS`；合法单值通过，缺失/多值/非法值失败关闭 | 测试服 `/28` 与 `.2/.3` 已部署；合法 Nginx 链、直连伪造头、XFF 覆盖动态通过，异常原始头由 Go 专项覆盖 |
| SMS-P5-05 | 短信指标 | 5 场景 × 8 结果共 40 条调用序列，5 场景各有耗时 sum/count，无敏感标签 | 测试服已部署：40+5+5，敏感标签 0，关闭态 Provider 总数 0 |
| SMS-P5-06 | 告警规则与通知链 | 失败率、配置异常、网络异常和平均延迟规则由 promtool/Prometheus 加载；通知接收链可达 | 通过：同版本 promtool 阈值正反例、运行实例 4 条规则、抓取目标 up、Prometheus 活跃 Alertmanager 1；修正版演练仅 1 次 firing 和 1 次 resolved，均由负责人确认 QQ 收件，结束恢复 `discard`，活动告警和通知失败 0，短信增量 0 |
| SMS-P5-07 | 测试服 Canary | 白名单、总上限 10 条、五入口、OTP 单次消费、结束恢复开关 | 收件层通过：双目标白名单、五入口各提交一次、供应商受理 5/5、人工收件 5/5、OTP 未消费、零重试，结束恢复 `SMS_ENABLED=false`；完整消费层不在本次授权范围 |
| SMS-P5-08 | 页面四宽度 | 1440/1024/768/390 的权限、MFA、模板、日志及五态可操作 | 通过：本地 Mock 与测试服真实 Chrome 均无横向溢出；实服展示 5 模板、5 场景、13 日志，短信请求仅 GET、写请求 0、功能性控制台错误 0；保留 1 条无路径静态 404 环境观察 |
| SMS-P5-09 | 回滚 Dry Run 与恢复材料 | 先关开关、保留证据、默认不 down migration；固定备份可验证且业务配置零变更，允许只读访问审计日志增长 | 通过：本地 test/production 两套 Dry Run、测试服材料与容器快照通过；固定旧二进制关闭态运行 10 秒后自动恢复当前二进制并稳定 10 秒，服务停止/启动各 2 次，零业务 POST、邮件和短信 |
| SMS-P5-10 | 生产灰度与观察 | 生产先关闭；Canary 与开关分开授权；完成 5m/15m/30m/2h/24h 观察 | 测试服通过：真实收件 Canary、五次供应商受理、OTP 未消费及 5m/15m/30m/2h/24h 五档关闭态观察全部完成。无 BOM 修正版 24h 快照实际经过 95952 秒，health/ready 200、累计 `21/20/1`、Provider `0/0`、零活动告警、零通知失败和零副作用；最终关闭态及五窗口证据已离线组装并通过权威验证。生产尚未开始，故整体仍为部分通过 |
| SMS-P5-11 | PR 发布安全门禁 | Linux CI 执行 MySQL 8、Redis 7、全库 `-race`、Nginx、promtool、准备度和双环境回滚检查 | 通过：PR #323 当前 head 的 6 个 CI job 全部成功；PR 可合并但尚未合并，合并仍需项目负责人独立批准 |
| SMS-P5-12 | 固定代理网络部署 | 手动前端工作流检查宿主路由/Docker 网段重叠，固定 `.2/.3`，失败自动恢复部署前镜像 | 测试服固定网络与 `.2/.3` 部署通过；部署前镜像已备份，前端自动恢复路径尚未故障触发 |
| SMS-P5-13 | 测试服短信管理只读 API | 只调用 5 个 GET；授权、未登录、无权限、MFA、脱敏和零副作用契约均正确 | 实服通过：授权 GET 5/5；`401/40001`、`403/40003`、`403/40031`；数据库/审计/Provider 零增量，4 个写接口调用 0 次 |

`accepted` 只表示阿里云受理；真实收件必须由收件人独立确认。观察指标和报告不得包含完整手机号、OTP、
AccessKey、Token、模板正文或供应商原始响应。

阶段 5 当前状态以 `docs/sms-phase5-acceptance-report.md` 第 2 节门禁表为唯一权威摘要；本测试计划只维护
测试项证据，状态变更时必须同步该门禁表，避免准备度、测试计划和 README 各自形成不同结论。

#### 3.6.2a 阶段 3：管理后台短信模板页面

| 编号 | 前端检查 | 期望结果 | 当前状态 |
|---|---|---|---|
| SMS-F01 | 九接口 API 封装与 TypeScript DTO | 路径、方法、请求体、字段、D-95 和错误码与 SSOT 一致 | 独立 QA 通过 |
| SMS-F02 | 四权限矩阵与 MFA | view/manage/sync/test 分离；短信 `403/40031` 精确进入双重认证 | 策略、路由及独立 QA 通过 |
| SMS-F03 | 模板列表、筛选和详情 | 五态完整；详情只读；无敏感信息 | 浏览器 Mock 与独立 QA 通过 |
| SMS-F04 | 五场景独立绑定与启停 | 仅合规模板；二次确认；409 刷新且保留场景草稿 | 策略测试与独立 QA 通过 |
| SMS-F05 | 同步防重与测试幂等 | 同步空 body 且 loading 防重；测试失败保留幂等键，新动作才换键 | 双击竞态已修复，专项与独立 QA 通过 |
| SMS-F06 | 日志、错误和受理语义 | 429 显示 Retry-After；503 关闭态；accepted 不冒充送达 | 重复提示已关闭，独立 QA 通过 |
| SMS-F07 | 敏感信息 | 完整手机号仅存在弹窗内存，不进入持久层、URL、控制台和列表 | 变更扫描与独立 QA 通过 |
| SMS-F08 | 响应式与触控 | 1440/1024/768/390 可操作，移动端卡片/抽屉，触控区至少 44px | 四宽度浏览器 Mock 通过且无横向溢出 |

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

### DirectMail Phase 4 全新 Redis unknown 周期复验记录

- 2026-07-31 的单次授权执行在源码快照 SHA 包装门禁失败后立即停止，未执行数据库写入、Redis 重启、migration、真实发信或 RAM 修改；远端唯一 stage 与本地归档已精确回收，授权已消耗。
- 成功周期新增 `cleanup_verified` 精确清理：只接受 `phase2_verified`、三个冻结主键和零意外日志主键；数据库事务必须精确删除 `1+1+1` 行，Redis 只做精确 `EXISTS` 前后验。
- 新 payload、PowerShell 控制器和 Python 契约只完成离线验证；控制器默认关闭，必须同时提供 `-Execute`、单次确认词、操作员 ID 和 Linux ELF 测试二进制才会发现 SSH 工具。
- 本地通过项：Bash `-n`、PowerShell SelfTest（含本地 stdin/LF/BOM/完整哈希传输链）、Python normal/`-O` 18 个攻击变异、auth/service Go test 与 vet、原 Redis unknown 契约 normal/`-O` 31 个攻击变异。
- 全工作树静态敏感扫描：1223 个文本文件，`FAIL=0`、`REVIEW=2`、`INFO=290`、受保护环境文件跳过数和读取错误均为 0；两项 REVIEW 已逐项确认具有 URL 校验或安全非生产环境双门禁。
- 未通过项保持不变：全新 Redis `phase1 -> restart -> phase2 -> cleanup_verified` 真实周期、000055/000056 隔离 MySQL 完整/partial/down 矩阵、RAM 有效权限与 Deny 证据、最终 QA/PM 签署。
- 000055/000056 本地准备复核：打包器 20 项清单及 normal/`-O` 13 个攻击模型通过；四个完整/partial runner 契约 normal/`-O` 均通过。固定副作用仍为 `database_access=false`、`migration_executed=false`、`package_created=false`，不得登记为真实矩阵通过。
- 最新单次 Redis 执行：stage 创建、上传哈希和只读 preflight 通过；`phase1` 调用返回 `remote_gate_failed`，固定停止且 `retries=0`。未取得 phase1 成功摘要，未进入 Redis 重启、phase2、cleanup、migration 或 RAM；保留 stage 状态未知，必须在新授权下先诊断和精确恢复。
- 最新恢复授权：只读 recovery preflight 返回通用 `remote_gate_failed` 后立即停止；未上传恢复二进制，未执行 cleanup、Redis 写入或 stage 删除。真实远端分类因外层折叠未知，修复后的 runner 仅传播固定白名单分类；再次诊断仍需新单次授权。
- 2026-08-01 首次 recovery preflight 曾因 `remote_stage=unknown` 失败关闭；后续只读诊断定位到 nonce mismatch，并最终通过独立单次授权完成精确 cleanup。首次 fresh cycle 的 controller nonce 脱绑定也已修复，第二轮资产进一步关闭 allowlist 表名、MySQL 默认配置、state 文件链接和哈希冻结问题。当前 payload SHA 为 `29EAA0B18959D9ABCCDCF10D3793AA6A0C8574B85028714AB7D6EB4E429DEF54`，Linux ELF SHA 为 `1179E29D9F43EFEA79F185E8D2319D015A627F69A48EF9ED7CE22E72BA6AD900`、大小 `25573597`；这些仅证明新周期资产本地就绪，不证明 fresh cycle 已执行。
- 随后的保留 stage 只读诊断严格启动一次 SSH、未重试且没有任何清理/重启/写入分支，但本地控制器在 SSH 返回后的脱敏汇总阶段因 `$result` 未赋值失败，捕获文件已由 `finally` 精确删除，无法恢复远端摘要；因此 `retained_stage_reconciled` 仍未通过，授权已消耗。静态审计确认旧 preflight 错用复数表名 `email_test_recipient_allowlists`，真实表为单数 `email_test_recipient_allowlist`，仅登记为本地缺陷候选。独立诊断控制器 AST/SelfTest 与 normal/`-O` 19 个攻击模型通过；正式 recovery payload 也已改为单数表名、`mysql --no-defaults` 和四个显式查询失败分类，固定失败摘要优先于 stderr 折叠，Bash/PowerShell 及 normal/`-O` 23 个攻击模型通过。三项 recovery SHA 依次为 `B22B4EFF...FB916`、`2777E041...A0DC`、`783F6A4C...D3D03`。全程未再次联网；Redis 状态保持 `recovery_diagnostic_reauthorization_required`。
- 第二次同范围 Redis 保留 stage 只读诊断授权仅执行一次 SSH 且未重试；SSH 返回后本地空 `PSObject` 捕获绑定为 null，脱敏摘要未生成且捕获文件已由 `finally` 删除，因此远端结果仍不可恢复，`retained_stage_reconciled` 未通过。未执行 cleanup、数据库写入、Redis 删除/扫描/重启、migration、RAM 或真实发信。控制器改为初始化后构造非空四字段 `PSCustomObject` 并补动态回归，AST/SelfTest 与 normal/`-O` 22 个攻击模型通过；controller/contract SHA 为 `45711793...8E13B`、`6FCC290D...65F3E`。本次授权已消耗，再次诊断需要新授权。
- 后续完整本地子进程回归确认更深层根因：Windows PowerShell 5.1 会按系统代码页解析 UTF-8 无 BOM 的中文控制器，使部分换行被并入注释；本地夹具中的 `cat >/dev/null` 也会消费同一 stdin 的后续摘要和 `exit 2`。控制器现固定 UTF-8 BOM、逐项参数数组、无 BOM stdin 文件重定向和精确 finally 清理，真实 Git Bash 链路已验证 `exit=2`、固定 stdout、stderr=0、分类正确且无临时目录残留。payload/controller/contract SHA 为 `8A99E4B5...DDF5`、`778F3649...A495`、`38529420...637C`；PS5 AST=0、SelfTest、Bash 语法及 normal/`-O` 26 个攻击模型通过。该验证未联网，不改变远端未知状态或授权消耗事实。
- RAM 证据解析器与 migration 执行资产继续完成纯离线复核：RAM 契约 15 个攻击模型，基线生成器 17 个，隔离包 13 个，000055 完整/partial 为 17/27，000056 完整/partial 为 20/32，normal 与 `-O` 全部通过；默认关闭路径确认 Docker/数据库访问和 migration 执行均为 false。隔离包 20 条冻结 manifest 自检通过，默认调用退出码 2 且未创建文件。当前机器缺少 Docker CLI，未生成任何基线或运行矩阵；RAM 也没有新增云侧审计证据。因此 `ram_effective_permissions=external_evidence_required`，000055/000056 均保持 `baseline_generation_and_authorization_required`，QA/PM 仍不可签署。
- 第三次 Redis 保留 stage 纯只读诊断严格执行一次 SSH，`ssh_attempts=1`、`retries=0`、stderr0，且未写入、清理、重启或落地远端资产。唯一 stage、3 个固定文件、SHA、state 完整性、schema57/dirty0、三条精确数据库夹具、scope 唯一性、Redis PING/身份和派生精确 key 不存在均通过；关键失败关闭事实为 `phase=phase1_created` 且 `stage_nonce_match=false`。因此 state 与夹具可相互证明，但不能证明属于当前 stage，不允许 cleanup 或新 phase1。机器状态更新为 `stage_nonce_mismatch_recovery_authorization_required`，本次授权已消费。
- 旧 recovery payload 已增加 state nonce 与 stage operation ID 强制等值，旧 controller 在任何正式传输前固定禁用。payload/controller/contract SHA 为 `36853BAE...C6172`、`C83BA172...B243F`、`520643CA...6C774`；PS5 SelfTest、默认禁用、Bash 语法及 normal/`-O` 25 个攻击模型通过。后续 nonce mismatch 专项恢复需要新授权和独立恢复资产。
- 独立 nonce mismatch 精确恢复资产当时已离线完成：只接受完整 `phase1_created` state、目录 nonce 明确不等、state 派生三条夹具全字段精确归属、唯一 scope、Redis 未重启和派生精确 key `EXISTS=0`；state 使用 `O_NOFOLLOW + fstat`，所有第二轮只读归属门禁均先于首次 `chmod`。控制器固定一次 preflight SSH、一次 SCP、一次 cleanup SSH且不重试，不输出 stage、nonce、Redis key 或业务原值。冻结 ELF SHA 为 `1179E29D...AD900`、大小 `25573597`，payload/controller/contract SHA 为 `12B57C09...CEC63`、`84619388...B02F6`、`3BA1C76D...C534B`；PS5 AST/SelfTest、Bash、默认拒绝、normal/`-O` 31 个攻击模型、目标 Go 测试与 ELF 身份均通过。该时点静态扫描 `files=1096`、`FAIL=0`、`REVIEW=3`、`INFO=268`、读取错误 0；尚未取得该恢复的写授权，Redis 门禁为 `stage_nonce_mismatch_recovery_authorization_required`。该状态已由下一条正式失败记录取代。
- 精确恢复授权随后严格执行一次。首次 preflight SSH 远端退出码为 0，但 stderr 非空，控制器立即以 `stage=preflight/retained=true/retries=0` 失败关闭；未进入 SCP、`chmod`、Go cleanup、数据库删除、Redis 操作或 stage 删除，本地捕获文件已精确回收。授权已消耗。离线增强后的 controller/contract SHA 为 `A2121034...3CCB7`、`FF5D605A...A8D20`，只新增实际传输次数、退出码和 stdout/stderr 长度的脱敏分类，normal/`-O` 32 个攻击模型及 SelfTest 通过。Redis 状态改为 `stage_nonce_mismatch_recovery_preflight_stderr_reauthorization_required`。
- 新增严格独立的 `-PreflightOnly` 模式和专用确认词；只允许一次 SSH，任何结果都不会进入 SCP/cleanup，也不要求恢复 ELF。最终 payload/controller/contract SHA 为 `12B57C09...CEC63`、`59F927FD...A96EA`、`939122A1...D1251F`；PS5 SelfTest、默认关闭和 normal/`-O` 34 个攻击模型通过。尚未执行该只读模式，需新的单次授权。
- 静态对比确认失败 recovery 与此前成功只读诊断的关键差异是缺少入口 stderr 封闭，且精确 Redis `EXISTS` 未单独重定向；当前只把它登记为候选原因。payload 已通过 `exec 2>/dev/null` 阻止远端原文进入传输，并把 Redis run-id/EXISTS 错误折叠为固定分类，不放宽业务门禁。最终 payload/controller/contract SHA 为 `76430E00...A41D60`、`C0EEE96D...169CEA`、`6BD2DAA6...DB305F`；normal/`-O` 36 个攻击模型及 SelfTest 通过，尚未联网验证。
- 新的 `-PreflightOnly` 授权严格执行一次并通过：`ssh_attempts=1`、`scp_attempts=0`、`exit_code=0`、`stderr_length=0`，完整 state、三条夹具归属、Redis 身份和精确 key 不存在均成立；`retained=true`、`writes=false`。没有上传或 cleanup，本次只读授权已消费。Redis 状态改为 `stage_nonce_mismatch_recovery_cleanup_authorization_required`。
- 精确 cleanup 授权随后执行一次：preflight 通过并完成一次 SCP，但 cleanup 在 `recovery_binary_identity` 门禁失败，`exit_code=2`、stderr0、两次 SSH、一次 SCP、零重试。没有 `chmod`、Go cleanup、数据库或 Redis 写入，四文件 Stage 保留。新增 `-UploadedBinaryPreflightOnly` 只读模式，仅报告 ELF regular/symlink/owner、模式白名单和 SHA 布尔值；payload/controller/contract SHA 为 `B2DF03AE...B41F1D`、`9F50446C...7C65A`、`18890BF1...E6D39`，normal/`-O` 38 个攻击模型及 SelfTest 通过。机器状态为 `stage_nonce_mismatch_recovery_binary_identity_reauthorization_required`。
- `-UploadedBinaryPreflightOnly` 证明 ELF 为普通文件、非链接、属主正确、SHA 匹配。随后 `-ResumeUploadedCleanup` 按单次授权执行成功：第一次 SSH 复核完整现场，第二次 SSH 将权限归一为 `0500` 后执行一次 Go `cleanup_phase1`；三个精确夹具、state、固定资产及空 Stage 均已清理，health/ready 通过。最终为 `preflight=true binary_hash_match=true cleanup=true retained=false ssh_attempts=2 scp_attempts=0 retries=0`，未执行 Redis 删除、扫描或重启。
- fresh-cycle 的 legacy `scp -O` 控制器两次均在首次 `upload_binary` 阶段失败，后续授权的纯只读诊断均确认唯一 Stage 为空、远端 SCP 工具和容量正常，未创建夹具、未重启 Redis。修复版只替换上传层为 OpenSSH 9 默认 SFTP，使用新的专用确认词，payload/ELF、mock adapter、两次 SSH、两次传输、一次 Redis 重启和零重试边界不变；normal/`-O` 36 个攻击模型、Bash 与 PS5 SelfTest 通过。新 controller/contract SHA 为 `D756D445...99FB9`、`AE3EA576...9443F`。项目负责人手工 SFTP 会话已证明 SSH 10003 的 SFTP 子系统可用，但未执行文件上传；当前先等待精确空 Stage 清理授权。
- 已为上述保留现场准备独立 upload-failure 纯只读诊断器：正式路径固定一次 SSH、零 SCP、零重试，只检查唯一 Stage 身份、inode 稳定性、空目录/部分二进制/完整二进制分类、冻结二进制 SHA、SCP 工具身份和容量分类；不访问 MySQL/Redis，不写入、不清理、不重启。首次授权启动因无 BOM UTF-8 被 PowerShell 5.1 错误解析，在唯一 SSH 语句前以 `$result` 未定义停止，AST 证明 SSH=0、远端未访问。控制器改为 UTF-8 BOM 并加入正式分支 AST SelfTest 后，经新授权正式执行通过：`upload_failure_stage_empty`、SSH1、SCP0、stderr0，现场保持不变。
- 空 Stage 精确清理资产仅允许唯一严格目录、`pc:700`、同一 inode 和两次空目录复核全部通过后执行一次 `rmdir`；不删除文件、不访问 MySQL/Redis、不重启、不上传、不重试。normal/`-O` 20 个攻击模型、Bash、PS5 AST SelfTest、默认关闭和敏感扫描通过。单次授权执行返回 `empty_stage_removed`：SSH1、SCP0、stderr0、零重试，数据库/Redis/重启均未触碰。后续 fresh cycle 新授权仍在 `upload_binary` 失败：SSH1、SCP1、零重试，Stage 保留；当前等待纯只读诊断授权。
- RAM 有效权限新增严格离线证据验收器：只接受同一身份、策略版本、部署 SHA、24 小时内证据窗、有效策略/附加策略/用户组策略完整性、Deny 优先级、直接用户或完整角色信任链、最近尝试审计和六个固定 Action 的脱敏摘要；递归拒绝凭据、RequestId、邮箱和供应商原文。normal/`-O` 15 个攻击模型通过，`external_access=false`、`writes=false`。当前未交付真实脱敏清单，故 RAM 门禁仍为 `external_evidence_required`。

### DirectMail Phase 4 migration 最新状态（2026-08-02）

- 六项 MySQL 8 基线已经在独立临时容器真实生成，000055/000056 不再处于“等待基线生成”状态。
- 当前唯一 migration 前序阻点为 000055 schema55 Down 的语句级真实矩阵；000056 因执行顺序被 000055 阻塞，不能提前登记通过。
- 000055 runner 已将 Down 固定拆分为 `down_statement_01..24` 的同会话只读标记，并补齐 `constraint` 错误分类；normal/`python -O` 30 个攻击模型、完整本地 migration 契约、Bash、PowerShell SelfTest 和 `git diff --check` 均通过。
- 最新远端授权执行只发生一次 SSH，并因命令遗漏 `-RecoverKnownFailure` 在 `retained_stage_present` 停止；SCP、Stage 删除/创建、Docker、数据库、migration、Redis 和邮件发送均未发生，禁止复用该授权。
- 后续重新授权已显式进入恢复模式，严格使用 SSH 2、`scp.exe -O` 1、零重试；旧 Stage 恢复和新 Stage/包执行已发生，最终在 `full_matrix/matrix55_execution` 失败并保留新 Stage。语句级失败标记尚未在独立只读通道读取，当前状态为 `matrix55_statement_failure_readonly_diagnostic_required`；禁止猜测、清理或直接重跑。
- 最新单次纯只读语句级诊断使用 SSH 1、SCP 0、零重试，确认 `matrix55_failure=schema55_down_statement_05`、`case=schema55`、`target_created=true`、`error=constraint`，且 Stage、66 项 source manifest、六项基线、四套资产和五个固定输出门禁均通过；现场保持不变。
- 第 5 条 Down 语句是四条中文权限元数据的精确计数断言。根因修复不改 migration 业务语义，而是在基线生成、`mysqldump` 和四套 full/partial runner 的所有 MySQL 客户端调用中固定 `--default-character-set=utf8mb4`。相关契约在 normal/`python -O` 全部通过，基线生成器、000055 full/partial、000056 full/partial 攻击模型分别为 36/31/31/24/36；完整编排 71、手动远端 9、精确清理 18、保留诊断 12+9 均通过。PowerShell SelfTest 4 项、Bash 语法 9 项通过。当前状态为 `schema55_down_charset_fix_final_matrix_authorization_required`，不得登记为真实矩阵通过。
- 新授权执行严格使用 SSH 2、`scp.exe -O` 1、零重试，原 `schema55_down_statement_05` 未再出现，流程推进到 `full_matrix/matrix55_summary` 后失败关闭；Stage 保留、stderr0、临时容器不保留，未访问主库、Redis、RAM 或真实邮件。
- 根因是成功 runner 输出包含 7 条动态目标进度行，而旧包装器只允许固定终止摘要，造成成功输出必然被误判。新解析器严格验证 case 顺序、schema、唯一 64 位哈希和固定摘要，并统一用于四套矩阵；normal/`python -O` 的完整编排契约现为 81 个静态攻击模型加 7 个真实 Bash stdout 回归场景，保留现场诊断和精确清理契约分别为 16/22。当前状态为 `matrix55_success_summary_readonly_diagnostic_required`，授权已消费，禁止直接重跑。
- 随后的单次纯只读诊断严格使用 SSH 1、SCP 0、零重试，确认 `classification=matrix55_success_summary_contract_mismatch_retained`，Stage/source/六项基线/四套资产/五个输出均匹配，matrix55 为 `summary_contract_mismatch/none/false/none`，且 `writes=false`、`database_access=false`、`docker_access=false`。这证明 matrix55 的 7 个有序唯一目标和固定成功摘要有效；仍需一次精确恢复及最终矩阵授权完成 partial55、matrix56、partial56 后才能关闭 000055/000056。
- 后续最终矩阵授权严格执行 SSH 2、`scp.exe -O` 1、零重试，旧成功 Stage 精确恢复后，matrix55 已被新解析器接受；流程在 `full_matrix/partial55_execution` 停止，Stage 保留、stderr0、临时容器不保留。新增纯只读解析器按冻结 boundary manifest 验证 31 个 partial case 加两个 baseline 的顺序、schema 和唯一哈希，仅输出 `partial55_failure/case/target_created/error`；normal/`python -O` 19 个攻击模型及 SelfTest 通过。当前状态为 `partial55_failure_readonly_diagnostic_required`。
- 单次只读诊断已确认 partial55 为 `environment_precheck/none/false/invalid`，即目标库尚未创建、migration 尚未执行，且 stderr 不是固定 MySQL 错误信封；不能归因于 3306/13306、SQL 或主库连接。增强后的只读资产仅把 stderr 归入固定白名单，并精确核验 partial55 七项资产的名称、模式、属主和 source/baseline SHA；payload SHA 为 `DAB28DAB...E86C53`，Bash、PowerShell SelfTest、normal/`python -O` 30 个攻击模型和 8 个运行时解析场景通过。当前状态为 `partial55_environment_precheck_stderr_classification_required`，必须新授权一次只读分类后才能针对性修复。
- 新授权的单次只读分类结果为 `partial55_stderr_class=other`、`partial55_assets_verified=true`，未访问 Docker/数据库/Redis。静态差分锁定 partial runner 相对已通过 full runner 的唯一新增系统工具 `/usr/bin/wc`；000055/000056 partial 均改为使用既有 awk 精确计数两行 manifest。新 runner SHA 为 `E9EC4C1F...EBA9C9`、`1BDAF145...3BFB1`，normal/`python -O` 32/37 个攻击模型、完整编排 82 个攻击模型、PowerShell 与隔离包 SelfTest 全部通过。当前状态为 `partial55_wc_dependency_fix_final_matrix_authorization_required`，远端 Stage 保留且不得无授权重跑。
- 恢复清理器只新增接受当前 7 输出现场，严格绑定 matrix55 成功、partial55 四行 `environment_precheck/none/false`、stderr `other` 且不超过 4096 字节、无归属容器；payload SHA `5B0191E3...10547`，26 个攻击模型、13 个运行时解析场景和 SelfTest 通过。最终矩阵恢复确认词为 `I_CONFIRM_EMAIL_MIGRATION_PARTIAL55_WC_RECOVERY_MATRIX_ONCE`。
- 后续获授权的正式恢复严格执行一次，实际使用 SSH 2、`scp.exe -O` 1、零重试；旧 Stage 已按门禁清理，新矩阵仍在 `partial55_execution` 以退出码 2 失败，新 Stage 保留且临时容器已精确移除。该证据推翻 `/usr/bin/wc` 为根因的结论，相关改动仅作为可移植性加固保留。partial55 预检现拆成七个有序白名单阶段，远端入口只从严格四行摘要映射脱敏 `partial55_*` 分类；本地完整编排 normal/`python -O` 均通过 86 个攻击模型、7 个成功摘要运行时场景和 8 个失败摘要运行时场景。当前状态为 `partial55_precheck_instrumented_recovery_authorization_required`，未取得新授权前禁止清理或重跑。
- 精确观测版随后按新授权执行一次：SSH 2、`scp.exe -O` 1、零重试，返回 `partial55_boundary_manifest_shape`、退出码 2；新 Stage 保留且临时容器已精确移除。该阶段位于目标库创建和 migration 执行前。纯本地执行正式 awk 后确认根因为相邻 action 缺少独立边界：`{bad++} seen[$2]++` 对 awk 是语法错误；000055/000056 两套 partial runner 已统一改为 `{bad++} {seen[$2]++}`，并新增真实 awk 运行时契约，normal/`python -O` 分别通过 34/39 个攻击模型。当前状态为 `partial55_boundary_awk_fix_recovery_authorization_required`，不得把本地修复登记为真实矩阵通过。
- 修复后恢复的第一次 SSH 在 `partial55_stderr_pair` 门禁停止，SCP 0、零重试，旧 Stage 未清理，Docker/数据库均未访问。此前的 `stderr_length=0` 只属于外层 SSH，不能证明 Stage 内 `partial55.stderr` 为空。只读诊断器现增加不输出原文的 `awk_syntax` 分类，normal/`python -O` 32 个攻击模型、8 个运行时解析场景和 SelfTest 通过。当前状态为 `partial55_boundary_manifest_stderr_classification_authorization_required`。
- 单次纯只读分类确认当前 Stage 为 `boundary_manifest_shape/none/false`、`partial55_stderr_class=awk_syntax`，matrix55 成功证据、六项基线、四套资产和 source SHA 全部完整；SSH 1、零重试且无写入、Docker或数据库访问。恢复清理器已精确绑定 1..4096 字节且每行均为 awk/mawk/gawk 固定语法错误外壳的现场，并新增 5 个真实 stderr 分类场景。当前状态为 `partial55_boundary_awk_stderr_fix_recovery_authorization_required`。
- 最终恢复和隔离矩阵已真实通过：SSH 2、`scp.exe -O` 1、零重试；六项基线重新生成，000055/000056 的 full、partial、down 全部为 true，94 个唯一隔离目标完成，两个临时 MySQL 8 容器和远端 Stage 均已精确清理，`main_database_modified=false`。000055/000056 技术门禁现关闭并标记不得重跑；该结果不替代真实邮件/E2E 或 QA/PM 签署。
- 控制器恢复摘要已从历史 `matrix_outputs=1` 修正为当前精确值 2，并由攻击用例禁止回退；否则会在旧 Stage 已清理、新 Stage 已创建后误判失败。该项仅离线验证，未消耗远端授权。
- Redis fresh cycle 和 000055/000056 独立 MySQL 8 矩阵已真实通过且不得重复；RAM、五场景真实重放/过期、真实邮件故障矩阵和五业务流 E2E 均登记为负责人豁免，不是技术 PASS。QA/PM 已附负责人豁免通过，Phase 4 已关闭；Phase 5 与生产上线仍未批准。

## 7. 缺陷管理

- 缺陷在 Git Issues 中跟踪。
- 优先级：P0（生产阻断）/ P1（核心功能缺陷）/ P2（一般缺陷）/ P3（体验问题）。
- P0、P1 缺陷必须在下一个迭代前修复，不得上线。
- 每个缺陷 Issue 必须包含：复现步骤、期望结果、实际结果、截图或日志。
## AI 网关 Phase 1 G0/G1 验收

### G0 证据

- `tests/audit-stage1-final.md`：核心商业闭环 37/37 通过。
- `tests/audit-stage1-closing-confirm.md`：阶段收尾确认通过。
- `docs/frontend-acceptance-stage1-pm-review.md`：产品经理前端业务验收通过；完整核心闭环产品复核必须另行签收。
- `docs/ai-gateway-g0-g1-acceptance.md`：产品经理采纳 37/37 QA、权限收口和前端证据，G0 完整核心闭环产品验收通过。

### G1 自动化矩阵

- `go test -count=1 ./internal/modules/token_gateway/...`。
- Native/Bifrost 标准响应与 input/output/reasoning/cached/total Usage 等价。
- 非流式、SSE、`include_usage`、`[DONE]`、断连、超时和结果未知。
- 401、429、500、HTTP 200 业务错误、非法 JSON、缺少 choices。
- 禁止自动 fallback；公开响应和日志不泄露内部 Token、路由、Key 名称和供应商错误正文。
- 内部入口缺失/错误 Token 固定 401；重复 Authorization 在上游前以 Nginx 400 或鉴权 401 拒绝。
- `000060` 必须包含四张 Expand 表、正交状态、唯一约束和 Decimal 精度，且 down 不执行破坏性删除。
- Bifrost 配置必须同时包含百炼和 OpenRouter，并且 Key 只能引用环境变量。

### G1 Linux POC

必须在固定 Bifrost 镜像上复验两个上游的普通响应、SSE、Usage、错误、内部鉴权、单节点退出、恢复和配置/镜像回滚。产生费用的真实调用需要负责人明确授权；Fake 通过不得替代真实 POC。证据统一写入 `docs/ai-gateway-g1-poc-report.md`。

性能测试分为两种不可混用的模式。`real_upstream_observation` 使用真实百炼做端到端观察，不判定纯网关开销；`controlled` 使用 `infra/testsupport/fixed-openai-upstream/main.go` 提供固定 JSON、SSE、Usage 和分片，Native/Bifrost 指向同一实例，不连接外网。受控模式先执行 5 组等量预热，再执行 20 组交替顺序的正式配对样本，共 80 次；每组按 `Bifrost - Native` 计算，所有请求及协议检查必须成功，非流式差值 P95 不超过 20ms，流式 TTFT 差值 P95 不超过 30ms。

`infra/scripts/run-bifrost-g1-benchmark.sh` 必须显式接收模式，保存权限 `0600` 的独立 TSV，记录模式、顺序、原始耗时和差值并输出 SHA256。失败文件不得覆盖或删除。真实调用授权与 4 次最小 POC 授权相互独立；受控模式不得携带真实上游 SK。

Migration 真实语法和约束使用 `infra/scripts/verify-ai-gateway-migration-000060.sh` 在隔离临时 MySQL 8 容器验证，必须确认项目数据库未被连接，并取得首次 up、保留结构 down、re-up 后 `60/dirty=0` 及租户/预算/幂等约束证据。

### G2 自动化与阶段门禁

- `go test ./...`，远程 Linux CI 必须执行 `go test -v -race -count=1 ./...`。
- Project CRUD、停用/归档、单用户租户隔离。
- Project SK 默认空 allowlist、显式 all、创建、列表、轮换、吊销和过期。
- 定向模型必须同时通过用户分组/角色可见性，不能只凭 Project SK allowlist 绕过。
- 空 messages、多模态内容、多值/逗号 Idempotency-Key 在写请求账本前返回 400；未实名为 70001，渠道不可用为 50300。
- Project SK 创建、轮换和吊销审计完整，摘要不含明文 SK、HMAC Secret 或上游凭据；审计写入失败必须输出脱敏告警，不能静默丢失。
- Key 明文只返回一次且响应 `no-store`，数据库/日志/错误不含明文或 HMAC Secret。
- 普通 JSON 与 SSE 共用 RequestOrchestrator；SSE 只在 Finalize 成功后发送 `[DONE]`。
- 公开 Chat 未装配 RequestOrchestrator 时失败关闭，不得回落旧 ForwardService、钱包或 `token_usage_logs`。
- 同 Idempotency-Key 相同指纹返回已有状态；不同指纹 409；并发重复请求只调用一次上游。
- 同 request_id 换用户、Project 或 SK 拒绝且不泄露原请求。
- 断连继续读取尾部 Usage；超时、流不完整和结果未知进入 `unknown`，禁止 fallback。
- Usage 缺失不生成计量行，不按 `max_tokens` 估算。
- Finalize 重试不重复 attempt 或 Usage；周期恢复扫描只选择超过安全窗口的遗留请求，并在事务锁内重查状态与截止时间，Reconcile 同时收敛请求与运行中的 attempt。
- `billing_status=unquoted`；不得生成价格、钱包 hold、settled、released、Outbox 或旧 `token_usage_logs` 双写。
- `infra/scripts/verify-ai-gateway-migration-000061.sh` 只在无网络、无端口、tmpfs 的隔离 MySQL 8 容器执行，验证首次 up、保留式 down、re-up、allowlist 和三元租户外键；禁止连接项目数据库。

### G3 自动化与阶段门禁

- Decimal 金样覆盖四类 Token、多 SKU、极小金额、八位舍入、最低收费、大数量和 uint64 边界。
- 价格发布拒绝未审批、人民币汇率不为 1、SKU 不完整、币种/数值非法、低于最低毛利和生效区间重叠；逐请求快照不受后续活动价格变化影响。
- 同一逻辑模型的两个已审批版本并发发布只能一个成功，数据库中只保留一个 active 版本。
- 余额不足在上游前拒绝；100 并发竞争钱包不得产生负余额。
- 20 并发相同请求只形成一个请求和 hold；G2 编排幂等保证只有事务赢家持有执行上下文。
- 重复 settle、settle/release 竞争和 Worker 重放只允许一个财务终态。
- 强制结算最后一步 Outbox 写失败时，请求、Usage、钱包、hold 和流水全部回滚。
- Usage 缺失进入 `settlement_pending`，不产生 consume 流水；超过对账期限转人工异常并保留 hold。
- 失败响应携带完整可信 Usage 时仍须 settled；失败 attempt 在对账中发现完整 Usage 时同样 settled，只有明确零成本且无 Usage 才 released。
- 正式 HTTP 驱动必须保留错误响应内的可信 Usage；请求已发出但错误响应无 Usage 时保持 hold 并进入 `settlement_pending`。
- 明确 JSON 失败响应携带可信 Usage 时必须 settled；SSE 错误事件、缺少 `[DONE]` 或读取失败导致结果未知时，即使携带 Usage 也必须保持 hold 并进入待对账。
- 缓存/推理拆分超过输入/输出总量，或 `total_tokens != prompt_tokens + completion_tokens` 时视为不可信并进入待对账。
- 既有客户端不传 `max_tokens` 时采用配置兜底值完成报价；显式零值、非法值或超过模型上限仍在调用上游前拒绝。
- 缺省 `max_tokens` 必须写入实际发往上游的请求体，保证执行输出上限与预占口径一致。
- `n` 缺省或 JSON 整数 1 时允许报价；字符串、浮点、指数写法、`n>1` 或其他非法值必须在预占和上游调用前返回 `unquotable_request`。
- 人工零用量 `settle` 必须拒绝并保持 exception/holding，确认零成本只能显式 `release`；Project/SK 越权统一为 HTTP 403 + `40003`。
- 人工 `release` 携带任何正用量必须优先按矛盾输入返回 `40010` 并保留审计；没有矛盾输入但与已有终态操作不一致时返回 409。
- 原始 Provider Usage 使用稳定序号 0，计费拆分使用序号 1；补价只更新数量一致行的空单价和金额，数量不一致必须冲突且不得改写原始事实。
- 正常成功、明确失败、待对账和超额异常都必须保存 sequence 0 原始 Usage；价格发布 CLI 在 `APP_ENV` 缺失、未知或生产值时必须在连接数据库前拒绝。
- 失败请求不得应用成功最低收费；人工终结重复调用只有与真实终态一致时才可幂等成功。
- 人工异常终结接口必须经过 `token:manage`、管理员二次认证和前置审计；审计失败时不得调用资金服务。
- SSE 结算待确认或异常必须输出 `molin.status` 事件；状态查询必须绑定原用户和 Project SK。
- 请求尚未写出时的网络失败释放 hold；已写出但结果未知时保留 hold。
- Bifrost 模型映射缺失等 HTTP 构造前失败统一归类 `request_not_sent`，立即释放 hold 且不产生消费流水。
- 单条损坏对账记录不得阻塞批次后续请求；人工核定释放或结算只能产生一个财务终态。
- 客户端断连后按可信 Usage 结算，freeze/unfreeze/consume 的 `balance_after` 可连续还原。
- `actual > held` 进入 exception、暂停价格、写 P0 Outbox，不静默封顶或补扣。
- RabbitMQ URL 未配置时不启动 Worker，Outbox 保持 pending；已配置但 Broker 临时停机时增加重试，恢复后必须 broker confirm，并从持久绑定队列读取相同 Message ID。
- Outbox 锁超时重领使用 `locked_at` 租约 CAS，旧 Worker 不得覆盖新拥有者。
- Outbox 退避重试覆盖至少 2 小时 Broker 故障，单次发布必须有限超时；同聚合前序失败时不得投递后序事件，dead 事件受控重新入队后按原 `event_id` 有序恢复。
- `infra/scripts/verify-ai-gateway-migration-000062.sh` 只使用隔离临时 MySQL/RabbitMQ 网络，验证首次/重复 up、保留 down/re-up、真实并发和 Broker 恢复。
- 必须通过 `go test -count=1 ./...`、`go vet ./...`、测试 Linux `go test -race -count=1 ./...` 和 `git diff --check`。
- 静态和 staged diff 必须扫描真实 SK、密码、Token、HMAC Secret、RabbitMQ URL 和上游密钥；测试凭据只能在临时环境生成。

## AI 网关 Phase 1 G4 验收

- 输入违规必须在报价、预算、钱包 hold 和上游调用前拒绝，返回 40310 与稳定文案。
- JSON 违规输出不得返回正文；SSE 违规分段不得外泄；所有实际透传字符串字段（含 legacy functions、工具定义及 tool_calls arguments）均必须审核。可信 Usage、冻结成本单价和平台成本金额以 `provider_cost` 行保留，用户钱包 hold 释放且消费为 0；Usage 暂缺时保持待对账，只能由具备 `ai_gateway:reconcile_manage` 和管理员二次认证的受控接口补录。验收必须覆盖前置审计失败不执行、相同 Usage 幂等、冲突 Usage 返回 409、平台成本入账、用户消费为 0、hold 释放和唯一 Outbox；禁止直接改库作为业务验收路径。
- 安全策略缺失或数据库异常返回 50320，不允许绕过。
- Redis 四层并发、RPM、TPM 任一超限返回 42921/42922、Retry-After、request_id 和脱敏 scope。
- Redis 停止时返回 50321；恢复后租约可重新准入，不存在永久计数或幽灵租约。
- 100 个并发、两个 SK、同一 Project hard 预算不得超卖；累计值精确等于限额时允许，只有超过限额才拒绝；soft 预算不阻断。
- 80/90/100 阈值按主体和周期幂等；日/月周期按 Project IANA 时区。
- 预算预留只能按 G3 settled/released 同步；没有 G3 请求的过期预留才可 expired。
- 释放或同步失败形成的补偿任务在 `next_retry_at` 到期后立即重试；持久化成功的明确释放失败在无 G3 请求事实时直接释放，不等待 24 小时。补偿事实也无法写入时保持 held 到自然过期并记录错误，不允许固定时间提前释放。成功任务进入 completed；连续八次失败进入 dead，可用乐观锁转 retry/manual_review；completed/dead/manual_review 不会被后到失败记录覆盖，只有显式 retry 恢复；坏任务不阻塞批次。
- 管理写接口必须先审计，具备 JWT、对应 `ai_gateway:*_manage` 细粒度权限和管理员双重认证；用户事件与申诉只允许 JWT 且响应最小化。
- 000063 up 可重复执行；down/re-up 保留治理事实；不得写旧 `token_usage_logs`。
- 必须执行本地全量测试、Linux `go test -race -count=1 ./...`、G3 回归和 `verify-ai-gateway-g4-governance.sh`。
- 独立 QA 与产品经理均需给出 P0/P1/P2；P0/P1 为 0 才允许提交阶段完成结论。

## AI 网关 Phase 1 G6 验收

- 模型目录只出现本人可见、已发布、文字、当前有有效人民币价格且存在健康路由的模型；后台工作副本修改不得在重新发布前影响用户端。
- 目录和详情不得包含 `cost_unit_price`、渠道、上游模型、Bifrost 地址、上游密钥或完整平台 SK；文档非 healthy 时必须禁用并说明原因。
- Project 创建、编辑、归档和平台 SK 签发、轮换、吊销均强制本人隔离；未实名签发/轮换返回 70001；完整 SK 只出现一次，危险操作二次确认。
- 请求账本按 Project、SK、模型、状态和时间筛选；详情可将 request_id、确认用量、价格版本、销售计价行、结算金额和钱包流水对账，差异必须为 0。
- 用户 A 查询用户 B 的 Project、SK、请求详情、导出和申诉均不可成功；响应不得泄漏记录是否存在的敏感细节。
- CSV 要求 93 天以内且最多 5000 行，覆盖 `= + - @` 公式注入；重复账单申诉保持一条事实并返回冲突；申诉包含 SK、Bearer Token 或 JWT 样式时拒绝且不得进入审计或申诉表。
- 同一请求同时存在 Provider 原始用量和 `reconciled` 人工核定用量时，用户列表、总览和计价明细必须统一展示人工核定事实，并与最终钱包结算金额可解释；真实账单抽屉需验证深色背景、关键字段高对比和三档视口边界。
- Playwright 覆盖搜索 URL、详情、复制、文档状态、Project 编辑、SK 一次展示/轮换/吊销、用量详情、导出、申诉、空/错误和 1440/768/375 无横向溢出。
- 本地执行 `gofmt`、`go test -count=1 ./...`、`go test -race ./...`、`go vet ./...`、`go mod verify`，管理端和用户端执行 `type-check/lint/build`，用户端执行 `test:g6-e2e`。
- 测试环境必须备份后部署 Migration 000065、000066、API、管理端和用户端；验证数据库拒绝跨用户申诉，再使用专用实名测试用户与最低成本文字模型执行唯一幂等真实 Bifrost 请求。
- 真实 E2E 必须验证目录发布快照、Project SK scope、request_id、Usage、价格快照、销售金额、钱包扣费、越权拒绝和吊销后不上游；凭据回收后保留不可变账本证据。
- CI、独立代码评审、QA 与产品经理均须 PASS，P0=0、P1=0，才能合并并声明 G6 完成；Mock 浏览器通过不等于真实测试环境验收。

## AI 网关 G7 可靠性验收

- `/api/internal/metrics` 必须继续通过内部 Token、来源 IP allowlist 和可信代理双闸；非法配置、重复 Token Header、伪造 XFF/X-Real-IP 和数据库 Gauge 读取失败均失败关闭，错误响应不得泄漏内部原因。
- AI 指标不得包含 request_id、用户、Project、平台 SK、提示词、响应正文或密钥；逻辑模型最多 32 个准入值，超限/非法值统一为 `other`。进程并发写指标和 Prometheus 抓取必须通过 Linux race。
- 账单申诉必须先校验请求归属；虚构 request_id、他人请求和仅格式相似的伪密钥不得生成 P0 事实。只有 HMAC 精确匹配本人请求所属有效平台 SK 时才写脱敏审计，五分钟指标按唯一 API Key 去重；审计失败必须关闭失败。
- Prometheus 规则必须覆盖 99% 可用性、P95/P99 请求耗时、P95 TTFT、上游失败/Bifrost timeout/双节点不足、在线及对账 Usage 异常、审核不可用、密钥泄漏发现、流式断连、三项账单差额、七类异常、账务异常/超龄、未释放预占金额或最老年龄、Outbox/补偿积压、心跳失败和幽灵租约；22/22 告警必须分别具有正向阈值用例，并用负向用例验证 P0/P1 不串级；每条告警必须有逐项中文 Runbook。
- Grafana SLO 看板必须由 provisioning 自动加载，UID 固定 `molin-ai-gateway-g7`，仅绑定本机回环；验证 health、数据源、16 个面板、常用刷新/时间范围和无错误日志。
- `ai-gateway-reconcile` 只允许精确非生产 allowlist 与精确 `YES` 批准值，在 MySQL READ ONLY、REPEATABLE READ 事务执行。任何三项金额差额、七类聚合异常、未释放 hold、Outbox 或补偿积压非零时输出 FAIL 并退出 2，同时输出有限条 request_id 明细；禁止修账、退款、补扣、释放预占或重排任务。
- G7 隔离脚本必须从空库应用当前全部 up migration，使用 103 个虚构租户/钱包、随机 G7 库名/密码和可达 Fake HTTP 上游；Go 测试校验 DSN 指向随机隔离库及本机/隔离容器，不连接项目库、不映射数据库端口、不调用付费上游，退出清理精确临时容器和网络。
- 1000 个请求按 100 并发完整结算，Fake 成功率不低于 99%；既有单钱包 100 并发不得超扣。100 路同幂等键只能创建、执行、结算和扣费一次。独立性能门直接累加 JSON 的 Prepare、调用上游前和上游首字节到客户端首次写出三个本地阶段，P95 不超过 20ms；SSE 用“上游响应体首字节 → 首个公开数据帧成功写入”的直接时间戳验证首包附加开销 P95 不超过 30ms。
- 流式客户端断连（包括最终 `[DONE]` 写入/Flush 失败）后继续读取可信 Usage 并结算；Fake HTTP 服务真实停止时未发送请求释放预占，恢复后同幂等键安全重试。真实停止 Redis 时治理失败关闭，恢复后租约可重新准入、幽灵租约 Gauge 正确回落。
- 最终请求账本↔Usage、账本↔预占、账本↔钱包消费流水均为 `0.00000000 CNY`；重复结算、成功未结算、缺失价格快照、缺失钱包流水、缺失可信 Usage、完成未收敛、账务 exception、未释放预占、Outbox 活跃积压和补偿活跃积压均为 0。真实 MySQL 还必须在事务内覆盖价格版本与四 SKU、raw↔sale 数量守恒、销售单价与逐项 `ceil_8` 重算、最低收费、钱包 owner/金额域以及 hold/freeze/release 等 18 项损坏，聚合与 request_id 明细均应失败关闭，回滚后重新达到零差额。
- 测试环境必须先核对目标、实际进程、环境文件、schema `66:0`、活动任务、ChangeId 和回滚目录，再部署 API/监控资产并执行 Fake-only E2E。生产、多模态、真实客户和付费上游压力测试均不在范围。
- 精确 PR HEAD 的 CI、独立代码评审、独立 QA 和产品经理均须 PASS，P0=0、P1=0，才允许合并并声明 G7 完成。

## AI 网关 G8 生产就绪与商业灰度验收

- 渠道健康检查必须拒绝 loopback、link-local、RFC1918、IPv6 本地/私网、DNS 指向受限地址、混合解析和重定向；实际拨号必须使用已校验 IP。测试内网只允许精确 `host` 或 `host:port` 白名单。
- `APP_ENV=production` 时商业流量默认关闭。显式开启后，密钥、内部指标鉴权、RabbitMQ/Outbox、正有限预占单价与上限，以及 5～8 个发布文字模型、两个健康渠道、逐模型价格/路由、唯一审核策略、成本有效期和 15% 毛利任一缺失均失败关闭；结果未知请求保持只调用一次，只有明确未发出的失败可在同一路由安全重试。
- 总闸关闭时 `/v1/chat/completions` 与 `/api/token/chat/completions` 返回 `503/50330`，不得创建请求账本、调用上游或产生扣费；前端不得自动重试。
- 最终精确 HEAD 必须通过全量 Go、Linux race、两端契约/typecheck/lint/build、Promtool、敏感扫描、G7 1000@100/幂等/性能/混沌/零差额回归。
- 在生产等价隔离环境执行关闭态部署、新→旧→新回滚、备份可读与恢复步骤、TLS/SSE/超时/请求体/连接数/日志/监控/告警验证；与真实生产证据严格分开。
- 真实后端浏览器 E2E 覆盖管理员发布、用户模型发现、Project/SK、调用、Usage、账单和申诉，视口为 1440/768/375，所有按钮有反馈。
- G8 工程就绪要求独立规格、代码安全、QA、产品对同一 PR HEAD 签署，P0/P1=0。Draft PR 只提供轻量质量和变更范围定向反馈，`CI Draft 快速门禁汇总` 不能作为合并证据；转 Ready 后必须在当前精确 HEAD 重新运行全部适用重型门禁，未命中重型 Job 只能由失败关闭分类器标记为 `skipped`，最终 `CI 必选门禁汇总` 成功后才可进入 merge commit 授权。G8 核心代码、脚本、基础设施或工作流变更在 Ready 始终触发完整 G8 回归。
- CI 分级回归必须覆盖：Draft/Ready 事件互斥、同 PR 旧运行自动取消、不同 PR 不互相取消、纯文档 Draft、G8 Python 定向测试、Go package 映射、双前端共享路径、工作流 actionlint、未知路径失败关闭、两个汇总名称及所有既有 Ready 命令仍存在。Draft 不运行 Docker、race、Playwright 或生产构建；Ready 不得删除、替换或降级任何现有测试。典型 G8 Draft 的近似取整分钟目标不高于 8，但计费指标不能替代测试通过。
- 当前 `main` 没有 classic branch protection/ruleset；测试报告必须把“Ready 汇总已成功”与“GitHub 平台已配置 required check”分开。未获仓库设置独立授权前，只允许人工核对精确 HEAD、Ready run 和独立评审，不得宣称平台自动阻止绕过。
- G8 最终阶段归档为 `G8_STAGE_ACCEPTANCE=PASS`、`G8_SOFTWARE_CLOSED_LOOP=COMPLETED`、`G8_TEST_ENV_USABLE=YES`、`G8_REAL_PROVIDER_SETTLEMENT=PASS`、`ACCEPTED_EXCEPTIONS=YES`。已有测试证据覆盖真实 Provider 调用、执行、Usage、结算、钱包流水、Outbox 和低敏证据持久化主链路。必须继续保留 `RESPONSE_MATCH=NO`，不得登记为响应内容 PASS；未配置临时 SK 的手工脚本不要求补跑。账单/争议追加核对接受关闭；测试服对账、失败补偿、双闸门、回滚演练以及 Prometheus/Grafana、告警规则、备份周期和 RabbitMQ ready 消息转入后续运维专项。测试服真实流量闸门保持开启且可能产生真实费用。商业观察与生产开放仍属于后续 `G8_COMMERCIAL_ACCEPTED`，不由本次归档证明。
- 测试到生产迁移清单必须不含 Secret，并使用精确字段白名单失败关闭。至少覆盖：合法测试候选、重复键/额外密码字段拒绝、固定低敏失败枚举、测试阶段开闸或伪造生产授权拒绝、四阶段完整前序摘要链、ChangeId 与各阶段审批回执唯一性、测试凭据轮换回执、单份生产阶段拒绝、生产路径规范化、发布制品/生产目标/测试源身份防漂移、生产灰度授权/预算/模型/毛利/证据门禁和低敏成功摘要。示例中任一 `PENDING` 未填时不得作为可部署清单。
- 测试服运行态只读审计不得通过 Docker 组或任意 sudo 放大权限。特权入口必须固定为 root-owned 审计器和对账器、可信 PATH、无 `SETENV`/通配符的单命令 sudoers；必须拒绝非法 ChangeId、额外参数、错误安装路径/所有权、调用者环境注入和用户可替换对账二进制。安装与实际只读核验使用不同 ChangeId，未安装时所有受权限阻断的 schema、Bifrost、监控和账务事实继续保持 UNKNOWN。
- 测试服只读入口候选包必须从精确 Git 提交归档生成，以固定 Go 环境连续构建两次并要求摘要一致；输出目录必须全新且失败不留半成品。001～005 均已消费，普通生成、stage、暂存取证和传输诊断重放必须在读取候选/身份文件或联网前失败关闭；历史复现只能按精确 ChangeId 在系统临时目录完成并自动销毁。005 唯一正式诊断已证明固定 SSH 与远端隔离 Python 标记可用，但未关闭暂存 `UNKNOWN`。006 必须冻结 004 helper 摘要并复用其 inode/fd/目录项竞态门禁，前置失败只允许六类固定枚举，成功状态只允许 `ABSENT`、`PRESENT/PASS` 或 `PRESENT/MISMATCH`；一次 SSH、零重试、业务/上游/费用 `0/0/0`，禁止 SFTP、下载、删除、sudo、Docker、数据库、队列、服务和业务请求。CI 必须使用 `python -I` 执行相关单测、语法和自检，并继续验证已消费入口失败关闭；仓库 PASS 不等于取得远端执行、清理或安装授权。
- 006 已在 machine-id 前置门禁返回 `BLOCKED/MACHINE_ID` 并消费，暂存查找未执行。007 主机身份诊断必须只读取固定 `/etc/machine-id` 最多 4097 字节，只返回 `READABLE_MATCH`、`READABLE_MISMATCH` 或 `UNREADABLE`，不得输出当前原文或摘要，不得自动更新批准基线。必须覆盖匹配、漂移、缺失、空、超限、读取异常、精确三键、错误/重复/额外键、未知状态、非 ASCII、有界 stdout/stderr、单 SSH、零重试、本地检查不联网和消费后在 helper/身份/网络前拒绝；工程合并后仍需用户独立授权。
- Drop 映射场景的 008 不得读取 hostname、machine-id、实例元数据或 CMDB；必须覆盖严格九键三态、五文件集合/元数据/摘要、父路径与文件目录项竞态、helper 类型/inode/摘要/契约漂移、64 KiB 有界双流、单 SSH、零重试、本地检查不联网和消费门禁。Windows 定向测试及 Linux `--network none` 动态取证均须通过；工程合并不构成执行授权。
- 009 Drop 安装候选必须保持历史 001～003 临时复现回执不漂移，同时只让 009 成为普通生成入口的唯一活动候选。manifest 必须包含 `TARGET_TRANSPORT=DROP_SSH` 与 `PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE`，且不得包含物理 hostname/machine-id。Drop stage 包装器必须覆盖本地五文件/回执/密钥门禁、一次 SSH、一次独占 SFTP、stderr/非零/超限失败关闭以及错误 ChangeId 在读取本地身份或联网前拒绝；工程门禁与合并均不构成远端执行授权。
- 009 与 010 均已消费，生成器不得保留活动候选。010 的一次本地检查、一次只读 SSH 和一次原子 SFTP已完成，root 安装未建立连接，live/sudoers/visudo/sudo self-test 均未执行。直连包装器的普通、本地检查和自检入口必须在 helper、候选、身份材料或网络读取前固定拒绝；生成器只允许在系统临时目录复现 010 的 Windows/Linux 历史回执，禁止创建持久候选。后续暂存清理、root 安装或新的 `pc` 非特权方案必须使用新 ChangeId。
- 011 必须是唯一活动候选并绑定 `DROP_SSH_INTERACTIVE_SUDO`。测试必须覆盖五文件与 Windows/Linux 回执、SFTP 包装器不具备 SSH/sudo 能力、一次 SFTP 和零重试、`sudo -k -v` 仅一次且密码不进入任何仓库资产、root-only/no-clobber、两次 visudo、精确 sudo 范围、Docker 组以及只回滚本次新建目标。Windows 测试与 Linux `--network none` 动态门禁均不得连接测试服或调用真实 sudo。
- 011 已消费且暂存保持 `UNKNOWN`。012 只读取证必须独立冻结固定端点的唯一 ED25519 known_hosts、客户端密钥对、五文件、manifest 和回执；远端以目录描述符锚定部署根、暂存和文件，严格输出九键三态，并覆盖路径/文件集/元数据/内容/manifest/回执/读取错误、同名替换竞态、64 KiB 有界双流、单 SSH、零重试、local-check 不联网和消费门禁。Windows 测试及 Linux `--network none` 必须通过；CI 不得传正式参数或连接测试服。
- 本地身份材料诊断必须与远端 ChangeId 生命周期拆分。本地诊断器无 ChangeId、可重复且源码不得包含 SSH/SFTP/远端访问能力；它必须覆盖绝对路径、链接/reparse、fd/目录项竞态、known_hosts 明文/哈希 ED25519 唯一性、允许的其他算法共存、公私钥配对、有界本地工具输出和固定低敏失败。Windows 最小环境必须显式保留 `SystemRoot` 与 `PROGRAMDATA` 且拒绝继承其他变量。一次性远端包装器不提供 `--local-check`，只允许一次固定 SSH、九键三态、六行低敏结果和消费门禁。Windows 与 Linux `--network none` 必须同时通过，CI 不得读取真实身份材料或连接测试服；任一冻结脚本摘要变化均使旧授权清单失效。

## 视频网关 VID-G1 Expand Schema验收

- 前置门禁必须证明VID-G0经PR #416合并到`main@f9aff4d2aace3d9bf862a88f0ed6304e2953dacc`，QA、产品、工程和CI均PASS。
- `000072`必须允许`ai_requests.modality=video + capability=video.generate`，并显式持久化`operation=text_to_video/image_to_video`；旧Chat/Image默认值和写入合同保持不变。
- 文生视频必须在Service事务内强制零TaskInput；图生视频必须强制恰好一个`role=reference_image,ordinal=0`。数据库唯一键只验证重复序号，不能替代跨行数量测试。
- `CreateVideoSchemaFacts`必须先完成operation、能力、ID、Quote、模型、owner和输入快照纯校验，再在唯一事务内按Request→Task→可选I2V TaskInput顺序写入；任一写入或commit失败都必须零部分提交，VID-G1不实现Repository/CAS。
- `RequestVideoInputPendingDelete`必须按输入ID、用户和Project在同一事务加锁读取并再次核对归属，限定`ready/rejected/quarantined`、拒绝legal hold、加锁统计活动租约为0，再同时写`delete_requested_at/pending_delete_at`；跨用户/Project或任一步失败零提交，本阶段不实现Repository/ObjectStore/Worker。
- `CompleteVideoUploadSession`必须在同一事务锁定会话，拒绝非`verifying`、过期、缺ETag/VersionID、重复完成、跨归属和snapshot来源漂移；插入snapshot或完成会话任一步失败均零提交。数据库还必须拒绝`completed_at>expires_at`。
- `ReleaseVideoInputLeases`必须锁定Task、Request和TaskInput：`succeeded`只允许`settled`，`failed/cancelled/expired`只允许`released/settled`，非终态和`pending_reconcile`拒绝；已释放输入与T2V零输入幂等，任一步失败零提交。
- Quote、任务、Usage均保存operation；任务外部ID只使用Molin `public_id`，Provider/Bifrost/内部ID不进入公开JSON。
- 共享Quote/Task/Asset及六个视频新模型的内部自增ID必须`json:"-"`；旧图片PublicID保持兼容，未来只能由专用DTO将Task.PublicID映射为`video_id`。
- `ai_upload_sessions`必须覆盖`created/uploading/verifying/completed/rejected/cancelled/expired`，验证过期、拒绝、重复完成、跨用户完成、同对象重复绑定、取消终态和完成后唯一input asset；purpose固定`video_reference_image`，MIME只接受JPEG/PNG。
- 上传和已有图片资产两种来源都必须形成独立`ai_gateway_input_assets`规范化快照，验证source二选一、原始/规范化hash、策略版本、版本号、私有对象边界及`pending/normalizing/moderating/ready/rejected/quarantined/pending_delete/expiring/deleting/deleted/delete_failed`完整生命周期；源图片后续隔离不得改变snapshot hash/version，FK必须阻止源事实删除。
- 上传会话、输入资产和TaskInput的`(user_id,project_id)`组合归属必须拒绝跨用户/Project绑定；上传会话存在SK时，`(api_key_id,project_id,user_id)`组合外键还必须拒绝跨Key绑定。
- 活动任务与`pending_reconcile`必须持有输入租约；清理不得越过未释放租约或legal hold；安全终结并完成对账后`lease_released_at`只写一次。
- 任务事件必须追加式且`event_id`全局唯一；Provider回调必须按Provider外部事件唯一去重，原始回调正文不得落库；密文payload表不得保存密钥或明文。
- TaskEvent必须通过BEFORE UPDATE/DELETE触发器在数据库层拒绝覆盖和删除；动态测试分别尝试两种操作并确认原行不变。
- 视频资产必须覆盖时长、帧率、容器、视频/音频Codec和音频标识；Image角色仍限既有`primary_output/thumbnail/moderation_copy/derived`，Video才允许`content/preview`，不得用视频扩展放宽旧图片约束；`media_deleted_at`只能表示媒体正文删除，不能删除或伪装删除账单与审计事实。
- 可用视频资产的MIME、大小、宽高、hash、时长、帧率、容器、视频Codec和`has_audio`必须逐项显式非空；动态测试至少覆盖缺MIME、时长、帧率和音频标记均失败，不能让SQL UNKNOWN放行。
- 价格表只扩`video_seconds/video_megapixel_seconds`模板、同名meter和variant JSON表达；视频meter的variant必须显式包含`$.operation=text_to_video/image_to_video`，缺失或非法operation失败；VID-G1不得实现视频选价、Quote或钱包运行逻辑。
- MySQL CHECK必须显式处理三值逻辑：视频Request/Quote/Task/Usage operation要求`IS NOT NULL`，视频SKU拒绝JSON缺字段、JSON null和错误值；input asset进入ready时规范化hash、MIME、大小、宽高、策略版本及对象定位逐项非空，不能让SQL UNKNOWN绕过。
- 上传ETag/VersionID、上传对象、ready输入对象和可用视频对象/Codec必须trim后非空；隔离测试必须覆盖空白ETag+Version、bucket和object key被拒绝，不能用非NULL空串冒充完整事实。
- `000073`必须幂等创建`video:view/model/price/task/safety/reconcile/resource/retention/secret/release`，仅自动映射`admin`；down不得删除历史授权。
- migration静态扫描必须拒绝`raw_body/provider_response_body/signed_url/prompt_plaintext/api_key_plaintext/secret_plaintext`等敏感字段。
- down/re-up不得包含`DROP TABLE`、`DROP COLUMN`、`DELETE`或`TRUNCATE`，并显式声明保留Expand Schema和权限事实。
- 隔离MySQL必须使用本机已有镜像、`--pull=never`、内部无出口网络、无宿主端口和tmpfs；先写入完整旧Chat/Image事实，再验证`000001→000073`首次up、重复up、down/re-up及旧金额/hash/状态不漂移。
- 本地必须运行VID-G1模型/migration/Service定向测试、Go全量、vet、目标包race、旧Chat/Image回归、敏感扫描和`git diff --check`。
- 动态回执必须明确`provider_calls=0`、`wallet_writes=0`且清理本轮容器/网络；不得连接项目库、测试服务器或真实Provider。
- 本阶段没有HTTP和页面验收；Schema、权限和OpenAPI规划均不能证明视频接口可调用。
- 共享表扩展后必须回归图片运行链隔离：图片Task的创建、读取、状态推进、取消、Provider领取、恢复和结算只允许`capability=image.generate AND operation IS NULL`；图片Asset的读取、状态推进、清理、引用和观测只允许`modality=image`。同一owner同时存在Image与Video事实时，图片路径对Video增量必须为0。
- 最终必须由独立测试工程师、产品经理和工程Agent复核；P0或P1不为0、CI/PR/merge未完成时不得`AUTO_PASS`。

完整字段、状态、回滚和统一门禁见[`video-gateway-vid-g1-schema.md`](./video-gateway-vid-g1-schema.md)。

当前本地证据（2026-08-28）：隔离MySQL已通过`000001→000073`首次up、重复up、保留式down/re-up，以及`preexisting_chat_image/upload_expiry/expired_complete_rejected/duplicate_complete/cross_owner_complete/source_snapshot/price_operation_variant/safe_lease_release/null_fail_closed/empty_string_fail_closed/pending_delete_guard/task_event_append_only/video_asset_null_fail_closed`、T2V/I2V、归属、唯一、租约和回调重放矩阵；`provider_calls=0`、`wallet_writes=0`。Go定向测试已覆盖创建事实、上传完成、输入删除申请和租约释放四类事务的纯校验、原子提交、任一步失败回滚及内部ID隐藏。DEF-VID-G1-001～015已在同一源码快照完成QA、产品、工程与规范复核并全部`CLOSED_VERIFIED`，当前`P0=0/P1=0/P2=0`；但commit、push、PR、CI与合并仍未完成，因此阶段保持`HUMAN_REQUIRED`，该记录不能单独支撑VID-G1 `AUTO_PASS`。

## 视频网关 VID-G2 价格与Quote验收

- 冻结矩阵必须同时包含T2V/I2V，每个允许variant恰好一条`video_seconds` SKU；六维缺失、未知或禁止值失败关闭。
- active视频价格仅允许`non_commercial_test_fixture`；缺价、零价、重复价、币种、成本过期和正式价格混入均拒绝。
- Decimal `ceil_8`、minimum charge、5秒Hold/3.25秒结算释放、零用量全释放和快照篡改金样通过。
- HMAC绑定owner、Project SK、operation、模型、Prompt摘要、variant及I2V输入ID/hash/version；可信resolver在创建与消费时重新读取ready快照。
- 同键同指纹返回原Quote且不受调价影响，同键异指纹冲突；100并发Quote创建一条、消费一个赢家。
- `/v1`自动与`/api/token`显式路径共享快照、Hold与结算输入；100并发Generation只形成一个Request、Hold、Task。
- T2V/I2V在隔离MySQL完成Quote→预算/余额→Hold→Task；I2V额外形成唯一TaskInput。
- 预算不足、余额不足、Quote过期/已消费和任务冲突必须回滚Request、Task、TaskInput、Link和Hold，Quote及钱包原事实保持一致。
- `000074`首次up、重复up、保留式down/re-up和旧Chat/Image价格/Quote逐字段兼容通过。
- 全量Go、vet、race、敏感扫描和`git diff --check`通过；Provider、真实钱包、项目数据库、测试服和生产写入均为0。

## 视频网关 VID-G3 任务、资产与事件验收

- 七类Repository必须全部落在共享`ai_requests/ai_gateway_quotes/ai_gateway_tasks/ai_gateway_assets`及VID-G1扩展表，不得创建平行视频账本。
- 执行、计费、交付所有允许与禁止迁移必须逐格矩阵测试；状态不得回退，相反终态不得覆盖，`pending_reconcile`不得交付或释放输入租约。
- Task、Request与Asset使用`version_no` CAS；真实MySQL 100并发任务迁移只能1胜者，其余稳定冲突且只追加1个TaskEvent。
- T2V必须零TaskInput；I2V必须唯一ready参考图。Provider提交前复核owner、hash、version、审核、过期、删除和租约。
- 绑定与删除100并发最终只能形成“一个绑定且ready”或“零绑定且pending_delete”，不得出现悬空TaskInput。
- TaskInput冻结字段不可UPDATE，TaskInput与TaskEvent不可DELETE；TaskEvent不可UPDATE。
- 回调三元唯一键、同正文重放、同事件异正文冲突、乱序、未知任务、错绑、重复ACK、相反终态与pending_reconcile迟到成功全部验证。
- 回调只能从received写入一次终态，终态二次UPDATE和DELETE必须由MySQL拒绝；非1062插入错误不得伪装成重放冲突。
- 视频Task普通JSON必须严格六键，result/error正文保持空；TaskEvent只接受四键结构白名单，message/data换名绕过需在Go与直接SQL两层拒绝。
- AES-GCM必须覆盖round-trip、nonce唯一、AAD、nonce、密文、key version和归属篡改；普通JSON和源码证据不得出现测试Prompt明文。
- UploadSession、InputAsset、GeneratedImageAsset来源、TaskInput、Task和Asset必须覆盖跨User、Project与API Key的统一不存在语义。
- content、cover、preview、thumbnail、moderation_copy与derived必须具有同request/task父子关系；对象位置只能由Fake ObjectStore工厂生成。
- Asset必须覆盖available、quarantined、expiring、deleting、deleted、delete_failed和争议访问阻断；媒体正文删除后hash、规格和追溯事实继续存在。
- 必须运行`verify-video-gateway-migration-000075.sh`证明完整`000001→000075`、重复up、保留式down/re-up、race与两类100并发；项目数据库、Provider、真实钱包和费用均为0。
- 必须回归VID-G2 000074脚本、Chat与Image全量Go测试、`go vet`、敏感信息扫描和`git diff --check`。
- 独立QA、产品、Standards与Spec审查必须绑定同一源码状态，最终P0/P1/P2均为0；VID-G4必须保持未开始。

## 视频网关 VID-G4 Fake异步、媒体安全与AI标识验收

- VideoGateway必须覆盖Submit、Query/Poll、Cancel、Callback、Content和Delete；T2V与I2V复用同一执行体系。
- ACK丢失已知taskUUID只能Query恢复；ACK未知、Submit/Query结果未知必须进入pending_reconcile，禁止重提、交付或释放租约。
- Poll、Callback和Cancel 100并发不得状态回退或覆盖相反终态；真实Task CAS、Provider绑定CAS和输入租约均只允许一次有效写入。
- 确定性Fake队列必须覆盖100次重复投递、单消费者领取、崩溃租约到期恢复和ACK终结。
- 参考图必须覆盖PNG/JPEG成功、EXIF方向、SVG/HTML/GIF/APNG/polyglot、截断、MIME冲突、像素炸弹、GPS、异常ICC、超大元数据、取消和资源上限。
- 视频Probe必须覆盖正常MP4、MIME伪造、损坏HTTP 200、大小/时长/宽高/帧率/Codec越界、Range异常、中途断流和超时。
- LocalOnlyMediaFetchPolicy必须覆盖外部URL、重定向、loopback/metadata目标和DNS重绑定，外部HTTP请求固定为0。
- T2V必须执行Prompt、四类帧和音轨审核；I2V额外执行OCR、视觉、二维码、文字和元数据审核。
- content及cover/preview/thumbnail/moderation_copy/derived必须具有正确父子关系、审核版本、显式/隐式标识版本和available事实。
- Fake ObjectStore必须覆盖服务端位置、临时/结果/隔离区、Range、有界Put、hash、幂等、冲突、隔离迁移和删除不存在对象。
- 删除六类媒体正文后必须保留Request、Quote、Task、Asset、hash、规格、父子、生命周期和审计事实。
- 普通JSON不得包含Prompt、参考图/回调/媒体正文、Provider task ID、bucket、object_key或签名参数。
- 必须运行verify-video-gateway-migration-000076.sh，证明完整000001→000076、重复up、保留down/re-up、四包Linux race及T2V/I2V真实Repository闭环。
- 必须通过go test ./...、go vet ./...、go mod verify、git diff --check、敏感扫描和Chat/Image全量兼容回归。
- 必须证明真实Provider、Provider Key、真实钱包、外部HTTP、测试服写入、生产操作和费用全部为0。
- 独立QA、产品、Standards与Spec审查必须绑定同一SOURCE_STATE_ID，最终P0/P1/P2均为0；VID-G5不得开始。

## 视频网关 VID-G5 财务、Outbox、补偿与对账本地验收

当前入口以`verify-video-gateway-migration-000077.sh`为准：默认all包括Reserve/Usage/Cancel/Media/Settle/Release/Compensation/Delivery/Reconciliation/Unknown/Submission/Adjustment/Golden/Compatibility。下文较小的筛选表达式描述各历史检查点，不是当前全量筛选。直接财务终态竞争的六组100并发与旧Chat G7本机Fake性能已通过，见[终态竞争证据](./evidence/video-gateway-vid-g5-terminal-race-checkpoint.json)。

旧Chat兼容新增`compatibility_chat_g4/g5/g6/g7`，每次启动独立临时MySQL，安装完整1—77，执行原有预算、管理、用户及可靠性测试；不与G5金样共库，不启动Redis/RabbitMQ/MinIO。是否通过以实际运行证据为准，不能用普通Go测试中的环境Skip代替；上述OFF基础设施段明确NOT_RUN。

上述四组已在主代理隔离运行中通过，具体时长及范围见[兼容与Outbox检查点](./evidence/video-gateway-vid-g5-legacy-chat-outbox-checkpoint.json)。额外财务Outbox反例为T2V/I2V×settled/released×held/final×additional/replaced共16组：8个additional先红，修复全集读取/类型校验后全部通过；重放拒绝且钱包不变。独立默认all会再次验证该用例及既有读取门禁，未返回前不登记全阶段PASS。

金额金样增加Golden筛选及可选focus=golden：十二案例、F06/F12两个中间快照、T2V/I2V及合计守恒，保留成本未知和未入账null。使用精确事实类型/行数防止开放案例掩盖重复Usage与零金额流水；独立Python校验与27种篡改测试见[金样文档](./video-gateway-vid-g5-golden-amounts.md)。完整G5验收状态仍独立于金样结果。

本轮默认all隔离MySQL/race通过（320.199秒），源码`55dd5e43872be73434fd87275c9761ff6747ecb3b914a67ad46a4410cd4ad1bf`，见[金样检查点](./evidence/video-gateway-vid-g5-goldens-checkpoint.json)。独立JSON校验不替代完整阶段兼容或验收。

提交恢复增加`Submission`筛选及VIDEO_GATEWAY_G5_TEST_FOCUS=submission定位模式：原事件租期、过期恢复、迟到ID不回退、期限前/等于/后各100回执、取消增版本、错误claim/请求/Provider、回滚/断连、空ID失败关闭。基础结果见[提交恢复检查点](./evidence/video-gateway-vid-g5-submission-checkpoint.json)。新增真实MySQL行锁等待跨期限、事务尾部跨期回滚、T2V/I2V×三种RPC回执/恢复顺序、拒绝审计幂等与不可改删、同ID异状态、SQL非法审计和合法对照、已settled/released后的重放与拒绝。每次新增用例后须重跑默认all；局部通过不等于全G5验收。

本轮默认all隔离MySQL/race、完整1..77迁移与重复up/保留down/re-up通过（248.789秒），源码清单及边界见[提交回执补强检查点](./evidence/video-gateway-vid-g5-submission-hardening-checkpoint.json)。

追加调账用例纳入默认all及可选`VIDEO_GATEWAY_G5_TEST_FOCUS=adjustment`：取消返还后10→10.25→10.15的独立金额样例；同序号100并发只修正一次；四个写点故障整体回滚；同序号异值、同人复核、跨Project、缺钱包动作拒绝；T2V/I2V已结算调整保留原价与成本；原消费流水不能冒充调整；历史复核主体停用不改变已有事实；不同序号100次0.2元扣减10元只成功50次，不透支或动冻结额。定向已通过基础两组用例；扩展矩阵与默认all的结果须以最新检查点为准，不能引用前一源码测试当作本轮通过。

上述已建立调账用例及默认all在源码`da22d30db5c30e19a8b0979a3aa0bcfdef32ccefa03ff2bb45e479908f9d79e1`通过（296.912秒），见[调账检查点](./evidence/video-gateway-vid-g5-adjustment-checkpoint.json)。未建立的金额溢出、跨钱包/重复资金引用及等行数缺关联反例仍为待验收。

调账后续边界已新增：最大合法余额999999999999.99999999成功、溢出及非法金额拒绝；跨User/Project/Key、未引用的外部钱包新流水和重复资金引用拒绝；Usage/资金/Outbox各一条且余额链正确时NULL关联仍失败；Outbox字段污染含数字version/sequence_no错误值及同类型金额错误。额外foreign aggregate漏检先红后修复，共享领取器也按video_字面前缀保护pending及过期publishing，含状态/租约不变及Chat/Image可领取对照。按最新完整性检查点核验，不回填旧检查点为已覆盖。

上述后续边界在默认all隔离MySQL/race通过（310.805秒），源码`6c40acb08a2bafd163662a1aa9de836b3d7d52b8dfc3611267ba5b395598e894`，见[完整性检查点](./evidence/video-gateway-vid-g5-adjustment-integrity-checkpoint.json)。领取器对照不是Chat/Image完整业务回归；完整G5验收仍待完成。

跨门面生成幂等用例纳入默认all及可选`VIDEO_GATEWAY_G5_TEST_FOCUS=facade`：显式创建后自动重放不新增Quote；权限已撤销不先写Quote；原输入pending_delete且报价resolver不可用时返回原三轴；同归属别名成功但不替换原绑定；跨User/Project/Key、缺失别名和伪造SHA拒绝；双别名与混合门面各100并发只创建一个生成请求；自动Quote遇到Key过期、余额不足、Hold写点故障整体回滚。旧G2门面单元测试继续执行；结果必须以本轮源码检查点为准。

独立审查追加反例：在Request读取后由另一事务完整提交pending/HPC，自动重放不得返回混合三轴；只转发Reserve的包装器不得静默降级并先写自动Quote。二者已在隔离MySQL先红，随后以单条JOIN和显式自动协调合同修复，最终默认all结果单独记录。

跨门面及上述追加反例已在默认all隔离MySQL/race通过（306.021秒），源码`5677e594100b0430580992629a8bfe9dc550715b2dced8332f24fc0f57828ad9`，见[本轮检查点](./evidence/video-gateway-vid-g5-facade-replay-checkpoint.json)。不等于完整G5验收。

执行待核对扩展筛选新增`Unknown`，定位可用VIDEO_GATEWAY_G5_TEST_FOCUS=unknown，最终仍需默认all。覆盖12个T2V/I2V异常组合各8次重试、四处事务故障、100重复安排、completed晚到冲突不重开、Callback原子性、断连与版本/输入读取竞争、6秒/5秒冲突及正常未提交取消不产生副作用。实际证据见[执行待核对检查点](./evidence/video-gateway-vid-g5-execution-reconcile-checkpoint.json)，不是完整G5验收。

取消扩展实际结果见[取消检查点](./evidence/video-gateway-vid-g5-cancellation-checkpoint.json)。增加8个T2V/I2V接受/拒绝/不支持/迟到成功组合、100取消意图CAS、完整Gateway与先退款竞争、原RPC在途重试/取消后绑定、14种Cancel/Poll确认反例、零成本不能单独退款、双取消回复两种顺序、相反成功回执与旧终态保护、冲突后不得扣费或继续读取、释放completed/checked后租约过期回滚。定位可设置VIDEO_GATEWAY_G5_TEST_FOCUS=cancel，但最终本切片回归使用默认all，不能用聚焦运行代替全量。

最新释放扩展筛选为`^TestVideoG5(Reserve|Usage|Cancel|Media|Settle|Release|Compensation|Delivery|Reconciliation)`，以下旧筛选描述仅对应各历史检查点。新增明确失败/审核拒绝/显隐标识失败×T2V/I2V×100并发，未知标识/派生/归档失败禁止释放，通用事件与原始原因篡改反例，以及10个释放事务故障和Worker恢复重放。实际结果见[释放检查点](./evidence/video-gateway-vid-g5-release-checkpoint.json)，不能解释为12种矩阵或完整G5已经验收。

交付扩展后的实际筛选增加`Delivery|Reconciliation`。本轮正常T2V/I2V各100交付、统一补偿发布/完成、11个发布故障点、读取/发布末尾过期、子资产保全、三轴矛盾历史、额外Attempt、回调错绑、旧Ledger降级以及旧G4事实共存回归均有测试。检查点见[交付/对账证据](./evidence/video-gateway-vid-g5-delivery-reconciliation-checkpoint.json)；不代替未完成的其他结果矩阵、调账及全阶段验收。

补偿扩展后的实际筛选为`^TestVideoG5(Reserve|Usage|Cancel|Media|Settle|Compensation)`。新增100认领唯一租约、8次失败/崩溃回收、旧围栏和跨请求拒绝、人工有效双主体/追加审核及SQL旁路、未闭合completed拒绝、租约中途过期回滚、P/C Outbox原子补记及Worker财务恢复；见[补偿检查点](./evidence/video-gateway-vid-g5-compensation-checkpoint.json)。统一交付/complete、其余矩阵及完整对账仍未完成。

前置：VID-G4最终合并证据已回填，VID-G5五项本地人审合同已批准。预占、Usage、结算/释放、补偿、交付/对账、金额金样和旧Chat四组MySQL兼容均已有切片执行证据；完整阶段仍待当前源码独立QA与最终SOURCE_STATE绑定。本节不是阶段PASS报告。

历史Usage/取消与结算检查点当时使用筛选`^TestVideoG5(Reserve|Usage|Cancel|Media|Settle)`及Linux race，覆盖两类任务各100次取消、11个释放写入点故障、取消/提交权竞争、追加事实与终态保护，以及额外Usage、错误Outbox、已有Attempt/submitting历史不得伪记零成本。正常结算切片另覆盖两类任务各100并发、8处写入点回滚、媒体中途过期、无实际消费的伪终态、完整Outbox重放、确认摘要及独立分母对照。见[Usage/取消检查点](./evidence/video-gateway-vid-g5-usage-cancel-checkpoint.json)和[正常结算检查点](./evidence/video-gateway-vid-g5-settlement-checkpoint.json)；它们仅为历史增量证据，当前运行器筛选与完整回归入口以本节顶部说明为准。

- Quote消费、Hold、冻结流水、请求关联、Task/Input租约及held Outbox同事务；每个写入点注入故障均整体回滚。
- 同请求100预占、100结算、100释放、settle/release相反终态竞争；同钱包100不同请求无负余额/冻结额。
- Quote重复/过期/越权、I2V输入hash/version漂移、输入审核拒绝前置零Hold/Queue/Provider、余额不足均失败关闭。
- 生成指纹与Quote原指纹分离；跨用户/Project/Key和两类门面幂等不改写归属，权限失效不泄露旧结果。
- 逐项验证12种结算/释放结果：成功、明确失败、审核拒绝、双标识失败、归档失败、结算失败、未知、queued取消、Provider接受/拒绝取消、迟到成功及Usage冲突。
- Outbox与对应事务共同提交、每项事实唯一且低敏，dispatcher关闭，重放不重复扣费/释放/交付。
- 补偿唯一、六态、version_no CAS、租约、8次上限、dead停止、人工核对不抢占活跃Worker；只能用持久化事实，不重新调用Provider。
- 未settled、安全版本不完整、活动补偿、争议/保全/删除态、非零差异、未闭合adjustment全部不得交付。
- 对账覆盖17类事实；验证请求/Sale/Hold/消费金额一致、净释放H-S，以及现有“全额解冻H再消费S”的真实流水顺序，不误把解冻H当净释放。
- maker/checker不同主体，adjustment缺钱包动作时仍失败；T2V/I2V各自和合计零差异。
- 运行完整隔离MySQL迁移、重复up、保留down/re-up、单元/Repository、故障注入、金额金样、Linux race、全量Go/vet/mod verify/gofmt、Python证据、敏感扫描与Chat/Image兼容回归。
- 独立QA/PM/Standards/Spec通过且P0/P1/P2=0；人工FINANCE_REVIEW不能由AI代签，VID-G6始终未开始。

详细矩阵见[开发合同](./video-gateway-vid-g5-billing-outbox-reconcile.md)，12个候选金额金样及五项已批准的本地合同见[人工财务审查包](./video-gateway-vid-g5-finance-review.md)。未执行的候选金样不能标记PASS。

## 视频网关 VID-G6 HTTP与显式准入验收（开发中）

`project-key-idempotency`专项验证视频Key签发/轮换/吊销重放、Secret只一次、同键异意图、严格结果Key/scope/audit完整性，并回归Project grant和旧Chat审计。schema107新增审计摘要SHA-256和严格双字段结果约束；真实MySQL还覆盖首写错误摘要整体回滚、issue结果已吊销、rotate结果错绑来源、revoke结果仍active四类损坏事实。COMMIT未知和全部写点故障仍是后续门禁。

`inline-i2v`专项在schema108真实临时MySQL和loopback HTTP上验证：无文件T2V、唯一PNG/JPEG的I2V、项目权利接受、服务端Target、规范化/审核、G5 Quote/Hold/TaskInput、同键同文件重放、同键异文件409、100并发唯一事实、空文件/伪扩展/外部URL字段/重复文件拒绝，并回归原I2V事务、平台上传和T2V门面。无权利与T2V→I2V同键均在任何inline会话前拒绝；Handler在读取前按用户请求字节预算限流且只保留一份正文。读取中断、Store未知结果、生成失败后的输入清理及COMMIT未知仍须补齐。

`queue-admission`专项在schema109验证G6关闭态queued容量：同用户第1/2成功、第3个429且只形成两套Request/Task/Hold；100个不同意图同起跑精确2个赢家、98个user拒绝。Quote/输入/权利复核后，同一事务先形成不可见的Hold/Task/事件/Outbox暂态事实，再在提交前执行末尾门闩；拒绝时全部回滚为零事实，旧Quote错误不被掩盖。平台/OpenAI错误Envelope、Retry-After、Project10、global100、门闩故障及末尾写失败全快照均进入最终all。

`running-admission`专项使用同一schema109门闩和原Task账本验证本地Fake执行名额：用户1、Project2、逻辑模型2，容量满保持queued、Provider调用0且Hold不释放；两个Worker同起跑用户名额唯一。真实Provider hard cap、分布式TTL和幽灵租约仍属G7。当前专项通过不替代其余终审缺陷或完整G6验收。

`budget-admission`专项复用G4预算表并参与G5同连接事务，验证Project hard低于/等于0.50、disabled、API Key hard/soft、重放唯一、非UTC注入时钟的UTC expiry、取消后released、平台/OpenAI 42920/50321映射，以及100并发精确1个预算赢家。成功settled、Provider失败release、后置故障/COMMIT未知、跨日/月真实锁等待和补偿仍须补齐。

`project-grant`专项验证100并发首次授权、CAS、revoke/regrant、停用态撤销、未知字段/Project SK/撤权重放拒绝、加密审计及零财务变化，并回归Key、准入和目录。终审增量还逐项覆盖MFA失效、空/控制字符reason、前置/事后审计写失败及整笔零事实回滚；Key生命周期幂等由独立Project Key专项覆盖。

`project-key`专项要求真实JWT、HMAC及临时MySQL，验证显式true、allowlist、发布快照七键及生效时间、Project grant锁定、列表回显/Secret不泄露、轮换从数据库重建全部配置、事务外篡改/非Project Key拒绝、撤权竞争和旧Key false；同时运行原Key三个单测、目录回归和视频准入完整矩阵。Key写幂等和grant管理API仍是后续门禁。

`model-publication`专项必须验证发布/下架/回滚、原键重放、历史无native版本拒绝、当前价格/native摘要、Provider Submit=0、财务不变及旧G5旁路关闭；随后强制在独立77号库运行原Chat发布。父子源码副本hash必须相同。默认模型并发和提交未知仍是后续必测项。

草稿详情与接管专项增加实际版本读取、历史URL脱敏、缺摘要400、旧摘要409、读取后变化、原ID/发布/财务保持、源摘要持久化、接管重放、重复接管拒绝、撤权读取/重放拒绝，以及有/无视频handler×有/无无关query的旧Chat详情兼容。跨管理员竞争、全部读取故障和完整发布仍须单独补齐。

受控模型草稿`model-draft`专项验证真实JWT/MFA、创建及100并发重放、更新/CAS、严格完整定义、撤权、原因AAD、旧视频删除拒绝及财务事实不变。还须补原应用装配、详情、历史接管和发布管理矩阵；局部通过不算完整G6验收。

模型合同基础专项`model-contract`包括快照七键保留/非法合同拒绝、原服务解析器、真实MySQL持久化与CHECK、公开目录真实HTTP及4项历史列表回归，共9项必测。102迁移须在同一源码副本完成up/down/up；不以仓储基础验证替代管理员发布、CAS、MFA、审计或完整阶段验收。

模型公开目录专项：`TestVideoG6CatalogPublishedHTTPMySQL`必须在真实临时MySQL和loopback HTTP运行，验证发布快照、公开能力/操作、草稿隔离、失效快照、SK资格撤销、缺依赖关闭及图片字段兼容。`catalog`焦点同时要求原`/v1/models`四项测试实际RUN/PASS。源码与证据尚须统一冻结，局部通过不得替代完整VID-G6或Chat/Image回归。详见[目录合同](./video-gateway-vid-g6-model-catalog-contract.md)。

调账`admin-adjustments`包含实际双JWT/MFA、冻结金额、两个待审批序号、100并发唯一执行、资金追加及原账不变/全对账、三种原因密钥变化重放503。G5兼容不共用G6库名：该范围强制运行`legacy-adjustments`临时专用库子阶段，保留原DSN隔离守卫；两部分均RUN/PASS才算整批通过，SKIP不能替代兼容。debit/余额边界、全时效/撤权、各写点回滚、提交确认丢失和审批篡改仍须完整证据。

归档HTTP `admin-archive`专项覆盖严格关闭/映射、T2V/I2V实际请求、停用主体追踪、原完成回执重放不再OpenContent、唯一命令/前后审计、I2V Head失败unknown/pending及新键从原事实恢复。`admin-archive-safety`另验正常/pending审核拒绝真实入库、普通真实moderating失败来源与pending不得伪造来源、围栏与失败回执原子收口、原钱包/Hold/Quote/Usage不变。完整100并发、死锁竞争及审计篡改等证据仍待，不能将局部PASS当成全阶段验收。

归档私有执行器`archive-executor`专项覆盖普通/待核对×T2V/I2V四种恢复，使用真实仓储、原G4媒体安全链和实际Fake私有对象；原主体停用仍可由认证管理员恢复，Provider Submit计数不增加，最终仅执行succeeded且billing=held/delivery=pending，I2V租约未释放。成功断言同时检查六角色对象/hash、Request精确状态/版本变化及其余资金事实不变。还须覆盖最终释放围栏后权限自然到期、整个成功事务回滚，以及存储读体期间接管/旧代次写入移动删除拒绝。当前不是HTTP命令验收，安全失败、确认丢失、部分资产及重放收口仍待完成。

归档共享围栏使用`archive-fence`专项：原Task认领100并发唯一、旧Worker和仅知新version的无证明写入拒绝、pending下技术phase不回退Task、不可跳级、接管代次及退让追加事件。虚拟时钟验证代次不等于真实等待；另用100ms实际租约和独立持锁事务验证锁等待跨期必须冲突且Task完整不变。无活动围栏/nil证明释放也必须冲突而非panic。该批同时回归管理轮询、回调及原G4 Fake流程；尚不能证明完整归档HTTP、成本/媒体成功门禁或所有资产IO围栏。

管理轮询`admin-poll`专项：T2V/I2V实际回环HTTP，原用户和Key停用后仅查询原已提交Provider；同key不重复Query，超时保留pending_reconcile，后续成功/明确失败观察追加原事实而不回退状态或Submit。需要验证前后审计唯一、运行中命令CAS、晚到冲突和输入租约保护；数据库重试只在最外层，内部保存点保留1213/1205错误链而不重试Provider。真实死锁/提交未知/过期命令善后/权限时效/100并发尚待专项证据，见[管理轮询合同](./video-gateway-vid-g6-admin-poll-contract.md)。

输出解除隔离采用`admin-output-release`受影响专项：两个不同合成管理员实际JWT认证、申请仅202不改隔离、同人403、客户端伪造checker字段400、错资产404、复核200及唯一消费、原状态/六资产元数据/财务和Store不变；同时包含上批隔离保存/额度快照与自然跨期屏障补强。必须覆盖最后checker等待跨maker资格期限后的整事务回滚，不能伪造无JWT的maker调用者代替持久化资格复验。SQL97、故障/竞争和独立回执未完成前不得签完整解除或G6通过。详见[解除隔离合同](./video-gateway-vid-g6-admin-output-release-contract.md)。

输出行政隔离增量采用`admin-output-quarantine`专项，11个必选RUN/PASS且无SKIP才算该批通过。必须验证原六角色安全事实不改写、窄SQL凭据/旧CHECK保护、单资产CAS、同键重放、目标停用后合法管理及旧临时/长期签名失效。保存重放需对同键和新键复验当前隔离且在Store Head前拒绝；最终详情查询等待至动态权限自然到期须回滚资产、prepared/completed及两份审计。93502已复现保存重放漏洞，不是PASS；缺陷与当前边界见[输出隔离合同](./video-gateway-vid-g6-admin-output-quarantine-contract.md)。完整六角色/保护组合、保存额度快照、SQL篡改、100并发和隔离清理仍须补齐，不能由该专项替代全G6验收。

平台资产删除增量使用`asset-delete`专项：实际JWT/SK路由、严格版本JSON、根组/单子粒度、父/兄弟/审核副本不受单删影响、根后续收敛、长期副本与原财务/容量不变、v1隐藏和平台部分删除投影。必须分别验证错误计划在SQL写入侧和合法失败记录读回侧被拒绝，后者不能由SQL保护替代；计划ref/hash/size漂移时Delete调用不得增加。52412写入侧及基础HTTP已过，增强读回与完整竞争/故障矩阵待验收。

历史迁移必须另跑`save-migration`：运行器装配89版，`TestVideoMigration91HistoryMySQL`执行90/91；四种旧保存、19表原列、9次ALTER后中断、结构定义、合法后继回滚及错身份拒绝需全部实际RUN/PASS。普通最新结构回归不能替代该测试，native环境的SKIP不能算通过。30701对应增强范围通过；37162进一步证明旧NULL未完成计划的新旧键恢复拒绝均为零业务写入。九个ALTER后SIGNAL不等价于所有触发器空窗或真实网络中断。

保存新尝试增量：`save-reattempt`专项验证schema91、旧键终止事实不漂移、新键独立计划、旧清理重放、100新键唯一后继/五次复制/一次容量及原生成财务不变。39166基础4项通过；100竞争和更完整故障矩阵验证中，带历史数据和DDL中断重入仍待实现。

内容故障夹具修正：46587原97回归的`TestVideoG6ContentMySQLFinancialGate`失败，49021定向复现7种故障均未影响已创建cap。原因是测试替换app字段，而cap已绑定原Store。现GetContent前装配固定指针wrapper，之后仅用atomic切故障，保留原cap/OpenRange及全部断言，每例增加恰好命中一次的检查。修复后须重新验证，不用忽略失败或重建cap规避。

长期副本读取增量：执行`verify-video-gateway-vid-g6.sh`的`saved-read`范围，要求schema90及真实临时MySQL/Linux race。覆盖原临时资产自然到期和删除后五角色仍可读、v1保持404、原Key归属、无Key JWT及吊销、长短期共享2/4租约、签名/角色/存储权益拒绝前零Store、独立Store与影子对象不可替代。读取不改变原生成财务和保存容量；下载租约的运行写入单独核算。当前测试在补齐中，精确源码回执前不能宣称PASS；详见[长期读取合同](./video-gateway-vid-g6-saved-read-contract.md)。

未发布保存清理：88698的8项MySQL/Linux race专项通过。覆盖源/权益到期、五目标标记含未创建目标、原视频/审核副本保留、删除/确认失败、删完后数据库写失败、过期权益精确释放一次、保全/匹配用户资产/completed/错误归属拒绝，以及旧复制取消后迟到Background写被标记拒绝。69727两种伪过去资格SQL被错误接受，修正首次意图必须关联真实到期实体后反例要求1644通过。COMMIT未知测试先调用真实sql.Tx.Commit成功再返回确认错误，重放读取aborted；后续增加重放后额度、事件数和事务次数的强化断言，须纳入最新整体验证。

用户资产保存：43262的12项保存专项通过Linux race，HTTP首次201/重放200/源删除后保留长期资产，100个不同幂等键同视频只能创建一份用户资产与一次容量结转；用户/Project/全局/权益余额分别拒绝且不触发复制。独立Store及“源侧同名影子不能掩盖长期目标丢失”均测试；部分复制失败保留reserved并阻止源删除，恢复沿原计划；八表完整财务行不变。73869在asset_event写入屏障实际跨source/entitlement/JWT期限仍提交完成，6710提交前复核修复后三者均回滚UserAsset/Event/quota结转/completed，保留第一阶段计划。完整跨Task容量竞争、真实保存/删除并发、所有写点/COMMIT未知和cleanup/abort尚未完成。

平台短效下载：69646的9项必选专项及Linux race通过，覆盖JWT/SK正例、五个用户角色真实字节/hash/MIME、Range、无认证/跨Key/来源隔离、签名/期限篡改、旧版本拒绝，以及首片后吊销JWT仅返回1MiB并断流、租约回收。95714修复前同一吊销反例完整返回4054453字节，禁止将旧JWT仅转UserID后沿用。初次与逐片吊销查询均需受30秒/JWT到期上界约束，内存credential不得进入JSON。新增JWT自然到期、吊销依赖故障、六资产最早到期导致合法签名自然到期、HEAD与跨路径篡改测试待97564加强验证；不能以改expires但不重签替代自然到期证据。

生命周期增量：29308实际隔离MySQL及Linux race全3项通过。`TestVideoG6AssetLifecycleHTTPMySQL`经真实loopback HTTP核对21个精确字段名、根/父关系、null、跨Key/JWT/图片资产404、审核副本隐藏、保全、争议开启/解决、原G4拒绝形成隔离、撤权、真实delete_failed及原命令恢复。每次GET对比16张关键表完整行快照及Store调用计数。`TestVideoG6AssetLifecycleExpiryMySQL`使用数据库读回期限在G5初始对账后注入末段延迟，要求真实跨越thumbnail期限、content仍有效而can_download=false；未触发屏障或初次就已过期均不得算通过。24757负向对照去除修复后该测试FAIL，修复版PASS。原生Go的SKIP不是SQL证据，未覆盖的父关系损坏、数据库故障和完整生命周期矩阵仍需补齐。

媒体删除增量：73704的6项专项与73883的62项当时完整G6测试均通过Linux race，后者不包含新生命周期。测试验证五个用户对象删除、审核副本原hash保留、确认失败恢复、删除误伤副本失败关闭、完成状态数据库写失败后恢复、prepare内部30秒期限。数据库写失败不是COMMIT响应不明的等价证明；全保存/删除/下载竞争仍待验收。

取消增量：`TestVideoG6TaskCancelMySQL`覆盖T2V/I2V各100请求，要求1首次/99重放并通过原G5对账；同键异任务冲突且不改变第二任务。`TestVideoG6TaskCancelHTTPMySQL`覆盖两别名重放、严格无正文/query、JWT无Key任务正例、跨Key/JWT404、撤权后重放403、提交后202保留Hold及终态200 already_terminal无操作。两个关闭态入口由`TestVideoG6CancelHTTPClosedRoutes`实际HTTP验证。

`TestVideoG6TaskCancelRollbackMySQL`注入原G5最后Outbox写失败，要求任务、回执、Usage和冻结资金一起回滚，原键恢复及回执不可修改/删除；错Key直接SQL必须1644。钱包balance为可用余额，10元预占0.5后应9.5可用/0.5冻结，不能把总额当balance。`TestVideoG6TaskCancelSubmittingMySQL`在真实Fake Submit RPC屏障期间取消，要求仅意图及原绑定保留。

`TestVideoG6TaskCancelDatabaseRetryMySQL`以当前连接ROLLBACK加1213错误注入复现整笔事务失效；另覆盖1205。25398旧实现出现返回成功却回执0条，修复后86729要求两次回执创建尝试、最终1条且完整对账通过。这是实际MySQL事务回滚结合故障注入，不冒称自然死锁复现。G5共用函数变更须额外运行隔离`legacy-cancel`，不得以原生Go中的SKIP代替兼容证据。

真实媒体增量：`TestVideoG6PlayableMP4Probe`验证锁定FFmpeg合成MP4的hash和5秒/24fps/1280×720，原探测器9168b9实际误报1fps；修复改用轨道mdhd时基。`TestVideoG6MediaClockMatrix`覆盖不同电影/媒体时基、0/1版本、64位时长高位非零、未知/缺失/零/截断时钟、完整CFR表、采样累计时长、零时长及空首视频轨道。cbcf11额外复现三项缺口，修复后59227d视频单测通过。

`TestVideoG6PlayableContentHTTPMySQL`通过原Fake/G5生成、归档、结算、交付实际4054453字节可播放媒体；第二片Store失败必须200头后UnexpectedEOF，正文恰好原首个1MiB，无JSON尾部，租约显式释放且全财务行快照不变。55397初版专项通过，后续解析器收紧须重跑。静态回环浏览器已实际解码、拖到3秒并播放到5秒结束，390px无横溢出；该证据不是带SK业务浏览器/SDK端到端，也不等于慢连接或撤权/删除竞争全部完成。

下载限额新增`TestVideoG6DownloadLimitsMySQL`：两个App对象、两个已交付Task共100申请，要求2成功/98限流；释放、重复释放、过期旧连接不得影响新名额。`TestVideoG6DownloadRenewalRaceMySQL`必须覆盖有效续约UPDATE暂停提交、DB时钟跨旧TTL、新申请等待实际scope锁、最后active=2；77066旧实现复现第三名额，59779修复后通过增强窗口。只验证申请100并发不足以关闭续约竞态。

`TestVideoG6ContentHTTPMySQL`扩展为另一有效Key下载自己的已交付任务也受用户共享上限；429时Store Head次数不增加，释放一个名额后可下载。新增读取后人为到期应503，且普通成功、Head失败、写前到期后active租约均为0。`TestVideoG6ContentHTTPBandwidth`以真实回环4MiB测20MiB/s加1MiB突发；`TestVideoG6ContentHTTPLeaseDeadlineAndCancel`仅验证设置租约约束的deadline和取消后不读第二片，不冒充实际慢TCP写超时。COMMIT未知、Project第五路独立边界、真实慢连接和大对象全业务断流仍待补。

内容业务新增`TestVideoG6ContentMySQLFinancialGate`及`TestVideoG6ContentHTTPMySQL`：真实G5生成、归档、结算、交付逐阶段校验，未闭合404；通过真实Project SK HTTP测试MP4 hash/长度、200/206/416、If-Range、跨Key/JWT拒绝、子资产保全。服务分片覆盖撤权、对账破坏、Head hash/大小/位置不符、对象缺失、短读/超读/关闭失败、取消及范围上限；每项须由隔离运行实际RUN/PASS，不把本地MySQL SKIP当通过。`TestVideoG6ContentHTTPApplicationErrorSingleEnvelope`复现并防止503追加默认500双JSON，实际HTTP故障也必须第二次解码EOF。大于1MiB中途失败/撤权、固定删除竞争、完整财务不变、下载并发/带宽、浏览器及SDK仍待完成。

留存清理新增3项与原用例一起由77875跑完39项必选顶层测试，零必选SKIP：`TestVideoG6ImportReadyRetentionMySQL`证明ready起算7天及重放不续期；`TestVideoG6InputCleanupMySQLImportHTTP`证明保全拒绝后正文仍在、仅删副本、源过期后的严格六字段200及全财务行摘要不变；`TestVideoG6InputCleanupMySQLBoundTasks`证明两个真实I2V分别结算且以更晚安全截止保护，前1秒拒绝。该源码冻结在新增content之前，不可用作后续content通过证据。

实际输入清理：`TestVideoG6InputCleanupMySQLUpload`通过真实受控上传/complete、删除申请和虚拟时钟推进，验证原到期前原件/封存/规范化三类正文存在；到期后Fake对象实际清除、墓碑阻止迟到写、唯一000084完成事实和原容量控制cleaned_at。内部清理不启动后台进程，不测试真实存储。19359整轮36项通过，随后增强确认写失败窗口：必须先断言三类正文均存在，在清理事实INSERT钩子中确认三类正文已消失，再注入写失败并验证DB仍pending、容量未释放和无完成事实。之后确认读取错误/报告未清理仍不得成功，最终同目标恢复。不要将多个故障共享对象的旧结果表述为各自独立首次删除场景。

还须验证pending_reconcile/缺失安全时间、Input及来源保全/争议/隔离固定窗口、100并发、COMMIT响应未知、实际容量重新准入，以及历史回执完整当前权限/缺少完成事实矩阵。多绑定最晚截止、前1秒、导入只删副本和历史HTTP200已见上方77875子集证据，不等于异步存储围栏或完整G6通过。

输入删除HTTP扩展`TestVideoG6ImportHTTPMySQL`：真实SK来源导入并形成I2V预占后，测试跨Key/JWT404、空/null/零/负数/小数/重复/大小写别名/未知字段拒绝、保全409、六字段202及media_deleted=false、同键同CAS重放不续期、同键不同CAS409、原输入期限与TaskInput不变、财务/Usage/Outbox/Provider零新增。65545复现VERSION_NO接受及额外版本漂移后原键成功；须核验修复后两个反例都通过。JWT自有无Key输入、跨输入同键、缺失/重复幂等头、来源变更固定窗口与实际清理仍需后续矩阵，不登记完整DELETE通过。

任务参考图专项82150通过读取中取消后任务/冻结资金/租约不变，以及MaxOpenConns=1的完整Fake I2V Submit/Poll/归档/结算/交付。上下文取消由测试Store协作响应，不声称硬中断任意实现。后续HTTP删除代码变化后仍须完整回归，不能只引用此专项。

`TestVideoG6TaskReferenceAfterDeleteMySQL`使用真实G6权利接受/G5 I2V预占、原规范化PNG和Fake私有Store，先申请pending_delete再通过NewTaskLedger执行原Fake异步链、结算与交付。必须证明冻结InputVersion/hash不变、Provider Submit一次、仅安全终态释放租约、正文仍保留。20491先缺入口失败，99398整轮35项通过；随后新增“发布新版本移除I2V后Load拒绝且Store读取增量0”的反例，结果独立记录。还须补导入来源、单连接嵌套Advance、IO跨到期/取消、pending_reconcile及固定读取/清理竞争，不把一次成功链当成完整矩阵。

运行器执行期间不得编辑`verify-video-gateway-vid-g6.sh`；Bash会继续从该文件读取后续语句。42810实际34项通过，但宿主脚本被修改导致后续`ts.up.sql`误执行，整轮exit1，必须保留失败并完整重跑；99398冻结脚本后exit0，不用源码测试通过掩盖运行器失败。

延迟删除基础：`TestVideoG6InputDeferredDeleteMySQL`从真实G6 I2V权利接受和G5预占形成TaskInput，验证pending_delete只递增资产版本、原键重放不续期、同键不同CAS冲突、新Quote拒绝、原绑定未改写、SQL清理被活跃租约阻止。`TestVideoG6InputDeleteReplayRRMySQL`先建立RR快照，再由独立连接提交删除赢家，原键须读到同一凭据。另以独立连接追加版本漂移验证当前执行读，并注入凭据读取错误验证原错误保留。33908缺入口红例，85658首项通过，51907三个增强反例失败；修复后需重跑。完整执行参考图读取、HTTP、并发绑定/删除、保留窗、实际对象清理和回滚兼容仍未验收。

平台任务/事件查询增量：`TestVideoG6TaskReadHTTPClosedRoutes`覆盖五条GET默认关闭503；`TestVideoG6ImportHTTPMySQL`在真实导入I2V预占后验证三种ID查询一致、原reserved/held/pending及0.75元预占、跨Key/JWT404、D-95列表、事件公开ID及轴、过滤后的total、大小写/未知/错误轴事件隐藏与原事件不变。数据库必须先拒绝任意诊断JSON，不能为测试过滤而关闭SQL白名单。

`TestVideoG6TaskReadMySQLSettlementSnapshot`在身份读取后由独立连接真实执行G5结算与交付，查询须返回succeeded/settled/available、0.50元结算/零当前冻结且can_deliver=true。3079曾复现旧RR拼接，42412该例通过；该轮整体仍因事件测试夹具错误FAIL，不作整轮PASS。`TestVideoG6TaskReadMySQLFinancialLifecycle`验证held的settled_amount=null、实际取消后的零结算和0.50净释放、真实完成交付、媒体删除元数据后的账单保留和v1隐藏；它不证明对象正文删除。`TestVideoG6CompletedListsMySQLConcurrency`还须以独立期限执行平台详情列表100并发、共享钱包对账和越过末页total=2/items=[]。新增项结果以实际运行回执为准。

39711四项专项已通过。随后新增事件查询100并发与真实取消竞争，以及仅在数据库读取边界注入缺Link、错Hold身份、Hold/Link结算不一致、settled缺金额、未知Hold状态和holding含结算额，不改写原资金事实。89610及52429分别复现损坏结算与四项Hold拒绝缺口；52429也复现历史exception进入视频事件total。修复后须重跑全部，HTTP另检查精确23键、显式null、未知/重复/越界query与Task/request公开ID混用404。不能用Go测试在无隔离DSN时的SKIP证明这些MySQL用例通过。

`TestVideoG6ImportHTTPMySQL`使用测试专用导出夹具及真实路由/认证：来源候选七字段，其他Key空页；导入储存读取暂停时同键返回202，释放后首次201及重放200，五字段中仅input_asset_id允许null、处理期限非零且不续期。检查导入前后八类事实数量、全库Outbox和钱包余额/冻结额不变；后续0.75合成I2V报价/预占需新增唯一Request/Quote/Task/Hold/冻结流水/钱包关联/held Outbox，不提前创建Usage。直接读关联行验证holding、freeze/out金额及held载荷。测试导出文件必须只出现在TestGoFiles，不能进入生产GoFiles。

`exerciseVideoImportScopeRevocation`确认真实RR，在目标Put后且发布首个api_keys一致性读完成时，由独立连接删除精确scope；断言RowsAffected=1、已撤权、404、目标rejected/墓碑及清理完成，最后只恢复合成夹具授权。不允许以顺序撤权或模拟鉴权失败替代旧快照场景。

来源导入增量由`exerciseVideoInputImport`在真实IMG-G5来源生成/结算后执行：首次100并发只创建一条导入回执和一个独立规范化InputAsset，不伪造UploadSession；原键重放、异源冲突、跨Key拒绝、目标Put写成后响应未知恢复、源版本漂移清理目标且原图可读、财务/任务计数零新增。检查目标墓碑及cleaned_at、成功后实际字节占用，不能只看返回码。还须补全目标保全解除后的原命令清理、旧租约/提交未知、源撤权当前读、上传混合额度和完整HTTP矩阵；未执行项不得登记PASS。

来源图片增量：`TestVideoG6SourceImagesMySQL`使用Fake640px图片和真实IMG-G5处理、归档、Reserve/Execute/Reconcile，必须证明媒体已入库但未结算时候选为空、结算后只返回主图、JWT不能读SK来源、allowlist/all均不能绕过撤销的图片模型scope、保全图片隐藏且查询不增加Provider调用。回环HTTP用例验证D-95空列表，关闭态503。不得以手工写settled或仅模型对象测试代替该链。from-image-asset、完整来源生命周期/规格/并发和正向HTTP候选矩阵仍待补齐。

输入元数据增量：`TestVideoG6InputReadHTTPClosedRoutes`必须验证两个GET默认503；真实上传HTTP用例增加SK详情与D-95列表、JWT无Key上传/查询、互相跨Key404及total隔离。`TestVideoG6InputMetadataMySQL`覆盖十键DTO、真实NULL规格夹具、分页空数组、跨User/Project/Key、隔离历史、撤权后拒绝、读取驱动故障不可伪装空页/404、legal hold与到期不可引用。旧夹具已有T2V Quote，零副作用须比较调用前后总数，不删旧事实。41837复现Count投影导致503及保全输入报价未拒绝，修复后需重新验证原反例。完整来源图、全生命周期与竞态矩阵仍待后续补齐。

上传增量使用真实临时MySQL和G4规范化，Store/Safety为外部边界Fake。`TestVideoG6UploadMySQLSealCompleteReplay`验证不可变封存、能力失效、唯一输入和跨Key404；`TestVideoG6UploadMySQLRejectAndCancelRace`验证hash不匹配及取消抢先；`TestVideoG6UploadMySQLInterruptedRetryAndConcurrency`验证取消/超时恢复、100并发和发布鉴权驱动故障。56971复现临时故障误转rejected；修复后必须观察verifying、同键完成、原对象保留和一条InputAsset。`TestVideoG6UploadMySQLRecoveryFences`新增发布INSERT的1213/1205驱动故障及租约接管后迟到旧执行者，不可将驱动注入写成真实死锁复现。

`TestVideoG6UploadHTTPMySQLRoundtripAndI2V`使用两个实际回环HTTP服务（Molin和Fake对象存储），执行真实SK/JWT、签名PUT、complete/重放/取消、权利接受及I2V Quote/G5 Hold；断言上传阶段财务/任务零写入，生成后恰好一组请求/Quote/Task/Hold。每次从空DTO解码，核对八个必需键/null，负例同时断言HTTP、平台数字码、error、request_id和data:null，防止残留字段假PASS。该场景不调用Provider；v1 multipart与双SDK现由独立专项覆盖，浏览器与全阶段验收仍需另证。运行器在测试开始前输出不含证据目录的复制源码树SHA256；事后源码摘要必须单独标注，不冒充运行摘要。

I2V事务增量：真实G4规范化640px PNG、G3输入事实及G5钱包；缺项目接受拒绝，合法声明形成原Quote/Hold/Task、唯一TaskInput租约及两条权利声明；预检后政策退役拒绝第二个请求。部分G6装配不能降级、同owner错Quote声明按1644拒绝、安全终态原输入删除后纯账本重放均须真实MySQL反例。73397先失败，20067默认19项通过。该检查点不包含I2V实际HTTP、v1 multipart、输入上传与完整日期/故障/100并发矩阵，不能代替完整阶段验收。

权利增量使用000079空schema及显式合成条款：全局政策、Project接受GET/POST必须真实loopback JWT/SK验证；首次接受/原键重放、SK代签、伪造accepted_by、跨Project拒绝不写第二条事实。MySQL100首次接受唯一且原期限相同，回执UPDATE/DELETE禁止，合法退役与合法迁移中正文/标题替换拒绝分别验证。接受过期、政策自身过期、无active及版本升级须保留历史回执valid=false；新版本新键明确接受。政策损坏/DB故障仍失败关闭，不能把无授权历史当有效声明。生成事务关联和I2V完整权利矩阵未执行前不得登记完整I2V通过。

新增报价复验：真实HTTP同键异报价意图、未知/跨Key/已消费/过期Quote，断言HTTP、平台数字码及error类型；首次100并发显式Quote使用统一起跑，不预先创建赢家；双连接固定RR旧快照必须返回同一已提交Quote且只有一条事实。72073已复现两项缺陷，修复后必须重新执行，不能引用19132的旧13项通过。

新增列表复验：真实loopback HTTP五字段VideoList、asc/desc稳定游标、同秒公开ID排序、空数组及null首尾、严格limit/order/after、跨Key游标不泄露；关闭态必须503。锁定双SDK进一步覆盖创建任务/完成夹具在单页可见及删除后隐藏；跨Project、并发新增/删除全矩阵仍待最终阶段验收。

完成态不能用queued替代：`TestVideoG6CompletedListsMySQLConcurrency`通过真实G5预占、Fake异步执行、结算和交付形成同钱包两个completed任务，再以asc/desc混合100并发查询。39496复现异常；32697探针记录52次1213后移除探针。固定页内Task锁顺序后必须让全部100次仍返回两个completed，不容忍偷偷降为in_progress、少项或吞错。空grant＋未实名的HTTP负例同时断言400与error.code=70001。

以原Goal和阶段规划完整G6清单验收，不以当前已实现用例缩小范围。运行器`infra/scripts/verify-video-gateway-vid-g6.sh`只建立临时MySQL，使用固定镜像ID、无宿主端口与内部网络，拒绝缺失隔离授权；源码先复制到容器再编译。必须核对指定顶层RUN/PASS和容器退出码，SKIP或零匹配不能通过。

内部回调增量：`callbacks`范围必须覆盖固定HMAC向量、签名域/时间/nonce/严格五字段、关闭503、真实回环HTTP原G5 Task绑定、首次100并发唯一与既有100重放、无效签名不抢占、同事件异body及同nonce异请求409、跨任务同外部事件号及128字符事件号、首个成功回调合法双步、fetching与pending_reconcile迟到终态忽略、人工核对阶段旧事件重放无新事件。`CallbackAtomicHTTPMySQL`验证nonce写入后失败回滚及真实COMMIT成功但确认丢失的原ACK恢复；新用例必须实际RUN/PASS，不能以编译跳过证明。详见[回调合同](./video-gateway-vid-g6-callback-contract.md)。

管理员只读增量：`admin-read`范围验证真实JWT、ai_gateway:view、AuthService双MFA、28字段、停用目标可读但不可冒用目标身份、业务整行/事件/补偿/租约/Store及Provider不变。必须拒绝把普通JWT证明配上管理员UserID，并区分MFA缺失403/40031与MFA仓储故障503。原bool认证接口保持兼容。末尾权限/MFA跨期、吊销及完整管理面矩阵仍须补齐，详见[管理只读合同](./video-gateway-vid-g6-admin-read-contract.md)。

管理任务列表新增必测：D-95/严格AND过滤、分页空页total、与用户列表混合100并发、成员SELECT后另一连接取消的整页重试、两用户四任务两不重叠页的跨钱包锁序。跨钱包绿灯必须两个HTTP200、全部原任务及CanDeliver=true、财务不变且无真实1213；夹具政策唯一冲突或短幂等键失败不算钱包缺陷复现。

管理输入列表新增必测：21字段/null、原UploadSession或图片Task/Request来源Key、严禁对象位置/使用许可、停用目标及隔离历史可读、组合过滤和空页、无权限/MFA负例、零财务或外部调用。真实JWT-null、已删除/过期来源、未规范化null、RR并发一致性和损坏/故障503尚需补齐，当前不能签署完整验收。

管理输出列表新增必测：28字段/null、六角色包含审核副本、公开父ID同Task/Request/owner、严格AND过滤、实际原删除后五个普通资产及审核副本历史保留、目标停用可读、MFA拒绝及资产/财务不变。错父关系/Key、真实JWT-null、SQL故障与完整隔离争议矩阵仍待补齐。

输入隔离新增必测：四允许状态与全部禁止状态，22字段、来源/Key完整归属、当前safety_review/MFA、原因领域隔离、版本/CAS/幂等、保全与原审核/hash/规格/期限不变；在途TaskInput和资金不变、隔离后Provider前复验拒绝、上传/导入发布竞争、审计及回执原子回滚。当前ready/保全/权限/重放/原绑定及Provider前失败关闭已有局部验证，其余矩阵不得预标PASS。使用admin-mutations受影响批次，I2V原G5夹具预占为0.75（0.15×5），T2V为0.50，禁止为适配断言修改计费规则。

管理员取消新增必测：停用目标仍由真实task_manage/MFA管理员安全取消，用户入口不能获得管理grant；reason/version_no严格JSON、CAS与同键意图冻结、31字段与原业务request_id；两条原审计、加密原因可在原AAD下审阅、回执不可修改；已提交只记意图且资金/租约不变，原终态不退款；审计/命令写失败及COMMIT未知、100并发、I2V与提交竞争。原因组件覆盖随机nonce、Actor/Task/版本/命令错绑及密文/nonce/版本/摘要篡改；普通JSON不序列化信封。原G5 CancelBeforeSubmit兼容测试必须另跑，不能仅依赖管理入口绿灯。

运行汇总新增必测：独立reconcile_manage权限、JWT/MFA、无query/body、六字段及八位金额，真实预占增加0.50与一条Outbox、原取消释放冻结额且三条Outbox仍保留，读取不执行任务/补偿/派发。聚合读取故障返回503/data:null；未知Hold状态不得漏计为零。补偿及死信非零分类、两连接快照与完整异常关联矩阵须单独验证，不能以计数全零代替G5逐任务核查。

当前新增矩阵包括：IAM真实授权/deny/过期/直接角色/100并发，Project/Key视频显式授权与模型发布范围，RR旧快照下另一连接提交deny后拒绝，七键模型合同与null/缺项区别，仅资产商品、精确权益类型、reserved耗尽、父资产和会员时间，以及父资产撤销旧快照。HTTP首次纵向切片测试使用真实SK解析和MySQL，验证T2V multipart创建/查询、100次同键重放、异意图409、唯一Quote/Hold/Task和固定0.50元合成预占。当前运行结果以逐轮证据为准，不能把未执行的新矩阵标为通过。

历史待办已由2026-09-01最终本地候选统一收口：平台JWT/SK与202门面、跨门面幂等、完整路由及管理负向矩阵、I2V不可变上传、预算/queued生命周期、双SDK、浏览器seek、Chat/Image与G5兼容均纳入当前测试与证据。历史增量段落保留原红绿过程，不能覆盖最终同源结果。

最终本地候选门禁：VID-G6默认`all`在一次性MySQL 8、内部网络、Linux race和000001→000109迁移上通过，所有必选顶层测试均由执行器确认RUN/PASS；锁定SDK另以真实loopback HTTP执行并通过。`go test ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff`、四个核心包Linux race、289个本阶段Go文件gofmt、`git diff --check`及562个文本文件敏感扫描通过。IMG-G6临时MySQL/race兼容和VID-G5完整财务/Legacy Chat 1→77兼容回归通过。真实Provider/Key/钱包/用户资金/调账/共享测试服/生产操作均为0。

上述仅表示本地软件候选满足进入独立QA、产品和工程终审的条件。四类独立回执、精确提交、Ready CI、PR合并及main包含性未完成前不得标记VID-G6 `AUTO_PASS`；VID-G7仍禁止开始。

### VID-G6 终审整改覆盖（取代本节历史待办状态）

独立QA/PM/Standards/Spec初审失败后，已新增并通过以下真实临时MySQL/Linux race整改：本地running用户1/Project2/模型2；inline近10MiB TCP429、读取与Complete后断连、跨用户/Project/Key、生成COMMIT未知；下载Project第5路、租约COMMIT未知、真实慢TCP、首片后撤权/删除；queue guard故障与拒绝全账本零变化；预算Asia/Shanghai日/月边界与生成COMMIT未知；模型发布默认并发、权限/MFA、SQL回滚和COMMIT未知；调账credit/debit、余额不足、过期审批、maker撤权、防篡改和COMMIT未知；poll/archive/release/quarantine的100并发、MFA/撤权及COMMIT未知。每项copy-tree SHA见`docs/evidence/video-gateway-vid-g6-review-remediation-progress.json`。

上述切片仍不是最终阶段PASS。必须在不再修改源码和主文档后重跑锁定双SDK、默认`all`隔离MySQL/race、Go全量/vet/mod/tidy、Chat/Image/G0—G5兼容、格式及敏感扫描，生成新SOURCE_STATE和证据索引，再重新执行四轴独立验收。Git/CI/PR/merge只在P0/P1/P2全清后执行；真实Provider/Key/用户资金/调账、测试服、生产和VID-G7仍为0。

## 图片网关 IMG-G1 Expand Schema 验收

- `000068` 必须把 `ai_requests.modality` 从仅 Chat 扩展为 `chat/image`，同时以 `capability + delivery_status` 组合约束确保旧 Chat 固定为 `chat.completions/not_applicable`、图片固定为 `image.generate`。
- migration 前已有 Chat 请求和 Usage 必须自动获得兼容默认值；旧二进制不提交新列时仍能写入 Chat，重复 Usage 仍被数据库唯一约束拒绝。
- `ai_gateway_quotes`、`ai_gateway_tasks`、`ai_gateway_assets` 必须通过用户、Project、request 和 Quote 归属键阻止跨租户事实。
- `ai_usage_items` 必须支持 `usage_fact/sale_line/cost_line/adjustment`、variant、价格版本、计量单位、计量基数和 CNY Decimal；旧 Chat 使用 `legacy_chat` 哨兵，不改写历史金额。
- `(request_id,result_index,asset_role)` 必须拒绝重复主图；非主图必须关联同请求父资产。
- `available` 资产必须同时具备审核通过、显式标识、隐式标识、对象定位、MIME、字节、hash 和尺寸；任一缺失必须失败关闭。
- migration 静态扫描必须确认表和列中不存在 Prompt、图片 Base64、签名 URL、Provider 原始响应或明文密钥字段。
- down/re-up 必须保留 Quote、任务、资产、Usage、标识和交付状态事实，不得包含 `DROP TABLE`、`DROP COLUMN`、`DELETE` 或 `TRUNCATE`。
- 本地必须通过图片模型/迁移定向测试、Go 全量测试、`go vet`、目标包 race 和 `git diff --check`。
- 隔离 MySQL 8 必须使用本机已有镜像、`--pull=never --network none`、无端口和 tmpfs，验证首次 up、旧 Chat、新图片约束、保留式 down 和 re-up；不得连接项目库或测试服务器。
- 本阶段没有 HTTP 或页面验收；图片 API、Repository、价格和前端属于 IMG-G2～IMG-G8，不能用 Schema 通过冒充业务闭环。

## 图片网关 IMG-G2 价格与 Quote 验收

- 图片必须按 `meter_type + variant_hash` 唯一选价；同 meter 多个 variant、缺价、重复 variant、零销售价、负成本、非正 unit size、成本过期和毛利不足均失败关闭。
- V2 快照只保存本次 `selected_lines`；解码必须重新规范化 variant、核对 hash、拒绝重复行/未知 meter/未知 schema version，并重算 `quoted_amount=held_amount`。
- 无 `schema_version` 的历史 Chat 快照继续按 V1 四类 Token SKU 解码，不改写历史 JSON 或金额。
- Quote 指纹必须使用至少32字节专用 HMAC 密钥，绑定用户、Project、SK、模型、Prompt HMAC、张数和 variant；数据库不得保存 Prompt。
- 未消费 Quote 5分钟后过期；同指纹同 request_id 已消费重放即使过期也返回原绑定；不同指纹或不同请求复用返回冲突。
- 内存合同和真实 MySQL Repository 均执行100并发同 Quote，必须恰好1个胜者且不得形成第二个消费事实。
- Decimal 金样覆盖成功、部分成功、完全失败、释放、部分退款、全额退款和超额拒绝；实际可交付数量不得超过报价数量。
- 测试价格必须明确 `price_purpose=test_fixture`，正式发布入口拒绝测试夹具；Provider调用和钱包写入均为0。
- MySQL 8.0.46 必须通过完整 `000001→000069`、Chat兼容、图片 CHECK、保留式 down 和 re-up。
- Go 定向、全量、vet、Linux race、migration静态、脚本默认关闭、敏感扫描和 `git diff --check` 全部通过后，才可进入人工财务/幂等审查。

## 图片网关 IMG-G3 任务与资产 Repository 验收

- 任务创建和查询必须绑定用户、Project、Quote和request；Project SK读取额外绑定api_key。
- 任务状态使用 `from_status + version_no` CAS，进度单调；100并发同版本只能1个胜者。
- `(request_id,result_index,asset_role)` 唯一；100并发相同主图只能1个成功；非主图必须关联同请求父资产。
- 用户A/ProjectA不能查询用户B/ProjectB的任务或资产，响应不能泄露记录是否存在。
- 普通资产交付必须同时满足主图、可计费、available、审核通过、双标识、非争议、未删除，以及请求已settled且delivery available。
- temporary、quarantined、deleted、争议中、未结算或未标识资产全部拒绝普通下载。
- 开启争议必须原子设置legal hold；争议解决后legal hold继续保留；保全期间expiring/deleting/deleted全部拒绝。
- Fake ObjectStore必须验证有界写入、同键幂等、冲突、读取副本、幂等删除、15分钟签名上限、路径拒绝和100并发。
- MySQL 8.0.46完整 `000001→000070`、down/re-up、Go全量、vet、Linux race、默认关闭、敏感扫描和diff全部通过后，才可进入资产/权限人工审查。

## 图片网关 IMG-G4 Fake执行与安全处理验收

- Prompt拒绝必须发生在Provider前；Fake Adapter调用增量为0。
- Fake Provider必须覆盖成功、部分成功、明确失败、超时、断连、结果未知和损坏结果；每次请求最多调用一次，unknown不得自动重试。
- URL和Base64都必须有界读取；无Content-Length响应同样不能越过字节上限。
- PNG/JPEG/WebP必须先config校验再完整解码；SVG、HTML、GIF、错误魔数、MIME不匹配、超宽高、超像素和图片炸弹必须拒绝。
- 原始EXIF/GPS/XMP/文本metadata必须通过完整解码和重编码清理；主图与缩略图必须重新写显式和隐式标识并复检。
- SSRF覆盖loopback、RFC1918、CGNAT、link-local、metadata、多播、文档/benchmark网段、IPv6私网、混合DNS、重定向和DNS rebinding；拨号必须使用本次已验证IP。
- 输出审核拒绝写Fake隔离区且可交付/可计费数为0；审核不可用失败关闭。
- 结果区存储失败不得返回资产或重调Provider；缩略图失败不增加计费且不阻断已安全主图。
- Fake ObjectStore 100并发、同键幂等、不同内容冲突、删除幂等和15分钟签名上限通过。
- `go mod verify`、`go mod tidy -diff`、Go定向/全量/vet、Linux race、敏感扫描和diff通过后，才可进入安全人工审查。

## 图片网关 IMG-G5 钱包、补偿与对账验收

- Quote消费、Wallet Hold、请求钱包关联和held Outbox必须在同一事务提交或回滚。
- 同request 100并发只能形成一个hold/冻结流水/关联/held事件，并全部幂等返回同一事实。
- 同钱包100个不同request并发不得超额预占；死锁/锁等待只能重试完整预占事务，不得调用Provider。
- 全部成功和部分成功按实际可交付主图结算；Provider成本按已确认产物记录，销售与成本不能混淆。
- Provider明确失败必须释放hold并形成0元销售/成本终态；超时和断连必须保留hold进入待核对，均只调用Provider一次。
- 输出审核拒绝必须销售额0、全额释放hold、资产隔离并记录已确认Provider成本。
- 结果未知、断连、超时、存储或结算失败必须保持 `settlement_pending`，禁止下载和自动重调Provider。
- 补偿只读取任务/资产事实；完成后只结算和交付一次，无事实时第8次失败进入dead。
- usage_fact、sale_line、cost_line、adjustment、请求金额、hold状态、冻结/解冻/消费流水、资产、补偿和Outbox payload必须按request_id零差异。
- 调账maker/checker必须不同；没有配套钱包动作的调账必须让对账失败关闭。
- MySQL 8.0.46完整 `000001→000071`、事实保留式down/re-up、金额金样、Go全量/vet/race、敏感扫描和diff全部通过后进入财务人工审查。

## 图片网关 IMG-G6 HTTP、Project SK与幂等验收

- `/v1/images/generations`只接受Project SK；JWT、无效SK、未实名、停用Project/Key、过期Key和无显式图片模型scope全部在Quote/Hold/Provider前拒绝。
- 历史 `all/legacy_all` SK没有显式图片scope时不得继承图片能力。
- 模型目录可见性必须在每次Quote/生成重新检查；组件缺失、读取失败或用户当前不可见均失败关闭。
- 所有生成写接口必须携带16～128字节单值Idempotency-Key；缺失、过短、过长、重复、逗号多值、首尾空白和控制字符全部返回400。
- OpenAI同步成功返回原始200兼容对象和Molin短效URL，不得套平台响应外壳或返回Provider URL/Base64。
- 平台生成必须提交Quote，返回202任务；JWT绑定本人Project且api_key_id为空，SK绑定所属Project和Key。
- 首次100并发同幂等键只能创建一个request/task/hold并调用Fake Provider一次；100次终态重放调用数仍为1；不同指纹返回40901。
- Provider执行前取消必须释放hold并零差异；执行开始后只记录取消意图，不直接判定免费。
- 结果未知返回50401和request_id，查询原请求保持settlement_pending，相同幂等键重放不得调用Provider。
- 跨用户、跨Project、跨SK任务查询不得泄露记录；未结算、隔离、争议、删除和未标识资产不得签发URL。
- 用户和管理列表必须D-95扁平分页；非法状态、project_id冲突、严格JSON重复键/未知字段必须前置拒绝。
- 管理读取要求 `ai_gateway:view + MFA`；隔离要求 `safety_manage + MFA + reason + version CAS + 前置审计`；对账要求 `reconcile_manage + MFA + reason + 前置审计`。
- 管理写入审计失败时业务服务增量为0；旧version隔离返回409且不覆盖新状态。
- 图片价格只允许创建test_fixture，Token上限为SQL NULL，test_fixture发布必须失败关闭。
- Prompt、Base64、内部对象地址、Provider原始响应、成本和SK不得出现在任务JSON、Outbox、日志或公开DTO。
- G6不新增migration；MySQL 8.0.46完整000001→000071、Linux race、httptest、全量Go、vet、依赖、敏感扫描和diff全部通过后进入权限/幂等/资产人工审查。

## 图片网关 IMG-G7 基础设施与关闭态验收

- 默认 `IMAGE_GATEWAY_ENABLED/TRAFFIC_ENABLED/OPENROUTER_ENABLED=false`；模块关闭但任一流量/真实Provider开关开启必须拒绝启动。
- G7只允许 `APP_ENV=test + LOCAL_FAKE_TEST=true + provider=fake` 的本地流量；OpenRouter真实启用属于IMG-G9。
- OpenRouter Adapter正式构造只接受固定HTTPS端点，禁止重定向、fallback和重试；httptest覆盖成功、传输未知、畸形200、弱Key和端点改写。
- Secret只从绝对普通文件读取；拒绝符号链接、宽权限、超大文件、空白/控制字符和跨用途复用。
- MinIO三个bucket保持私有；内部连接与浏览器公开签名入口分离；匿名GET拒绝、公开签名GET返回正确MIME和图片签名、同键同内容幂等、冲突/删除/不存在正确。
- RabbitMQ主队列和DLQ持久化，publisher confirm和mandatory开启；消息只有request_id，失败Nack不requeue并进入DLQ。
- 异步平台任务只调用Fake Provider一次；内存Prompt丢失必须取消未执行任务、释放hold且Provider调用不增加。
- 失败/取消temporary满24小时、quarantined过期和delete_failed资产进入清理；settlement_pending、exception、活动补偿、legal hold和开放争议跳过；删除使用version CAS，陈旧deleting可安全恢复。
- `available` 成功资产的30天自动删除属于IMG-G10，本阶段只验证策略冻结且G7不会误删可交付资产。
- 临时、结果或隔离对象已写入但元数据未落库时，删除失败必须幂等写入 `image_object_cleanup`；伪造bucket/key/reason组合拒绝，第8次失败dead，管理DTO不泄露对象路径。
- 四类Put结果未知必须强制写tombstone并保持5分钟静默窗；覆盖Delete先NotFound、Put后到、引用/Head瞬时失败、commit结果未知完整/零/部分/查询未知和插入/删除交错。
- 同步与异步图片执行共用Redis四维租约；用户1、Project 2、API Key 1、模型4为不可放宽硬上限，队列1000满载和并发超限返回429，所有终态释放租约。
- 覆盖Hold提交后进程崩溃且无内存/消息的陈旧reserved恢复，以及活跃执行收到重复Rabbit消息时不释放原租约、不写取消、DLQ为0。
- 覆盖Provider后结算与pending补偿连续落库失败：安全窗内不误收口，超过300秒后原子写unknown/pending、唯一补偿和Outbox，保留Hold且Provider调用不增加。
- 补偿Worker不重调Provider，第8次失败dead；关闭态仍可执行对账与安全清理。
- Prometheus输出图片请求、Provider、任务、资产和对账差异；标签不得包含request_id、用户、Project、SK、Prompt或错误原文。
- Grafana JSON可解析，Prometheus规则通过promtool；告警覆盖pending_reconcile、非零对账、dead补偿和临时资产积压。
- 无外网隔离Docker必须使用MySQL 8.0.46、MinIO、RabbitMQ和Fake凭据，宿主端口0、真实Provider调用0。
- 全量Go、vet、Linux race、依赖、敏感扫描、diff和Chat回归通过后进入G7人工审查；没有测试服务器授权时不得报告测试环境集成通过。

## 图片网关 IMG-G8 页面与真实后端浏览器验收

- 管理端覆盖图片模型、非商业测试价格、任务、资产、账单对账、异常处理和隔离入口；写操作验证MFA、细粒度权限、reason、审计和version CAS。
- 用户端覆盖固定规格、Quote、钱包预估、幂等生成、任务查询/取消、主图画廊、短效下载和失败/超时/安全拒绝/待结算提示。
- 使用临时MySQL、Redis、RabbitMQ、MinIO、真实Go HTTP和Fake Provider；浏览器不得注册API Mock，并必须实际加载短效签名PNG，校验HTTP 200、MIME和图片签名。
- 响应式固定验证 `1440×900`、`768×1024`、`390×844`、`375×667`，要求无横向溢出、无重叠、按钮有反馈。
- 同次验收回归Chat、Project/SK、账单与申诉，最终 `request_usage/request_hold/request_wallet=0` 且Outbox backlog=0。
- 真实Provider、正式价格、生产部署和商业开放不属于本阶段证据。

## 图片网关 IMG-G9 真实Provider与人民币测试计费验收

- 唯一源码必须从批准基线创建本地候选提交；真实调用前记录提交、二进制SHA和工作树状态。
- 固定模型为 `bytedance-seed/seedream-5-0-lite`，固定 `provider_tag=seed`，规格为 `n=1 / 2K / 1:1 / standard / url`；Gemini/Vertex只作为历史失败证据，不得进入当前候选配置。
- OpenRouter专用Key必须是0600普通文件、非符号链接且费用限制覆盖本轮书面授权；调用前必须冻结Usage基线并按增量核对，新Key优先要求初始Usage为0；不得复用Bifrost或业务Key。
- 关闭态必须允许 `provider=openrouter` 但不读取Key，图片入口返回50330且生成消费者为0。
- 真实态只允许一次Provider调用；请求必须包含单一Provider `only`、`allow_fallbacks=false`、`stream=false`，Adapter和Worker均不得重试。
- 成功响应必须包含合法图片和 `usage.cost`；费用缺失、非正或超过0.25美元一律结果未知并禁止交付。
- Provider美元费用必须以规范Decimal字符串进入任务低敏结果摘要，禁止保存Provider原文、Prompt或Base64。
- 上游成功、明确失败和结果未知都必须记录低敏尝试证据：`provider_code`、`provider_attempted`、HTTP状态和封闭字符集错误码；实际调用后任务`attempt_count`必须为1，禁止把已尝试请求记录为0。
- Provider调用前必须先以行锁和version CAS提交唯一尝试事实；模拟提交后进程退出时，重放不得再次进入Provider，陈旧恢复必须保留`provider_code/attempt_count`并转为结果未知。
- Provider已确认费用后发生本地解码、审核、存储或终态失败时，任务摘要仍必须保留`provider_cost_usd/provider_request_id`；隔离MySQL需覆盖成功、非2xx明确失败、HTTP成功但结果未知三类终态。
- OpenRouter图片失败返回502且费用为0时，不得只依据Key Usage或Last Used判断请求未发出；必须结合新候选保存的HTTP状态、低敏错误码和OpenRouter控制台Activity/Guardrail证据定位根因。
- Project SK正式签发必须允许已激活、已发布且对用户可见的图片模型进入显式allowlist；不存在、未发布、非chat/image或不可见模型仍失败关闭，`all/legacy_all`不得隐式获得图片能力。
- SQL价格夹具必须覆盖应用`loc=Local`与MySQL会话时区差异；真实调用前必须以Quote 0.50元成功、未消费、未Hold和已冻结Provider Usage基线作为最终价格门禁。
- OpenRouter返回HTTP 403时必须记录`provider_code/attempt_count/http_status/error_code`，用户销售额、Provider成本和结算额均为0，Hold完整释放且Outbox按顺序发布；原始Provider错误消息仍不得落库或写日志。
- OpenRouter请求必须携带`X-OpenRouter-Experimental-Metadata: enabled`；HTTP 403只能映射为费用额度、Workspace预算、模型策略、Provider策略、数据策略、内容护栏、Key权限、上游权限或unknown九种固定低敏分类。
- 403分类测试必须覆盖字符串/数字错误码、路由Pipeline、Provider响应状态和未知错误体，并断言完整错误消息、Prompt、Key、长Base64片段及原始元数据不会进入`ProviderImageResult`或任务摘要。
- 测试钱包最多预占/结算0.60元，正常test_fixture销售价为0.50元；每次余额变化都有唯一流水。
- 主图必须经过完整解码、格式与尺寸校验、元数据清理、双标识审核和MinIO私有存储，结算提交前不得available。
- 请求、Quote、Usage、sale_line、cost_line、钱包、资产和Outbox对账差异必须为0；同时单独核对OpenRouter Key Usage增量与任务中的Provider费用回执一致。
- 无论成功失败均关闭traffic/OpenRouter、停止消费者、恢复原API与环境、删除临时Project SK和Key文件，并由用户从OpenRouter控制台撤销短效Key。
- 最终必须复验Chat、Bifrost、用户端、管理端和共享监控；不得进入生产或IMG-G10。
- 2026-08-27验收结果：`d97baf1400840d5e707b5ed2c9bfc4237885353c`在测试服务器以Seedream/seed完成唯一真实调用，图片交付、0.50元结算、0.035美元费用增量、私有MinIO、三条Outbox、0差异对账、Key撤销和实际回滚全部PASS；生产与商业证据仍为NO。

## VID-G6 最终同源本地验收候选（2026-09-01）

本节取代本文件较早VID-G6条目中的“待补、局部、运行中、未完成”过程状态；历史反例仍保留用于说明缺陷发现和关闭过程。

- 冻结源码副本SHA-256与SOURCE_STATE：统一读取最终`docs/evidence/video-gateway-vid-g6-local-verification.json`和`video-gateway-vid-g6-source-state.json`，本计划不重复硬编码易漂移值。
- `verify-video-gateway-vid-g6.sh`默认`all`：一次性MySQL 8、迁移000001→000109、Linux race、真实loopback HTTP和必需测试RUN/PASS审计全部通过；精确耗时见最终本地验证回执。
- 独立专项：锁定Python/TypeScript SDK通过；VID-G5完整迁移、财务与Chat兼容通过；IMG-G6 HTTP兼容通过；callback、model-default、cancel修复后均专项通过并在最终all中再次通过。
- 通用门禁：`go test ./...`、`go vet ./...`、`go mod verify`、`go mod tidy -diff`、G6变更Go文件gofmt、Bash语法、diff与高风险凭据模式扫描通过。
- 安全边界：真实Provider请求/Key、真实钱包写入、真实用户资金、真实调账、测试服写入、生产操作均为0；Outbox Dispatcher、RabbitMQ、Redis、MinIO和Bifrost视频数据面关闭。
- 最终结论须等待新SOURCE_STATE绑定的QA、PM、Standards、Spec四轴独立复核；之后才允许提交、PR、Ready CI和普通合并。VID-G7不得开始。

## VID-G7 共享Outbox领取专项

本节新增G7测试范围，不改写前述G6历史过程记录。使用`verify-video-gateway-vid-g7-outbox.ps1`及可选`-LinuxRace`，从空临时MySQL应用全部109个up migration，使用原G5真实预占/取消事务和合成钱包。

七个必选顶层测试覆盖：旧发布器视频关闭、双向领取范围与视频回写隔离、100并发唯一认领与接管、同秒dead及管理重排防令牌重用、真实取消事件有序发布、连续普通重试/旧回写拒绝/未来高水位接管边界、批量不同令牌与数据库一致。每项必须实际RUN/PASS，无SKIP；脚本退出及精确清理均通过，并核对运行前后源码哈希未变化。

同秒ABA先在真实MySQL复现旧令牌MarkPublished错误成功，修复后原反例复验；不得删除该反例。运行器PowerShell主机参数必须使用完整带引号参数，避免`-h127.0.0.1`被拆成`-h127`与`.0.0.1`。本专项不覆盖完整MQ发布窗口、Provider任务计数、Redis/MinIO或全阶段兼容，最终范围及结果见同源证据。

G7增量加入运输状态与财务重放、首次终结、H/P/C恢复、大小写身份和坏运输结构矩阵。`-FinanceRegression`沿用G5默认all筛选，动态发现99项并要求逐项RUN/PASS；曾与11项G7在同源Linux race下110/110通过，该历史源码不自动覆盖后续新增投影。

投影专项`-Focus projection`覆盖T2V/I2V的held/released/settled、执行中粗细状态映射、unknown及adjustment、全部原事件ID、四字段最小输出、失效/过期/伪造租约、同消息发布重试仍attempt0，以及无原结算/释放/补偿依据的六类伪造事件。默认all纳入全部投影及原Outbox用例；测试结果必须另以当前SOURCE_STATE绑定。历史图片导入来源、媒体清理后引用及真正Broker发布/持久化消费仍为待验收。

新测试统一UTC秒级夹具时钟：MySQL 8.0.46在本机隔离只读探针中将DATETIME(0)的`.900000`舍入到下一秒，不能用带小数的next_retry_at写入后立刻以截秒时间断言已到期；生产领取规则不因此放宽。dead完成时间/次数/币种各反例必须保留合法锁，缺锁单独验证，避免多重非法条件掩盖缺测。

发布桥接专项使用`-Broker -Focus relay -LinuxRace`，要求独立MySQL/RabbitMQ双门禁、全部109个迁移和真实Broker。必须验证两个依赖/联合顶层测试实际RUN/PASS，联合测试七个场景为T2V、I2V、不可路由、真实basic.ack丢失、确认持久化故障、100并发以及坏事实前置拒绝；原Task/事件唯一且七张财务表完整不变。缺Broker批准或relay未启用Broker时在Docker前退出3。`-Broker -LinuxRace`默认合并原17项与新增2项，19项不得SKIP。

数据库确认故障是GORM回调注入，接管采用受控时钟；不得将其表述为真实DB断连或进程kill。测试观察者取消息并ACK不代表业务消费者已实现；真实任务处理、重复消息不重提Provider和运行时默认关闭仍需独立验收。

## VID-G7 外层取消围栏与组合隔离增量

`TestVideoG7WorkerCancelOuterTransactionMySQL`通过真实G6创建服务验证T2V/I2V的用户/管理员外层取消：真实租约到期全事务回滚、过期证明入口拒绝、原无证明控制面授权、管理员权限/MFA负例、旧证明只读重放以及独立任务有效Worker首次成功。仅数据库详情响应注入延迟，用户Caller为服务边界，不宣称HTTP路由全量验收。

`verify-video-gateway-vid-g7-outbox.ps1 -LinuxRace -Broker -FinanceRegression`须隔离G6创建型测试与Outbox大批量任务夹具，分别使用全新临时库，合计检查全部RUN/PASS与零SKIP；不得提高全局queued=100或删除历史任务以绕过容量。对应证据见`docs/evidence/video-gateway-vid-g7-outer-cancel-fence-verification.json`。session58972最外层exit=0，groups=2/required=135全部通过，绑定server哈希f6372389。后续新增Redis策略的纯单元测试单独记录，不据此宣称Redis或完整G7集成通过。

## VID-G7 真实Redis存储组件增量

`infra/scripts/verify-video-gateway-vid-g7-redis.ps1`要求本轮临时Redis授权、锁定镜像、实际run_id及精确资源ID清理；加`-LinuxRace`验证竞态。session80555 native与session56671 Linux race同源十四项全部通过、SKIP=0，server哈希218f7ee7。测试包括100并发2/98裁决、坏状态、Request唯一性、轴收紧、大整数/JWT、promoting双占、confirm/release、queued名额恢复、过期债务exact清理、真实30秒到期、原nonce重放、客户端丢结果及普通输出脱敏。包内confirm/release当前只证明Redis原子动作，不能代替MySQL业务提交和安全终态证明。

单实例goroutine不能替代2/4/8个Go进程；客户端Hook丢结果不能写成TCP断连；手工初始Redis快照不证明MySQL授权/重建。当前已补2/4/8独立进程、初始化、确认/释放、数据库提交未知、进程kill和完整本地运行时验证；测试服真实重启仍待授权，见[Redis合同](video-gateway-vid-g7-redis-capacity-contract.md)。

## VID-G7 MySQL恢复租约专项（本地已验证）

新增`-Focus capacity_boundary`：新库+runner server_uuid绑定的合成uint64边界，恢复完整SQL守卫后通过公开Repository验证末次Begin/Renew/Block、耗尽拒绝、阻断只读重放及直接SQL回绕拒绝。夹具不是业务恢复或真实崩溃证据。完整all将该不可重置门闩放在独立新库；分组必须精确匹配子Focus实际顶层测试，禁止通配吞掉未运行测试后虚计PASS。

新增`-Focus submission_plan`：T2V/I2V通过真实预占与提交claim验证无Worker证明零写、计划与事件原子创建、原回执/attempt不变、同计划重放、错claim/Provider拒绝、输入与资金不变及JSON隐藏。后续已补100并发、SQL直接绕过、事务失败/COMMIT未知、尾部过期和完整本地运行时证明；测试服门禁仍独立等待授权。

提交计划专项现纳入七个明确顶层测试：基础、100并发、SQL计划保护、首次/后续身份冻结、归属/根事务、COMMIT未知和真实尾到期。session16923同源码Linux race七项全部通过，SKIP0、清理通过，server哈希8285b73a。身份负例使用真实存在的同归属替代Key/Quote，要求明确命中计划守卫1644而不是外键拒绝。尾到期先等待原30秒Worker租约接近截止，再在5秒根事务内于事件后跨期，实际运行60.51秒，命中Worker失权且非context超时。COMMIT包装执行真实sql.Tx.Commit后丢确认，不替换核心事务，也不声称TCP断连。session22256再执行`-Focus receipt -LinuxRace`，发现13项原G5提交并与七项计划、两项既有G7提交围栏合并，22/22通过、SKIP0、清理通过；两个真实尾到期分别60.52/60.86秒。

新增`-Focus capacity_cutoff`：用单一真实恢复epoch将Begin插入Claim COMMIT后和Provider紧前，验证旧G6新创建整笔回滚、queued不推进、计划写入/重放及claim校验拒绝、非defer投影路径Provider调用0、输入/Task/事件/钱包不变。session57910先复现recovering后Claim成功；session65089最终Linux race通过。恢复状态须返回治理不可用而非容量429；session98750复现错误语义，修复后由同一最终专项验证。完整`capacity_epoch`回归另行执行，确保历史兼容断言不重新打开cutoff。

Redis runner新增`-Focus recovery`并自动发现四项恢复测试。session91943精确复现旧run_id阻断；修复后session86078 native四项通过。session78608完整十二项Linux通过，含原30秒债务；QA补测试自行DEL/确认固定键空后，session93633恢复Linux四项复验通过。覆盖stage/activate、staged拒绝普通操作、同/异快照、旧epoch、旧run_id新epoch、staged/ready TTL、超前期限、Provider hard cap、EVAL丢返回、100并发和快照值/指针脱敏。它不是MySQL账本快照或ready证明。

容量执行专项使用`-Focus capacity_execution|capacity_execution_history|capacity_execution_recovery|capacity_send_crash -Redis`，Linux增加`-LinuxRace`。14f0d284下四项native/Linux全部通过：主专项100并发验证Provider Submit入口1、任务1及T2V/I2V共享Provider=2；历史计划验证NULL epoch补绑、缺事件拒绝和COMMIT未知；下一epoch验证原首次epoch不变而Redis以新nonce恢复running；崩溃专项验证permit消费后无Provider调用、重启不可重提、2分钟后pending_reconcile且Hold/running容量保留。000114/115部分CHECK/Trigger缺失后重入分别恢复1/2和1/3。

`capacity_reservation`覆盖Redis queued与原财务事务：回执丢失、MySQL明确回滚、显式/自动Quote、global满零Request/Task/Hold/Outbox、COMMIT未知和100并发同意图；session33793/27836 native/Linux通过。`capacity_terminal_release`与更新后的`capacity_send_crash`覆盖安全取消释放、跨归属拒绝、重放和pending保守占用，session45067/95205及96157/28025通过。`capacity_process_2|4|8`实际从父测试启动独立Go子进程，各自打开MySQL/Redis、领取Worker lease并经TCP barrier同时起跑；native和Linux race六轮均严格保持Provider running=2，剩余queued。

新增`-Focus capacity_snapshot`：真实创建reserved T2V、queued I2V、planned pending_reconcile、已绑定submitted、103个完整预占后取消历史、settled+delivered成功终态、Provider明确失败并释放终态，以及无可靠结束证明failed。成功终态含seq1 credit与seq2 debit，失败终态含credit；Builder须分页扫描全部历史，仅返回queued=2/running=2，同proof两次digest一致，交付Outbox payload损坏须阻断并在恢复原合成事实后零差异。session72893复现历史总数>102错误阻断，session74325复现故障注入改变updated_at，session93914复现proof超时；修复分页、纯读调账/终态全集、固定时间注入和每页续期后，session23276 native及最终session49686 Linux race通过。该专项未同时启动Redis，不代替跨系统stage/ready测试。

`verify-video-gateway-vid-g7-outbox.ps1 -Focus capacity_epoch`覆盖原门闩的100 CAS、真实30秒到期接管、I2V/资金不变、G6锁读兼容、审计失败回滚、嵌套事务拒绝、根PreparedStmt、Block尾部到期以及真实COMMIT后的包装层丢确认。`-Focus capacity_epoch_version`覆盖单独版本回退/跳号、恢复审计改删/重复/别名和其他模块兼容。完整all为该单行状态提供独立临时库，全部分组累计必需测试，不削减门禁。

session43096补强native/Linux race各两项为历史结果；之后session98087无唯一键遮蔽地复现缺schema和数字owner漏洞，已修为七字段/NULL安全类型检查。session39272原生17例及同源码Linux capacity_epoch两项均通过，SKIP=0、清理通过；最大uint64数据库边界已由独立`capacity_boundary`新库验证。完整阶段仍受最终审查和测试服门禁约束，详情见[恢复租约合同](video-gateway-vid-g7-capacity-recovery-epoch.md)。

## VID-G7关闭态运行时、MinIO、对象补偿与回滚

执行：

```powershell
$env:VIDEO_GATEWAY_G7_MYSQL_ISOLATED_APPROVED='YES'
$env:VIDEO_GATEWAY_G7_REDIS_ISOLATED_APPROVED='YES'
$env:VIDEO_GATEWAY_G7_RABBIT_ISOLATED_APPROVED='YES'
$env:VIDEO_GATEWAY_G7_MINIO_ISOLATED_APPROVED='YES'
.\infra\scripts\verify-video-gateway-vid-g7-runtime.ps1
```

脚本只创建无宿主端口的本轮内部网络和临时MySQL、Redis、RabbitMQ、MinIO、Go容器。当前22项必需测试必须精确RUN/PASS且SKIP=0：

- `TestVideoG7BootstrapClosedRuntimeMySQLRedisRabbitMinIO`：模块关闭路由404；模块装配但流量关闭503；submit/poll/fetch各2个Worker；Outbox、容量恢复、MinIO和统一低基数指标装配。
- `TestVideoG7ObjectScannerMySQLMinIO`：双向静默观察、持久分页/重启续页、实际`vid_`与历史`video_`前缀、保存目标、DB失败恢复及事实保留。
- `TestVideoG7ImportRetentionCursorSkipsProtectedPrefixMySQL`与`TestVideoG7UploadSessionRetentionMySQL`：输入公平续页、24小时未完成会话墓碑及追加事实。
- `TestVideoG7OutputRetentionWorkerMySQL`：真实G7容量/Worker/Provider链形成六项父子资产，五项交付对象到期删除、审核副本保留、财务不变，并留下queued/pending_reconcile回滚事实。
- Native HTTP严格JSON、禁止重定向、图片三项兼容和组件独立指标故障同批执行。
- Rabbit新增真实Broker死信恢复/重复ACK/无许可保留、毒消息摘要处置/前方合法消息暂存回队；MySQL验证管理员权限、MFA、同Key意图冻结、原Task/Request/version、TaskEvent和前后审计。MySQL+Rabbit联合验证confirm丢失、发布后完成审计失败、完成审计后DLQ ACK未知三类窗口。运行时单元边界验证毒消息只执行一次、瞬态失败健康降级/重连及Shutdown超时保留生命周期；真实MySQL验证实例A写熔断、实例B阻断、恢复后实例C放行；bootstrap另验证监听失败仍同步收口Worker。

随后用锁定promtool验证10条视频告警规则，解析Grafana 8面板JSON；逆序执行110—122共13个Expand-only down，比较14字段快照，包括容量epoch、Worker/发送权列、扫描游标、对象观察、Rabbit毒消息熔断、会话/输入/输出retention、queued/pending_reconcile、两个holding Hold及提交计划/发送事件，随后再次等待关闭态runtime九类组件首次健康并优雅停机。最新同源结果以证据文件为准；它不替代共享测试服务器备份、安装、告警送达和实际回滚授权。

独立事实快照门禁使用`verify-video-gateway-vid-g7-fact-snapshot.ps1`：两个隔离MySQL组分别验证逻辑备份恢复与110—122升级/兼容撤回。每组必须运行4个G6真实服务种子，形成13类非空请求、任务、输入、载荷、回调、Usage、资产、事件、Outbox、Hold、钱包关联和审计事实，并同时包含T2V/I2V；expanded快照还必须包含受约束Rabbit熔断表。快照只输出表名、行数和聚合摘要。输入manifest的表集合、顺序和WHERE必须与当前base/expanded内建白名单逐项一致，恶意`COMMIT/DDL` manifest必须在执行快照SQL前拒绝且原表保留。Prompt密文与nonce使用`--hex-blob`恢复，钱包一分钱受控篡改必须被摘要检测。两组均须PASS、SKIP=0、无宿主端口且清理容器/网络/卷；该本地结果仍不能替代测试服实际备份和回滚。
