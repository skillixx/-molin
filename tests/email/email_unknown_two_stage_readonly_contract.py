"""验证两个保留 Stage 聚合只读诊断资产的安全边界。"""

from __future__ import annotations

import hashlib
import pathlib
import re
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[2]
PAYLOAD = ROOT / "scripts" / "email-unknown-two-stage-readonly.payload.sh"
CONTROLLER = ROOT / "scripts" / "run-email-unknown-two-stage-readonly.ps1"


class ContractFailure(RuntimeError):
    pass


def require(value: bool, name: str) -> None:
    if not value:
        raise ContractFailure(name)


def validate(payload: str, controller: str) -> None:
    require("${#stages[@]} -eq 2" in payload, "two_stages")
    require("empty_count + partial_count + complete_count" in payload, "count_invariant")
    require("expected_size=25573597" in payload and "1179e29d9f43" in payload, "binary_identity")
    require("sha256sum -- \"$binary\"" in payload, "complete_hash")
    require(not re.search(r"(?im)\b(?:rm|rmdir|unlink|chmod|chown|touch|mkdir|mysql|redis-cli|docker)\b", payload), "readonly")
    require("I_CONFIRM_EMAIL_UNKNOWN_TWO_STAGE_READONLY_ONCE" in controller, "confirmation")
    require("if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase)" in controller, "default_closed")
    require(controller.count("Invoke-OneSSH -Payload") == 1, "ssh_once")
    require("scp.exe" not in controller and "sftp.exe" not in controller, "no_transfer")
    require("empty_count=$empty partial_count=$partial complete_count=$complete" in controller, "safe_counts")
    require("throw 'two_stage_readonly_failed'" in controller, "closed_failure")
    require("database_access=false redis_access=false cleanup=false restart=false" in controller, "safe_summary")


def main() -> int:
    payload_raw = PAYLOAD.read_bytes(); controller_raw = CONTROLLER.read_bytes()
    require(not payload_raw.startswith(b"\xef\xbb\xbf") and controller_raw.startswith(b"\xef\xbb\xbf"), "encoding")
    payload = payload_raw.decode("utf-8"); controller = controller_raw.decode("utf-8-sig")
    expected = hashlib.sha256(payload_raw).hexdigest()
    require(expected in controller, "payload_hash")
    validate(payload,controller)
    result = subprocess.run([r"C:\Program Files\Git\bin\bash.exe","-n",str(PAYLOAD)],capture_output=True,text=True,timeout=10)
    require(result.returncode == 0 and not result.stderr, "bash_syntax")
    attacks=(
        (payload.replace("${#stages[@]} -eq 2","${#stages[@]} -ge 1",1),controller),
        (payload+"\nrm -rf -- /tmp/x\n",controller),
        (payload.replace("sha256sum -- \"$binary\"","true",1),controller),
        (payload,controller.replace("-not $Execute -or ","",1)),
        (payload,controller.replace("Invoke-OneSSH -Payload","Invoke-OneSSH -Payload",1)+"\nInvoke-OneSSH -Payload @()\n"),
        (payload,controller.replace("throw 'two_stage_readonly_failed'","throw $_",1)),
        (payload,controller.replace("cleanup=false","cleanup=true")),
    )
    rejected=0
    for attacked_payload,attacked_controller in attacks:
        try: validate(attacked_payload,attacked_controller)
        except ContractFailure: rejected+=1
        else: raise ContractFailure("mutation_accepted")
    print(f"status=pass mode=email_unknown_two_stage_readonly_contract attack_cases={rejected} external_access=false writes=false")
    return 0


if __name__ == "__main__":
    try: raise SystemExit(main())
    except (ContractFailure,OSError,UnicodeError,subprocess.SubprocessError):
        print("status=failed mode=email_unknown_two_stage_readonly_contract classification=closed")
        raise SystemExit(1)
