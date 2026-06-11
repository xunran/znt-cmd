param(
    [Parameter(Mandatory = $true)] [string]$PackageDir,
    [string]$ReportPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$package = Read-AgentPackage -PackageDir $PackageDir
$lint = Invoke-OriginPromptLint -PackageDir $PackageDir
$evalFiles = @()
$evalDir = Join-Path (Resolve-RepoRelativePath $PackageDir) "eval"
if (Test-Path $evalDir) {
    foreach ($file in @(Get-ChildItem -Path $evalDir -File | Where-Object { $_.Extension -in @(".yaml", ".yml", ".json") } | Sort-Object Name)) {
        try {
            $suite = Read-OriginEvalSuiteFile -Path $file.FullName
            $evalFiles += [pscustomobject]@{
                name = [System.IO.Path]::GetFileNameWithoutExtension($file.Name)
                path = $file.FullName
                suite_name = Get-ObjectValue $suite "name" ""
                case_count = @(Get-ObjectValue $suite "cases" @()).Count
                parsed = $true
            }
        } catch {
            $evalFiles += [pscustomobject]@{
                name = [System.IO.Path]::GetFileNameWithoutExtension($file.Name)
                path = $file.FullName
                suite_name = ""
                case_count = 0
                parsed = $false
                error = $_.Exception.Message
            }
        }
    }
}

$errors = @($lint.errors)
if ($package.AgentID -eq "") {
    $errors += "agent_id is required"
}
if ($package.Version -eq "") {
    $errors += "version is required"
}
if ($package.Prompt.Trim() -eq "") {
    $errors += "prompt.md must not be empty"
}
if ($package.System.Trim() -eq "") {
    $errors += "system.md must not be empty"
}
if ($package.Developer.Trim() -eq "") {
    $errors += "developer.md must not be empty"
}
if ($evalFiles.Count -eq 0) {
    $errors += "at least one eval suite file is required under eval/"
}
foreach ($evalFile in @($evalFiles)) {
    if (-not [bool](Get-ObjectValue $evalFile "parsed" $false)) {
        $errors += "eval suite parse failed: $($evalFile.path): $($evalFile.error)"
    }
    if ([int](Get-ObjectValue $evalFile "case_count" 0) -le 0) {
        $errors += "eval suite must contain at least one case: $($evalFile.path)"
    }
}

$report = [pscustomobject]@{
    passed = ($errors.Count -eq 0)
    package_dir = $package.PackageDir
    agent_id = $package.AgentID
    version = $package.Version
    eval_files = $evalFiles
    lint = $lint
    errors = $errors
    generated_at = (Get-Date).ToUniversalTime().ToString("o")
}

if ($ReportPath -ne "") {
    Write-E2EJson -Path $ReportPath -Value $report
}

if (-not $report.passed) {
    $errors | ForEach-Object { Write-Error $_ }
    exit 1
}

Write-Host "Package validation passed. package=$($package.AgentID)@$($package.Version)"
