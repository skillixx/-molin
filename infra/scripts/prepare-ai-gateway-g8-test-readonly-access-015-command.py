#!/usr/bin/env python3
"""已消费的 015 历史生成入口；必须在解析参数、读取材料和联网前固定拒绝。"""


def main() -> int:
    """015 已在本地路径门禁失败，任何后续调用都只返回固定低敏消费状态。"""
    print("G8_TEST_READONLY_ACCESS_015_COMMAND=FAILED reason=change_id_consumed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
