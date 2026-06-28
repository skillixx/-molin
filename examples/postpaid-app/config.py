"""配置：从环境变量读取平台对接所需信息。

只在这里集中读取 env，其余模块通过导入常量使用，方便排查"少配了哪个值"。
启动时自动加载同目录下的 .env（由 .env.example 复制而来）。
"""
import os

from dotenv import load_dotenv

# 加载同目录 .env（若不存在则静默跳过，改用真实环境变量）
load_dotenv()


def _require(name: str) -> str:
    """读取必填环境变量，缺失时直接报错（fail-fast，避免带着空密钥裸奔）。"""
    val = os.getenv(name, "").strip()
    if not val:
        raise RuntimeError(f"缺少必填环境变量 {name}，请参考 .env.example 配置后再启动")
    return val


# 平台 API 根地址（同机部署用 127.0.0.1，不要用 localhost）
PLATFORM_BASE_URL = os.getenv("PLATFORM_BASE_URL", "http://127.0.0.1:8080").rstrip("/")

# 平台方下发的内部接口密钥
INTERNAL_API_TOKEN = _require("INTERNAL_API_TOKEN")

# 平台方约定的用量类型 / 单位
USAGE_TYPE = os.getenv("USAGE_TYPE", "text_convert").strip()
USAGE_UNIT = os.getenv("USAGE_UNIT", "count").strip()

# 本应用自用的 Cookie 签名密钥
SESSION_SECRET = os.getenv("SESSION_SECRET", "dev-insecure-secret").strip()
