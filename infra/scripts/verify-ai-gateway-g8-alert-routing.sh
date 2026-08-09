#!/usr/bin/env bash
set -euo pipefail

# 本脚本只在一次性 Docker 网络内验证 P0/P1 firing 与 resolved 路由，不连接真实联系人或外部通知渠道。
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHANGE_ID="g8-alert-$RANDOM-$$"
NETWORK="molin-${CHANGE_ID}"
ALERTMANAGER="molin-${CHANGE_ID}-alertmanager"
RELAY="molin-${CHANGE_ID}-relay"
TMP_DIR="$(mktemp -d)"

# Git Bash 会把容器内绝对路径误改写为 Windows 路径；只关闭参数改写，并单独把宿主挂载源转为 Docker 可识别路径。
ROOT_MOUNT="$ROOT_DIR"
TMP_MOUNT="$TMP_DIR"
if command -v cygpath >/dev/null 2>&1; then
  ROOT_MOUNT="$(cygpath -m "$ROOT_DIR")"
  TMP_MOUNT="$(cygpath -m "$TMP_DIR")"
  export MSYS_NO_PATHCONV=1
fi

cleanup() {
  docker rm -f "$ALERTMANAGER" "$RELAY" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

cat >"$TMP_DIR/relay.py" <<'PY'
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length))
        with open("/evidence/events.jsonl", "a", encoding="utf-8") as output:
            output.write(json.dumps({"path": self.path, "status": payload.get("status")}) + "\n")
        self.send_response(200)
        self.end_headers()
    def log_message(self, *_):
        return

HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
PY

docker network create "$NETWORK" >/dev/null
docker run -d --name "$RELAY" --network "$NETWORK" --network-alias alert-relay \
  -v "$TMP_MOUNT:/evidence" python:3.13-alpine python /evidence/relay.py >/dev/null
docker run -d --name "$ALERTMANAGER" --network "$NETWORK" -p 127.0.0.1::9093 \
  -v "$ROOT_MOUNT/infra/alertmanager/ai-gateway-g8.test.yml:/etc/alertmanager/alertmanager.yml:ro" \
  prom/alertmanager:v0.28.1 --config.file=/etc/alertmanager/alertmanager.yml >/dev/null

for _ in $(seq 1 30); do
  if docker run --rm --network "$NETWORK" curlimages/curl:8.15.0 -fsS http://"$ALERTMANAGER":9093/-/ready >/dev/null; then
    break
  fi
  sleep 1
done

STARTS_AT="$(date -u -d '-1 minute' +%Y-%m-%dT%H:%M:%SZ)"
ENDS_AT="$(date -u -d '+10 minutes' +%Y-%m-%dT%H:%M:%SZ)"
docker run --rm --network "$NETWORK" curlimages/curl:8.15.0 -fsS -X POST \
  -H 'Content-Type: application/json' --data "[
    {\"labels\":{\"alertname\":\"G8ControlledP0\",\"p_level\":\"P0\"},\"startsAt\":\"$STARTS_AT\",\"endsAt\":\"$ENDS_AT\"},
    {\"labels\":{\"alertname\":\"G8ControlledP1\",\"p_level\":\"P1\"},\"startsAt\":\"$STARTS_AT\",\"endsAt\":\"$ENDS_AT\"}
  ]" http://"$ALERTMANAGER":9093/api/v2/alerts >/dev/null

for _ in $(seq 1 30); do
  if [[ -f "$TMP_DIR/events.jsonl" ]] && grep -q '"path": "/alerts/p0".*"status": "firing"' "$TMP_DIR/events.jsonl" && grep -q '"path": "/alerts/p1".*"status": "firing"' "$TMP_DIR/events.jsonl"; then
    break
  fi
  sleep 1
done

RESOLVED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
docker run --rm --network "$NETWORK" curlimages/curl:8.15.0 -fsS -X POST \
  -H 'Content-Type: application/json' --data "[
    {\"labels\":{\"alertname\":\"G8ControlledP0\",\"p_level\":\"P0\"},\"startsAt\":\"$STARTS_AT\",\"endsAt\":\"$RESOLVED_AT\"},
    {\"labels\":{\"alertname\":\"G8ControlledP1\",\"p_level\":\"P1\"},\"startsAt\":\"$STARTS_AT\",\"endsAt\":\"$RESOLVED_AT\"}
  ]" http://"$ALERTMANAGER":9093/api/v2/alerts >/dev/null

for _ in $(seq 1 30); do
  if grep -q '"path": "/alerts/p0".*"status": "resolved"' "$TMP_DIR/events.jsonl" && grep -q '"path": "/alerts/p1".*"status": "resolved"' "$TMP_DIR/events.jsonl"; then
    echo "G8_ALERT_ROUTING=PASS p0=firing,resolved p1=firing,resolved real_contact=false"
    exit 0
  fi
  sleep 1
done

echo "G8_ALERT_ROUTING=FAIL" >&2
exit 1
