#!/usr/bin/env bash
set -Eeuo pipefail
exec 2>/dev/null

# 验证器只读取固定候选并输出非敏感摘要；所有环境值都只在内存中比较，禁止打印。
candidate='__CANDIDATE_PATH__'

python3 - "${1:-}" "$candidate" <<'PY'
import hashlib
import os
import pathlib
import pwd
import re
import stat
import sys
import tempfile


def verify_candidate(path: pathlib.Path, expected_owner: str, expected_root: pathlib.Path | None) -> str:
    """只读验证候选文件的路径身份、权限和关闭态环境契约。"""
    root = path.parent
    if expected_root is not None and root != expected_root:
        raise ValueError("candidate_root")

    root_stat = root.lstat()
    if stat.S_ISLNK(root_stat.st_mode) or not stat.S_ISDIR(root_stat.st_mode):
        raise ValueError("candidate_root_type")
    if pwd.getpwuid(root_stat.st_uid).pw_name != expected_owner or stat.S_IMODE(root_stat.st_mode) != 0o700:
        raise ValueError("candidate_root_identity")

    candidate_stat = path.lstat()
    if stat.S_ISLNK(candidate_stat.st_mode) or not stat.S_ISREG(candidate_stat.st_mode):
        raise ValueError("candidate_type")
    if candidate_stat.st_nlink != 1:
        raise ValueError("candidate_hardlink")
    if pwd.getpwuid(candidate_stat.st_uid).pw_name != expected_owner or stat.S_IMODE(candidate_stat.st_mode) != 0o600:
        raise ValueError("candidate_identity")
    if candidate_stat.st_size <= 0 or candidate_stat.st_size > 1024 * 1024:
        raise ValueError("candidate_size")

    raw = path.read_bytes()
    if raw.startswith(b"\xef\xbb\xbf") or b"\x00" in raw or b"\r" in raw:
        raise ValueError("candidate_encoding")
    text = raw.decode("utf-8")
    values: dict[str, str] = {}
    duplicates = 0
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if "=" not in line:
            raise ValueError("candidate_line")
        key, value = line.split("=", 1)
        key = key.strip()
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*", key):
            raise ValueError("candidate_key")
        if key in values:
            duplicates += 1
        values[key] = value.strip()

    trusted = {item.strip() for item in values.get("TRUSTED_PROXY_IPS", "").split(",") if item.strip()}
    legacy = sum(1 for key in values if key.startswith("SMS_TEMPLATE_CODE_"))
    if duplicates != 0:
        raise ValueError("candidate_duplicate_keys")
    if values.get("APP_ENV") != "test":
        raise ValueError("candidate_environment")
    if values.get("SMS_ENABLED") != "false" or values.get("SMS_TEST_MODE") != "true":
        raise ValueError("candidate_sms_state")
    if trusted != {"172.20.250.0/28"}:
        raise ValueError("candidate_proxy")
    if legacy != 0:
        raise ValueError("candidate_legacy_templates")
    if values.get("SMS_PROVIDER") != "aliyun" or values.get("SMS_ALIYUN_ENDPOINT") != "dysmsapi.aliyuncs.com":
        raise ValueError("candidate_provider")
    for key in (
        "SMS_ALIYUN_ACCESS_KEY_ID",
        "SMS_ALIYUN_ACCESS_KEY_SECRET",
        "SMS_ALIYUN_SIGN_NAME",
        "SMS_PHONE_HMAC_SECRET",
        "SMS_TEST_PHONE_WHITELIST",
    ):
        if not values.get(key):
            raise ValueError("candidate_required_key")
    if len(values["SMS_PHONE_HMAC_SECRET"]) < 32:
        raise ValueError("candidate_hmac")
    return hashlib.sha256(raw).hexdigest()


def fixture_text() -> str:
    return "\n".join(
        (
            "APP_ENV=test",
            "TRUSTED_PROXY_IPS=172.20.250.0/28",
            "SMS_ENABLED=false",
            "SMS_TEST_MODE=true",
            "SMS_PROVIDER=aliyun",
            "SMS_ALIYUN_ENDPOINT=dysmsapi.aliyuncs.com",
            "SMS_ALIYUN_ACCESS_KEY_ID=SELF_TEST_ACCESS_KEY",
            "SMS_ALIYUN_ACCESS_KEY_SECRET=SELF_TEST_SECRET",
            "SMS_ALIYUN_SIGN_NAME=自测签名",
            "SMS_PHONE_HMAC_SECRET=SELF_TEST_HMAC_SECRET_32_CHARS_ONLY",
            "SMS_TEST_PHONE_WHITELIST=SELF_TEST_PHONE",
            "",
        )
    )


def run_self_test() -> None:
    owner = pwd.getpwuid(os.getuid()).pw_name
    with tempfile.TemporaryDirectory() as temporary:
        root = pathlib.Path(temporary) / "rollback"
        root.mkdir(mode=0o700)
        os.chmod(root, 0o700)
        path = root / "candidate.env"

        def write_candidate(text: str, mode: int = 0o600) -> None:
            path.unlink(missing_ok=True)
            path.write_text(text, encoding="utf-8", newline="\n")
            os.chmod(path, mode)

        valid = fixture_text()
        write_candidate(valid)
        verify_candidate(path, owner, root)
        print("valid_candidate=passed")

        path.unlink()
        try:
            verify_candidate(path, owner, root)
        except (FileNotFoundError, ValueError):
            print("missing_candidate=passed")
        else:
            raise AssertionError("missing_candidate")

        target = root / "target.env"
        target.write_text(valid, encoding="utf-8", newline="\n")
        os.chmod(target, 0o600)
        path.symlink_to(target)
        try:
            verify_candidate(path, owner, root)
        except ValueError:
            print("symlink_candidate=passed")
        else:
            raise AssertionError("symlink_candidate")
        path.unlink()

        cases = (
            ("wrong_mode_candidate", valid, 0o644),
            ("sms_enabled_candidate", valid.replace("SMS_ENABLED=false", "SMS_ENABLED=" + "true"), 0o600),
            ("proxy_drift_candidate", valid.replace("172.20.250.0/28", "0.0.0.0/0"), 0o600),
            ("legacy_key_candidate", valid + "SMS_TEMPLATE_CODE_LOGIN=legacy\n", 0o600),
            ("duplicate_key_candidate", valid + "SMS_ENABLED=false\n", 0o600),
        )
        for name, content, mode in cases:
            write_candidate(content, mode)
            try:
                verify_candidate(path, owner, root)
            except ValueError:
                print(f"{name}=passed")
            else:
                raise AssertionError(name)

    print("payload_self_test=passed")
    print("business_configuration_mutations=0")
    print("service_restarts=0")
    print("real_sms_sent=0")


if sys.argv[1] == "--self-test":
    run_self_test()
else:
    candidate_path = pathlib.Path(sys.argv[2])
    expected_root = pathlib.Path("/home/pc/molin/rollback/sms-phase5")
    digest = verify_candidate(candidate_path, "pc", expected_root)
    print("candidate_verification=passed")
    print(f"candidate_sha256={digest}")
    print("candidate_owner_mode=pc:600")
    print("candidate_root_owner_mode=pc:700")
    print("candidate_sms_enabled=false")
    print("candidate_sms_test_mode=true")
    print("candidate_fixed_proxy_preserved=true")
    print("candidate_release_keys_verified=true")
    print("candidate_legacy_template_keys=0")
    print("candidate_duplicate_keys=0")
    print("candidate_sensitive_values_printed=0")
    print("business_configuration_mutations=0")
    print("access_audit_logs_may_increase=true")
    print("real_sms_delivery_not_verified=true")
PY
