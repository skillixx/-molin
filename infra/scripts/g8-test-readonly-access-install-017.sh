#!/bin/bash
# 017 只从已由 014 证明完整的 011 暂存安装最小只读入口；不接受调用方参数。
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME
umask 077

AUTH_CHANGE_ID='CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017'
SOURCE_CHANGE_ID='CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011'
STAGING='/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011'
ROOT_COPY='/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017'
EXPECTED_FILES='SHA256SUMS ai-gateway-reconcile g8-test-readonly-audit manifest.env molin-g8-test-readonly-audit.sudoers'
EXPECTED_RECEIPT='15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f'
AUDITOR_SHA='308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256'
SUDOERS_SHA='1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f'
RECONCILE_SHA='37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1'
RECONCILE_SIZE='13066129'
AUDITOR_TARGET='/usr/local/libexec/molin/g8-test-readonly-audit'
RECONCILE_TARGET='/usr/local/libexec/molin/ai-gateway-reconcile'
SUDOERS_TARGET='/etc/sudoers.d/molin-g8-test-readonly-audit'
TOOLS_PARENT='/usr/local/libexec/molin'
VISUDO_BIN='/usr/sbin/visudo'

created_auditor=0
created_reconcile=0
created_sudoers=0
created_parent=0
install_complete=0

fail() {
    /usr/bin/printf '%s\n' 'G8_TEST_READONLY_ACCESS_INSTALL_017=FAILED reason=install_gate_failed'
    return 1
}

rollback() {
    rc=$?
    if [ "$install_complete" -eq 0 ]; then
        # sudoers 必须先移除并重新校验全局语法，再撤销本次创建的两个工具。
        if [ "$created_sudoers" -eq 1 ]; then
            /usr/bin/rm -f -- "$SUDOERS_TARGET"
            "$VISUDO_BIN" -cf /etc/sudoers >/dev/null 2>&1 || :
        fi
        if [ "$created_reconcile" -eq 1 ]; then
            /usr/bin/rm -f -- "$RECONCILE_TARGET"
        fi
        if [ "$created_auditor" -eq 1 ]; then
            /usr/bin/rm -f -- "$AUDITOR_TARGET"
        fi
        if [ "$created_parent" -eq 1 ] && [ -d "$TOOLS_PARENT" ]; then
            /usr/bin/rmdir -- "$TOOLS_PARENT" 2>/dev/null || :
        fi
    fi
    if [ "$rc" -ne 0 ]; then
        fail || :
    fi
    exit "$rc"
}
trap rollback EXIT

check_secure_directory() {
    directory=$1
    expected_owner=$2
    expected_mode=$3
    [ -d "$directory" ] && [ ! -L "$directory" ] || return 1
    [ "$(/usr/bin/realpath -e -- "$directory")" = "$directory" ] || return 1
    [ "$(/usr/bin/stat -c '%U:%G:%a' -- "$directory")" = "$expected_owner:$expected_mode" ]
}

check_parent_directory() {
    directory=$1
    [ -d "$directory" ] && [ ! -L "$directory" ] || return 1
    [ "$(/usr/bin/stat -c '%U:%G' -- "$directory")" = 'root:root' ] || return 1
    mode=$((8#$(/usr/bin/stat -c '%a' -- "$directory")))
    [ $((mode & 0022)) -eq 0 ]
}

check_file() {
    path=$1
    [ -f "$path" ] && [ ! -L "$path" ]
}

check_manifest_line() {
    expected=$1
    manifest=$2
    # 冻结的 011 manifest 使用 CRLF；严格保留该字节契约，禁止静默改写已核验制品。
    /usr/bin/grep -Fqx "$expected"$'\r' "$manifest"
}

check_candidate() {
    directory=$1
    allow_installer=$2
    expected_owner=$3
    if [ "$allow_installer" -eq 1 ]; then
        actual=$(/usr/bin/find "$directory" -mindepth 1 -maxdepth 1 ! -name 'g8-test-readonly-access-install-017.sh' -printf '%f\n' | /usr/bin/sort | /usr/bin/tr '\n' ' ')
        check_file "$directory/g8-test-readonly-access-install-017.sh" || return 1
        [ "$(/usr/bin/stat -c '%U:%G:%a' -- "$directory/g8-test-readonly-access-install-017.sh")" = 'root:root:700' ] || return 1
    else
        actual=$(/usr/bin/find "$directory" -mindepth 1 -maxdepth 1 -printf '%f\n' | /usr/bin/sort | /usr/bin/tr '\n' ' ')
    fi
    [ "$actual" = "$EXPECTED_FILES " ] || return 1
    for entry in SHA256SUMS:600 ai-gateway-reconcile:700 g8-test-readonly-audit:700 manifest.env:600 molin-g8-test-readonly-audit.sudoers:600; do
        name=${entry%%:*}
        mode=${entry##*:}
        check_file "$directory/$name" || return 1
        [ "$(/usr/bin/stat -c '%U:%G:%a' -- "$directory/$name")" = "$expected_owner:$mode" ] || return 1
    done
    [ "$(/usr/bin/sha256sum "$directory/SHA256SUMS" | /usr/bin/cut -d' ' -f1)" = "$EXPECTED_RECEIPT" ] || return 1
    (cd "$directory" && /usr/bin/sha256sum -c SHA256SUMS >/dev/null 2>&1) || return 1
    [ "$(/usr/bin/sha256sum "$directory/g8-test-readonly-audit" | /usr/bin/cut -d' ' -f1)" = "$AUDITOR_SHA" ] || return 1
    [ "$(/usr/bin/sha256sum "$directory/molin-g8-test-readonly-audit.sudoers" | /usr/bin/cut -d' ' -f1)" = "$SUDOERS_SHA" ] || return 1
    [ "$(/usr/bin/sha256sum "$directory/ai-gateway-reconcile" | /usr/bin/cut -d' ' -f1)" = "$RECONCILE_SHA" ] || return 1
    [ "$(/usr/bin/stat -c '%s' -- "$directory/ai-gateway-reconcile")" = "$RECONCILE_SIZE" ] || return 1
    check_manifest_line "CHANGE_ID=$SOURCE_CHANGE_ID" "$directory/manifest.env" || return 1
    check_manifest_line 'TARGET_TRANSPORT=DROP_SSH_INTERACTIVE_SUDO' "$directory/manifest.env" || return 1
    check_manifest_line 'PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE' "$directory/manifest.env"
}

copy_no_clobber() {
    source=$1
    target=$2
    target_mode=$3
    [ ! -e "$target" ] && [ ! -L "$target" ] || return 1
    set -o noclobber
    if ! exec 3> "$target"; then
        set +o noclobber
        return 1
    fi
    set +o noclobber
    if ! /usr/bin/cat "$source" >&3 || ! exec 3>&- \
        || ! /usr/bin/chown root:root "$target" || ! /usr/bin/chmod "$target_mode" "$target"; then
        exec 3>&- 2>/dev/null || :
        /usr/bin/rm -f -- "$target"
        return 1
    fi
}

install_live_file() {
    source=$1
    target=$2
    target_mode=$3
    created_variable=$4
    copy_no_clobber "$source" "$target" "$target_mode" || return 1
    builtin printf -v "$created_variable" '%s' 1
}

validate_sudo_scope() {
    # 只接受一个冻结的 NOPASSWD 审计命令；额外 SETENV、通配符、Shell 或 Docker 能力全部拒绝。
    scope=$(LC_ALL=C /usr/bin/sudo -n -l -U pc 2>/dev/null) || return 1
    /usr/bin/printf '%s\n' "$scope" | /usr/bin/grep -q 'SETENV' && return 1
    nopasswd=$(/usr/bin/printf '%s\n' "$scope" | /usr/bin/grep 'NOPASSWD:' || :)
    [ "$(/usr/bin/printf '%s\n' "$nopasswd" | /usr/bin/grep -c .)" -eq 1 ] || return 1
    /usr/bin/printf '%s\n' "$nopasswd" \
        | /usr/bin/grep -Eq '^[[:space:]]*\(root\) NOPASSWD: /usr/local/libexec/molin/g8-test-readonly-audit[[:space:]]*$' \
        || return 1
    /usr/bin/printf '%s\n' "$nopasswd" | /usr/bin/grep -Eq '[*]|/bin/(ba)?sh|docker' && return 1
    return 0
}

validate_auditor_entry() {
    # 以 pc 身份实际命中新增的唯一 NOPASSWD 规则；失败时仍处于回滚事务内。
    /usr/bin/sudo -u pc -- /usr/bin/sudo -n "$AUDITOR_TARGET" --self-test
}

main() {
    if [ "$#" -ne 0 ] || [ "$(/usr/bin/id -u)" -ne 0 ]; then
        exit 2
    fi

    check_secure_directory "$STAGING" 'pc:pc' '700'
    check_secure_directory "$ROOT_COPY" 'root:root' '700'
    check_candidate "$STAGING" 0 'pc:pc'

    # root-only 副本逐文件 no-clobber，随后重新按冻结摘要验证，阻断低权限暂存漂移。
    for entry in SHA256SUMS:600 ai-gateway-reconcile:700 g8-test-readonly-audit:700 manifest.env:600 molin-g8-test-readonly-audit.sudoers:600; do
        name=${entry%%:*}
        mode=${entry##*:}
        copy_no_clobber "$STAGING/$name" "$ROOT_COPY/$name" "0$mode"
    done
    check_candidate "$ROOT_COPY" 1 'root:root'
    "$VISUDO_BIN" -cf "$ROOT_COPY/molin-g8-test-readonly-audit.sudoers" >/dev/null

    for parent in /usr /usr/local /usr/local/libexec /etc /etc/sudoers.d; do
        check_parent_directory "$parent"
    done
    if [ -e "$TOOLS_PARENT" ] || [ -L "$TOOLS_PARENT" ]; then
        check_parent_directory "$TOOLS_PARENT"
    else
        /usr/bin/mkdir -m 0755 -- "$TOOLS_PARENT"
        created_parent=1
        /usr/bin/chown root:root "$TOOLS_PARENT"
        check_parent_directory "$TOOLS_PARENT"
    fi

    for target in "$AUDITOR_TARGET" "$RECONCILE_TARGET" "$SUDOERS_TARGET"; do
        [ ! -e "$target" ] && [ ! -L "$target" ]
    done

    install_live_file "$ROOT_COPY/g8-test-readonly-audit" "$AUDITOR_TARGET" 0755 created_auditor
    install_live_file "$ROOT_COPY/ai-gateway-reconcile" "$RECONCILE_TARGET" 0755 created_reconcile
    install_live_file "$ROOT_COPY/molin-g8-test-readonly-audit.sudoers" "$SUDOERS_TARGET" 0440 created_sudoers

    [ "$(/usr/bin/stat -c '%U:%G:%a' -- "$AUDITOR_TARGET")" = 'root:root:755' ]
    [ "$(/usr/bin/stat -c '%U:%G:%a' -- "$RECONCILE_TARGET")" = 'root:root:755' ]
    [ "$(/usr/bin/stat -c '%U:%G:%a' -- "$SUDOERS_TARGET")" = 'root:root:440' ]
    [ "$(/usr/bin/sha256sum "$AUDITOR_TARGET" | /usr/bin/cut -d' ' -f1)" = "$AUDITOR_SHA" ]
    [ "$(/usr/bin/sha256sum "$RECONCILE_TARGET" | /usr/bin/cut -d' ' -f1)" = "$RECONCILE_SHA" ]
    [ "$(/usr/bin/stat -c '%s' -- "$RECONCILE_TARGET")" = "$RECONCILE_SIZE" ]
    [ "$(/usr/bin/sha256sum "$SUDOERS_TARGET" | /usr/bin/cut -d' ' -f1)" = "$SUDOERS_SHA" ]
    "$VISUDO_BIN" -cf "$SUDOERS_TARGET" >/dev/null
    validate_sudo_scope
    if /usr/bin/id -nG pc | /usr/bin/grep -Eq '(^|[[:space:]])docker([[:space:]]|$)'; then
        exit 1
    fi
    validate_auditor_entry

    install_complete=1
    trap - EXIT
    /usr/bin/printf '%s\n' 'G8_TEST_READONLY_ACCESS_INSTALL_017=PASS'
}

main "$@"
