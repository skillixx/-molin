#!/usr/bin/env python3
"""已消费的 014 历史入口；必须在解析参数、读取材料和联网前固定拒绝。"""


def main() -> int:
    """014 已形成唯一远端三态证据，任何后续调用都只返回固定低敏消费状态。"""
    print("G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=FAILED reason=change_id_consumed")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
