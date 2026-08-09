#!/bin/sh

set -eu

# 仅替换内部鉴权令牌，保留 Nginx 在请求阶段解析的变量，避免模板入口误处理完整主配置。
envsubst '$BIFROST_INTERNAL_TOKEN' < /etc/nginx/nginx.conf.template > /tmp/nginx.conf
exec nginx -c /tmp/nginx.conf -g 'daemon off;'
