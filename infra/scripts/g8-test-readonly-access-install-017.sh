#!/bin/bash
# 017 已按失败关闭规则消费；历史安装器不得重放。
set -eu
printf '%s\n' 'G8_TEST_READONLY_ACCESS_INSTALL_017=FAILED reason=change_id_consumed'
exit 2
