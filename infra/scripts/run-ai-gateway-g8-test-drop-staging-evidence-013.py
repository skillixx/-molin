#!/usr/bin/env python3
"""已失效的 013 历史入口；必须在解析参数、读取材料和联网前固定拒绝。"""


def main() -> int:
    """013 已被工程修复作废，任何调用都只返回固定低敏消费状态。"""
    print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=FAILED reason=change_id_consumed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
