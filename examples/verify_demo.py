"""进程内端到端演示（不依赖浏览器、不占用端口）。

把示例应用 + 本地 mock 平台用 Starlette TestClient 在同一进程里串起来跑，
完整走一遍「进入应用 → 认人 → 用功能 → 计费/扣额度」，直接打印结果。
适合在受限环境（无法对 localhost 起服务）里"看效果"。

用法：
    python verify_demo.py postpaid
    python verify_demo.py prepaid

想用浏览器交互式体验，改用 run_local.sh（见 README）。
"""
import sys
import os
import types
import importlib.util

from starlette.testclient import TestClient

mode = sys.argv[1] if len(sys.argv) > 1 else "postpaid"
assert mode in ("postpaid", "prepaid"), "用法：python verify_demo.py postpaid|prepaid"

BASE = os.path.dirname(os.path.abspath(__file__))
appdir = os.path.join(BASE, f"{mode}-app")

os.chdir(appdir)                 # 让 dotenv 读到本目录 .env
sys.path.insert(0, appdir)
import config            # noqa: E402
import platform_client   # noqa: E402

# 加载 mock 平台为独立模块
spec = importlib.util.spec_from_file_location("mockmod", os.path.join(BASE, "mock-platform", "app.py"))
mockmod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mockmod)
mock_client = TestClient(mockmod.app)

# 把示例应用 platform_client 的 httpx 换成"指向 mock 的进程内 TestClient"
class _Wrap:
    def __enter__(self):
        return mock_client

    def __exit__(self, *a):
        return False


platform_client.httpx = types.SimpleNamespace(Client=lambda *a, **k: _Wrap())
config.PLATFORM_BASE_URL = "http://testserver"   # 与 TestClient base_url 对齐

# 加载示例应用
spec2 = importlib.util.spec_from_file_location("exampleapp", os.path.join(appdir, "app.py"))
exa = importlib.util.module_from_spec(spec2)
spec2.loader.exec_module(exa)
app_client = TestClient(exa.app)

print(f"==================== {mode} 端到端 ====================")
r = mock_client.get(f"/launch?app={mode}", follow_redirects=False)
ticket = r.headers["location"].split("ticket=")[1]
print(f"① 平台签发一次性票据: {ticket}")

r = app_client.get(f"/enter?ticket={ticket}", follow_redirects=True)
print(f"② 进入应用 /enter -> HTTP {r.status_code}，已进入工作台: {'user_id' in r.text}")

if mode == "postpaid":
    print("   初始钱包:", mock_client.get("/state").json()["data"]["wallet"])
    for i in range(3):
        rr = app_client.post("/api/convert", data={"text": f"hello molin {i}"}).json()
        print(f"   第{i+1}次转换 -> result={rr.get('result')!r}  本次扣费={rr.get('billed_amount')}")
    for _ in range(3):
        app_client.post("/api/convert", data={"text": "x"})
    rr = app_client.post("/api/convert", data={"text": "x"})
    print(f"   余额耗尽后再点 -> HTTP {rr.status_code} {rr.json()}")
    print("   最终钱包:", mock_client.get("/state").json()["data"]["wallet"])
else:
    print("   初始积分:", mock_client.get("/state").json()["data"]["entitlement"])
    for i in range(2):
        rr = app_client.post("/api/generate", data={"topic": f"夏日饮品{i}"}).json()
        print(f"   第{i+1}次生成 -> 预占={rr.get('reserved')} 实扣={rr.get('actual_cost')} "
              f"累计已用={rr.get('quota_used')} 剩余={rr.get('available')}")
    print("   最终积分:", mock_client.get("/state").json()["data"]["entitlement"])

r2 = app_client.get(f"/enter?ticket={ticket}", follow_redirects=False)
print(f"④ 同票据重放 -> HTTP {r2.status_code}（应 403，票据一次性）")
print("[OK] 端到端跑通")
