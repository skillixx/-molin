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


def verify_candidate(
    path: pathlib.Path,
    expected_owner: str,
    expected_root: pathlib.Path | None,
    after_open=None,
) -> str:
    """只读验证候选文件的路径身份、权限和关闭态环境契约。"""
    root = path.parent
    if expected_root is not None and root != expected_root:
        raise ValueError("candidate_root")

    if root.resolve(strict=True) != root:
        raise ValueError("candidate_root_path")
    directory_flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    root_descriptor = os.open(root, directory_flags)
    candidate_descriptor = None
    try:
        root_stat = os.fstat(root_descriptor)
        if not stat.S_ISDIR(root_stat.st_mode):
            raise ValueError("candidate_root_type")
        if pwd.getpwuid(root_stat.st_uid).pw_name != expected_owner or stat.S_IMODE(root_stat.st_mode) != 0o700:
            raise ValueError("candidate_root_identity")

        candidate_flags = os.O_RDONLY | os.O_NOFOLLOW
        candidate_descriptor = os.open(path.name, candidate_flags, dir_fd=root_descriptor)
        candidate_stat = os.fstat(candidate_descriptor)
        if not stat.S_ISREG(candidate_stat.st_mode):
            raise ValueError("candidate_type")
        if candidate_stat.st_nlink != 1:
            raise ValueError("candidate_hardlink")
        if pwd.getpwuid(candidate_stat.st_uid).pw_name != expected_owner or stat.S_IMODE(candidate_stat.st_mode) != 0o600:
            raise ValueError("candidate_identity")
        if candidate_stat.st_size <= 0 or candidate_stat.st_size > 1024 * 1024:
            raise ValueError("candidate_size")

        if after_open is not None:
            after_open()
        chunks = []
        while True:
            chunk = os.read(candidate_descriptor, 65536)
            if not chunk:
                break
            chunks.append(chunk)
        raw = b"".join(chunks)

        # 读取后再次核对目录项与已打开描述符指向同一 inode，拒绝验证窗口内的替换竞态。
        current_candidate = os.stat(path.name, dir_fd=root_descriptor, follow_symlinks=False)
        current_root = os.stat(root, follow_symlinks=False)
        if (current_candidate.st_dev, current_candidate.st_ino) != (candidate_stat.st_dev, candidate_stat.st_ino):
            raise ValueError("candidate_replaced")
        if (current_root.st_dev, current_root.st_ino) != (root_stat.st_dev, root_stat.st_ino):
            raise ValueError("candidate_root_replaced")
    finally:
        if candidate_descriptor is not None:
            os.close(candidate_descriptor)
        os.close(root_descriptor)

    if raw.startswith(b"\xef\xbb\xbf") or b"\x00" in raw or b"\r" in raw:
        raise ValueError("candidate_encoding")
    text = raw.decode("utf-8")
    values: dict[str, str] = {}
    duplicates = 0
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", stripped)
        if match is None:
            raise ValueError("candidate_line")
        key, value = match.groups()
        if key in values:
            duplicates += 1
        normalized = value.strip()
        if len(normalized) not in (0, 1) and normalized[0] == normalized[-1] and normalized[0] in "'\"":
            normalized = normalized[1:-1]
        values[key] = normalized

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

        quoted = valid.replace("APP_ENV=test", "export APP_ENV='test'")
        quoted = quoted.replace("SMS_PROVIDER=aliyun", 'SMS_PROVIDER="aliyun"')
        quoted = quoted.replace(
            "SMS_PHONE_HMAC_SECRET=SELF_TEST_HMAC_SECRET_32_CHARS_ONLY",
            'SMS_PHONE_HMAC_SECRET="SELF_TEST_HMAC_SECRET_32_CHARS_ONLY"',
        )
        write_candidate(quoted)
        verify_candidate(path, owner, root)
        print("quoted_export_candidate=passed")

        quoted_short_hmac = quoted.replace(
            '"SELF_TEST_HMAC_SECRET_32_CHARS_ONLY"',
            '"' + ("x" * 30) + '"',
        )
        write_candidate(quoted_short_hmac)
        try:
            verify_candidate(path, owner, root)
        except ValueError:
            print("quoted_short_hmac_candidate=passed")
        else:
            raise AssertionError("quoted_short_hmac_candidate")

        path.unlink()
        try:
            verify_candidate(path, owner, root)
        except (FileNotFoundError, OSError, ValueError):
            print("missing_candidate=passed")
        else:
            raise AssertionError("missing_candidate")

        target = root / "target.env"
        target.write_text(valid, encoding="utf-8", newline="\n")
        os.chmod(target, 0o600)
        path.symlink_to(target)
        try:
            verify_candidate(path, owner, root)
        except (OSError, ValueError):
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

        write_candidate(valid)
        opened_path = root / "opened.env"

        def replace_after_open() -> None:
            path.rename(opened_path)
            replacement = valid.replace("SMS_ENABLED=false", "SMS_ENABLED=" + "true")
            path.write_text(replacement, encoding="utf-8", newline="\n")
            os.chmod(path, 0o600)

        try:
            verify_candidate(path, owner, root, after_open=replace_after_open)
        except ValueError:
            print("concurrent_replacement_candidate=passed")
        else:
            raise AssertionError("concurrent_replacement_candidate")

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
