param(
    [string]$Source = 'https://github.com/cosin2077/daylog/releases/latest/download/release.json',
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Daylog\bin')
)
$ErrorActionPreference = 'Stop'
# A parent PowerShell 7 process can export its module search path to Windows
# PowerShell 5.1. Load each built-in module from this host's own installation.
foreach ($module in @('Microsoft.PowerShell.Management','Microsoft.PowerShell.Utility','Microsoft.PowerShell.Security')) {
    Import-Module (Join-Path $PSHOME "Modules\$module\$module.psd1") -ErrorAction Stop
}
if (-not [Environment]::Is64BitOperatingSystem -or $env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { throw 'This release supports Windows x64 only.' }
$PublisherCertificate = 'MIIEPjCCAqagAwIBAgIJANAhjHgjVfevMA0GCSqGSIb3DQEBCwUAMDIxHzAdBgNVBAMMFkRheWxvZyBSZWxlYXNlIFNpZ25pbmcxDzANBgNVBAoMBkRheWxvZzAeFw0yNjEwMTQwODA4MDBaFw0zNjEwMTEwODA4MDBaMDIxHzAdBgNVBAMMFkRheWxvZyBSZWxlYXNlIFNpZ25pbmcxDzANBgNVBAoMBkRheWxvZzCCAaIwDQYJKoZIhvcNAQEBBQADggGPADCCAYoCggGBAOk32hqHLHoGBeynSPndApJoNtkEhC/txoEDyAyd0nvFX1/Xj2dBN89UJp+Qvivq/9f9NQr6Dk6ipg3utlB/mPKHEADW3wRm618DHyiaM5qg/ATsvo3y2Ezxm5LJ3RcgSlVaWJpDVCb3J0m7ByZc3v0GQQdixNIDBKGdjgc2eL4QkpWFhgQvhaWVsJjS7egWCgBWH92Av3Mj7a03PT5ZozCBkrwYfge9zh9Fd5Ehwx97FbOELGG5BERnTkDkKVqPRNp5DkqyYbKQZiwxfo7yRwTfKcdxxcMpLBkwsdzxo3YJkU8iMTiF5TolcrOZX8i2t6hLfpVQbIR55VnhESn1NmUUDKjI8F5DpJiRh5OorZJY8gjGQJg4wFjvZJcrvtJBUPkHlUGvgCldxGQQ3L4fvI/TB7W62KZuBfTke1awkyebSRbvSwYE80AL6skaRqrAvF3ZnRk4WiZ0xwFg+yQzdjnV2U7Ay3D30Zo57wEQaWrXMIbCre4fhYByflg1w2uC2QIDAQABo1cwVTAMBgNVHRMBAf8EAjAAMA4GA1UdDwEB/wQEAwIHgDAWBgNVHSUBAf8EDDAKBggrBgEFBQcDAzAdBgNVHQ4EFgQUF1NVwcpdvUSenvRZFXSOMzsYM0UwDQYJKoZIhvcNAQELBQADggGBANhcJsMyMPyXa9PMYmuKgyaDtYa3afX3qRI+lwg5IC7vcfzQXs8GHW59SHy1uzk5Tmb/1DmXc7ZBzQxxSJqo+qig7d1ERy8qJfPmSucQec52gRYJGamdEDuYWOcdfrQFrebWnBudJmmQhqXD4zlisr+7uQnEusXgYr9v2ZogM7TJ0IIe1EwKTi2X0qJ33v3W7yDnL4UYYR3owhc2TRCSCmYzHEqtocB0O0diCi2sOjd3pkg6tdOf7h7wbg+AM8rGts2RVo2ataKusb2X4lkmTigVonbV5Ze2XvpBFY4q+wmLkMsb/DO8HSnsHZlBTx+0u93aDQSk2Ysvsxvaa9GYCnq9Ty78YIhcjtcfBsHoBo80mX4Qa0Q5NUR9Co7oehwIRy2lvbwQUMo4tqMGcxnuYONXkWJ9CO6BtVn4xRpos6fXEB7NKA47OFwM6ZUGG5doOfc/zDXUNYaA80pYiN+Q6/7Esm/djZS7pHUoxY3cdEg9m/AtUZKToJhoIj4MVVJouQ=='
$IsLocal = -not ($Source -match '^https?://')
$AllowDevelopment = $IsLocal -and $env:DAYLOG_DEVELOPMENT_INSTALL -eq '1'
Add-Type -AssemblyName System.Net.Http
Add-Type -AssemblyName System.IO.Compression.FileSystem
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

function Set-PrivateDirectory([string]$Path) {
    [IO.Directory]::CreateDirectory($Path) | Out-Null
    $item = Get-Item -LiteralPath $Path
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Install directory must not be a reparse point.' }
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    foreach ($identity in @($sid, (New-Object Security.Principal.SecurityIdentifier('S-1-5-18')))) {
        $rule = New-Object Security.AccessControl.FileSystemAccessRule($identity, 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Get-Artifact([string]$Address, [string]$Destination, [long]$Limit) {
    if ($Address -notmatch '^https?://') {
        if ((Get-Item -LiteralPath $Address).Length -gt $Limit) { throw 'Artifact exceeds size limit.' }
        Copy-Item -LiteralPath $Address -Destination $Destination
        return
    }
    $uri = [Uri]$Address
    $handler = New-Object Net.Http.HttpClientHandler
    $handler.AllowAutoRedirect = $false
    $client = New-Object Net.Http.HttpClient($handler)
    $client.Timeout = [TimeSpan]::FromSeconds(45)
    try {
        for ($redirect = 0; $redirect -le 5; $redirect++) {
            if ($uri.UserInfo -or $uri.Fragment -or ($uri.Scheme -ne 'https' -and -not ($AllowDevelopment -and $uri.IsLoopback))) { throw 'Artifact URL must use HTTPS.' }
            $response = $client.GetAsync($uri, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
            if ([int]$response.StatusCode -ge 300 -and [int]$response.StatusCode -lt 400) {
                $location = $response.Headers.Location
                if (-not $location) { throw 'Redirect has no destination.' }
                $uri = New-Object Uri($uri, $location)
                $response.Dispose()
                continue
            }
            $response.EnsureSuccessStatusCode() | Out-Null
            $stream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
            $output = [IO.File]::Create($Destination)
            try {
                $buffer = New-Object byte[] 65536
                [long]$total = 0
                while (($count = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                    $total += $count
                    if ($total -gt $Limit) { throw 'Artifact exceeds size limit.' }
                    $output.Write($buffer, 0, $count)
                }
            } finally { $output.Dispose(); $stream.Dispose(); $response.Dispose() }
            return
        }
        throw 'Too many redirects.'
    } finally { $client.Dispose(); $handler.Dispose() }
}

$temp = Join-Path ([IO.Path]::GetTempPath()) ('daylog-install-' + [Guid]::NewGuid())
Set-PrivateDirectory $temp
try {
    Write-Host 'Checking the Daylog release...'
    $manifest = Join-Path $temp 'release.json'
    Get-Artifact $Source $manifest 1048576
    $feed = Get-Content -LiteralPath $manifest -Raw | ConvertFrom-Json
    if ($feed.version -notmatch '^\d+\.\d+\.\d+$') { throw 'Invalid release version.' }
    $artifact = $feed.artifacts.'windows-amd64'
    if (-not $artifact -or $artifact.sha256 -notmatch '^[a-fA-F0-9]{64}$') { throw 'Release has no valid Windows x64 package.' }
    $address = [string]$artifact.url
    if ($address -notmatch '^https?://') {
        if ([IO.Path]::GetFileName($address) -ne $address -or $address -notmatch '\.zip$') { throw 'Unsafe relative artifact URL.' }
        if ($IsLocal) { $address = Join-Path (Split-Path -Parent ([IO.Path]::GetFullPath($Source))) $address }
        else { $address = (New-Object Uri(([Uri]$Source), $address)).AbsoluteUri }
    }
    $zipPath = Join-Path $temp 'daylog-windows-amd64.zip'
    Get-Artifact $address $zipPath 33554432
    if ((Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash -ne $artifact.sha256) { throw 'Package checksum mismatch.' }
    $zip = [IO.Compression.ZipFile]::OpenRead($zipPath)
    try {
        if ($zip.Entries.Count -ne 4) { throw 'Unexpected package entries.' }
        $seen = @{}
        foreach ($entry in $zip.Entries) {
            if ($entry.FullName -notin @('daylog.exe','signature.json','LICENSE','THIRD_PARTY_NOTICES.txt') -or $seen.ContainsKey($entry.FullName)) { throw 'Unsafe package entry.' }
            $seen[$entry.FullName] = $true
            $limit = if ($entry.FullName -eq 'signature.json') { 8192 } elseif ($entry.FullName -eq 'daylog.exe') { 33554432 } else { 65536 }
            if ($entry.Length -gt $limit) { throw 'Package entry exceeds limit.' }
            if ((($entry.ExternalAttributes -shr 16) -band 0xF000) -eq 0xA000) { throw 'Symbolic links are not allowed.' }
            $input = $entry.Open()
            $output = [IO.File]::Create((Join-Path $temp $entry.FullName))
            try {
                $buffer = New-Object byte[] 65536
                [long]$total = 0
                while (($count = $input.Read($buffer, 0, $buffer.Length)) -gt 0) {
                    $total += $count
                    if ($total -gt $limit) { throw 'Expanded entry exceeds size limit.' }
                    $output.Write($buffer, 0, $count)
                }
            } finally { $output.Dispose(); $input.Dispose() }
        }
    } finally { $zip.Dispose() }
    $meta = Get-Content -LiteralPath (Join-Path $temp 'signature.json') -Raw | ConvertFrom-Json
    $exe = Join-Path $temp 'daylog.exe'
    $digest = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($meta.version -ne $feed.version -or $meta.platform -ne 'windows-amd64' -or $meta.sha256 -cne $digest) { throw 'Signed package identity mismatch.' }
    if (-not ($AllowDevelopment -and $meta.signature -ceq 'development')) {
        $certificate = New-Object Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList (,[Convert]::FromBase64String($PublisherCertificate))
        $rsa = [Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPublicKey($certificate)
        try {
            $message = [Text.Encoding]::UTF8.GetBytes("daylog-windows-v1`n$($feed.version)`nwindows-amd64`n$digest`n")
            if (-not $rsa.VerifyData($message, [Convert]::FromBase64String($meta.signature), [Security.Cryptography.HashAlgorithmName]::SHA256, [Security.Cryptography.RSASignaturePadding]::Pkcs1)) { throw 'Publisher signature mismatch.' }
        } finally { $rsa.Dispose(); $certificate.Dispose() }
    }
    $runtime = & $exe version --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $runtime.version -ne $feed.version) { throw 'Executable version mismatch.' }
    Set-PrivateDirectory $InstallDir
    $target = Join-Path $InstallDir 'daylog.exe'
    if (Test-Path -LiteralPath $target) {
        # The validated local archive is reused; normal updates retain the public feed.
        $artifact.url = 'daylog-windows-amd64.zip'
        [IO.File]::WriteAllText($manifest, ($feed | ConvertTo-Json -Depth 8), (New-Object Text.UTF8Encoding($false)))
        & $target update --source $manifest
        if ($LASTEXITCODE -ne 0) { throw 'Existing installation update failed.' }
    } else { Move-Item -LiteralPath $exe -Destination $target }
    foreach ($name in @('LICENSE','THIRD_PARTY_NOTICES.txt')) { Copy-Item -LiteralPath (Join-Path $temp $name) -Destination (Join-Path $InstallDir $name) -Force }
    $userPath = [string][Environment]::GetEnvironmentVariable('Path', 'User')
    if ($InstallDir -notin ($userPath -split ';')) { [Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';') + ';' + $InstallDir).TrimStart(';')), 'User') }
    Write-Host "Installed Daylog $($feed.version): $target"
    Write-Host 'Open a new terminal to use daylog. Recording has not been started.'
} finally { if (Test-Path -LiteralPath $temp) { Remove-Item -LiteralPath $temp -Recurse -Force } }
