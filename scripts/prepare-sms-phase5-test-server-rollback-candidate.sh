#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null
umask 077

# 本脚本只在独立授权后生成关闭态候选配置，不重启服务、不替换当前配置、不调用短信或其他外部服务。
current_environment='__CURRENT_ENVIRONMENT_PATH__'
candidate_path='__CANDIDATE_PATH__'
candidate_root='__CANDIDATE_ROOT__'

fail() {
  printf 'rollback_candidate=failed\n'
  printf 'failure_stage=%s\n' "$1"
  printf 'candidate_sensitive_values_printed=0\n'
  printf 'service_restarts=0\n'
  printf 'real_sms_sent=0\n'
  exit 2
}

[ -f "$current_environment" ] && [ ! -L "$current_environment" ] || fail current_environment_identity
[ "$(realpath -- "$current_environment")" = "$current_environment" ] || fail current_environment_path
[ "$(stat -c '%U:%a' "$current_environment")" = 'pc:600' ] || fail current_environment_permissions

python3 - "$current_environment" "$candidate_root" "$candidate_path" <<'PY' || fail candidate_generation
import os
import re
import stat
import sys


source_path, root_path, candidate_path = sys.argv[1:]
expected_root = "__CANDIDATE_ROOT__"
if root_path != expected_root:
    raise SystemExit(2)
candidate_pattern = re.escape(expected_root) + r"/candidate-[0-9]{8}T[0-9]{6}Z\.env"
if re.fullmatch(candidate_pattern, candidate_path) is None:
    raise SystemExit(2)

source_flags = os.O_RDONLY
if hasattr(os, "O_NOFOLLOW"):
    source_flags |= os.O_NOFOLLOW
source_descriptor = os.open(source_path, source_flags)
source_stat = os.fstat(source_descriptor)
source_uid = os.getuid() if hasattr(os, "getuid") else source_stat.st_uid
source_mode_valid = stat.S_IMODE(source_stat.st_mode) == 0o600 if os.name != "nt" else True
if not stat.S_ISREG(source_stat.st_mode) or source_stat.st_uid != source_uid or not source_mode_valid:
    os.close(source_descriptor)
    raise SystemExit(2)
with os.fdopen(source_descriptor, "r", encoding="utf-8", newline="") as stream:
    raw_lines = stream.readlines()

seen_keys = set()
values = {}
parsed_lines = []
for raw_line in raw_lines:
    line = raw_line.strip()
    if not line or line.startswith("#"):
        parsed_lines.append((None, raw_line))
        continue
    match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
    if match is None or match.group(1) in seen_keys:
        raise SystemExit(2)
    key, value = match.groups()
    seen_keys.add(key)
    normalized = value.strip()
    if len(normalized) not in (0, 1) and normalized[0] == normalized[-1] and normalized[0] in "'\"":
        normalized = normalized[1:-1]
    values[key] = normalized
    parsed_lines.append((key, raw_line))

required_keys = {
    "APP_ENV", "API_HOST", "API_PORT", "MYSQL_HOST", "MYSQL_PORT", "MYSQL_DATABASE",
    "MYSQL_USER", "MYSQL_PASSWORD", "REDIS_ADDR", "JWT_SECRET", "REFRESH_TOKEN_SECRET",
    "SMS_ENABLED", "SMS_TEST_MODE", "TRUSTED_PROXY_IPS",
}
if not required_keys.issubset(seen_keys):
    raise SystemExit(2)

trusted_items = {item.strip() for item in values["TRUSTED_PROXY_IPS"].split(",") if item.strip()}
fixed_proxy_compatible = (
    "172.20.250.0/28" in trusted_items
    or {"172.20.250.2", "172.20.250.3"}.issubset(trusted_items)
)
if not fixed_proxy_compatible:
    raise SystemExit(2)

candidate_lines = []
for key, raw_line in parsed_lines:
    if key is not None and key.startswith("SMS_TEMPLATE_CODE_"):
        continue
    if key == "SMS_ENABLED":
        candidate_lines.append("SMS_ENABLED=false\n")
    elif key == "SMS_TEST_MODE":
        candidate_lines.append("SMS_TEST_MODE=true\n")
    else:
        candidate_lines.append(raw_line if raw_line.endswith("\n") else raw_line + "\n")
candidate_bytes = "".join(candidate_lines).encode("utf-8")

flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
if hasattr(os, "O_NOFOLLOW"):
    flags |= os.O_NOFOLLOW
descriptor = None
created = False
parent_descriptor = None
root_descriptor = None
candidate_name = os.path.basename(candidate_path)
try:
    if os.name == "nt":
        os.makedirs(root_path, mode=0o700, exist_ok=True)
        root_stat = os.lstat(root_path)
        if not stat.S_ISDIR(root_stat.st_mode):
            raise OSError("候选目录异常")
        descriptor = os.open(candidate_path, flags, 0o600)
    else:
        expected_parent = os.path.dirname(expected_root)
        if os.path.realpath(expected_parent) != expected_parent:
            raise OSError("候选父目录路径异常")
        parent_stat = os.lstat(expected_parent)
        if (
            not stat.S_ISDIR(parent_stat.st_mode)
            or parent_stat.st_uid != os.getuid()
            or stat.S_IMODE(parent_stat.st_mode) != 0o700
        ):
            raise OSError("候选父目录身份异常")
        directory_flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
        parent_descriptor = os.open(expected_parent, directory_flags)
        try:
            os.mkdir(os.path.basename(expected_root), mode=0o700, dir_fd=parent_descriptor)
        except FileExistsError:
            pass
        root_descriptor = os.open(
            os.path.basename(expected_root),
            directory_flags,
            dir_fd=parent_descriptor,
        )
        root_stat = os.fstat(root_descriptor)
        if (
            not stat.S_ISDIR(root_stat.st_mode)
            or root_stat.st_uid != os.getuid()
            or stat.S_IMODE(root_stat.st_mode) != 0o700
        ):
            raise OSError("候选目录身份异常")
        descriptor = os.open(candidate_name, flags, 0o600, dir_fd=root_descriptor)
    created = True
    view = memoryview(candidate_bytes)
    while view:
        written = os.write(descriptor, view)
        if written < 1:
            raise OSError("候选配置写入失败")
        view = view[written:]
    os.fsync(descriptor)
    os.close(descriptor)
    descriptor = None
    candidate_stat = (
        os.lstat(candidate_path)
        if os.name == "nt"
        else os.stat(candidate_name, dir_fd=root_descriptor, follow_symlinks=False)
    )
    mode = stat.S_IMODE(candidate_stat.st_mode)
    mode_valid = mode == 0o600 if os.name != "nt" else True
    if not mode_valid or not stat.S_ISREG(candidate_stat.st_mode):
        raise OSError("候选配置权限异常")
except BaseException:
    if descriptor is not None:
        os.close(descriptor)
    if created:
        try:
            if os.name == "nt":
                os.unlink(candidate_path)
            else:
                os.unlink(candidate_name, dir_fd=root_descriptor)
        except OSError:
            pass
    raise
finally:
    if root_descriptor is not None:
        os.close(root_descriptor)
    if parent_descriptor is not None:
        os.close(parent_descriptor)
PY

printf 'rollback_candidate=passed\n'
printf 'candidate_environment_created=true\n'
printf 'candidate_mode=600\n'
printf 'candidate_sms_enabled=false\n'
printf 'candidate_sms_test_mode=true\n'
printf 'candidate_fixed_proxy_preserved=true\n'
printf 'candidate_legacy_template_keys=0\n'
printf 'candidate_sensitive_values_printed=0\n'
printf 'current_environment_replaced=false\n'
printf 'service_restarts=0\n'
printf 'real_sms_sent=0\n'
