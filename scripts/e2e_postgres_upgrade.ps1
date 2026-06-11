param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [string]$MigrationDir = "migrations",
    [string]$OldMigrationVersion = "001",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_DATABASE_URL)) {
    throw "missing CLEAN_CORE_DATABASE_URL. Start an isolated Postgres database and set the URL before running this script."
}
if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "postgres-upgrade"
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
    old_migration_version = $OldMigrationVersion
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

try {
    $resolvedMigrationDir = Resolve-RepoRelativePath $MigrationDir
    $oldMigrationDir = Join-Path $ReportDir "old-migrations"
    New-Item -ItemType Directory -Path $oldMigrationDir -Force | Out-Null
    $selected = @()
    foreach ($file in @(Get-ChildItem -Path $resolvedMigrationDir -Filter "*.sql" | Sort-Object Name)) {
        $separator = $file.Name.IndexOf("_")
        if ($separator -lt 1) {
            throw "migration file $($file.Name) must use version_name.sql format"
        }
        $version = $file.Name.Substring(0, $separator)
        if ([string]::CompareOrdinal($version, $OldMigrationVersion) -le 0) {
            Copy-Item -Path $file.FullName -Destination (Join-Path $oldMigrationDir $file.Name)
            $selected += $file.Name
        }
    }
    if ($selected.Count -eq 0) {
        throw "no old migrations selected up to version $OldMigrationVersion"
    }
    $summary.old_migrations = $selected

    Push-Location $root
    try {
        $go = Get-GoCommand
        $oldUp = & $go run ./cmd/clean-core-server -migration up -migration-dir $oldMigrationDir 2>&1
        if ($LASTEXITCODE -ne 0) {
            throw "old migration up failed: $($oldUp -join "`n")"
        }
        $summary.old_migration_up = ($oldUp -join "`n")
        $oldStatus = & $go run ./cmd/clean-core-server -migration status -migration-dir $oldMigrationDir 2>&1
        if ($LASTEXITCODE -ne 0) {
            throw "old migration status failed: $($oldStatus -join "`n")"
        }
        $summary.old_migration_status = ($oldStatus -join "`n")
        $currentUp = & $go run ./cmd/clean-core-server -migration up -migration-dir $resolvedMigrationDir 2>&1
        if ($LASTEXITCODE -ne 0) {
            throw "current migration up failed: $($currentUp -join "`n")"
        }
        $summary.current_migration_up = ($currentUp -join "`n")
        $currentStatus = & $go run ./cmd/clean-core-server -migration status -migration-dir $resolvedMigrationDir 2>&1
        if ($LASTEXITCODE -ne 0) {
            throw "current migration status failed: $($currentStatus -join "`n")"
        }
        $summary.current_migration_status = ($currentStatus -join "`n")
    } finally {
        Pop-Location
    }
    Assert-True ($summary.current_migration_status -match "applied=2 total=2") "all current migrations should be applied"
    Assert-True ($summary.current_migration_status -match "live_schema=ready") "upgraded Postgres live schema should be ready"

    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv @{
        CLEAN_CORE_DATABASE_URL = $env:CLEAN_CORE_DATABASE_URL
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = $env:CLEAN_CORE_SERVICE_TOKEN
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl
    $readiness = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/readiness/report"
    Assert-Equal $readiness.status "ready" "upgraded readiness status"
    $summary.readiness_status = $readiness.status

    $traceID = "trace_pg_upgrade_" + (Get-Date -Format "yyyyMMddHHmmssfff")
    $run = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -TenantID "tenant_upgrade" -TraceID $traceID -Target @{
        agent_id = "test-agent"
        version = "v1"
    } -Payload @{ input = "postgres upgrade smoke" }
    Assert-Equal $run.status "completed" "upgraded agent.run status"
    $summary.run_id = $run.run_id
    $summary.task_id = $run.task_id
    $summary.trace_id = $traceID
    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "postgres-upgrade-report.json") -Value $summary
    Write-Host "Postgres upgrade E2E passed. report=$ReportDir"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "postgres-upgrade-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
