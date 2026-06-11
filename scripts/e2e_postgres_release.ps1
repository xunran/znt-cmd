param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [string]$PackageDir = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_DATABASE_URL)) {
    throw "missing CLEAN_CORE_DATABASE_URL. Start Postgres and set the database URL before running this script."
}

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "postgres-release"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_SERVICE_TOKEN)) {
    $env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
}

$root = Get-RepoRoot
$server = $null
$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    database = "postgres"
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

try {
    Push-Location $root
    try {
        $go = Get-GoCommand
        $migrationStatusBefore = & $go run ./cmd/clean-core-server -migration status -migration-dir migrations 2>&1
        $summary.migration_status_before = ($migrationStatusBefore -join "`n")
        $migrationUp = & $go run ./cmd/clean-core-server -migration up -migration-dir migrations 2>&1
        $summary.migration_up = ($migrationUp -join "`n")
        $migrationStatusAfter = & $go run ./cmd/clean-core-server -migration status -migration-dir migrations 2>&1
        $summary.migration_status_after = ($migrationStatusAfter -join "`n")
    } finally {
        Pop-Location
    }
    Assert-True ($summary.migration_status_after -match "live_schema=ready") "Postgres live schema should be ready"

    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv @{
        CLEAN_CORE_DATABASE_URL = $env:CLEAN_CORE_DATABASE_URL
        CLEAN_CORE_READINESS_MODE = "deep"
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = if ($env:CLEAN_CORE_SERVICE_TOKEN) { $env:CLEAN_CORE_SERVICE_TOKEN } else { "dev-token" }
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl

    $readyz = Invoke-RestMethod -Uri "$baseUrl/readyz" -Method Get -TimeoutSec 10
    Assert-Equal $readyz.status "ready" "deep readyz status"
    $summary.readyz_status = $readyz.status
    $readiness = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/readiness/report"
    Assert-True ($readiness.status -eq "ready" -or $readiness.status -eq "degraded") "readiness should be ready or explainable degraded"
    $summary.readiness_status = $readiness.status

    $agentID = "origin-coordinator-" + (Get-Date -Format "yyyyMMddHHmmss")
    if ($PackageDir -ne "") {
        $summary.package_dir = (Resolve-RepoRelativePath $PackageDir)
        $baseline = Publish-OriginCoordinatorPackage -BaseUrl $baseUrl -AgentID $agentID -Version "v1" -PackageDir $PackageDir
    } else {
        $baseline = Publish-OriginCoordinatorPackage -BaseUrl $baseUrl -AgentID $agentID -Version "v1"
    }
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{
        package_version_id = $baseline.Release.package_version_id
        canary_percent = 25
    } | Out-Null
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "eval.run" -Roles "optimizer" -Target @{
        agent_id = $baseline.AgentID
        version = $baseline.Version
    } -Payload @{
        package_version_id = $baseline.Release.package_version_id
        input = "hello"
        final_reply_contains = @("ok")
        should_end_status = "completed"
        max_tool_calls = 0
    } | Out-Null
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{
        package_version_id = $baseline.Release.package_version_id
    } | Out-Null

    if ($PackageDir -ne "") {
        $published = Publish-OriginCoordinatorPackage -BaseUrl $baseUrl -AgentID $agentID -Version "v2" -PackageDir $PackageDir
    } else {
        $published = Publish-OriginCoordinatorPackage -BaseUrl $baseUrl -AgentID $agentID -Version "v2"
    }
    $release = $published.Release
    $summary.package_version_id = $release.package_version_id
    $summary.agent_id = $published.AgentID
    $summary.agent_version = $published.Version

    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{
        package_version_id = $release.package_version_id
        canary_percent = 25
    } | Out-Null
    $eval = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "eval.run" -Roles "optimizer" -Target @{
        agent_id = $published.AgentID
        version = $published.Version
    } -Payload @{
        package_version_id = $release.package_version_id
        input = "hello"
        final_reply_contains = @("ok")
        should_end_status = "completed"
        max_tool_calls = 0
    }
    Assert-True ([bool]$eval.passed) "Postgres-backed eval should pass"
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{
        package_version_id = $release.package_version_id
    } | Out-Null

    $traceID = "trace_pg_release_" + (Get-Date -Format "yyyyMMddHHmmssfff")
    $run = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -TraceID $traceID -Target @{
        agent_id = $published.AgentID
    } -Payload @{ input = Get-SmokeText "capability_question" }
    Assert-Equal $run.status "completed" "Postgres-backed agent.run status"
    $summary.eval_run_id = $eval.eval_run_id
    $summary.run_id = $run.run_id
    $summary.task_id = $run.task_id
    $summary.trace_id = $traceID

    Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    $server = $null

    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server-restart.log") -ExtraEnv @{
        CLEAN_CORE_DATABASE_URL = $env:CLEAN_CORE_DATABASE_URL
        CLEAN_CORE_READINESS_MODE = "deep"
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = if ($env:CLEAN_CORE_SERVICE_TOKEN) { $env:CLEAN_CORE_SERVICE_TOKEN } else { "dev-token" }
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
    $baseUrl = $server.BaseUrl

    $readyzAfterRestart = Invoke-RestMethod -Uri "$baseUrl/readyz" -Method Get -TimeoutSec 10
    Assert-Equal $readyzAfterRestart.status "ready" "deep readyz status after restart"
    $summary.readyz_after_restart_status = $readyzAfterRestart.status
    $persistedTrace = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/traces/$traceID"
    Assert-True (@($persistedTrace.events).Count -gt 0) "trace should persist after restart"
    $persistedEval = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/evals/results/$($eval.eval_run_id)"
    Assert-Equal $persistedEval.eval_run_id $eval.eval_run_id "eval result should persist after restart"
    $goNoGo = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/release/go-no-go"
    $summary.go_no_go = $goNoGo.decision

    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "postgres-release-report.json") -Value $summary
    Write-Host "Postgres release E2E passed. report=$ReportDir package=$($summary.package_version_id) eval=$($summary.eval_run_id)"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "postgres-release-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
