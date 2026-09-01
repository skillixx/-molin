param([Parameter(Mandatory=$true)][string]$FfmpegPath)
$ErrorActionPreference='Stop'
# 仅使用锁定的本地工具合成测试图样，不访问Provider或公网媒体，不覆盖已有夹具。
$manifest=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'media-toolchain.json') -Raw | ConvertFrom-Json
if ((Get-FileHash -LiteralPath $FfmpegPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $manifest.ffmpeg_binary_sha256) {throw 'FFmpeg校验失败'}
$repoRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))
$target=[IO.Path]::GetFullPath((Join-Path $repoRoot $manifest.fixture))
if (-not $target.StartsWith($repoRoot+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)) {throw '夹具路径越界'}
if (Test-Path -LiteralPath $target) {throw '夹具已存在，禁止覆盖'}
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
& $FfmpegPath -hide_banner -loglevel warning -n -f lavfi -i 'testsrc2=size=1280x720:rate=24' -t 5 -an -c:v libx264 -preset veryfast -threads 1 -b:v 6M -minrate 6M -maxrate 6M -bufsize 12M -x264-params 'nal-hrd=cbr:force-cfr=1' -pix_fmt yuv420p -movflags +faststart -fflags +bitexact -flags:v +bitexact -map_metadata -1 -metadata 'comment=仅用于本地非商业测试的合成图样' $target
if ($LASTEXITCODE -ne 0) {throw '合成失败，保留现场供诊断'}
Get-FileHash -LiteralPath $target -Algorithm SHA256
Get-Item -LiteralPath $target | Select-Object Length
