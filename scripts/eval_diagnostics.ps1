param(
    [Parameter(Mandatory = $true)] [string]$EvalResultPath,
    [string]$OutputDir = "",
    [string]$BaseUrl = "",
    [switch]$PassThru
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$result = Read-TextFileUtf8 -Path $EvalResultPath | ConvertFrom-Json
$diagnostics = Convert-EvalResultToDiagnostics -EvalResult $result
if ($BaseUrl -ne "") {
    $diagnostics = Add-TraceFactsToDiagnostics -Diagnostics $diagnostics -BaseUrl $BaseUrl
}

if ($OutputDir -eq "") {
    $OutputDir = Split-Path -Parent (Resolve-Path $EvalResultPath)
}
New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null

$jsonPath = Join-Path $OutputDir "diagnostics.json"
$mdPath = Join-Path $OutputDir "diagnostics.md"
Write-E2EJson -Path $jsonPath -Value $diagnostics

$builder = [System.Text.StringBuilder]::new()
[void]$builder.AppendLine("# Eval Diagnostics")
[void]$builder.AppendLine()
[void]$builder.AppendLine("- eval_run_id: $($diagnostics.eval_run_id)")
[void]$builder.AppendLine("- suite_id: $($diagnostics.suite_id)")
[void]$builder.AppendLine("- passed: $($diagnostics.passed)")
[void]$builder.AppendLine("- pass_rate: $($diagnostics.pass_rate)")
[void]$builder.AppendLine("- tool_misuse_rate: $($diagnostics.tool_misuse_rate)")
[void]$builder.AppendLine("- failure_count: $($diagnostics.failure_count)")
[void]$builder.AppendLine()

if ($diagnostics.failure_count -eq 0) {
    [void]$builder.AppendLine("No failed cases.")
} else {
    foreach ($failure in @($diagnostics.failures)) {
        [void]$builder.AppendLine("## $($failure.case_name)")
        [void]$builder.AppendLine()
        [void]$builder.AppendLine("- category: $($failure.category)")
        [void]$builder.AppendLine("- trace_id: $($failure.trace_id)")
        [void]$builder.AppendLine("- run_id: $($failure.run_id)")
        if ($failure.PSObject.Properties.Name -contains "prompt_bundle_hashes") {
            [void]$builder.AppendLine("- prompt_bundle_hashes: $(@($failure.prompt_bundle_hashes) -join ', ')")
        }
        if ($failure.PSObject.Properties.Name -contains "model_provider") {
            [void]$builder.AppendLine("- model: $($failure.model_provider) / $($failure.model_name)")
        }
        if ($failure.PSObject.Properties.Name -contains "end_status") {
            [void]$builder.AppendLine("- end_status: $($failure.end_status)")
        }
        if ($failure.PSObject.Properties.Name -contains "prompt_tokens") {
            [void]$builder.AppendLine("- tokens: prompt=$($failure.prompt_tokens) completion=$($failure.completion_tokens)")
        }
        [void]$builder.AppendLine("- tool_calls_total: $($failure.tool_calls_total)")
        [void]$builder.AppendLine("- tool_misuse_total: $($failure.tool_misuse_total)")
        [void]$builder.AppendLine("- fix_hint: $($failure.fix_hint)")
        [void]$builder.AppendLine()
        [void]$builder.AppendLine("Failures:")
        foreach ($message in @($failure.failures)) {
            [void]$builder.AppendLine("- $message")
        }
        [void]$builder.AppendLine()
        [void]$builder.AppendLine("Final reply:")
        [void]$builder.AppendLine()
        [void]$builder.AppendLine('```text')
        [void]$builder.AppendLine([string]$failure.final_reply)
        [void]$builder.AppendLine('```')
        [void]$builder.AppendLine()
    }
}

[System.IO.File]::WriteAllText($mdPath, $builder.ToString(), [System.Text.UTF8Encoding]::new($false))

if ($PassThru) {
    $diagnostics | ConvertTo-Json -Depth 40
} else {
    Write-Host "Eval diagnostics written. dir=$OutputDir failures=$($diagnostics.failure_count)"
}
