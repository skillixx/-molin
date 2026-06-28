#!/usr/bin/env bash
# 在你自己的机器上一键启动「mock 平台 + 两个示例应用」，用浏览器交互体验。
#
# 用法：  bash run_local.sh
# 然后浏览器打开： http://127.0.0.1:8080  →  点「进入应用」按钮即可。
# 停止：  按 Ctrl+C（会一并停掉三个服务）。
set -euo pipefail
cd "$(dirname "$0")"

# 准备虚拟环境与依赖（仅首次需要）
if [ ! -x .venv/bin/python ]; then
  echo "== 创建虚拟环境并安装依赖 =="
  python3 -m venv .venv 2>/dev/null || python3 -m venv --without-pip .venv
  if [ ! -x .venv/bin/pip ]; then
    curl -s https://bootstrap.pypa.io/get-pip.py | .venv/bin/python
  fi
  .venv/bin/pip install -q fastapi "uvicorn[standard]" httpx itsdangerous python-dotenv python-multipart
fi
PY=.venv/bin/python

# 准备示例 .env（仅首次）
for app in postpaid-app prepaid-app; do
  [ -f "$app/.env" ] || cp "$app/.env.example" "$app/.env"
done
# 把示例 .env 的内部密钥对齐到 mock 平台的 demo token
sed -i 's/^INTERNAL_API_TOKEN=.*/INTERNAL_API_TOKEN=demo-internal-token-123/' postpaid-app/.env prepaid-app/.env

echo "== 启动 mock 平台(8080) / 按量付费(9001) / 预付(9002) =="
( cd mock-platform && exec $PY -m uvicorn app:app --host 127.0.0.1 --port 8080 ) &
( cd postpaid-app  && exec $PY -m uvicorn app:app --host 127.0.0.1 --port 9001 ) &
( cd prepaid-app   && exec $PY -m uvicorn app:app --host 127.0.0.1 --port 9002 ) &
trap 'echo; echo "停止所有服务"; kill 0' INT TERM EXIT

sleep 2
echo
echo "================================================================"
echo "  打开浏览器： http://127.0.0.1:8080"
echo "  点「进入应用」按钮 → 体验按量付费 / 预付扣积分两条链路"
echo "  Ctrl+C 退出"
echo "================================================================"
wait
