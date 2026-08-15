#!/usr/bin/env python3
"""已消费的 021 历史生成入口；必须在解析参数、读取材料和联网前固定拒绝。"""


def main() -> int:
    """唯一授权尝试在外部能力前失败关闭，任何后续调用只返回固定消费状态。"""
    print("G8_TEST_READONLY_RUNTIME_AUDIT_021_COMMAND=FAILED reason=change_id_consumed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
