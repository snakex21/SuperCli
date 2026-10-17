# SuperCli fresh, per-user Windows installer. Requires Windows PowerShell 5.1.
# SUPERCLI_VERSION: optional v followed by a stable x.y.z version; otherwise latest.
# SUPERCLI_DIR: a NEW directory (default: $HOME\SuperCli).
# SUPERCLI_NO_PATH=1: portable install; leave both user and process PATH unchanged.
# Downloads are verified and unpacked, never executed. No administrator is needed.
param(
    # Injectable low-level boundary for offline tests; only HKCU is used normally.
    [scriptblock] $OpenUserEnvironment = {
        [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    }
)
& {
    Set-StrictMode -Version 2.0
    $ErrorActionPreference = 'Stop'
    $scratch = $null
    $oldTls = [Net.ServicePointManager]::SecurityProtocol

    function ConvertTo-SuperCliVersion([string] $Value) {
        if ($Value -cnotmatch '\Av?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z') {
            throw "Invalid version '$Value'; expected x.y.z with an optional leading v (no prerelease or leading zeros)."
        }
        return ($Value -creplace '\Av', '')
    }

    function Test-SuperCliExists([string] $Path) {
        # Get-Item also catches hidden entries and dangling reparse points.
        return ($null -ne (Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue))
    }

    function Stop-SuperCliExisting([string] $Path) {
        throw "Refusing to overwrite existing target '$Path'. Existing users: run supercli --update, or choose a new folder with SUPERCLI_DIR."
    }

    function Add-SuperCliPathEntry([string] $Value, [string] $Directory, [bool] $ExpandReferences = $true) {
        foreach ($part in ($Value -split ';')) {
            $expanded = $part.Trim().Trim('"')
            if ($ExpandReferences) { $expanded = [Environment]::ExpandEnvironmentVariables($expanded) }
            $expanded = $expanded.TrimEnd('\', '/')
            if ($expanded -ieq $Directory.TrimEnd('\', '/')) {
                return $Value
            }
        }
        if ([string]::IsNullOrEmpty($Value)) { return $Directory }
        if ($Value.EndsWith(';')) { return ($Value + $Directory) }
        return ($Value + ';' + $Directory)
    }

    function Invoke-SuperCliHttpsRequest([string] $Uri, [hashtable] $Headers, [string] $OutFile) {
        $current = [Uri] $Uri
        for ($redirects = 0; ; $redirects++) {
            if (-not $current.IsAbsoluteUri -or $current.Scheme -ine 'https') {
                throw "Refusing non-HTTPS request or redirect: '$current'."
            }
            $parameters = @{
                Uri = $current.AbsoluteUri; Headers = $Headers; UseBasicParsing = $true
                MaximumRedirection = 0; ErrorAction = 'SilentlyContinue'; ErrorVariable = 'requestErrors'
            }
            if (-not [string]::IsNullOrEmpty($OutFile)) {
                $parameters.OutFile = $OutFile
                $parameters.PassThru = $true
            }
            $requestErrors = @()
            # PS 5.1 writes the 301/302 response, THEN a nonterminating
            # MaximumRedirectExceeded error with a plain Exception (no Response).
            # Capture both; -ErrorAction Stop would lose the response/Location.
            # Genuine terminating web/transport errors must still fail closed.
            $response = Invoke-WebRequest @parameters
            if ($null -eq $response -or $response -is [array] -or
                $null -eq $response.PSObject.Properties['StatusCode']) {
                throw "HTTPS request did not return an HTTP response: '$current'."
            }
            $status = [int] $response.StatusCode
            $isRedirect = $status -in @(301, 302, 303, 307, 308)
            foreach ($requestError in $requestErrors) {
                if (-not $isRedirect -or $requestError.FullyQualifiedErrorId -notmatch '\AMaximumRedirectExceeded(,|\z)') {
                    throw $requestError
                }
            }
            if (-not $isRedirect) {
                if ($status -lt 200 -or $status -ge 300) { throw "HTTPS request failed with status $status at '$current'." }
                return $response
            }
            if ($null -eq $response.PSObject.Properties['Headers'] -or
                [string]::IsNullOrWhiteSpace([string] $response.Headers['Location'])) {
                throw "HTTPS redirect has no Location at '$current'."
            }
            $next = New-Object Uri ($current, [string] $response.Headers['Location'])
            if (-not $next.IsAbsoluteUri -or $next.Scheme -ine 'https') {
                throw "Refusing non-HTTPS request or redirect: '$next'."
            }
            if ($redirects -ge 5) { throw 'Too many HTTPS redirects (maximum 5).' }
            $current = $next
        }
    }

    try {
        if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
            throw 'Unsupported operating system. This installer supports Windows/amd64 only.'
        }
        if (-not ('SuperCliInstaller.NativePlatform' -as [type])) {
            Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
namespace SuperCliInstaller {
    public static class NativePlatform {
        [StructLayout(LayoutKind.Sequential)]
        private struct SystemInfo {
            public ushort Architecture;
            public ushort Reserved;
            public uint PageSize;
            public IntPtr MinimumAddress;
            public IntPtr MaximumAddress;
            public UIntPtr ProcessorMask;
            public uint ProcessorCount;
            public uint ProcessorType;
            public uint AllocationGranularity;
            public ushort ProcessorLevel;
            public ushort ProcessorRevision;
        }
        [DllImport("kernel32.dll")]
        private static extern void GetNativeSystemInfo(out SystemInfo info);
        public static ushort Architecture() {
            SystemInfo info;
            GetNativeSystemInfo(out info);
            return info.Architecture;
        }
    }
}
'@
        }
        # GetNativeSystemInfo reports the OS architecture, not this PS process's.
        $nativeArch = [SuperCliInstaller.NativePlatform]::Architecture()
        if ($nativeArch -ne 9) {
            $archName = switch ($nativeArch) {
                0 { 'x86' }
                5 { 'arm' }
                6 { 'ia64' }
                12 { 'arm64' }
                default { "architecture-$nativeArch" }
            }
            throw "Unsupported platform windows/$archName. Public installer releases currently support windows/amd64 only."
        }

        $requestedVersion = $env:SUPERCLI_VERSION
        $version = $null
        if (-not [string]::IsNullOrEmpty($requestedVersion)) {
            $version = ConvertTo-SuperCliVersion $requestedVersion
        }
        $requestedDirectory = $env:SUPERCLI_DIR
        if ([string]::IsNullOrEmpty($requestedDirectory)) {
            $requestedDirectory = Join-Path $HOME 'SuperCli'
        }
        $target = [IO.Path]::GetFullPath($requestedDirectory)
        if ($target.TrimEnd('\', '/') -ieq [IO.Path]::GetPathRoot($target).TrimEnd('\', '/')) {
            throw 'The install target must be a new directory, not a filesystem root.'
        }
        $target = $target.TrimEnd('\', '/')
        if (Test-SuperCliExists $target) { Stop-SuperCliExisting $target }
        if ($env:SUPERCLI_NO_PATH -ne '1' -and $target.Contains(';')) {
            throw 'An install directory containing a semicolon cannot be added to PATH. Choose another directory or set SUPERCLI_NO_PATH=1.'
        }

        # Find the first missing ancestor. Stage beside it so publication is a
        # same-volume, no-replace rename, even for a new nested SUPERCLI_DIR.
        $publishPath = $target
        $parent = [IO.Path]::GetDirectoryName($publishPath)
        while (-not [IO.Directory]::Exists($parent)) {
            if ([string]::IsNullOrEmpty($parent) -or (Test-SuperCliExists $parent)) {
                throw "The install parent is not an accessible directory: '$parent'."
            }
            $publishPath = $parent
            $parent = [IO.Path]::GetDirectoryName($publishPath)
        }

        [Net.ServicePointManager]::SecurityProtocol = $oldTls -bor [Net.SecurityProtocolType]::Tls12
        $headers = @{ 'User-Agent' = 'SuperCli-Windows-Installer'; 'Accept' = 'application/vnd.github+json' }
        $api = 'https://api.github.com/repos/snakex21/SuperCli/releases/latest'
        if ($null -ne $version) {
            $api = "https://api.github.com/repos/snakex21/SuperCli/releases/tags/v$version"
        }
        $apiResponse = Invoke-SuperCliHttpsRequest -Uri $api -Headers $headers
        $release = $apiResponse.Content | ConvertFrom-Json -ErrorAction Stop
        if ($null -eq $release -or $release -is [array] -or
            $null -eq $release.PSObject.Properties['tag_name'] -or
            $null -eq $release.PSObject.Properties['prerelease'] -or
            $null -eq $release.PSObject.Properties['draft'] -or
            $null -eq $release.PSObject.Properties['assets']) {
            throw 'Invalid GitHub release response; expected a stable public release with assets.'
        }
        if ($release.prerelease -ne $false -or $release.draft -ne $false) {
            throw 'Unsupported release: prerelease and draft releases are not installed.'
        }
        $tag = [string] $release.tag_name
        $releaseVersion = ConvertTo-SuperCliVersion $tag
        if ($null -ne $version -and $releaseVersion -cne $version) {
            throw "Unsupported release: requested $version but GitHub returned '$tag'."
        }
        $version = $releaseVersion
        $assetName = "supercli_${version}_windows_amd64.zip"
        foreach ($required in @($assetName, 'SHA256SUMS')) {
            $assets = @($release.assets | Where-Object { $_.name -ceq $required })
            if ($assets.Count -ne 1) {
                throw "Unsupported release '$tag': expected exactly one '$required' asset (windows/amd64 only)."
            }
        }

        $candidate = Join-Path $parent ('.supercli-install-' + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $candidate -ErrorAction Stop | Out-Null
        $scratch = $candidate
        $zipPath = Join-Path $scratch $assetName
        $sumsPath = Join-Path $scratch 'SHA256SUMS'
        $base = "https://github.com/snakex21/SuperCli/releases/download/$tag"
        Write-Host "Downloading SuperCli $version (windows/amd64)..."
        Invoke-SuperCliHttpsRequest -Uri "$base/SHA256SUMS" -Headers $headers -OutFile $sumsPath | Out-Null
        Invoke-SuperCliHttpsRequest -Uri "$base/$assetName" -Headers $headers -OutFile $zipPath | Out-Null

        $checksumMatches = 0
        $expectedHash = $null
        foreach ($line in [IO.File]::ReadAllLines($sumsPath)) {
            $match = [regex]::Match($line, '\A(\S+)[ \t]+\*?(.+)\z')
            if ($match.Success -and $match.Groups[2].Value -ceq $assetName) {
                $checksumMatches++
                $expectedHash = $match.Groups[1].Value
            }
        }
        if ($checksumMatches -ne 1) {
            throw "Expected exactly one SHA256SUMS entry for '$assetName'; found $checksumMatches."
        }
        if ($expectedHash -cnotmatch '\A[0-9a-fA-F]{64}\z') {
            throw "Invalid SHA256 checksum for '$assetName'."
        }
        $actualHash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256 -ErrorAction Stop).Hash
        if ($actualHash -ine $expectedHash) { throw "SHA256 mismatch for '$assetName'. Nothing was installed." }

        $publishRoot = Join-Path $scratch 'install'
        $payload = $publishRoot
        if ($publishPath -ine $target) {
            $payload = Join-Path $publishRoot $target.Substring($publishPath.Length).TrimStart('\', '/')
        }
        [IO.Directory]::CreateDirectory($payload) | Out-Null
        $payloadPrefix = $payload + [IO.Path]::DirectorySeparatorChar
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $archive = [IO.Compression.ZipFile]::OpenRead($zipPath)
        try {
            $paths = New-Object 'System.Collections.Generic.Dictionary[string,bool]' ([StringComparer]::OrdinalIgnoreCase)
            $plan = New-Object 'System.Collections.Generic.List[object]'
            foreach ($entry in $archive.Entries) {
                $name = $entry.FullName
                $kind = ([int64] $entry.ExternalAttributes -shr 16) -band 0xF000
                if (($kind -ne 0 -and $kind -ne 0x8000 -and $kind -ne 0x4000) -or
                    ($entry.ExternalAttributes -band 0x400) -ne 0) {
                    throw "Unsafe ZIP entry (symlink, reparse point or special file): '$name'."
                }
                if ([string]::IsNullOrEmpty($name) -or $name.Contains('\') -or $name.StartsWith('/')) {
                    throw "Unsafe ZIP path: '$name'."
                }
                $isDirectory = $name.EndsWith('/')
                if (($kind -eq 0x4000 -or ($entry.ExternalAttributes -band 0x10) -ne 0) -and -not $isDirectory) {
                    throw "Unsafe ZIP directory entry: '$name'."
                }
                $relative = $name
                if ($isDirectory) { $relative = $name.Substring(0, $name.Length - 1) }
                foreach ($segment in ($relative -split '/')) {
                    if ([string]::IsNullOrEmpty($segment) -or $segment -eq '.' -or $segment -eq '..' -or
                        $segment.IndexOfAny([IO.Path]::GetInvalidFileNameChars()) -ge 0 -or
                        $segment.EndsWith('.') -or $segment.EndsWith(' ') -or
                        $segment -imatch '\A(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9]|CONIN\$|CONOUT\$)(\.|\z)') {
                        throw "Unsafe ZIP path: '$name'."
                    }
                }
                $destination = [IO.Path]::GetFullPath((Join-Path $payload $relative))
                if (-not $destination.StartsWith($payloadPrefix, [StringComparison]::OrdinalIgnoreCase)) {
                    throw "Unsafe ZIP path outside the install directory: '$name'."
                }
                if ($paths.ContainsKey($destination)) { throw "Duplicate ZIP path: '$name'." }
                $paths.Add($destination, $isDirectory)
                $plan.Add([pscustomobject] @{ Entry = $entry; Path = $destination; Directory = $isDirectory })
            }
            foreach ($item in $plan) {
                $ancestor = [IO.Path]::GetDirectoryName($item.Path)
                while ($ancestor.Length -ge $payloadPrefix.Length) {
                    if ($paths.ContainsKey($ancestor) -and -not $paths[$ancestor]) {
                        throw "Conflicting ZIP file and directory paths: '$($item.Entry.FullName)'."
                    }
                    $ancestor = [IO.Path]::GetDirectoryName($ancestor)
                }
            }
            foreach ($exe in @('supercli.exe', 'supercli-web.exe')) {
                $exePath = Join-Path $payload $exe
                if (-not $paths.ContainsKey($exePath) -or $paths[$exePath]) {
                    throw "Archive must contain the root file '$exe'."
                }
                $exeEntry = @($plan | Where-Object { $_.Path -ieq $exePath })[0].Entry
                if ($exeEntry.Length -eq 0) { throw "Archive contains an empty '$exe'." }
            }
            # Only after the checksum and every path have passed validation.
            foreach ($item in $plan) {
                if ($item.Directory) {
                    [IO.Directory]::CreateDirectory($item.Path) | Out-Null
                    continue
                }
                [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($item.Path)) | Out-Null
                $source = $null
                $output = $null
                try {
                    $source = $item.Entry.Open()
                    $output = [IO.File]::Open($item.Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
                    $source.CopyTo($output)
                } finally {
                    if ($null -ne $output) { $output.Dispose() }
                    if ($null -ne $source) { $source.Dispose() }
                }
            }
        } finally {
            $archive.Dispose()
        }
        foreach ($exe in @('supercli.exe', 'supercli-web.exe')) {
            if (-not [IO.File]::Exists((Join-Path $payload $exe))) { throw "Extraction did not produce '$exe'." }
        }
        if (Test-SuperCliExists $publishPath) { Stop-SuperCliExisting $publishPath }
        # Directory.Move never merges with, replaces, or deletes an existing target.
        [IO.Directory]::Move($publishRoot, $publishPath)

        if ($env:SUPERCLI_NO_PATH -ne '1') {
            $env:Path = Add-SuperCliPathEntry $env:Path $target
            try {
                $key = & $OpenUserEnvironment
                if ($null -eq $key) { throw 'Could not open the user Environment registry key.' }
                try {
                    # Environment.Get/SetEnvironmentVariable(User) expands existing
                    # REG_EXPAND_SZ references and rewrites its registry value kind.
                    # Read the raw value and preserve its original kind instead.
                    $userPath = $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
                    $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
                    if ($null -ne $userPath) {
                        $kind = $key.GetValueKind('Path')
                        if ($kind -ne [Microsoft.Win32.RegistryValueKind]::String -and
                            $kind -ne [Microsoft.Win32.RegistryValueKind]::ExpandString) {
                            throw "Unsupported user PATH registry value kind: $kind."
                        }
                    }
                    $newUserPath = Add-SuperCliPathEntry $userPath $target ($kind -eq [Microsoft.Win32.RegistryValueKind]::ExpandString)
                    if ($newUserPath -cne $userPath) { $key.SetValue('Path', $newUserPath, $kind) }
                } finally {
                    $key.Dispose()
                }
            } catch {
                Write-Warning "SuperCli is installed, but user PATH could not be updated: $($_.Exception.Message) Add '$target' to your user PATH manually."
            }
        }
        Write-Host "Installed SuperCli $version to '$target'. No downloaded executables were started."
        if ($env:SUPERCLI_NO_PATH -eq '1') {
            Write-Host 'Portable mode: PATH was not changed.'
        } else {
            Write-Host 'User PATH is configured. Open a new terminal if your current host does not see it.'
        }
    } catch {
        throw "SuperCli installation failed: $($_.Exception.Message)"
    } finally {
        [Net.ServicePointManager]::SecurityProtocol = $oldTls
        if ($null -ne $scratch) {
            try {
                Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction Stop
            } catch {
                Write-Warning "Could not remove installer scratch directory '$scratch': $($_.Exception.Message)"
            }
        }
    }
}
