#!/usr/bin/env bash
# 等待指定 host:port 可连接后再继续，常用于 Docker Compose 启动顺序控制
# 用法：./scripts/wait-for-it.sh <host>:<port> [-t 超时秒数] [-- 命令]
# 示例：./scripts/wait-for-it.sh 127.0.0.1:3306 -t 30 -- go run ./cmd/api

set -euo pipefail

WAITFORIT_cmdname=$(basename "$0")

usage() {
    cat <<EOF
用法: $WAITFORIT_cmdname <host>:<port> [选项] [-- 命令 [参数...]]

选项:
  -t TIMEOUT   最多等待秒数（默认 15）
  -q           静默模式，不输出等待信息
  -s           严格模式：子命令失败时也返回失败
  --           分隔符，后接等待成功后要执行的命令
EOF
    exit 1
}

WAITFORIT_TIMEOUT=15
WAITFORIT_QUIET=0
WAITFORIT_STRICT=0
WAITFORIT_CHILD=0
WAITFORIT_CLI=()

# 解析 host:port
if [[ $# -lt 1 || "$1" == "--help" ]]; then
    usage
fi
WAITFORIT_HOST_PORT="$1"; shift
WAITFORIT_HOST="${WAITFORIT_HOST_PORT%%:*}"
WAITFORIT_PORT="${WAITFORIT_HOST_PORT##*:}"

if [[ -z "$WAITFORIT_HOST" || -z "$WAITFORIT_PORT" ]]; then
    echo "错误：请提供 host:port 格式的参数" >&2
    usage
fi

# 解析其余选项
while [[ $# -gt 0 ]]; do
    case "$1" in
        -t) WAITFORIT_TIMEOUT="$2"; shift 2 ;;
        -q) WAITFORIT_QUIET=1; shift ;;
        -s) WAITFORIT_STRICT=1; shift ;;
        --) shift; WAITFORIT_CLI=("$@"); break ;;
        *) echo "未知选项：$1" >&2; usage ;;
    esac
done

wait_for() {
    local start_ts
    start_ts=$(date +%s)
    while true; do
        if (echo > /dev/tcp/"$WAITFORIT_HOST"/"$WAITFORIT_PORT") 2>/dev/null; then
            local end_ts
            end_ts=$(date +%s)
            [[ $WAITFORIT_QUIET -eq 0 ]] && echo "$WAITFORIT_cmdname: $WAITFORIT_HOST:$WAITFORIT_PORT 已就绪，耗时 $((end_ts - start_ts)) 秒"
            return 0
        fi
        local now
        now=$(date +%s)
        if [[ $((now - start_ts)) -ge $WAITFORIT_TIMEOUT ]]; then
            echo "$WAITFORIT_cmdname: 超时（${WAITFORIT_TIMEOUT}s），$WAITFORIT_HOST:$WAITFORIT_PORT 仍不可达" >&2
            return 1
        fi
        [[ $WAITFORIT_QUIET -eq 0 ]] && echo "$WAITFORIT_cmdname: 等待 $WAITFORIT_HOST:$WAITFORIT_PORT ..."
        sleep 1
    done
}

wait_for
WAITFORIT_RESULT=$?

if [[ ${#WAITFORIT_CLI[@]} -gt 0 ]]; then
    if [[ $WAITFORIT_RESULT -ne 0 && $WAITFORIT_STRICT -eq 1 ]]; then
        echo "$WAITFORIT_cmdname: 严格模式下，端口未就绪，跳过执行命令" >&2
        exit $WAITFORIT_RESULT
    fi
    exec "${WAITFORIT_CLI[@]}"
else
    exit $WAITFORIT_RESULT
fi
