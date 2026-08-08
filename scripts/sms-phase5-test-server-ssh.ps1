function Assert-SmsPhase5FixedTestServerTarget {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerHost,
        [Parameter(Mandatory = $true)]
        [int]$SSHPort,
        [Parameter(Mandatory = $true)]
        [string]$SSHUser
    )

    # 该函数不读取本机 SSH 文件，可供离线 SelfTest 先验证参数冻结边界。
    if ($ServerHost -cne "8.130.9.163" -or $SSHUser -cne "pc" -or $SSHPort -ne 10003) {
        throw "SSH 目标必须固定为阶段 5 测试服务器"
    }
}

function Assert-SmsPhase5FixedTestServerIdentity {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerHost,
        [Parameter(Mandatory = $true)]
        [int]$SSHPort,
        [Parameter(Mandatory = $true)]
        [string]$SSHUser
    )

    Assert-SmsPhase5FixedTestServerTarget -ServerHost $ServerHost -SSHPort $SSHPort -SSHUser $SSHUser

    # known_hosts 必须是普通文件，重解析路径可能绕过本地固定身份边界。
    $knownHostsPath = [IO.Path]::GetFullPath((Join-Path $env:USERPROFILE ".ssh\known_hosts"))
    if (-not (Test-Path -LiteralPath $knownHostsPath -PathType Leaf) -or
        ([IO.FileInfo]$knownHostsPath).Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
        throw "固定 known_hosts 文件不存在或属于重解析路径"
    }

    # 除地址匹配外还核对唯一 ED25519 公钥指纹，防止本地记录被替换后连接到错误主机。
    $knownHostLines = @(& ssh-keygen -F "[8.130.9.163]:10003" -f $knownHostsPath)
    if ($LASTEXITCODE -ne 0) {
        throw "known_hosts 中缺少固定测试服身份"
    }
    $ed25519Keys = @()
    foreach ($line in $knownHostLines) {
        $trimmed = $line.Trim()
        if ($trimmed.Length -eq 0 -or $trimmed.StartsWith("#")) {
            continue
        }
        $parts = @($trimmed -split '\s+')
        if ($parts.Count -ge 3 -and $parts[1] -ceq "ssh-ed25519") {
            $ed25519Keys += $parts[2]
        }
    }
    if ($ed25519Keys.Count -ne 1) {
        throw "固定测试服 ED25519 公钥数量异常"
    }

    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $fingerprint = "SHA256:" + [Convert]::ToBase64String(
            $sha256.ComputeHash([Convert]::FromBase64String($ed25519Keys[0]))
        ).TrimEnd('=')
    }
    finally {
        $sha256.Dispose()
    }
    if ($fingerprint -cne "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I") {
        throw "固定测试服 ED25519 公钥指纹不匹配"
    }

    # 调用方只接收已验证的绝对路径，并继续通过 UserKnownHostsFile 固定 SSH 行为。
    return $knownHostsPath
}
