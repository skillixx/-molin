"""配置：从环境变量读取平台对接所需信息。启动时自动加载同目录下的 .env。"""
import os

from dotenv import load_dotenv

# 加载同目录 .env（若不存在则静默跳过，改用真实环境变量）
load_dotenv()


def _require(name: str) -> str:
    val = os.getenv(name, "").strip()
    if not val:
        raise RuntimeError(f"缺少必填环境变量 {name}，请参考 .env.example 配置后再启动")
    return val


# 平台 API 根地址（同机部署用 127.0.0.1，不要用 localhost）
PLATFORM_BASE_URL = os.getenv("PLATFORM_BASE_URL", "http://127.0.0.1:8080").rstrip("/")

# 平台方下发的内部接口密钥
INTERNAL_API_TOKEN = _require("INTERNAL_API_TOKEN")

# 一次"生成"动作预占的积分上限
RESERVE_ESTIMATE = int(os.getenv("RESERVE_ESTIMATE", "10"))

# 本应用自用的 Cookie 签名密钥
SESSION_SECRET = os.getenv("SESSION_SECRET", "dev-insecure-secret").strip()
