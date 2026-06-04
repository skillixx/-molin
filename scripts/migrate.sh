#!/usr/bin/env bash
# 执行数据库 Migration（使用 golang-migrate 工具）
# 依赖：golang-migrate CLI，安装：go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest
#
# 用法：
#   ./scripts/migrate.sh up           # 执行所有未运行的 migration
#   ./scripts/migrate.sh up 1         # 只执行下一条 migration
#   ./scripts/migrate.sh down 1       # 回滚最近一条 migration
#   ./scripts/migrate.sh version      # 查看当前 migration 版本
#   ./scripts/migrate.sh force 3      # 强制设置版本号（修复 dirty 状态时用）
#
# 环境变量（未设置时使用默认值，对应 infra/docker-compose.yml 本地开发配置）：
#   MYSQL_HOST / MYSQL_PORT / MYSQL_DATABASE / MYSQL_USER / MYSQL_PASSWORD

set -euo pipefail

MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_DATABASE="${MYSQL_DATABASE:-molin}"
MYSQL_USER="${MYSQL_USER:-molin}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:-molin_password}"

MIGRATIONS_DIR="$(cd "$(dirname "$0")/../server/migrations" && pwd)"
DB_URL="mysql://${MYSQL_USER}:${MYSQL_PASSWORD}@tcp(${MYSQL_HOST}:${MYSQL_PORT})/${MYSQL_DATABASE}?multiStatements=true"

# 检查 migrate 命令是否存在
if ! command -v migrate &>/dev/null; then
    echo "错误：未找到 migrate 命令。" >&2
    echo "安装方式：go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest" >&2
    exit 1
fi

if [[ $# -lt 1 ]]; then
    echo "用法：$0 <up|down|version|force> [步数/版本号]" >&2
    exit 1
fi

COMMAND="$1"
STEP="${2:-}"

echo "Migration 目录：$MIGRATIONS_DIR"
echo "数据库：${MYSQL_HOST}:${MYSQL_PORT}/${MYSQL_DATABASE}"
echo "执行：migrate $COMMAND ${STEP}"
echo "---"

case "$COMMAND" in
    up)
        if [[ -n "$STEP" ]]; then
            migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" up "$STEP"
        else
            migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" up
        fi
        ;;
    down)
        if [[ -z "$STEP" ]]; then
            echo "错误：down 命令需要指定步数，例如：$0 down 1" >&2
            exit 1
        fi
        migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" down "$STEP"
        ;;
    version)
        migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" version
        ;;
    force)
        if [[ -z "$STEP" ]]; then
            echo "错误：force 命令需要指定版本号，例如：$0 force 3" >&2
            exit 1
        fi
        migrate -path "$MIGRATIONS_DIR" -database "$DB_URL" force "$STEP"
        ;;
    *)
        echo "错误：不支持的命令 '$COMMAND'，支持：up / down / version / force" >&2
        exit 1
        ;;
esac

echo "---"
echo "Migration 执行完成"
