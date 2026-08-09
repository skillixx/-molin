#!/bin/sh

set -eu

# 内部令牌只允许固定安全字符，拒绝空值、短值、换行和可注入 Nginx 配置的字符。
internal_token=${BIFROST_INTERNAL_TOKEN:-}
token_length=${#internal_token}
if [ "${token_length}" -lt 32 ] || [ "${token_length}" -gt 256 ]; then
  echo "Bifrost 内部鉴权令牌长度不合法" >&2
  exit 1
fi
case "${internal_token}" in
  *[!A-Za-z0-9_-]*)
    echo "Bifrost 内部鉴权令牌包含不安全字符" >&2
    exit 1
    ;;
esac

# 仅替换内部鉴权令牌，保留 Nginx 在请求阶段解析的变量，避免模板入口误处理完整主配置。
envsubst '$BIFROST_INTERNAL_TOKEN' < /etc/nginx/nginx.conf.template > /tmp/nginx.conf
exec nginx -c /tmp/nginx.conf -g 'daemon off;'
