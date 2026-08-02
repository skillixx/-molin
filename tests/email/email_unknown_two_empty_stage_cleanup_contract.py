"""验证两个空 Stage 精确清理资产。"""

from __future__ import annotations
import hashlib,pathlib,re,subprocess

ROOT=pathlib.Path(__file__).resolve().parents[2]
PAYLOAD=ROOT/'scripts'/'email-unknown-two-empty-stage-cleanup.payload.sh'
CONTROLLER=ROOT/'scripts'/'run-email-unknown-two-empty-stage-cleanup.ps1'

class ContractFailure(RuntimeError):pass
def require(v:bool,n:str)->None:
    if not v:raise ContractFailure(n)

def validate(p:str,c:str)->None:
    require('${#stages[@]} -eq 2' in p and '${#remaining[@]} -eq 0' in p,'counts')
    require(p.count('${#first[@]} -eq 0')==1 and p.count('${#second[@]} -eq 0')==1,'double_empty')
    require('rmdir -- "${stages[0]}" "${stages[1]}"' in p,'exact_rmdir')
    require(not re.search(r'(?im)\b(?:rm|unlink|mysql|redis-cli|docker)\b',p),'forbidden')
    require('I_CONFIRM_EMAIL_UNKNOWN_TWO_EMPTY_STAGE_CLEANUP_ONCE' in c,'confirm')
    require('if(-not $Execute -or $Confirm -cne $script:ConfirmPhrase)' in c,'default_closed')
    require(c.count('Invoke-OneSSH -Payload')==1,'ssh_once')
    require('scp.exe' not in c and 'sftp.exe' not in c,'no_transfer')
    require("throw 'two_empty_stage_cleanup_failed'" in c,'closed_failure')

def main()->int:
    pr=PAYLOAD.read_bytes();cr=CONTROLLER.read_bytes();require(not pr.startswith(b'\xef\xbb\xbf') and cr.startswith(b'\xef\xbb\xbf'),'encoding')
    p=pr.decode('utf-8');c=cr.decode('utf-8-sig');require(hashlib.sha256(pr).hexdigest() in c,'hash');validate(p,c)
    r=subprocess.run([r'C:\Program Files\Git\bin\bash.exe','-n',str(PAYLOAD)],capture_output=True,text=True,timeout=10);require(r.returncode==0 and not r.stderr,'bash')
    attacks=((p.replace('${#stages[@]} -eq 2','${#stages[@]} -ge 1',1),c),(p.replace('rmdir -- "${stages[0]}" "${stages[1]}"','rm -rf -- "$parent"',1),c),(p,c.replace('-not $Execute -or ','' ,1)),(p,c+"\nInvoke-OneSSH -Payload @()\n"),(p,c.replace("throw 'two_empty_stage_cleanup_failed'",'throw $_',1)))
    rejected=0
    for ap,ac in attacks:
        try:validate(ap,ac)
        except ContractFailure:rejected+=1
        else:raise ContractFailure('mutation')
    print(f'status=pass mode=email_unknown_two_empty_stage_cleanup_contract attack_cases={rejected} external_access=false writes=false')
    return 0

if __name__=='__main__':
    try:raise SystemExit(main())
    except (ContractFailure,OSError,UnicodeError,subprocess.SubprocessError):print('status=failed mode=email_unknown_two_empty_stage_cleanup_contract classification=closed');raise SystemExit(1)
