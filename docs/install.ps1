# Plonkr installer for Windows (64-bit).
#
#   irm https://plonkr.dev/install.ps1 | iex
#
# Installs plonk.exe from a GitHub release of vanvanni/plonkr for the current
# user. No administrator rights are needed. The release archive is checked
# against the release's SHA256SUMS before anything is installed.
#
# Choose a version or folder with environment variables, then run the line above:
#   $env:PLONK_VERSION = '2026.11.0-beta.3'    # default: newest published release, prereleases included
#   $env:PLONK_INSTALL_DIR = 'D:\Tools\plonk'  # default: %LOCALAPPDATA%\Programs\plonk
#   $env:PLONK_NO_PATH = '1'                   # do not add the folder to your user PATH
#   $env:PLONK_UNINSTALL = '1'                 # remove plonk.exe and its PATH entry instead
#
# Or save the script and pass parameters:  .\install.ps1 -Version 2026.11.0-beta.3
#
# PLONK_API_URL and PLONK_DOWNLOAD_URL replace the GitHub endpoints, for a
# mirror or for testing.

[CmdletBinding()]
param(
  [string]$Version = $env:PLONK_VERSION,
  [string]$InstallDir = $env:PLONK_INSTALL_DIR,
  [switch]$NoPath = [bool]$env:PLONK_NO_PATH,
  [switch]$Uninstall = [bool]$env:PLONK_UNINSTALL
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Windows PowerShell 5.1 downloads are very slow with the progress bar.

$repo = 'vanvanni/plonkr'
$apiUrl = if ($env:PLONK_API_URL) { $env:PLONK_API_URL } else { "https://api.github.com/repos/$repo/releases" }
$downloadUrl = if ($env:PLONK_DOWNLOAD_URL) { $env:PLONK_DOWNLOAD_URL } else { "https://github.com/$repo/releases/download" }

function Install-Plonk {
  try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

  if (-not $InstallDir) {
    if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is not set; pass -InstallDir.' }
    $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\plonk'
  }
  $InstallDir = [IO.Path]::GetFullPath($InstallDir)

  if ($Uninstall) {
    $exe = Join-Path $InstallDir 'plonk.exe'
    if (Test-Path -LiteralPath $exe) { Remove-Item -LiteralPath $exe -Force; Write-Host "Removed $exe" }
    else { Write-Host "Nothing to remove: $exe does not exist" }
    Remove-FromUserPath $InstallDir
    return
  }

  if ($env:OS -ne 'Windows_NT') { throw 'This installer is for Windows. On Linux use install.sh.' }
  $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
  if ($arch -ne 'AMD64') { throw "Unsupported CPU architecture: $arch. Releases cover 64-bit x86 (amd64) on Windows." }

  if (-not $Version) {
    Write-Host 'Looking up the newest release...'
    try { $releases = Invoke-RestMethod -Uri $apiUrl -Headers @{ 'User-Agent' = 'plonkr-installer' } }
    catch { throw "Could not reach $apiUrl : $($_.Exception.Message)" }
    $latest = @($releases) | Where-Object { -not $_.draft } | Select-Object -First 1
    if (-not $latest) { throw "No published release was found at $apiUrl. Pass -Version to choose one." }
    $Version = $latest.tag_name
  }
  $Version = $Version.TrimStart('v')
  if ($Version -notmatch '^[1-9][0-9]{3}\.(0[1-9]|1[0-2])\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$') {
    throw "Invalid version: $Version"
  }
  $tag = "v$Version"
  $archive = "plonk_${tag}_windows_amd64.zip"

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ("plonk-install-" + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "Downloading plonk $Version (windows/amd64)..."
    try {
      Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$tag/$archive" -OutFile (Join-Path $tmp $archive)
      Invoke-WebRequest -UseBasicParsing -Uri "$downloadUrl/$tag/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')
    } catch { throw "Could not download $archive for ${tag}: $($_.Exception.Message)" }

    $expected = $null
    foreach ($line in Get-Content -LiteralPath (Join-Path $tmp 'SHA256SUMS')) {
      if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+?)\s*$' -and $Matches[2] -eq $archive) { $expected = $Matches[1].ToLowerInvariant(); break }
    }
    if (-not $expected) { throw "$archive is not listed in SHA256SUMS." }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $tmp $archive)).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "Checksum mismatch for $archive (expected $expected, got $actual). Nothing was installed." }
    Write-Host 'Checksum verified.'

    $unpack = Join-Path $tmp 'unpack'
    Expand-Archive -LiteralPath (Join-Path $tmp $archive) -DestinationPath $unpack
    $source = Join-Path $unpack 'plonk.exe'
    if (-not (Test-Path -LiteralPath $source)) { throw 'The archive does not contain plonk.exe.' }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $target = Join-Path $InstallDir 'plonk.exe'
    # Stage beside the destination, then replace, so a failed copy never leaves a half-written plonk.exe.
    $staged = Join-Path $InstallDir ('.plonk.new.' + [Guid]::NewGuid().ToString('N'))
    try {
      Copy-Item -LiteralPath $source -Destination $staged
      Move-Item -LiteralPath $staged -Destination $target -Force
    } catch {
      Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
      throw "Could not install into ${InstallDir}: $($_.Exception.Message) (is plonk.exe running?)"
    }
  } finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }

  Write-Host "Installed plonk $Version to $target"
  if (-not $NoPath) {
    if (Add-ToUserPath $InstallDir) { Write-Host "Added $InstallDir to your user PATH. Open a new terminal to use it." }
  } else {
    Write-Host "Skipped PATH. Run it as: & '$target'"
  }
  if (-not (Get-Command ssh -ErrorAction SilentlyContinue)) {
    Write-Warning 'The OpenSSH client (ssh.exe) was not found. plonk needs it; enable "OpenSSH Client" under Settings > Optional features.'
  }
  Write-Host 'Check it with:  plonk version'
}

function Get-UserPathEntries {
  $current = [Environment]::GetEnvironmentVariable('Path', 'User')
  if ([string]::IsNullOrEmpty($current)) { return @() }
  return @($current.Split(';') | Where-Object { $_ -ne '' })
}

function Add-ToUserPath([string]$dir) {
  $entries = Get-UserPathEntries
  if ($entries | Where-Object { $_.TrimEnd('\') -ieq $dir.TrimEnd('\') }) { return $false }
  [Environment]::SetEnvironmentVariable('Path', (($entries + $dir) -join ';'), 'User')
  $env:Path = "$env:Path;$dir"   # this session too
  return $true
}

function Remove-FromUserPath([string]$dir) {
  $entries = Get-UserPathEntries
  $kept = @($entries | Where-Object { $_.TrimEnd('\') -ine $dir.TrimEnd('\') })
  if ($kept.Count -ne $entries.Count) {
    [Environment]::SetEnvironmentVariable('Path', ($kept -join ';'), 'User')
    Write-Host "Removed $dir from your user PATH."
  }
}

Install-Plonk
