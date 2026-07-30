#!/usr/bin/env python3
"""验证 API 不可达门禁固定退出 2；通过注入执行，不访问真实 API。"""

import pathlib
import subprocess
import sys


def main():
    target = pathlib.Path(__file__).with_name("phase2_email_api.py")
    completed = subprocess.run(
        [sys.executable, str(target), "--self-test-unreachable"],
        capture_output=True,
        text=True,
        timeout=10,
        check=False,
    )
    if completed.returncode != 2:
        print(f"[FAIL] API 不可达退出码应为 2，实际为 {completed.returncode}")
        return 1
    if "[BLOCKED]" not in completed.stdout:
        print("[FAIL] API 不可达时缺少 BLOCKED 输出")
        return 1
    print("[PASS] API 不可达固定输出 BLOCKED 且退出码为 2")
    return 0


if __name__ == "__main__":
    sys.exit(main())
