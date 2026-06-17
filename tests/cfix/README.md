# C-FIX 回归测试（PR #151 后端丙缺陷修复）

覆盖 C-FIX-1（会员续期）、C-FIX-2a（资产取消）、C-FIX-4（列表 page_size）、
C-FIX-5（会员到期任务）、C-FIX-6（公告 JSON_CONTAINS 可见性 + 分页）。

## 前置
- 本地基础服务：`docker compose -f infra/docker-compose.yml up -d`（MySQL 13306 / Redis 16379）
- 应用全部 migration（**含 `000026_add_user_membership_level_index`**）
- 复制 `test.env.example` 为 `test.env` 并填入本地值

## 运行

### service / repository 层（39 断言，直连真库）
`main.go` 为独立 `package main`，需放到 module 内可解析路径运行：
```bash
mkdir -p server/cmd/cfixtest && cp tests/cfix/main.go server/cmd/cfixtest/
set -a && source tests/cfix/test.env && set +a
go run -buildvcs=false ./cmd/cfixtest        # 在 server/ 目录下
rm -rf server/cmd/cfixtest                   # 跑完清理
```

### HTTP 层（10 断言）
先构建并启动 API（读取 test.env），再：
```bash
bash tests/cfix/http_test.sh
```

## 备注
- 时区：DB session 为 UTC，DSN `loc=Local`；产品代码统一用同一连接 SQL `NOW()` 比较，逻辑一致。
  测试数据若用 SQL 拼时间请用 `DATE_SUB(NOW(), ...)`，勿用 Go `time.Now()` 跨层拼，避免时差歧义。
