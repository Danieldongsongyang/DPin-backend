# 只采集指定安装器的签名、版本与系统信息，不启动安装器或招聘任务。
# 安装、启动、原生工具和数据保留的结果由可信验收人员先实际完成并记录。
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Installer,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$MinimumSystemVersion,
    [Parameter(Mandatory = $true)][string]$ChecksFile,
    [string]$ExpectedPublisher = '',
    [string]$UpdateSignatureKeyId = ''
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw 'This collector must run on the trusted Windows acceptance computer.'
}
$installerItem = Get-Item -LiteralPath $Installer
if ($installerItem.PSIsContainer -or ($installerItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'Installer must be a regular file, not a directory or reparse point.'
}
# 持有只读共享句柄，采集签名、版本及摘要期间拒绝写入与删除。
$installerLock = [IO.File]::Open($installerItem.FullName, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
try {
    $signature = Get-AuthenticodeSignature -LiteralPath $installerItem.FullName
    if ($UpdateSignatureKeyId) {
        if ($UpdateSignatureKeyId -cnotmatch '^[0-9a-f]{64}$') {
            throw 'Update signature key identifier must be a SHA256 fingerprint.'
        }
    } else {
        if ($signature.Status.ToString() -ne 'Valid' -or $null -eq $signature.SignerCertificate) {
            throw 'Installer Authenticode signature is not Valid.'
        }
        if (-not $ExpectedPublisher -or $signature.SignerCertificate.Subject -cne $ExpectedPublisher) {
            throw 'Installer signer certificate subject does not match the configured publisher.'
        }
    }
    $versionInfo = $installerItem.VersionInfo
    if ($Version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
        throw 'Only a stable three-part application version is accepted.'
    }
    $readVersion = '{0}.{1}.{2}' -f $versionInfo.ProductMajorPart, $versionInfo.ProductMinorPart, $versionInfo.ProductBuildPart
    if ($readVersion -cne $Version -or $versionInfo.ProductPrivatePart -ne 0) {
        throw 'Installer version resource does not match the specified application version.'
    }
    $checksItem = Get-Item -LiteralPath $ChecksFile
    if ($checksItem.PSIsContainer -or $checksItem.Length -gt 65536) {
        throw 'Manual acceptance evidence file is invalid or too large.'
    }
    $manual = Get-Content -LiteralPath $checksItem.FullName -Raw -Encoding UTF8 | ConvertFrom-Json
    $hash = (Get-FileHash -LiteralPath $installerItem.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($manual.version -cne $Version -or $manual.installer_sha256 -cne $hash) {
        throw 'Manual acceptance evidence must name this exact version and installer SHA256.'
    }
    if ($UpdateSignatureKeyId -and $manual.update_signature_key_id -cne $UpdateSignatureKeyId) {
        throw 'Manual acceptance evidence must bind the trusted update signature key identifier.'
    }
    $verifiedAt = 0L
    if (-not [long]::TryParse([string]$manual.verified_at, [ref]$verifiedAt) -or
        $verifiedAt -lt [DateTimeOffset]::UtcNow.AddDays(-90).ToUnixTimeSeconds() -or
        $verifiedAt -gt [DateTimeOffset]::UtcNow.AddMinutes(5).ToUnixTimeSeconds()) {
        throw 'Manual evidence requires a Unix verified_at time within 90 days, not a future time.'
    }
    $checks = [ordered]@{}
    $checkEvidence = [ordered]@{}
    foreach ($name in @('installation', 'launch', 'native_tools', 'data_preserved')) {
        $entry = $manual.$name
        if ($null -eq $entry -or $entry.passed -isnot [bool] -or $entry.passed -ne $true -or
            $entry.evidence -isnot [string] -or [string]::IsNullOrWhiteSpace($entry.evidence) -or
            $entry.evidence.Length -gt 4096) {
            throw "Each manual acceptance item requires passed=true and a concrete evidence description: $name"
        }
        $checks[$name] = $true
        $checkEvidence[$name] = $entry.evidence.Trim()
    }
    $windowsVersion = (Get-CimInstance -ClassName Win32_OperatingSystem).Version
    if ($MinimumSystemVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?$' -or [version]$windowsVersion -lt [version]$MinimumSystemVersion) {
        throw 'Acceptance computer does not meet the supplied minimum Windows version.'
    }
    $report = [ordered]@{
        schema_version = 1
        product = 'bellobuddy'
        version = $Version
        platform = 'win32'
        arch = 'x64'
        installer_sha256 = $hash
        verified_at = $verifiedAt
        windows_version = $windowsVersion
        minimum_system_version = $MinimumSystemVersion
        update_signature_key_id = $UpdateSignatureKeyId
        authenticode = [ordered]@{
            status = $signature.Status.ToString()
            publisher = $(if ($null -ne $signature.SignerCertificate) { $signature.SignerCertificate.Subject } else { '' })
            certificate_thumbprint = $(if ($null -ne $signature.SignerCertificate) { $signature.SignerCertificate.Thumbprint } else { '' })
        }
        checks = $checks
        check_evidence = $checkEvidence
    }
    # 使用显式 UTF-8 输出，避免 PowerShell 5.1 的控制台代码页损坏中文证据。
    [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $report | ConvertTo-Json -Depth 8 -Compress

} finally {
    $installerLock.Dispose()
}
