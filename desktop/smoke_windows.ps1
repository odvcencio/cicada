param(
    [Parameter(Mandatory = $true)][string]$Bundle,
    [Parameter(Mandatory = $true)][string]$Output,
    [ValidateRange(30, 600)][int]$TimeoutSeconds = 120
)

# Exercise a prepared Windows bundle in real WebView2 windows. Each case owns
# its score and profile; an installed or running Studio is never reused.
$ErrorActionPreference = 'Stop'
$bundlePath = (Resolve-Path -LiteralPath $Bundle).Path
$program = Join-Path $bundlePath 'cicada-studio.exe'
foreach ($relative in @('cicada-studio.exe', 'cicada.exe', 'cicada-workstation.exe', 'WebView2Loader.dll', 'workstation\build.json')) {
    if (-not (Test-Path -LiteralPath (Join-Path $bundlePath $relative) -PathType Leaf)) {
        throw "Windows smoke bundle is missing $relative"
    }
}
$workflowDir = Join-Path $PSScriptRoot 'testdata'
foreach ($workflow in @('native-startup-workflow.js', 'native-editor-workflow.js')) {
    if (-not (Test-Path -LiteralPath (Join-Path $workflowDir $workflow) -PathType Leaf)) {
        throw "Windows smoke workflow is missing $workflow"
    }
}
New-Item -ItemType Directory -Force -Path $Output | Out-Null
$outputPath = (Resolve-Path -LiteralPath $Output).Path
$runPath = Join-Path $outputPath ('windows-smoke-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $runPath | Out-Null
$fixture = @'
title "Desktop CI"
tempo 96
key c minor
track bass acid { level = -24dB }
pattern pulse { 1 . 5 . }
scene main { bass = pulse }
song { main*4 }
'@
$profilesToClean = [System.Collections.Generic.List[string]]::new()

function Quote-Path([string]$Value) {
    # Windows paths cannot contain a quote; each path here ends in a file or
    # directory name, not a trailing separator.
    return '"' + $Value + '"'
}

function Invoke-Smoke([string]$Name, [string]$Workflow) {
    $casePath = Join-Path $runPath $Name
    New-Item -ItemType Directory -Path $casePath | Out-Null
    $score = Join-Path $casePath 'main.cicada'
    $profilePath = Join-Path $casePath 'data'
    $report = Join-Path $casePath 'startup.json'
    $workflowPath = Join-Path $workflowDir $Workflow
    [System.IO.File]::WriteAllText($score, $fixture, [System.Text.UTF8Encoding]::new($false))
    $arguments = @(
        '--audio', 'null', '--mute', '--data-dir', (Quote-Path $profilePath),
        '--smoke-out', (Quote-Path $report), '--smoke-timeout', ($TimeoutSeconds.ToString() + 's'),
        '--smoke-hold', '2s', '--smoke-script', (Quote-Path $workflowPath), (Quote-Path $score)
    )
    $process = Start-Process -FilePath $program -WorkingDirectory $bundlePath -ArgumentList $arguments -PassThru
    if (-not $process.WaitForExit(($TimeoutSeconds + 15) * 1000)) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        throw "$Name timed out; see $casePath"
    }
    if ($process.ExitCode -ne 0 -or -not (Test-Path -LiteralPath $report -PathType Leaf)) {
        throw "$Name exited with code $($process.ExitCode) without a successful report; see $casePath"
    }
    $result = Get-Content -LiteralPath $report -Raw | ConvertFrom-Json
    if ($null -eq $result.frontend -or $result.frontend.ok -ne $true) {
        throw "$Name frontend workflow failed; see $report"
    }
    $profilesToClean.Add((Join-Path $profilePath 'WebView2'))
    Write-Output ("{0}: passed ({1} checks); report: {2}" -f $Name, @($result.frontend.checks).Count, $report)
}

try {
    Invoke-Smoke 'startup' 'native-startup-workflow.js'
    Invoke-Smoke 'editor' 'native-editor-workflow.js'
} finally {
    # Only this run's successful browser profiles are removed. Keep reports,
    # scores, application logs, and failed cases available for diagnosis.
    foreach ($webviewProfile in $profilesToClean) {
        for ($attempt = 0; $attempt -lt 20 -and (Test-Path -LiteralPath $webviewProfile); $attempt++) {
            try {
                Remove-Item -LiteralPath $webviewProfile -Recurse -Force -ErrorAction Stop
            } catch {
                # WebView2 can briefly retain files after the host window exits.
                Start-Sleep -Milliseconds 500
            }
        }
        if (Test-Path -LiteralPath $webviewProfile) {
            Write-Warning "Could not remove the disposable WebView2 profile: $webviewProfile"
        }
    }
}
Write-Output "Windows WebView2 smoke reports: $runPath"
