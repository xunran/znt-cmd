param(
    [Parameter(Mandatory = $true)] [string]$BaseUrl,
    [string]$PackageDir = "",
    [string]$PackageVersionID = "",
    [int]$CanaryPercent = -1,
    [switch]$Stable,
    [switch]$Rollback,
    [string]$Reason = "package command rollback",
    [string]$ReportPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$results = [ordered]@{
    base_url = $BaseUrl
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    actions = @()
}

if ($PackageDir -ne "") {
    $published = Publish-OriginCoordinatorPackage -BaseUrl $BaseUrl -PackageDir $PackageDir
    $PackageVersionID = $published.Release.package_version_id
    $results.package_version_id = $PackageVersionID
    $results.agent_id = $published.AgentID
    $results.version = $published.Version
    $results.actions += [pscustomobject]@{ action = "publish"; status = "passed"; package_version_id = $PackageVersionID }
}

if ($PackageVersionID -eq "" -and ($CanaryPercent -ge 0 -or $Stable -or $Rollback)) {
    throw "PackageVersionID is required for canary/stable/rollback when PackageDir is not provided"
}

if ($CanaryPercent -ge 0) {
    $canary = Invoke-CleanCoreCommand -BaseUrl $BaseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{
        package_version_id = $PackageVersionID
        canary_percent = $CanaryPercent
    }
    $results.actions += [pscustomobject]@{ action = "canary"; status = $canary.status; canary_percent = $CanaryPercent }
}

if ($Stable) {
    $stable = Invoke-CleanCoreCommand -BaseUrl $BaseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{
        package_version_id = $PackageVersionID
    }
    $results.actions += [pscustomobject]@{ action = "stable"; status = $stable.status }
}

if ($Rollback) {
    $rollbackResult = Invoke-CleanCoreCommand -BaseUrl $BaseUrl -Command "agent.package.rollback" -Roles "optimizer" -Payload @{
        package_version_id = $PackageVersionID
        reason = $Reason
    }
    $results.actions += [pscustomobject]@{ action = "rollback"; status = $rollbackResult.status; reason = $Reason }
}

$results.completed_at = (Get-Date).ToUniversalTime().ToString("o")
if ($ReportPath -ne "") {
    Write-E2EJson -Path $ReportPath -Value $results
}

Write-Host "Package command completed. package_version_id=$PackageVersionID"
