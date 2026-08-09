#!/usr/bin/env python3
"""只核对 Bifrost 专用环境文件的变量名，不读取或输出任何密钥值。"""

import argparse
import re
from pathlib import Path


NODE_KEYS = {"BIFROST_ENCRYPTION_KEY", "OPENROUTER_API_KEY", "BAILIAN_API_KEY"}
LB_KEYS = {"BIFROST_INTERNAL_TOKEN"}
KEY_PATTERN = re.compile(r"^[A-Z][A-Z0-9_]*$")


def read_keys(path: Path) -> set[str]:
    """解析 Compose env_file 的变量名，并拒绝重复键或不明确语法。"""
    if not path.is_file():
        raise ValueError(f"环境文件不存在：{path}")
    keys: set[str] = set()
    for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise ValueError(f"{path} 第 {line_number} 行缺少等号")
        key = line.split("=", 1)[0].strip()
        if not KEY_PATTERN.fullmatch(key):
            raise ValueError(f"{path} 第 {line_number} 行变量名不合法")
        if key in keys:
            raise ValueError(f"{path} 存在重复变量名：{key}")
        keys.add(key)
    return keys


def verify_scope(path: Path, expected: set[str], role: str) -> None:
    """按精确白名单核对变量名，既拒绝越权变量，也拒绝必需变量缺失。"""
    actual = read_keys(path)
    if actual != expected:
        missing = sorted(expected - actual)
        extra = sorted(actual - expected)
        raise ValueError(f"{role} 环境变量范围不合法：缺失={missing}，越权={extra}")


def main() -> int:
    parser = argparse.ArgumentParser(description="校验 G8 Bifrost 环境文件最小权限范围")
    parser.add_argument("--node-env", type=Path, required=True)
    parser.add_argument("--lb-env", type=Path, required=True)
    args = parser.parse_args()
    try:
        verify_scope(args.node_env, NODE_KEYS, "Bifrost 节点")
        verify_scope(args.lb_env, LB_KEYS, "Bifrost LB")
    except ValueError as exc:
        print(f"G8_BIFROST_SECRET_SCOPE=FAILED reason={exc}")
        return 2
    print("G8_BIFROST_SECRET_SCOPE=PASS node_keys=3 lb_keys=1 values_read=false")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
