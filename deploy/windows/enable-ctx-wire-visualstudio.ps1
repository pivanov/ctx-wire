# Enables the ctx-wire MCP server and both ctx-wire tools in every Visual Studio
# user instance. Intended for managed (GPO user-logon / unlock) deployments.
#
# `ctx-wire init visualstudio` writes the MCP server config to ~/.mcp.json, but
# Visual Studio additionally requires the server and its tools to be enabled in
# each instance's Copilot settings. This script does that across all instances.
#
# CAVEATS (read before deploying):
#   * It sets autoExecutionMode = 'Always' for ctx_wire_run_command, so Visual
#     Studio will run commands through ctx-wire WITHOUT prompting. That is the
#     right trade for a controlled fleet, but it removes the per-call approval
#     gate, so it is a deliberate permission decision, not a default for every
#     solo user.
#   * It writes UNDOCUMENTED `copilot.featureFlags.*` preview settings, which
#     Microsoft may rename or relocate between Visual Studio versions. Re-test
#     after a VS major upgrade.
#   * The server id is `ctx-wire::<path-to-.mcp.json>`. -McpConfigPath must match
#     the path `ctx-wire init visualstudio` wrote to (default: ~/.mcp.json), or
#     the enable silently no-ops.
#   * It round-trips each settings.json through ConvertFrom/ConvertTo-Json, which
#     reformats the file (functionally fine; VS re-reads it as JSON).

[CmdletBinding()]
param(
    [string]$VisualStudioRoot = (Join-Path $env:LOCALAPPDATA 'Microsoft\VisualStudio'),
    [string]$McpConfigPath = (Join-Path $HOME '.mcp.json')
)

$ErrorActionPreference = 'Stop'
$serverId = "ctx-wire::$McpConfigPath"
$toolNames = @('ctx_wire_read_file', 'ctx_wire_run_command')

function Set-Property {
    param(
        [object]$Object,
        [string]$Name,
        [object]$Value
    )

    if ($Object.PSObject.Properties[$Name]) {
        $Object.$Name = $Value
    }
    else {
        $Object | Add-Member -NotePropertyName $Name -NotePropertyValue $Value
    }
}

function Split-SettingList {
    param(
        [object]$Value,
        [char]$Separator
    )

    if ($null -eq $Value -or [string]::IsNullOrWhiteSpace([string]$Value)) {
        return @()
    }

    return @(
        ([string]$Value) -split [regex]::Escape([string]$Separator) |
            ForEach-Object { $_.Trim() } |
            Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    )
}

function Enable-CtxWire {
    param([string]$SettingsPath)

    $content = Get-Content -LiteralPath $SettingsPath -Raw
    $header = ''
    if ($content -match '(?s)^\s*(/\*.*?\*/\s*)') {
        $header = $matches[1]
        $content = $content.Substring($matches[0].Length)
    }

    $settings = $content | ConvertFrom-Json

    $disabled = @(
        Split-SettingList -Value $settings.'copilot.featureFlags.chatUI.disabledMcpServers' -Separator ';' |
            Where-Object { $_ -ine $serverId }
    )
    Set-Property -Object $settings -Name 'copilot.featureFlags.chatUI.disabledMcpServers' -Value ($disabled -join ';')

    $enabledServers = @(Split-SettingList -Value $settings.'copilot.featureFlags.chatUI.enabledMcpServers' -Separator ';')
    if ($enabledServers -inotcontains $serverId) {
        $enabledServers += $serverId
    }
    Set-Property -Object $settings -Name 'copilot.featureFlags.chatUI.enabledMcpServers' -Value ($enabledServers -join ';')

    $enabledTools = @(Split-SettingList -Value $settings.'copilot.featureFlags.chatUI.enabledTools' -Separator ',')
    foreach ($toolName in $toolNames) {
        if ($enabledTools -inotcontains $toolName) {
            $enabledTools += $toolName
        }
    }
    Set-Property -Object $settings -Name 'copilot.featureFlags.chatUI.enabledTools' -Value ($enabledTools -join ',')

    $toolSettings = @($settings.'copilot.general.tools.toolSettings')
    foreach ($toolName in $toolNames) {
        if (-not ($toolSettings | Where-Object { $_.toolName -ieq $toolName })) {
            $toolSettings += [pscustomobject]@{
                toolName = $toolName
                permissionScope = ''
                autoExecutionMode = 'Always'
                resetPerSolution = $false
                directoryPath = ''
                access = 'None'
            }
        }
    }
    Set-Property -Object $settings -Name 'copilot.general.tools.toolSettings' -Value $toolSettings

    $updated = $header + ($settings | ConvertTo-Json -Depth 20)
    $tempPath = "$SettingsPath.ctx-wire.tmp"
    Set-Content -LiteralPath $tempPath -Value $updated -Encoding utf8
    Move-Item -LiteralPath $tempPath -Destination $SettingsPath -Force
}

if (-not (Test-Path -LiteralPath $VisualStudioRoot)) {
    exit 0
}

$instances = @(
    Get-ChildItem -LiteralPath $VisualStudioRoot -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match '^\d+\.\d+_' }
)

foreach ($instance in $instances) {
    $settingsPath = Join-Path $instance.FullName 'settings.json'
    if (Test-Path -LiteralPath $settingsPath) {
        Enable-CtxWire -SettingsPath $settingsPath
        Write-Host "Enabled ctx-wire MCP and tools in $settingsPath"
    }
}
