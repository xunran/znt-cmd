param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [string]$PackageDir = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "api-smoke"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

$env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"

$server = $null
$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    model_provider = "stub"
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

try {
    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv @{
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = "dev-token"
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl

    $health = Invoke-RestMethod -Uri "$baseUrl/healthz" -Method Get -TimeoutSec 10
    Assert-Equal $health.status "ok" "healthz status"
    $ready = Invoke-RestMethod -Uri "$baseUrl/readyz" -Method Get -TimeoutSec 10
    Assert-Equal $ready.status "ready" "readyz status"
    $readiness = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/readiness/report"
    Assert-True ($null -ne $readiness.status) "readiness report should include status"

    $agentID = "origin-coordinator-" + (Get-Date -Format "yyyyMMddHHmmss")
    if ($PackageDir -ne "") {
        $summary.package_dir = (Resolve-RepoRelativePath $PackageDir)
        $preview = Invoke-ServerPromptPreview -BaseUrl $baseUrl -PackageDir $PackageDir -Input (Get-SmokeText "capability_question")
        $summary.server_prompt_preview_hash = $preview.prompt_bundle_hash
        Write-E2EJson -Path (Join-Path $ReportDir "server-prompt-preview.json") -Value $preview
        Assert-True ($preview.prompt_bundle_hash -ne "") "server prompt preview hash should not be empty"
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
    Assert-True ($release.package_version_id -ne "") "published package_version_id should not be empty"
    $summary.package_version_id = $release.package_version_id
    $summary.agent_id = $published.AgentID
    $summary.agent_version = $published.Version

    $blockedBeforeCanary = $false
    try {
        Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -Target @{
            agent_id = $published.AgentID
            version = $published.Version
        } -Payload @{ input = "hello before canary" } | Out-Null
    } catch {
        $blockedBeforeCanary = $true
    }
    Assert-True $blockedBeforeCanary "published package should be blocked before canary"

    $canary = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{
        package_version_id = $release.package_version_id
        canary_percent = 25
    }
    Assert-Equal $canary.status "canary" "canary status"

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
    $summary.eval_run_id = $eval.eval_run_id
    $summary.eval_passed = $eval.passed
    $summary.eval_tool_misuse_total = $eval.tool_misuse_total
    Assert-True ([bool]$eval.passed) "stub eval should pass"
    Assert-Equal $eval.tool_misuse_total 0 "tool misuse total"

    $stable = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{
        package_version_id = $release.package_version_id
    }
    Assert-Equal $stable.status "stable" "stable status"

    $traceID = "trace_api_smoke_" + (Get-Date -Format "yyyyMMddHHmmssfff")
    $run = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -TraceID $traceID -Target @{
        agent_id = $published.AgentID
    } -Payload @{ input = Get-SmokeText "capability_question" }
    Assert-Equal $run.status "completed" "agent.run status"
    Assert-True ($run.run_id -ne "") "run_id should not be empty"
    $summary.run_id = $run.run_id
    $summary.task_id = $run.task_id
    $summary.trace_id = $traceID

    $trace = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/traces/$traceID"
    $eventTypes = @($trace.events | ForEach-Object { $_.type })
    foreach ($required in @("input.received", "agent.loaded", "promptbundle.built", "model.called", "decision.validated", "response.sent")) {
        Assert-True ($eventTypes -contains $required) "trace should contain $required"
    }

    $goNoGo = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/release/go-no-go"
    Assert-True ($goNoGo.decision -ne "") "go/no-go should include decision"
    $summary.go_no_go = $goNoGo.decision

    $rollback = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.rollback" -Roles "optimizer" -Payload @{
        package_version_id = $release.package_version_id
        reason = "api smoke rollback"
    }
    Assert-Equal $rollback.status "rolled_back" "rollback status"

    $blockedAfterRollback = $false
    try {
        Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -Target @{
            agent_id = $published.AgentID
            version = $published.Version
        } -Payload @{ input = "hello after rollback" } | Out-Null
    } catch {
        $blockedAfterRollback = $true
    }
    Assert-True $blockedAfterRollback "rolled back version should be blocked"

    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "api-smoke-report.json") -Value $summary
    Write-Host "API smoke passed. report=$ReportDir package=$($summary.package_version_id) eval=$($summary.eval_run_id)"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "api-smoke-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
