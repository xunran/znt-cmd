param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [switch]$KeepDatabases
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "postgres-readiness-negative"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_SERVICE_TOKEN)) {
    $env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
}

$root = Get-RepoRoot
$server = $null
$createdDatabases = New-Object System.Collections.Generic.List[string]
$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    database = "postgres"
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

function Wait-PostgresReady {
    param([int]$TimeoutSeconds = 60)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        & docker compose exec -T postgres pg_isready -U clean_core -d clean_core | Out-Null
        if ($LASTEXITCODE -eq 0) {
            return
        }
        Start-Sleep -Milliseconds 1000
    } while ((Get-Date) -lt $deadline)
    throw "postgres did not become ready within ${TimeoutSeconds}s"
}

function Invoke-ReadinessPsql {
    param(
        [Parameter(Mandatory = $true)] [string]$Database,
        [Parameter(Mandatory = $true)] [string]$Sql
    )
    $output = & docker compose exec -T postgres psql -U clean_core -d $Database -v ON_ERROR_STOP=1 -At -c $Sql 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "psql failed against ${Database}: $($output -join "`n")"
    }
    return ($output -join "`n")
}

function New-ReadinessDatabase {
    param([Parameter(Mandatory = $true)] [string]$Name)
    Invoke-ReadinessPsql -Database "postgres" -Sql "CREATE DATABASE $Name OWNER clean_core;" | Out-Null
    $createdDatabases.Add($Name) | Out-Null
}

function Remove-ReadinessDatabase {
    param([Parameter(Mandatory = $true)] [string]$Name)
    Invoke-ReadinessPsql -Database "postgres" -Sql "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$Name';" | Out-Null
    Invoke-ReadinessPsql -Database "postgres" -Sql "DROP DATABASE IF EXISTS $Name;" | Out-Null
}

function Get-HttpStatus {
    param([Parameter(Mandatory = $true)] [string]$Uri)
    try {
        $response = Invoke-WebRequest -Uri $Uri -Method Get -TimeoutSec 20 -UseBasicParsing
        return [int]$response.StatusCode
    } catch {
        if ($_.Exception.Response) {
            return [int]$_.Exception.Response.StatusCode
        }
        throw
    }
}

function Get-ReadinessCheck {
    param(
        [Parameter(Mandatory = $true)] $Report,
        [Parameter(Mandatory = $true)] [string]$Name
    )
    foreach ($check in @($Report.checks)) {
        if ([string]$check.name -eq $Name) {
            return $check
        }
    }
    throw "readiness check '$Name' not found"
}

function Start-ReadinessNegativeServer {
    param([Parameter(Mandatory = $true)] [string]$DatabaseName)
    $dsn = "postgres://clean_core:clean_core_dev@localhost:5432/$DatabaseName`?sslmode=disable"
    return Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server-$DatabaseName.log") -ExtraEnv @{
        CLEAN_CORE_DATABASE_URL = $dsn
        CLEAN_CORE_READINESS_MODE = "deep"
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = $env:CLEAN_CORE_SERVICE_TOKEN
        CLEAN_CORE_METRICS_AUTH_MODE = "required"
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
}

try {
    Push-Location $root
    try {
        & docker compose up -d postgres | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "docker compose up -d postgres failed"
        }
        Wait-PostgresReady

        $suffix = (Get-Date -Format "yyyyMMddHHmmssfff")
        $missingSchemaDB = "cc_readiness_missing_$suffix"
        $checksumDB = "cc_readiness_checksum_$suffix"
        New-ReadinessDatabase -Name $missingSchemaDB
        New-ReadinessDatabase -Name $checksumDB
        $summary.missing_schema_database = $missingSchemaDB
        $summary.checksum_database = $checksumDB

        $server = Start-ReadinessNegativeServer -DatabaseName $missingSchemaDB
        $missingReadyzStatus = Get-HttpStatus -Uri "$($server.BaseUrl)/readyz"
        $missingReport = Invoke-CleanCoreGet -BaseUrl $server.BaseUrl -Path "/v1/readiness/report"
        $missingCheck = Get-ReadinessCheck -Report $missingReport -Name "migration.live_schema"
        Assert-Equal $missingReadyzStatus 503 "missing schema readyz status"
        Assert-Equal $missingReport.status "not_ready" "missing schema readiness report"
        Assert-True ([string]$missingCheck.details -match "missing_tables=") "missing schema should mention missing tables"
        $summary.missing_schema_readyz_status = $missingReadyzStatus
        $summary.missing_schema_report_status = $missingReport.status
        $summary.missing_schema_live_schema = $missingCheck.details
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
        $server = $null

        $env:CLEAN_CORE_DATABASE_URL = "postgres://clean_core:clean_core_dev@localhost:5432/$checksumDB`?sslmode=disable"
        $go = Get-GoCommand
        $migrationUp = & $go run ./cmd/clean-core-server -migration up -migration-dir migrations 2>&1
        if ($LASTEXITCODE -ne 0) {
            throw "migration up failed: $($migrationUp -join "`n")"
        }
        $summary.checksum_migration_up = ($migrationUp -join "`n")
        Invoke-ReadinessPsql -Database $checksumDB -Sql "UPDATE clean_core_schema_migrations SET checksum='bad-checksum' WHERE version=(SELECT version FROM clean_core_schema_migrations ORDER BY version LIMIT 1);" | Out-Null

        $server = Start-ReadinessNegativeServer -DatabaseName $checksumDB
        $checksumReadyzStatus = Get-HttpStatus -Uri "$($server.BaseUrl)/readyz"
        $checksumReport = Invoke-CleanCoreGet -BaseUrl $server.BaseUrl -Path "/v1/readiness/report"
        $checksumCheck = Get-ReadinessCheck -Report $checksumReport -Name "migration.live_schema"
        Assert-Equal $checksumReadyzStatus 503 "checksum mismatch readyz status"
        Assert-Equal $checksumReport.status "not_ready" "checksum readiness report"
        Assert-True ([string]$checksumCheck.details -match "checksum_mismatches=") "checksum mismatch should be reported"
        $summary.checksum_readyz_status = $checksumReadyzStatus
        $summary.checksum_report_status = $checksumReport.status
        $summary.checksum_live_schema = $checksumCheck.details
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
        $server = $null

        $summary.status = "passed"
        $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
        Write-E2EJson -Path (Join-Path $ReportDir "postgres-readiness-negative-report.json") -Value $summary
        Write-Host "Postgres readiness negative E2E passed. report=$ReportDir"
    } finally {
        Pop-Location
    }
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "postgres-readiness-negative-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if ($null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
    if (-not $KeepDatabases) {
        foreach ($db in @($createdDatabases)) {
            try {
                Remove-ReadinessDatabase -Name $db
            } catch {
            }
        }
    }
}
