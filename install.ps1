#Requires -Version 5.1
<#
.SYNOPSIS
    install.ps1 -- fetch and install the ishakat binary for Windows.

.DESCRIPTION
    Usage:
      irm https://raw.githubusercontent.com/michiTrader/ishakat/main/install.ps1 | iex

    What it does, in order: detect the CPU architecture, pick the matching
    release asset from the latest GitHub Release, download it, verify its
    checksum, and place it on PATH -- $env:LOCALAPPDATA\Programs\ishakat by
    default, added to the current user's PATH if it is not already there (no
    admin rights required for either step). Idempotent: running it again
    just overwrites the binary.

    This is the PowerShell counterpart of install.sh; see that file for the
    POSIX/Termux/macOS/Linux installer. Only windows/amd64 is published by
    release.yml today, so that is the only architecture this script knows
    how to install -- windows/arm64 users get a clear message pointing at
    'go build'/'make windows-arm64' instead of a confusing 404.
#>

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Repo = 'michiTrader/ishakat'
$BinName = 'ishakat'
$GitHub = "https://github.com/$Repo"

function Write-Log {
    param([string]$Message)
    Write-Host "ishakat: $Message"
}

function Die {
    param([string]$Message)
    Write-Host "install.ps1: $Message" -ForegroundColor Red
    exit 1
}

# This is a Windows-only installer -- install.sh already covers Linux,
# macOS and Termux/Android, and this script's PATH/download logic (registry
# env vars, .exe naming) has no meaning on those platforms. $IsWindows only
# exists on PowerShell 6+; Windows PowerShell 5.1 has no such variable at
# all and is Windows-only by construction, so its absence itself means yes.
if ((Test-Path variable:IsWindows) -and -not $IsWindows) {
    Die "this script only installs the Windows build; use install.sh on Linux/macOS/Termux"
}

# Get-Platform returns the release.yml asset suffix for this machine, or
# dies with a clear message. release.yml's matrix publishes exactly one
# Windows target today (windows/amd64); ARM64 Windows devices are real but
# unpublished, so they get the same "not published yet, build from source"
# treatment install.sh gives darwin/amd64.
function Get-Platform {
    $arch = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) {
        # A 32-bit process (including 32-bit PowerShell) running under WOW64
        # on 64-bit Windows reports x86 in PROCESSOR_ARCHITECTURE; this
        # variable holds the real, un-emulated architecture in that case.
        $arch = $env:PROCESSOR_ARCHITEW6432
    }

    switch ($arch) {
        'AMD64' { return 'windows-amd64' }
        'ARM64' {
            Die "windows-arm64 is not published by release.yml yet -- build from source with 'go build ./cmd/ishakat', or cross-compile from Linux/macOS with 'make windows-arm64'"
        }
        default {
            Die "unsupported architecture: $arch (only windows/amd64 is published by release.yml)"
        }
    }
}

# Resolve-LatestTag finds the newest release's tag via the plain HTTP
# redirect GitHub serves at /releases/latest, exactly like install.sh does,
# instead of api.github.com -- an unauthenticated script should never spend
# the caller's share of GitHub's low anonymous API rate limit on a single
# redirect lookup. If no release exists yet, GitHub serves /releases with a
# 200 and no redirect, which is treated as "not found" below.
function Resolve-LatestTag {
    $latestUrl = "$GitHub/releases/latest"
    $request = [System.Net.HttpWebRequest]::Create($latestUrl)
    $request.AllowAutoRedirect = $false
    $request.Method = 'HEAD'

    $location = $null
    try {
        $response = $request.GetResponse()
        $location = $response.Headers['Location']
        $response.Close()
    } catch [System.Net.WebException] {
        $errorResponse = $_.Exception.Response
        if ($errorResponse) {
            $location = $errorResponse.Headers['Location']
            $errorResponse.Close()
        }
    }

    if (-not $location) {
        Die "no published release found at $latestUrl yet"
    }
    if ($location -notmatch '/tag/([^/]+)$') {
        Die "could not resolve the latest release tag from $latestUrl"
    }
    return $Matches[1]
}

# Get-InstallDir prints where the binary should land: LOCALAPPDATA is
# per-user and writable without admin rights on every supported Windows
# version, which is why installers like rustup/uv/deno all default to a
# sibling of it -- the same "no elevation required" property install.sh's
# own comment calls out as non-negotiable for Termux.
function Get-InstallDir {
    $dir = Join-Path $env:LOCALAPPDATA 'Programs\ishakat'
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    return $dir
}

# Add-ToUserPath persists $Dir on the current user's PATH via the registry
# (no admin rights needed for the HKCU hive) so that "just type ishakat"
# holds after opening a *new* terminal -- unlike install.sh, which only
# warns and leaves editing the shell profile to the user, Windows has no
# equivalent of a universally-sourced ~/.bashrc, so a silent warning here
# would leave most users stuck. The running process's own $env:PATH is
# updated too, but that has no effect on the current shell once this
# script's process exits; only new shells pick up the registry change.
#
function Add-ToUserPath {
    param([string]$Dir)

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($null -eq $userPath) { $userPath = '' }

    $alreadyPresent = $userPath.Split(';') | Where-Object { $_ -ieq $Dir }
    if ($alreadyPresent) {
        return $false
    }

    $newPath = if ($userPath -eq '') { $Dir } else { "$userPath;$Dir" }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    $env:PATH = "$env:PATH;$Dir"
    return $true
}

function Main {
    $platform = Get-Platform
    Write-Log "detected platform $platform"

    $tag = Resolve-LatestTag
    Write-Log "latest release is $tag"

    $asset = "$BinName-$platform.exe"
    $downloadUrl = "$GitHub/releases/download/$tag/$asset"
    $checksumUrl = "$downloadUrl.sha256"

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        $tmpAsset = Join-Path $tmp $asset

        Write-Log "downloading $asset"
        try {
            Invoke-WebRequest -Uri $downloadUrl -OutFile $tmpAsset -UseBasicParsing
        } catch {
            Die "download failed: $downloadUrl (does this release have a $platform asset?)"
        }

        $tmpChecksum = Join-Path $tmp "$asset.sha256"
        $checksumFetched = $true
        try {
            Invoke-WebRequest -Uri $checksumUrl -OutFile $tmpChecksum -UseBasicParsing
        } catch {
            $checksumFetched = $false
        }

        if ($checksumFetched) {
            # release.yml writes checksum files as "<hash>  <filename>" via
            # sha256sum, matching install.sh's own sha256sum/shasum check;
            # only the first whitespace-separated field is the hash.
            $expected = ((Get-Content $tmpChecksum -Raw).Trim() -split '\s+')[0]
            $actual = (Get-FileHash -Path $tmpAsset -Algorithm SHA256).Hash
            if ($actual.ToLowerInvariant() -ne $expected.ToLowerInvariant()) {
                Die "checksum verification failed for $asset -- the download is corrupt or was tampered with"
            }
            Write-Log 'checksum OK'
        } else {
            Write-Log 'no checksum file published for this asset, skipping verification'
        }

        $destDir = Get-InstallDir
        $dest = Join-Path $destDir "$BinName.exe"
        Copy-Item -Path $tmpAsset -Destination $dest -Force

        Write-Log "installed to $dest"

        $pathChanged = Add-ToUserPath -Dir $destDir
        if ($pathChanged) {
            Write-Log "added $destDir to your user PATH -- open a new terminal for it to take effect"
        } else {
            Write-Log "$destDir is already on PATH"
        }

        if (Get-Command $BinName -ErrorAction SilentlyContinue) {
            Write-Log "run 'ishakat doctor' to check the environment"
        } else {
            Write-Log "run '$dest doctor' to check the environment (or open a new terminal first)"
        }
    } finally {
        Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Main
