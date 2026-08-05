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
| SMS-P5-07 | 测试服 Canary | 白名单、总上限 10 条、五入口、OTP 单次消费、结束恢复开关 | 技术聚合预检通过；当前单号码白名单无法同时满足未注册、已注册管理员与五场景消费前置，执行计划门禁阻断，待产品确认收件层或完整消费层并单独授权 |
| SMS-P5-08 | 页面四宽度 | 1440/1024/768/390 的权限、MFA、模板、日志及五态可操作 | 通过：本地 Mock 与测试服真实 Chrome 均无横向溢出；实服展示 5 模板、5 场景、13 日志，短信请求仅 GET、写请求 0、功能性控制台错误 0；保留 1 条无路径静态 404 环境观察 |
| SMS-P5-09 | 回滚 Dry Run 与恢复材料 | 先关开关、保留证据、默认不 down migration；固定备份可验证且业务配置零变更，允许只读访问审计日志增长 | 本地 test/production 两套通过；测试服材料、容器快照和引用镜像完整。当前环境派生候选与最终 runner 已生成；runner 已上传固定暂存目录，远端摘要、权限、语法、SelfTest 和关闭态只读预检通过。恢复运行时及实际回滚演练仍待新的独立授权 |
| SMS-P5-10 | 生产灰度与观察 | 生产先关闭；Canary 与开关分开授权；完成 5m/15m/30m/2h/24h 观察 | 未开始 |
| SMS-P5-11 | PR 发布安全门禁 | Linux CI 执行 MySQL 8、Redis 7、全库 `-race`、Nginx、promtool、准备度和双环境回滚检查 | 工作流 actionlint 通过；待 PR CI 实际执行 |
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
