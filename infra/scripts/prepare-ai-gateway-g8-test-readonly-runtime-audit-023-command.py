#!/usr/bin/env python3
"""已消费的 023 历史生成入口；必须在解析参数、读取材料和联网前固定拒绝。"""


def main() -> int:
    """023 已失败关闭消费，任何后续调用只返回固定消费状态。"""
    print("G8_TEST_READONLY_RUNTIME_AUDIT_023_COMMAND=FAILED reason=change_id_consumed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
