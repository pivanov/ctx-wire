# End-to-end smoke test for ctx-wire on Windows (PowerShell).
#
# Builds ctx-wire.exe and exercises the core surface (version, verify, run,
# gain scrubbing + analytics, rewrite, exclude_commands, telemetry, Windows
# data path) inside a temp workdir so it never touches your real profile.
# Exits non-zero if any check fails.
#
#   pwsh scripts/smoke.ps1     # or: powershell -File scripts/smoke.ps1
#
# Counterpart to scripts/smoke.sh. The auto-rewrite hook itself still needs a
# real agent to drive it; this validates the binary and filters.

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

$script:pass = 0
$script:fail = 0
function Step($m) { Write-Host "`n== $m ==" -ForegroundColor Cyan }
function Ok($m)   { Write-Host "  ok   $m" -ForegroundColor Green; $script:pass++ }
function Bad($m)  { Write-Host "  FAIL $m" -ForegroundColor Red;   $script:fail++ }

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("ctxwire-smoke-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$bin = Join-Path $work 'ctx-wire.exe'

# cw runs the built binary with all state redirected into the workdir, and with
# a fake LOCALAPPDATA so the default data path is exercised without touching the
# real profile. Returns combined stdout as a single string.
$gainFile = Join-Path $work 'gain.jsonl'
$localApp = Join-Path $work 'localappdata'
$roaming  = Join-Path $work 'appdata'
New-Item -ItemType Directory -Force -Path $localApp, $roaming | Out-Null
function cw {
    $env:CTX_WIRE_GAIN_FILE = $gainFile
    $env:CTX_WIRE_TELEMETRY_URL = 'http://127.0.0.1:9/ctx-wire-smoke'
    $env:LOCALAPPDATA = $localApp
    $env:APPDATA = $roaming
    Remove-Item Env:XDG_DATA_HOME, Env:XDG_CONFIG_HOME -ErrorAction SilentlyContinue
    & $bin @args 2>&1 | Out-String
}

Step 'build'
$ver = '0.0.0-smoke'
try {
    go build -ldflags "-X main.version=$ver" -o $bin ./cmd/ctx-wire
    Ok 'go build ctx-wire.exe'
} catch { Bad "go build: $_"; Write-Host "cannot continue"; exit 1 }

Step 'version'
if ((cw version) -match [regex]::Escape($ver)) { Ok "version reports $ver" } else { Bad 'version metadata' }

Step 'verify (filter conformance)'
if ((cw verify) -match '0 failed') { Ok 'ctx-wire verify' } else { Bad 'ctx-wire verify' }

Step 'run'
if ((cw run cmd /c 'echo hello').Trim() -eq 'hello') { Ok 'run cmd echo' } else { Bad 'run cmd echo' }

Step 'gain scrubbing + analytics'
Remove-Item $gainFile -ErrorAction SilentlyContinue
cw run cmd /c 'echo done' --password supersecret --token abc123 | Out-Null
if ((Test-Path $gainFile) -and -not (Select-String -Path $gainFile -Pattern 'supersecret|abc123' -Quiet) -and (Select-String -Path $gainFile -Pattern 'REDACTED' -Quiet)) {
    Ok 'gain log scrubs secret flags'
} else { Bad 'gain log scrubbing' }
if ((cw gain) -match 'commands:') { Ok 'gain summary' } else { Bad 'gain summary' }
try { (cw gain --json) | ConvertFrom-Json | Out-Null; Ok 'gain --json is valid JSON' } catch { Bad 'gain --json' }
if ((cw gain --csv) -match 'date,commands') { Ok 'gain --csv' } else { Bad 'gain --csv' }
if ((cw gain --daily) -match 'daily') { Ok 'gain --daily' } else { Bad 'gain --daily' }
if ((cw gain --graph)) { Ok 'gain --graph' } else { Bad 'gain --graph' }

Step 'rewrite'
if ((cw rewrite 'git status').Trim() -eq 'ctx-wire run git status') { Ok 'rewrite wraps git status' } else { Bad 'rewrite git status' }

Step 'exclude_commands (config)'
$cfgDir = Join-Path $roaming 'ctx-wire'
New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
"[hooks]`nexclude_commands = [`"curl`"]" | Set-Content -Path (Join-Path $cfgDir 'config.toml') -Encoding UTF8
if ((cw rewrite 'curl https://x.test').Trim() -eq 'curl https://x.test') { Ok 'excluded command not wrapped' } else { Bad 'exclude_commands' }
Remove-Item (Join-Path $cfgDir 'config.toml') -ErrorAction SilentlyContinue

Step 'telemetry'
if ((cw telemetry status) -match 'Aggregate telemetry:') { Ok 'telemetry status' } else { Bad 'telemetry status' }
if ((cw telemetry disable) -match 'command breakdown disabled') { Ok 'telemetry disable' } else { Bad 'telemetry disable' }
if ((cw telemetry enable) -match 'command breakdown enabled') { Ok 'telemetry enable' } else { Bad 'telemetry enable' }

Step 'Windows data path (%LOCALAPPDATA%)'
$state = (cw telemetry status --verbose | Select-String 'State:').ToString()
if ($state -match [regex]::Escape($localApp)) { Ok 'data dir under %LOCALAPPDATA%' } else { Bad "data dir not under LOCALAPPDATA: $state" }

Write-Host "`n$($script:pass) passed, $($script:fail) failed" -ForegroundColor $(if ($script:fail) {'Red'} else {'Green'})
Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
if ($script:fail -gt 0) { exit 1 }
