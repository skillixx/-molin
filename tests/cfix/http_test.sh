#!/usr/bin/env bash
# 后端丙 PR#151 HTTP 层回归（C-FIX-4 分页契约 / C-FIX-2a 资产取消 handler / C-FIX-6 公告端点）。
# 依赖：API 运行于 127.0.0.1:8080，tests/cfix/token.txt 为 user_id=1 的管理员 JWT。
set -u
BASE="http://127.0.0.1:8080"
TOKEN="$(cat "$(dirname "$0")/token.txt")"
CURL="curl -s -m 10"
export NO_PROXY='*'
export no_proxy='*'
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
PASS=0; FAIL=0
declare -a FAILED

ck() { # name  condition_result(0/1)  detail
  if [ "$2" = "0" ]; then PASS=$((PASS+1)); echo "  [PASS] $1";
  else FAIL=$((FAIL+1)); FAILED+=("$1 -> $3"); echo "  [FAIL] $1"; echo "     证据: $3"; fi
}

auth=(-H "Authorization: Bearer $TOKEN")

echo "========== C-FIX-4 管理端 6 列表分页契约 {items,page,page_size,total} =========="
declare -a ENDPOINTS=(
  "/api/admin/assets"
  "/api/admin/user-memberships"
  "/api/admin/announcements"
  "/api/admin/help/articles"
  "/api/admin/apps"
  "/api/admin/app-adapters"
)
for ep in "${ENDPOINTS[@]}"; do
  body=$($CURL "${auth[@]}" "$BASE$ep?page=1&page_size=7")
  # 提取 data 对象的 key
  has_ps=$(echo "$body" | python3 -c 'import sys,json;d=json.load(sys.stdin).get("data") or {};print("1" if "page_size" in d else "0")' 2>/dev/null)
  has_all=$(echo "$body" | python3 -c 'import sys,json;d=json.load(sys.stdin).get("data") or {};print("1" if all(k in d for k in ("items","page","page_size","total")) else "0")' 2>/dev/null)
  ps_val=$(echo "$body" | python3 -c 'import sys,json;d=json.load(sys.stdin).get("data") or {};print(d.get("page_size"))' 2>/dev/null)
  if [ "$has_all" = "1" ]; then ck "$ep 含 items/page/page_size/total (page_size=$ps_val)" 0 "";
  else ck "$ep 含 items/page/page_size/total" 1 "resp=$(echo "$body" | head -c 200)"; fi
done

echo ""
echo "========== C-FIX-2a 资产取消 HTTP（PATCH /api/admin/assets/{id}） =========="
# 取一个 active 资产 id（直接查库会更稳，这里用脚本传入环境变量 ASSET_ID）
if [ -n "${ASSET_ID:-}" ]; then
  # 非法 action 返回支持列表含 cancel
  badbody=$($CURL "${auth[@]}" -X PATCH -H "Content-Type: application/json" -d '{"action":"bogus"}' "$BASE/api/admin/assets/$ASSET_ID")
  echo "$badbody" | grep -q "cancel" && ck "非法 action 错误信息含 cancel" 0 "" || ck "非法 action 错误信息含 cancel" 1 "resp=$badbody"

  # 合法 cancel
  okbody=$($CURL "${auth[@]}" -X PATCH -H "Content-Type: application/json" -d '{"action":"cancel","remark":"http-cancel"}' "$BASE/api/admin/assets/$ASSET_ID")
  code=$(echo "$okbody" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("code"))' 2>/dev/null)
  [ "$code" = "0" ] && ck "cancel action 返回成功 (code=0)" 0 "" || ck "cancel action 返回成功" 1 "resp=$okbody"

  # 重复 cancel 应失败
  again=$($CURL "${auth[@]}" -X PATCH -H "Content-Type: application/json" -d '{"action":"cancel"}' "$BASE/api/admin/assets/$ASSET_ID")
  acode=$(echo "$again" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("code"))' 2>/dev/null)
  [ "$acode" != "0" ] && ck "已取消资产重复 cancel 被拒绝" 0 "" || ck "已取消资产重复 cancel 被拒绝" 1 "resp=$again"
else
  echo "  [SKIP] 未提供 ASSET_ID，跳过 HTTP 资产取消（service 层已覆盖）"
fi

echo ""
echo "========== C-FIX-6 用户端公告端点扁平分页 GET /api/announcements =========="
abody=$($CURL "${auth[@]}" "$BASE/api/announcements?page=1&page_size=10")
ahas=$(echo "$abody" | python3 -c 'import sys,json;d=json.load(sys.stdin).get("data") or {};print("1" if all(k in d for k in ("items","page","page_size","total")) else "0")' 2>/dev/null)
[ "$ahas" = "1" ] && ck "/api/announcements 返回扁平分页信封 {items,page,page_size,total}" 0 "" || ck "/api/announcements 返回扁平分页信封" 1 "resp=$(echo "$abody"|head -c 200)"

echo ""
echo "========== HTTP 层小结：PASS=$PASS FAIL=$FAIL =========="
if [ "$FAIL" -gt 0 ]; then
  echo "失败项："
  for f in "${FAILED[@]}"; do echo "  - $f"; done
  exit 1
fi
