<#
.SYNOPSIS
  ctx-wire installer for Windows (PowerShell).

.DESCRIPTION
  Downloads the latest ctx-wire release for your CPU architecture from GitHub and
  installs it. By default it installs PER-USER (no admin needed):

    irm https://ctx-wire.dev/install.ps1 | iex

  To pass options, run the script block form:

    & ([scriptblock]::Create((irm https://ctx-wire.dev/install.ps1))) -Machine
    & ([scriptblock]::Create((irm https://ctx-wire.dev/install.ps1))) -Version 0.1.28

  -Machine installs machine-wide under %ProgramFiles% and requires an elevated
  (Administrator) session, e.g. a GPO computer-startup script. For an offline or
  GPO deployment, point -SourcePath at a downloaded release .zip or ctx-wire.exe.

.PARAMETER Version
  Install a specific version (e.g. "0.1.28"). Default: the latest release.
.PARAMETER Machine
  Install machine-wide (%ProgramFiles%, machine PATH). Requires Administrator.
.PARAMETER SourcePath
  Install from a local release .zip or ctx-wire.exe instead of downloading.
.PARAMETER ExpectedSha256
  Verify the source against this SHA-256 before installing.
.PARAMETER InstallDir
  Override the install directory.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [switch]$Machine,
    [string]$SourcePath,
    [string]$ExpectedSha256,
    [string]$InstallDir
)

$ErrorActionPreference = 'Stop'
$Repo = 'pivanov/ctx-wire'

function Say  { param([string]$m) Write-Host $m }
function Fail { param([string]$m) throw "ctx-wire install: $m" }

# Make a machine/user PATH change visible to processes started afterwards,
# without a reboot, by broadcasting WM_SETTINGCHANGE.
function Send-EnvChange {
    if (-not ('CtxWire.Native' -as [type])) {
        Add-Type -Namespace CtxWire -Name Native -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError=true, CharSet=System.Runtime.InteropServices.CharSet.Auto)]
public static extern System.IntPtr SendMessageTimeout(System.IntPtr hWnd, uint Msg, System.UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out System.UIntPtr lpdwResult);
'@
    }
    $HWND_BROADCAST = [IntPtr]0xffff
    $WM_SETTINGCHANGE = 0x1A
    $res = [UIntPtr]::Zero
    [void][CtxWire.Native]::SendMessageTimeout($HWND_BROADCAST, $WM_SETTINGCHANGE, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$res)
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    Fail 'Windows only. For macOS/Linux: curl -fsSL https://ctx-wire.dev/install.sh | sh'
}

# GitHub requires TLS 1.2; Windows PowerShell 5.1 may default to an older protocol.
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {}

$scope = if ($Machine) { 'Machine' } else { 'User' }

if ($Machine) {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $pr = New-Object Security.Principal.WindowsPrincipal($id)
    if (-not $pr.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Fail '-Machine requires an elevated session (run PowerShell as Administrator, or a GPO computer-startup context)'
    }
}

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $base = if ($Machine) { $env:ProgramFiles } else { $env:LOCALAPPDATA }
    $InstallDir = Join-Path $base 'ctx-wire\bin'
}

# Detect the OS architecture from the environment (works on all PowerShell
# versions). PROCESSOR_ARCHITEW6432 is set for a 32-bit shell on a 64-bit OS and
# carries the true arch, so it takes priority.
$osArch = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($osArch)) { $osArch = $env:PROCESSOR_ARCHITECTURE }
switch ($osArch.ToUpperInvariant()) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Fail "unsupported architecture '$osArch' (ctx-wire ships amd64 and arm64 Windows builds)" }
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
$null = New-Item -ItemType Directory -Path $tmp -Force
try {
    if (-not [string]::IsNullOrWhiteSpace($SourcePath)) {
        $SourcePath = (Resolve-Path -LiteralPath $SourcePath).Path
        $tag = 'local source'
    }
    else {
        if (-not [string]::IsNullOrWhiteSpace($Version)) {
            $tag = if ($Version.StartsWith('v')) { $Version } else { "v$Version" }
        }
        else {
            Say 'Finding the latest ctx-wire release...'
            $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ 'User-Agent' = 'ctx-wire-install' }
            $tag = $rel.tag_name
        }
        if ([string]::IsNullOrWhiteSpace($tag)) {
            Fail "could not find a release (is $Repo public with a published release?)"
        }
        $ver = $tag.TrimStart('v')
        $asset = "ctx-wire_${ver}_windows_${arch}.zip"
        $url = "https://github.com/$Repo/releases/download/$tag/$asset"
        $SourcePath = Join-Path $tmp $asset
        Say "Downloading $asset ..."
        Invoke-WebRequest -Uri $url -OutFile $SourcePath -UseBasicParsing

        # Best-effort: verify the published .sha256 unless the caller supplied one.
        if ([string]::IsNullOrWhiteSpace($ExpectedSha256)) {
            try {
                $sumFile = Join-Path $tmp "$asset.sha256"
                Invoke-WebRequest -Uri "$url.sha256" -OutFile $sumFile -UseBasicParsing
                $ExpectedSha256 = ((Get-Content -LiteralPath $sumFile -Raw).Trim() -split '\s+')[0]
            } catch {
                Say 'Note: published checksum not found; relying on TLS for integrity.'
            }
        }
    }

    if (-not [string]::IsNullOrWhiteSpace($ExpectedSha256)) {
        $actual = (Get-FileHash -LiteralPath $SourcePath -Algorithm SHA256).Hash
        if ($actual -ine $ExpectedSha256.Trim()) {
            Fail "SHA-256 mismatch for $SourcePath (expected $ExpectedSha256, got $actual)"
        }
        Say 'Verified SHA-256.'
    }

    if ([IO.Path]::GetExtension($SourcePath) -ieq '.exe') {
        $binaryPath = $SourcePath
    }
    else {
        Say 'Extracting...'
        $extractDir = Join-Path $tmp 'extract'
        Expand-Archive -LiteralPath $SourcePath -DestinationPath $extractDir -Force
        $found = Get-ChildItem -Path $extractDir -Recurse -File -Filter 'ctx-wire.exe' | Select-Object -First 1
        if (-not $found) { Fail 'ctx-wire.exe not found in the archive' }
        $binaryPath = $found.FullName
    }

    $null = New-Item -ItemType Directory -Path $InstallDir -Force
    $target = Join-Path $InstallDir 'ctx-wire.exe'
    Copy-Item -LiteralPath $binaryPath -Destination $target -Force

    Say ''
    Say "Installed ctx-wire $tag to $target"
    & $target version
    if ($LASTEXITCODE -ne 0) {
        Fail "installed binary failed validation (exit code $LASTEXITCODE)"
    }

    # PATH: add InstallDir to the chosen scope if not already present.
    $current = [Environment]::GetEnvironmentVariable('Path', $scope)
    $segments = @()
    if (-not [string]::IsNullOrWhiteSpace($current)) { $segments = $current -split ';' }
    $onPath = $false
    foreach ($s in $segments) {
        if ($s.TrimEnd('\') -ieq $InstallDir.TrimEnd('\')) { $onPath = $true; break }
    }
    if (-not $onPath) {
        $newPath = if ([string]::IsNullOrWhiteSpace($current)) { $InstallDir } else { ($current.TrimEnd(';') + ';' + $InstallDir) }
        [Environment]::SetEnvironmentVariable('Path', $newPath, $scope)
        Say "Added $InstallDir to the $($scope.ToLower()) PATH."
        if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path = "$env:Path;$InstallDir" }
        Send-EnvChange
    }

    Say ''
    Say 'Next: wire up your agent, then watch the savings:'
    Say '  ctx-wire init copilot       # or vscode, visualstudio, claude, cursor, codex, gemini, ...'
    Say '  ctx-wire gain'
}
finally {
    if (Test-Path $tmp) { Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue }
}
