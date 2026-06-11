param(
    [Parameter(Mandatory = $true)] [string]$LeftResultPath,
    [Parameter(Mandatory = $true)] [string]$RightResultPath,
    [string]$OutputPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$left = Read-TextFileUtf8 -Path $LeftResultPath | ConvertFrom-Json
$right = Read-TextFileUtf8 -Path $RightResultPath | ConvertFrom-Json
$leftCases = @{}
$rightCases = @{}
foreach ($case in @(Get-ObjectValue $left "results" @())) {
    $leftCases[[string](Get-ObjectValue $case "case_name" "")] = $case
}
foreach ($case in @(Get-ObjectValue $right "results" @())) {
    $rightCases[[string](Get-ObjectValue $case "case_name" "")] = $case
}
$names = @($leftCases.Keys + $rightCases.Keys | Sort-Object -Unique)

$builder = [System.Text.StringBuilder]::new()
[void]$builder.AppendLine("# Eval Compare")
[void]$builder.AppendLine()
[void]$builder.AppendLine("| Metric | Left | Right |")
[void]$builder.AppendLine("|---|---:|---:|")
[void]$builder.AppendLine("| passed | $([bool](Get-ObjectValue $left "passed" $false)) | $([bool](Get-ObjectValue $right "passed" $false)) |")
[void]$builder.AppendLine("| pass_rate | $(Get-ObjectValue $left "pass_rate" 0) | $(Get-ObjectValue $right "pass_rate" 0) |")
[void]$builder.AppendLine("| tool_misuse_rate | $(Get-ObjectValue $left "tool_misuse_rate" 0) | $(Get-ObjectValue $right "tool_misuse_rate" 0) |")
[void]$builder.AppendLine()
[void]$builder.AppendLine("| Case | Left | Right | Change |")
[void]$builder.AppendLine("|---|---|---|---|")
foreach ($name in $names) {
    $l = if ($leftCases.ContainsKey($name)) { [bool](Get-ObjectValue $leftCases[$name] "passed" $false) } else { $null }
    $r = if ($rightCases.ContainsKey($name)) { [bool](Get-ObjectValue $rightCases[$name] "passed" $false) } else { $null }
    $change = "same"
    if ($null -eq $l) { $change = "added" }
    elseif ($null -eq $r) { $change = "removed" }
    elseif ($l -and -not $r) { $change = "regressed" }
    elseif (-not $l -and $r) { $change = "fixed" }
    [void]$builder.AppendLine("| $name | $l | $r | $change |")
}

$content = $builder.ToString()
if ($OutputPath -ne "") {
    if ([System.IO.Path]::IsPathRooted($OutputPath)) {
        $targetPath = $OutputPath
    } else {
        $targetPath = Join-Path (Get-Location) $OutputPath
    }
    [System.IO.File]::WriteAllText($targetPath, $content, [System.Text.UTF8Encoding]::new($false))
    Write-Host "Eval compare written. path=$OutputPath"
} else {
    Write-Output $content
}
