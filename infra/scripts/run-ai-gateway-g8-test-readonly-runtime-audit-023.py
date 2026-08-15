#!/usr/bin/env python3
"""已消费的 023 固定启动入口；必须在解析参数、读取材料和启动子进程前固定拒绝。"""


def main() -> int:
    """023 已失败关闭消费，后续调用只返回固定消费状态。"""
    print("G8_TEST_READONLY_RUNTIME_AUDIT_023_RUNNER=FAILED reason=change_id_consumed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
