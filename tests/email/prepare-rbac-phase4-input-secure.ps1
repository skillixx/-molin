param(
    # 自测只使用进程内假传输，不读取真实输入、不启动外部进程。
    [switch]$SelfTest,
    # 自动生成模式只在内存中生成密码，邮箱和手机号仍由操作人员输入。
    [switch]$GeneratePasswords,
    # 测试身份生成必须与自动密码组合使用，启用后只提示管理员会话材料。
    [switch]$GenerateTestIdentities,
    # 替代账号专项允许只提供已完成双 MFA 的短期 Access Token；退出时仅吊销该 Access Token。
    [switch]$AccessOnly
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$script:TargetHost = "pc@8.130.9.163"
$script:TargetPort = "10003"
$script:TargetFile = "/home/pc/molin-runtime/email-rbac-phase4-input.json"
$script:SuccessOutput = "uploaded=true schema=true owner=true mode600=true regular=true size=true"

# 远端命令固定目标目录和文件；失败清理只允许删除本次刚创建的精确文件。
$script:RemoteCommand = @'
exec 2>/dev/null
set -eu
umask 077
target_dir=/home/pc/molin-runtime
target_file=/home/pc/molin-runtime/email-rbac-phase4-input.json
created=0
cleanup() {
  rc=$?
  trap - EXIT HUP INT TERM
  if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then
    /usr/bin/rm -f -- "$target_file"
  fi
  exit "$rc"
}
trap cleanup EXIT HUP INT TERM
if [ -L "$target_dir" ]; then exit 74; fi
if [ ! -e "$target_dir" ]; then /usr/bin/mkdir -m 700 -- "$target_dir"; fi
if [ ! -d "$target_dir" ] || [ -L "$target_dir" ]; then exit 74; fi
if [ "$(/usr/bin/stat -c %u "$target_dir")" != "$(/usr/bin/id -u)" ]; then exit 74; fi
if [ "$(/usr/bin/stat -c %a "$target_dir")" != "700" ]; then exit 74; fi
if [ -e "$target_file" ] || [ -L "$target_file" ]; then exit 73; fi
set -C
exec 3> "$target_file"
created=1
set +C
/usr/bin/cat >&3
exec 3>&-
size=$(/usr/bin/stat -c %s "$target_file")
if [ "$size" -lt 512 ] || [ "$size" -gt 65536 ]; then exit 75; fi
/usr/bin/python3 - "$target_file" <<'PY'
import json,re,sys
with open(sys.argv[1],"r",encoding="utf-8") as stream:
    data=json.load(stream)
if set(data)!={"schema","admin_session","accounts"}: raise SystemExit(1)
if data["schema"]!="molin.email_rbac_phase4_input.v1": raise SystemExit(1)
if set(data["admin_session"])!={"access_token","refresh_token"}: raise SystemExit(1)
if set(data["accounts"])!={"view","view_manage","view_sync","view_test"}: raise SystemExit(1)
if any("otp" in str(key).lower() or "code" == str(key).lower() for key in data for _ in (0,)): raise SystemExit(1)
jwt=re.compile(r"^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$")
if not jwt.fullmatch(data["admin_session"]["access_token"]): raise SystemExit(1)
if not isinstance(data["admin_session"]["refresh_token"],str): raise SystemExit(1)
emails=[]; phones=[]; passwords=[]
for scene in ("view","view_manage","view_sync","view_test"):
    account=data["accounts"][scene]
    if set(account)!={"email","phone","password"}: raise SystemExit(1)
    if not re.fullmatch(r"[^\s@]+@[^\s@]+\.[^\s@]+",account["email"]): raise SystemExit(1)
    if not re.fullmatch(r"\+?[0-9]{7,20}",account["phone"]): raise SystemExit(1)
    if not 12<=len(account["password"])<=72: raise SystemExit(1)
    emails.append(account["email"].lower()); phones.append(account["phone"]); passwords.append(account["password"])
if len(set(emails))!=4 or len(set(phones))!=4 or len(set(passwords))!=4: raise SystemExit(1)
PY
if [ ! -f "$target_file" ] || [ -L "$target_file" ]; then exit 76; fi
if [ "$(/usr/bin/stat -c %u "$target_file")" != "$(/usr/bin/id -u)" ]; then exit 76; fi
if [ "$(/usr/bin/stat -c %a "$target_file")" != "600" ]; then exit 76; fi
created=0
printf '%s\n' 'uploaded=true schema=true owner=true mode600=true regular=true size=true'
'@

function Read-SecretText {
    param([Parameter(Mandatory = $true)][string]$Prompt)

    $secureValue = Microsoft.PowerShell.Utility\Read-Host -Prompt $Prompt -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureValue)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        $secureValue.Dispose()
    }
}

function Assert-SafeSingleLine {
    param([Parameter(Mandatory = $true)][string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value) -or $Value.Contains("`r") -or
        $Value.Contains("`n") -or $Value.Contains([char]0)) {
        throw "输入不符合单行安全约束。"
    }
}

function Assert-AccountInput {
    param(
        [Parameter(Mandatory = $true)][string]$Email,
        [Parameter(Mandatory = $true)][string]$Phone,
        [Parameter(Mandatory = $true)][string]$Password
    )

    Assert-SafeSingleLine $Email
    Assert-SafeSingleLine $Phone
    Assert-SafeSingleLine $Password
    if ($Email -notmatch '^[^\s@]+@[^\s@]+\.[^\s@]+$') { throw "邮箱格式不正确。" }
    if ($Phone -notmatch '^\+?[0-9]{7,20}$') { throw "手机号格式不正确。" }
    Assert-PasswordPolicy $Password
}

function Get-CryptoRandomIndex {
    param([Parameter(Mandatory = $true)][ValidateRange(2, 256)][int]$UpperBound)

    # 使用拒绝采样消除取模偏差，每次只保留一个随机字节并在返回前清零。
    $randomBytes = [byte[]]::new(1)
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $limit = 256 - (256 % $UpperBound)
        do { $generator.GetBytes($randomBytes) } while ([int]$randomBytes[0] -ge $limit)
        return ([int]$randomBytes[0] % $UpperBound)
    }
    finally {
        [Array]::Clear($randomBytes, 0, $randomBytes.Length)
        $generator.Dispose()
    }
}

function New-CryptoPassword {
    # 字符集限定为可打印 ASCII，明确排除空白和双引号，避免传输及人工复制歧义。
    $upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
    $lower = "abcdefghijklmnopqrstuvwxyz"
    $digits = "0123456789"
    $special = '!#$%&()*+,-./:;<=>?@[]^_{|}~'
    $all = $upper + $lower + $digits + $special
    $characters = [Collections.Generic.List[char]]::new()
    try {
        foreach ($group in @($upper, $lower, $digits, $special)) {
            $characters.Add($group[(Get-CryptoRandomIndex $group.Length)])
        }
        while ($characters.Count -lt 20) {
            $characters.Add($all[(Get-CryptoRandomIndex $all.Length)])
        }
        # Fisher-Yates 洗牌避免四类必选字符始终出现在固定位置。
        for ($index = $characters.Count - 1; $index -gt 0; $index--) {
            $swapIndex = Get-CryptoRandomIndex ($index + 1)
            $temporary = $characters[$index]
            $characters[$index] = $characters[$swapIndex]
            $characters[$swapIndex] = $temporary
        }
        return (-join $characters.ToArray())
    }
    finally {
        for ($index = 0; $index -lt $characters.Count; $index++) { $characters[$index] = [char]0 }
        $characters.Clear()
        $upper = $null; $lower = $null; $digits = $null; $special = $null; $all = $null
    }
}

function New-GeneratedPasswords {
    $passwords = @{}
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    try {
        foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
            do { $candidate = New-CryptoPassword } while (-not $seen.Add($candidate))
            $passwords[$scene] = $candidate
            $candidate = $null
        }
        return $passwords
    }
    catch {
        foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
            if ($passwords.ContainsKey($scene)) { $passwords[$scene] = $null }
        }
        throw
    }
    finally {
        $seen.Clear()
        $candidate = $null
    }
}

function New-TestIdentities {
    $identities = @{}
    $emails = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $phones = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    try {
        foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
            do {
                $emailSuffix = -join (1..16 | ForEach-Object { "0123456789abcdef"[(Get-CryptoRandomIndex 16)] })
                $email = "qa-rbac-" + $scene + "-" + $emailSuffix + "@example.invalid"
            } while (-not $emails.Add($email))
            do {
                # +999 不是可投递测试目标，仅满足服务端可选加号与数字格式，避免误触真实短信号码。
                $phoneSuffix = -join (1..12 | ForEach-Object { "0123456789"[(Get-CryptoRandomIndex 10)] })
                $phone = "+999" + $phoneSuffix
            } while (-not $phones.Add($phone))
            $identities[$scene] = @{ email = $email; phone = $phone }
            $email = $null; $phone = $null; $emailSuffix = $null; $phoneSuffix = $null
        }
        return $identities
    }
    catch {
        foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
            if ($identities.ContainsKey($scene)) {
                $identities[$scene].email = $null; $identities[$scene].phone = $null
            }
        }
        throw
    }
    finally {
        $emails.Clear(); $phones.Clear()
        $email = $null; $phone = $null; $emailSuffix = $null; $phoneSuffix = $null
    }
}

function Assert-PasswordPolicy {
    param([Parameter(Mandatory = $true)][string]$Password)

    if ((Get-PasswordPolicyClassification $Password) -ne "pass") {
        throw "初始密码强度不足。"
    }
}

function Get-PasswordPolicyClassification {
    param([AllowEmptyString()][string]$Password)

    # 分类只返回固定规则名，禁止携带密码值或实际长度。
    if ($null -eq $Password -or $Password.Length -lt 12 -or $Password.Length -gt 72) { return "password_length" }
    if ($Password -match '[^\x21-\x7E]') { return "password_unsafe_character" }
    # PowerShell 默认正则忽略大小写，必须使用区分大小写的运算符分别校验大小写字母。
    if ($Password -cnotmatch '[A-Z]') { return "password_missing_upper" }
    if ($Password -cnotmatch '[a-z]') { return "password_missing_lower" }
    if ($Password -notmatch '[0-9]') { return "password_missing_digit" }
    if ($Password -notmatch '[^A-Za-z0-9]') { return "password_missing_special" }
    return "pass"
}

function Get-InputValidationClassification {
    param(
        [AllowEmptyString()][string]$AccessToken,
        [AllowEmptyString()][string]$RefreshToken,
        [Parameter(Mandatory = $true)][hashtable]$Accounts
    )

    try {
        Assert-SafeSingleLine $AccessToken
        if ($AccessToken -notmatch '^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$') {
            return "access_token_format"
        }
    }
    catch { return "access_token_format" }
    try {
        # Access-only 模式使用空字符串；非空 Refresh Token 仍必须满足原单行安全约束。
        if (-not [string]::IsNullOrEmpty($RefreshToken)) { Assert-SafeSingleLine $RefreshToken }
    }
    catch { return "refresh_token_format" }

    $emails = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $phones = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    $passwords = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
        if (-not $Accounts.ContainsKey($scene) -or $null -eq $Accounts[$scene]) { return "email_format:" + $scene }
        $account = $Accounts[$scene]
        $email = [string]$account.email
        $phone = [string]$account.phone
        # 邮箱先限制为无空白、无控制字符的可打印 ASCII，再沿用既有结构正则。
        if ($email -match '[^\x21-\x7E]') { return "email_unsafe_character:" + $scene }
        if ($email -notmatch '^[^\s@]+@[^\s@]+\.[^\s@]+$') { return "email_format:" + $scene }
        # 手机号中的空白、控制字符和非 ASCII 字符归为安全字符错误，其余交由格式规则判断。
        if ($phone -match '[^\x21-\x7E]') { return "phone_unsafe_character:" + $scene }
        if ($phone -notmatch '^\+?[0-9]{7,20}$') { return "phone_format:" + $scene }
        $passwordClassification = Get-PasswordPolicyClassification $account.password
        if ($passwordClassification -ne "pass") { return $passwordClassification + ":" + $scene }
        if (-not $emails.Add($account.email) -or -not $phones.Add($account.phone) -or
            -not $passwords.Add($account.password)) {
            return "uniqueness"
        }
    }
    return "pass"
}

function ConvertTo-WindowsArgument {
    param([Parameter(Mandatory = $true)][string]$Value)

    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') { return $Value }
    $builder = [Text.StringBuilder]::new()
    [void]$builder.Append('"')
    $slashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '\') {
            $slashes++
            continue
        }
        if ($character -eq '"') {
            [void]$builder.Append(('\' * ($slashes * 2 + 1)))
            [void]$builder.Append('"')
            $slashes = 0
            continue
        }
        if ($slashes -gt 0) { [void]$builder.Append(('\' * $slashes)); $slashes = 0 }
        [void]$builder.Append($character)
    }
    if ($slashes -gt 0) { [void]$builder.Append(('\' * ($slashes * 2))) }
    [void]$builder.Append('"')
    return $builder.ToString()
}

function ConvertTo-PosixSingleQuoted {
    param([Parameter(Mandatory = $true)][string]$Value)

    return "'" + $Value.Replace("'", "'\''") + "'"
}

function Find-FixedSshExecutable {
    # 只接受 Windows 系统目录中的 OpenSSH 客户端，避免 PATH、函数或别名劫持。
    $systemDirectory = [Environment]::SystemDirectory
    $candidate = [IO.Path]::GetFullPath([IO.Path]::Combine($systemDirectory, "OpenSSH", "ssh.exe"))
    $allowedRoot = [IO.Path]::GetFullPath([IO.Path]::Combine($systemDirectory, "OpenSSH")) + [IO.Path]::DirectorySeparatorChar
    if (-not $candidate.StartsWith($allowedRoot, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Path]::GetFileName($candidate) -cne "ssh.exe" -or
        -not [IO.File]::Exists($candidate)) {
        throw "固定 OpenSSH 客户端不可用。"
    }
    return $candidate
}

function New-TransportRequest {
    param([Parameter(Mandatory = $true)][byte[]]$PayloadBytes)

    $sshPath = Find-FixedSshExecutable
    $remoteShellCommand = "bash -c " + (ConvertTo-PosixSingleQuoted $script:RemoteCommand)
    $arguments = @(
        "-p", $script:TargetPort,
        "-o", "StrictHostKeyChecking=yes",
        "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=10",
        "-o", "ServerAliveInterval=5",
        "-o", "ServerAliveCountMax=2",
        $script:TargetHost,
        $remoteShellCommand
    )
    $encodedArguments = [Collections.Generic.List[string]]::new()
    foreach ($argument in $arguments) {
        $encodedArguments.Add((ConvertTo-WindowsArgument ([string]$argument)))
    }
    return [pscustomobject]@{
        SshPath = $sshPath
        Arguments = $arguments
        CommandLine = [string]::Join(" ", $encodedArguments.ToArray())
        PayloadBytes = $PayloadBytes
    }
}

$script:RealTransport = {
    param($Request)

    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $Request.SshPath
    $startInfo.Arguments = $Request.CommandLine
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    [void]$process.Start()
    try {
        $process.StandardInput.BaseStream.Write($Request.PayloadBytes, 0, $Request.PayloadBytes.Length)
        $process.StandardInput.BaseStream.Flush()
        $process.StandardInput.Close()
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(30000)) {
            $process.Kill()
            $process.WaitForExit()
            return [pscustomobject]@{ ExitCode = 124; Stdout = ""; Stderr = "" }
        }
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            Stdout = $stdoutTask.Result
            Stderr = $stderrTask.Result
        }
    }
    finally {
        $process.Dispose()
    }
}

function Invoke-SafeUpload {
    param(
        [Parameter(Mandatory = $true)][byte[]]$PayloadBytes,
        [Parameter(Mandatory = $true)][ScriptBlock]$Transport
    )

    try { $request = New-TransportRequest $PayloadBytes }
    catch { return "ssh_transport" }
    try {
        $result = & $Transport $request
        if ($null -eq $result -or $null -eq $result.ExitCode -or
            $null -eq $result.Stdout -or $null -eq $result.Stderr) {
            return "ssh_transport"
        }
        if ($result.ExitCode -eq 73 -and $result.Stdout.Length -eq 0 -and $result.Stderr.Length -eq 0) { return "remote_exists" }
        if ($result.ExitCode -eq 124 -or $result.ExitCode -eq 255) { return "ssh_transport" }
        if ($result.ExitCode -ne 0) { return "remote_validation" }
        if ($result.Stderr.Length -ne 0 -or $result.Stdout.TrimEnd("`r", "`n") -cne $script:SuccessOutput) {
            return "remote_validation"
        }
        return "pass"
    }
    catch {
        return "ssh_transport"
    }
}

function New-InputPayload {
    param(
        [Parameter(Mandatory = $true)][string]$AccessToken,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RefreshToken,
        [Parameter(Mandatory = $true)][hashtable]$Accounts
    )

    if ((Get-InputValidationClassification $AccessToken $RefreshToken $Accounts) -ne "pass") {
        throw "输入校验未通过。"
    }
    $document = [ordered]@{
        schema = "molin.email_rbac_phase4_input.v1"
        admin_session = [ordered]@{ access_token = $AccessToken; refresh_token = $RefreshToken }
        accounts = [ordered]@{
            view = [ordered]@{ email = $Accounts.view.email; phone = $Accounts.view.phone; password = $Accounts.view.password }
            view_manage = [ordered]@{ email = $Accounts.view_manage.email; phone = $Accounts.view_manage.phone; password = $Accounts.view_manage.password }
            view_sync = [ordered]@{ email = $Accounts.view_sync.email; phone = $Accounts.view_sync.phone; password = $Accounts.view_sync.password }
            view_test = [ordered]@{ email = $Accounts.view_test.email; phone = $Accounts.view_test.phone; password = $Accounts.view_test.password }
        }
    }
    $json = Microsoft.PowerShell.Utility\ConvertTo-Json -InputObject $document -Depth 5 -Compress
    if ($json -match '(?i)"(?:otp|code)"\s*:') { throw "JSON schema 禁止验证码字段。" }
    $bytes = [Text.UTF8Encoding]::new($false).GetBytes($json)
    $json = $null
    $document = $null
    return ,$bytes
}

function Invoke-SelfTest {
    $dummyAccess = [string]::Join(".", @("selftestheader", "selftestpayload", "selftestsignature"))
    $dummyRefresh = [string]::Join("-", @("self", "test", "refresh"))
    $passwordPart = "!Password"
    $accounts = @{
        view = @{ email = "view@example.invalid"; phone = "+8613800000001"; password = "View" + $passwordPart + "01" }
        view_manage = @{ email = "manage@example.invalid"; phone = "+8613800000002"; password = "Manage" + $passwordPart + "02" }
        view_sync = @{ email = "sync@example.invalid"; phone = "+8613800000003"; password = "Sync" + $passwordPart + "03" }
        view_test = @{ email = "test@example.invalid"; phone = "+8613800000004"; password = "Test" + $passwordPart + "04" }
    }
    $payload = $null
    $accessOnlyPayload = $null
    $generatedPasswords = $null
    $generatedOutput = $null
    $generatedUnique = $null
    $generatorDefinition = $null
    $generatedIdentities = $null
    $generatedIdentityOutput = $null
    $identityEmails = $null
    $identityPhones = $null
    $combinedAccounts = $null
    try {
        $payload = New-InputPayload $dummyAccess $dummyRefresh $accounts
        $accessOnlyPayload = New-InputPayload $dummyAccess "" $accounts
        $successTransport = {
            param($Request)
            $text = [Text.Encoding]::UTF8.GetString($Request.PayloadBytes)
            $parsed = Microsoft.PowerShell.Utility\ConvertFrom-Json -InputObject $text
            $argvOk = $Request.Arguments.Count -eq 14 -and
                $Request.Arguments[0] -eq "-p" -and $Request.Arguments[1] -eq "10003" -and
                $Request.Arguments[12] -eq "pc@8.130.9.163" -and
                $Request.Arguments[13].StartsWith("bash -c ") -and
                -not [string]::IsNullOrWhiteSpace($Request.CommandLine)
            $schemaOk = $parsed.schema -eq "molin.email_rbac_phase4_input.v1" -and
                $parsed.accounts.PSObject.Properties.Name.Count -eq 4 -and
                $text -notmatch '(?i)"(?:otp|code)"\s*:'
            $secretInArgv = ($Request.Arguments -join " ").Contains($dummyAccess) -or
                ($Request.Arguments -join " ").Contains($dummyRefresh) -or
                $Request.CommandLine.Contains($dummyAccess) -or $Request.CommandLine.Contains($dummyRefresh)
            if (-not $argvOk -or -not $schemaOk -or $secretInArgv) {
                return [pscustomobject]@{ ExitCode = 1; Stdout = ""; Stderr = "" }
            }
            return [pscustomobject]@{ ExitCode = 0; Stdout = $script:SuccessOutput + "`n"; Stderr = "" }
        }
        $repeatTransport = { param($Request) [pscustomobject]@{ ExitCode = 73; Stdout = ""; Stderr = "" } }
        $failureTransport = { param($Request) [pscustomobject]@{ ExitCode = 75; Stdout = ""; Stderr = "" } }
        $leakTransport = { param($Request) [pscustomobject]@{ ExitCode = 0; Stdout = $dummyAccess; Stderr = "" } }
        $validationOk = (Get-InputValidationClassification $dummyAccess $dummyRefresh $accounts) -eq "pass"
        $accessOnlyOk = (Get-InputValidationClassification $dummyAccess "" $accounts) -eq "pass"
        $badAccess = Get-InputValidationClassification ("Bearer " + $dummyAccess) $dummyRefresh $accounts
        $accountCases = [ordered]@{
            "email_format:view" = @{ field = "email"; value = "invalid" }
            "email_unsafe_character:view" = @{ field = "email"; value = "unsafe email@example.invalid" }
            "phone_format:view" = @{ field = "phone"; value = "123ABC" }
            "phone_unsafe_character:view" = @{ field = "phone"; value = "+8613800 00001" }
        }
        $accountCasesOk = $true
        foreach ($expectedClassification in $accountCases.Keys) {
            $badAccountSet = @{}
            foreach ($key in $accounts.Keys) { $badAccountSet[$key] = @{} + $accounts[$key] }
            $badAccountSet.view[$accountCases[$expectedClassification].field] = $accountCases[$expectedClassification].value
            if ((Get-InputValidationClassification $dummyAccess $dummyRefresh $badAccountSet) -cne $expectedClassification) {
                $accountCasesOk = $false
            }
        }
        $passwordCases = [ordered]@{
            "password_length:view" = "Short1!"
            "password_missing_upper:view" = "lowercaseonly1!"
            "password_missing_lower:view" = "UPPERCASEONLY1!"
            "password_missing_digit:view" = "MissingNumber!"
            "password_missing_special:view" = "MissingSpecial1"
            "password_unsafe_character:view" = "Unsafe Space1!"
        }
        $passwordCasesOk = $true
        foreach ($expectedClassification in $passwordCases.Keys) {
            $badPasswordSet = @{}
            foreach ($key in $accounts.Keys) { $badPasswordSet[$key] = @{} + $accounts[$key] }
            $badPasswordSet.view.password = $passwordCases[$expectedClassification]
            if ((Get-InputValidationClassification $dummyAccess $dummyRefresh $badPasswordSet) -cne $expectedClassification) {
                $passwordCasesOk = $false
            }
        }
        $duplicateSet = @{}
        foreach ($key in $accounts.Keys) { $duplicateSet[$key] = @{} + $accounts[$key] }
        $duplicateSet.view_test.email = $duplicateSet.view.email
        $duplicate = Get-InputValidationClassification $dummyAccess $dummyRefresh $duplicateSet
        $generatedOutput = @(New-GeneratedPasswords)
        $generatedOutputShapeOk = $generatedOutput.Count -eq 1 -and $generatedOutput[0] -is [Collections.IDictionary]
        if ($generatedOutputShapeOk) { $generatedPasswords = $generatedOutput[0] }
        $generatedCompliant = $generatedOutputShapeOk
        $generatedUnique = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
        foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
            if (-not $generatedOutputShapeOk -or
                (Get-PasswordPolicyClassification $generatedPasswords[$scene]) -ne "pass" -or
                $generatedPasswords[$scene].Contains('"') -or $generatedPasswords[$scene] -match '\s') {
                $generatedCompliant = $false
            }
            if ($generatedOutputShapeOk) { [void]$generatedUnique.Add($generatedPasswords[$scene]) }
        }
        $generatedUniqueOk = $generatedUnique.Count -eq 4
        $generatedIdentityOutput = @(New-TestIdentities)
        $generatedIdentityShapeOk = $generatedIdentityOutput.Count -eq 1 -and
            $generatedIdentityOutput[0] -is [Collections.IDictionary]
        if ($generatedIdentityShapeOk) { $generatedIdentities = $generatedIdentityOutput[0] }
        $identityEmails = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
        $identityPhones = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
        $generatedIdentityCompliant = $generatedIdentityShapeOk
        $combinedAccounts = @{}
        foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
            if (-not $generatedIdentityShapeOk -or
                $generatedIdentities[$scene].email -notmatch '^[^\s@]+@[^\s@]+\.[^\s@]+$' -or
                $generatedIdentities[$scene].email -match '[^\x21-\x7E]' -or
                $generatedIdentities[$scene].phone -notmatch '^\+?[0-9]{7,20}$') {
                $generatedIdentityCompliant = $false
            }
            if ($generatedIdentityShapeOk) {
                [void]$identityEmails.Add($generatedIdentities[$scene].email)
                [void]$identityPhones.Add($generatedIdentities[$scene].phone)
                $combinedAccounts[$scene] = @{
                    email = $generatedIdentities[$scene].email
                    phone = $generatedIdentities[$scene].phone
                    password = $generatedPasswords[$scene]
                }
            }
        }
        $generatedIdentityUniqueOk = $identityEmails.Count -eq 4 -and $identityPhones.Count -eq 4
        $combinedInputOk = $generatedIdentityShapeOk -and
            (Get-InputValidationClassification $dummyAccess $dummyRefresh $combinedAccounts) -eq "pass"
        # 生成器定义中不允许出现本地文件写入入口，自测自身也不创建临时文件。
        $generatorDefinition = ${function:New-GeneratedPasswords}.ToString() +
            ${function:New-CryptoPassword}.ToString() + ${function:New-TestIdentities}.ToString()
        $noLocalWrite = $generatorDefinition -notmatch '(?i)Set-Content|Add-Content|Out-File|New-Item|WriteAll|FileStream'
        $ok = $validationOk -and $accessOnlyOk -and $badAccess -eq "access_token_format" -and
            $accountCasesOk -and $passwordCasesOk -and
            $duplicate -eq "uniqueness" -and $generatedCompliant -and $generatedUniqueOk -and
            $generatedOutputShapeOk -and $generatedIdentityCompliant -and $generatedIdentityUniqueOk -and
            $generatedIdentityShapeOk -and $combinedInputOk -and $noLocalWrite -and
            (Invoke-SafeUpload $payload $successTransport) -eq "pass" -and
            (Invoke-SafeUpload $accessOnlyPayload $successTransport) -eq "pass" -and
            (Invoke-SafeUpload $payload $repeatTransport) -eq "remote_exists" -and
            (Invoke-SafeUpload $payload $failureTransport) -eq "remote_validation" -and
            (Invoke-SafeUpload $payload $leakTransport) -eq "remote_validation"
        Microsoft.PowerShell.Utility\Write-Output ("[" + $(if ($ok) { "PASS" } else { "FAIL" }) + "] mode=selftest cases=27 external_access=false remote_writes=false local_writes=false sensitive_output=false")
        $script:SelfTestExitCode = $(if ($ok) { 0 } else { 1 })
    }
    catch {
        Microsoft.PowerShell.Utility\Write-Output "[FAIL] mode=selftest cases=27 external_access=false remote_writes=false local_writes=false sensitive_output=false"
        $script:SelfTestExitCode = 1
    }
    finally {
        if ($null -ne $payload) { [Array]::Clear($payload, 0, $payload.Length) }
        if ($null -ne $accessOnlyPayload) { [Array]::Clear($accessOnlyPayload, 0, $accessOnlyPayload.Length) }
        if ($null -ne $generatedPasswords) {
            foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
                if ($generatedPasswords.ContainsKey($scene)) { $generatedPasswords[$scene] = $null }
            }
        }
        if ($null -ne $generatedIdentities) {
            foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
                if ($generatedIdentities.ContainsKey($scene)) {
                    $generatedIdentities[$scene].email = $null; $generatedIdentities[$scene].phone = $null
                }
            }
        }
        if ($null -ne $generatedUnique) { $generatedUnique.Clear() }
        if ($null -ne $identityEmails) { $identityEmails.Clear() }
        if ($null -ne $identityPhones) { $identityPhones.Clear() }
        if ($null -ne $combinedAccounts) {
            foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
                if ($combinedAccounts.ContainsKey($scene)) {
                    $combinedAccounts[$scene].email = $null; $combinedAccounts[$scene].phone = $null
                    $combinedAccounts[$scene].password = $null
                }
            }
        }
        $dummyAccess = $null; $dummyRefresh = $null; $passwordPart = $null; $accounts = $null
        $generatedPasswords = $null; $generatedOutput = $null; $generatorDefinition = $null
        $generatedIdentities = $null; $generatedIdentityOutput = $null; $combinedAccounts = $null
    }
}

if ($SelfTest) {
    $script:SelfTestExitCode = 1
    Invoke-SelfTest
    exit $script:SelfTestExitCode
}

if ($GenerateTestIdentities -and -not $GeneratePasswords) {
    Microsoft.PowerShell.Utility\Write-Output "[FAIL] prepared=false classification=mode_combination values_exposed=false"
    exit 1
}

$accessToken = $null; $refreshToken = $null; $accounts = @{}; $payloadBytes = $null
$generatedPasswords = $null
$generatedIdentities = $null
$classification = "input_read"
try {
    $accessToken = Read-SecretText "操作管理员短期 Access Token"
    if ($AccessOnly) { $refreshToken = "" }
    else { $refreshToken = Read-SecretText "操作管理员短期 Refresh Token" }
    if ($GeneratePasswords) { $generatedPasswords = New-GeneratedPasswords }
    if ($GenerateTestIdentities) { $generatedIdentities = New-TestIdentities }
    foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
        if ($GenerateTestIdentities) {
            $email = $generatedIdentities[$scene].email
            $phone = $generatedIdentities[$scene].phone
        }
        else {
            $email = Read-SecretText "$scene 账号邮箱"
            $phone = Read-SecretText "$scene 账号手机号"
        }
        if ($GeneratePasswords) { $password = $generatedPasswords[$scene] }
        else { $password = Read-SecretText "$scene 初始密码" }
        $accounts[$scene] = @{ email = $email; phone = $phone; password = $password }
        $email = $null; $phone = $null; $password = $null
    }
    $classification = Get-InputValidationClassification $accessToken $refreshToken $accounts
    if ($classification -ne "pass") {
        Microsoft.PowerShell.Utility\Write-Output "[FAIL] prepared=false classification=$classification values_exposed=false"
        exit 1
    }
    $classification = "payload_schema"
    $payloadBytes = New-InputPayload $accessToken $refreshToken $accounts
    $classification = Invoke-SafeUpload $payloadBytes $script:RealTransport
    if ($classification -eq "pass") {
        Microsoft.PowerShell.Utility\Write-Output "[PASS] prepared=true schema=true target_fixed=true owner=true mode600=true values_exposed=false"
        exit 0
    }
    Microsoft.PowerShell.Utility\Write-Output "[FAIL] prepared=false classification=$classification values_exposed=false"
    exit 1
}
catch {
    $allowedClassifications = @("input_read", "payload_schema", "ssh_transport", "remote_exists", "remote_validation")
    if ($allowedClassifications -notcontains $classification) { $classification = "runner_internal" }
    Microsoft.PowerShell.Utility\Write-Output "[FAIL] prepared=false classification=$classification values_exposed=false"
    exit 1
}
finally {
    if ($null -ne $payloadBytes) { [Array]::Clear($payloadBytes, 0, $payloadBytes.Length) }
    foreach ($scene in @("view", "view_manage", "view_sync", "view_test")) {
        if ($accounts.ContainsKey($scene)) {
            $accounts[$scene].email = $null; $accounts[$scene].phone = $null; $accounts[$scene].password = $null
        }
        if ($null -ne $generatedPasswords -and $generatedPasswords.ContainsKey($scene)) {
            $generatedPasswords[$scene] = $null
        }
        if ($null -ne $generatedIdentities -and $generatedIdentities.ContainsKey($scene)) {
            $generatedIdentities[$scene].email = $null; $generatedIdentities[$scene].phone = $null
        }
    }
    $accessToken = $null; $refreshToken = $null; $accounts = $null
    $generatedPasswords = $null; $generatedIdentities = $null; $classification = $null
}
