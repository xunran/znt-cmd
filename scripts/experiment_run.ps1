param(
    [Parameter(Mandatory = $true)] [string]$ExperimentPath,
    [string]$ReportDir = "",
    [switch]$RunRealModel,
    [string]$EnvFile = ".\.env"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

Import-E2EEnvFile -Path $EnvFile

$experiment = Read-TextFileUtf8 -Path $ExperimentPath | ConvertFrom-Json
if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "experiment"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

$basePackageDir = Resolve-RepoRelativePath ([string]$experiment.base_package)
$summary = [ordered]@{
    name = [string]$experiment.name
    base_package = $basePackageDir
    report_dir = $ReportDir
    run_real_model = [bool]$RunRealModel
    variants = @()
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

foreach ($variant in @($experiment.variants)) {
    $variantName = [string](Get-ObjectValue $variant "name" "variant")
    $variantDir = Join-Path $ReportDir ("package-" + $variantName)
    Copy-Item -Path $basePackageDir -Destination $variantDir -Recurse -Force
    $developerAppend = [string](Get-ObjectValue $variant "developer_append" "")
    if ($developerAppend -ne "") {
        $developerPath = Join-Path $variantDir "developer.md"
        $current = Read-TextFileUtf8 -Path $developerPath
        [System.IO.File]::WriteAllText($developerPath, ($current.TrimEnd() + "`n`n" + $developerAppend + "`n"), [System.Text.UTF8Encoding]::new($false))
    }
    $previewPath = Join-Path $ReportDir ("preview-" + $variantName + ".md")
    $lintPath = Join-Path $ReportDir ("lint-" + $variantName + ".json")
    & "$PSScriptRoot\prompt_preview.ps1" -PackageDir $variantDir -Input "你能做什么？" -OutputPath $previewPath | Out-Null
    & "$PSScriptRoot\prompt_lint.ps1" -PackageDir $variantDir -ReportPath $lintPath | Out-Null
    $record = [ordered]@{
        name = $variantName
        package_dir = $variantDir
        preview = $previewPath
        lint = $lintPath
    }
    if ($RunRealModel) {
        $variantReportDir = Join-Path $ReportDir ("real-model-" + $variantName)
        if ($EnvFile -ne "") {
            & "$PSScriptRoot\e2e_deepseek_smoke.ps1" -PackageDir $variantDir -EnvFile $EnvFile -ReportDir $variantReportDir
        } else {
            & "$PSScriptRoot\e2e_deepseek_smoke.ps1" -PackageDir $variantDir -ReportDir $variantReportDir
        }
        $record.real_model_report = $variantReportDir
    }
    $summary.variants += [pscustomobject]$record
}

$summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
Write-E2EJson -Path (Join-Path $ReportDir "experiment-report.json") -Value $summary
Write-Host "Experiment completed. report=$ReportDir variants=$(@($summary.variants).Count)"
