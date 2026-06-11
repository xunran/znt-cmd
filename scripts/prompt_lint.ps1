param(
    [Parameter(Mandatory = $true)] [string]$PackageDir,
    [string]$ReportPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$report = Invoke-OriginPromptLint -PackageDir $PackageDir
if ($ReportPath -ne "") {
    Write-E2EJson -Path $ReportPath -Value $report
}

foreach ($warning in @($report.warnings)) {
    Write-Warning $warning
}
if (-not $report.passed) {
    foreach ($errorText in @($report.errors)) {
        Write-Error $errorText
    }
    exit 1
}

Write-Host "Prompt lint passed. package=$($report.agent_id)@$($report.version) warnings=$(@($report.warnings).Count)"
