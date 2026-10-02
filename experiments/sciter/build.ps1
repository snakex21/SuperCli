param([string]$OutputDirectory)
$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $repoRoot '.tmp/sciter-preview' }
$destination = [IO.Path]::GetFullPath($OutputDirectory)
$runtimeDirectory = Join-Path $destination 'sciter-runtime'
New-Item -ItemType Directory -Path $runtimeDirectory -Force | Out-Null
$revision = '2a890cde76e178cd159fbfbe696cc0959621e4b7'
$api = 'https://gitlab.com/api/v4/projects/sciter-engine%2Fsciter-js-sdk/repository/files/'
$runtimePath = Join-Path $runtimeDirectory 'scapp.exe'
$expectedHash = 'F9EDB2AC98CFDF10C6F0A1E14C74B46DA553C62B5EF66F4CE8A6001E5A9CF5F2'
if (-not (Test-Path -LiteralPath $runtimePath) -or (Get-FileHash -LiteralPath $runtimePath -Algorithm SHA256).Hash -ne $expectedHash) {
 $remoteFile = [Uri]::EscapeDataString('bin/windows.d2d/x64/scapp.exe')
 Invoke-WebRequest -UseBasicParsing -Uri ($api + $remoteFile + '/raw?ref=' + $revision) -OutFile $runtimePath
}
if ((Get-FileHash -LiteralPath $runtimePath -Algorithm SHA256).Hash -ne $expectedHash) { throw 'Sciter runtime checksum mismatch' }
foreach ($license in @('LICENSE','SCITER-ENGINE-EULA.md')) {
 $remoteFile = [Uri]::EscapeDataString($license)
 Invoke-WebRequest -UseBasicParsing -Uri ($api + $remoteFile + '/raw?ref=' + $revision) -OutFile (Join-Path $runtimeDirectory $license)
}
$buildCache = Join-Path $repoRoot '.tmp/sciter-go-cache'
$moduleHome = Join-Path $repoRoot '.tmp/sciter-go-modules'
$temporary = Join-Path $repoRoot '.tmp/sciter-go-temp'
foreach ($dir in @($buildCache,$moduleHome,$temporary)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
$previousEnvironment = @{}
$buildEnvironment = @{GOCACHE=$buildCache;GOPATH=$moduleHome;GOMODCACHE=(Join-Path $moduleHome 'pkg/mod');GOTMPDIR=$temporary;TEMP=$temporary;TMP=$temporary;CGO_ENABLED='0';GOFLAGS='-mod=readonly'}
foreach ($key in $buildEnvironment.Keys) { $previousEnvironment[$key] = [Environment]::GetEnvironmentVariable($key,'Process');[Environment]::SetEnvironmentVariable($key,$buildEnvironment[$key],'Process') }
Push-Location -LiteralPath $repoRoot
try {
 & go build -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $destination 'supercli-sciter.exe') ./experiments/sciter
 if ($LASTEXITCODE -ne 0) { throw 'Sciter launcher build failed' }
} finally {
 Pop-Location
 foreach ($key in $previousEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key,$previousEnvironment[$key],'Process') }
}
Write-Output ('Ready: ' + (Join-Path $destination 'supercli-sciter.exe'))
Write-Output 'Run normally for the isolated preview, or add --smoke / --compare-webview for tests.'
