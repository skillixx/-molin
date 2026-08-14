#!/bin/bash
# 016 已在本地模块门禁失败并消费；历史安装器不得重放。
set -eu
printf '%s\n' 'G8_TEST_READONLY_ACCESS_INSTALL_016=FAILED reason=change_id_consumed'
exit 2
