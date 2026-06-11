param(
    [int]$Port = 0,
    [string]$EnvFile = ".\.env",
    [string]$ReportDir = "",
    [string]$PackageDir = "",
    [string]$EvalFile = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

Import-E2EEnvFile -Path $EnvFile

foreach ($required in @("CLEAN_CORE_MODEL_BASE_URL", "CLEAN_CORE_MODEL_API_KEY", "CLEAN_CORE_MODEL_NAME")) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($required))) {
        throw "missing required env $required. Set it in .env or provide CI secrets first."
    }
}
if ($env:CLEAN_CORE_MODEL_API_KEY -eq "REPLACE_WITH_YOUR_DEEPSEEK_API_KEY") {
    throw "CLEAN_CORE_MODEL_API_KEY still contains the local template placeholder."
}

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "deepseek-smoke"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_SERVICE_TOKEN)) {
    $env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
}

$server = $null
$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    model_provider = "openai-compatible"
    model_base_url = $env:CLEAN_CORE_MODEL_BASE_URL
    model_name = $env:CLEAN_CORE_MODEL_NAME
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

try {
    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv @{
        CLEAN_CORE_MODEL_PROVIDER = "openai-compatible"
        CLEAN_CORE_MODEL_BASE_URL = $env:CLEAN_CORE_MODEL_BASE_URL
        CLEAN_CORE_MODEL_API_KEY = $env:CLEAN_CORE_MODEL_API_KEY
        CLEAN_CORE_MODEL_NAME = $env:CLEAN_CORE_MODEL_NAME
        CLEAN_CORE_MODEL_MAX_TOKENS = if ($env:CLEAN_CORE_MODEL_MAX_TOKENS) { $env:CLEAN_CORE_MODEL_MAX_TOKENS } else { "2048" }
        CLEAN_CORE_MODEL_TEMPERATURE = if ($env:CLEAN_CORE_MODEL_TEMPERATURE) { $env:CLEAN_CORE_MODEL_TEMPERATURE } else { "0" }
        CLEAN_CORE_MODEL_THINKING = if ($env:CLEAN_CORE_MODEL_THINKING) { $env:CLEAN_CORE_MODEL_THINKING } else { "" }
        CLEAN_CORE_MODEL_REASONING_EFFORT = if ($env:CLEAN_CORE_MODEL_REASONING_EFFORT) { $env:CLEAN_CORE_MODEL_REASONING_EFFORT } else { "" }
        CLEAN_CORE_CONVERSATION_JUDGE_MODE = if ($env:CLEAN_CORE_CONVERSATION_JUDGE_MODE) { $env:CLEAN_CORE_CONVERSATION_JUDGE_MODE } else { "hybrid" }
        CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS = if ($env:CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS) { $env:CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS } else { "1500" }
        CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED = if ($env:CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED) { $env:CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED } else { "true" }
        CLEAN_CORE_CONVERSATION_MAX_RETRIEVED = if ($env:CLEAN_CORE_CONVERSATION_MAX_RETRIEVED) { $env:CLEAN_CORE_CONVERSATION_MAX_RETRIEVED } else { "8" }
        CLEAN_CORE_SERVICE_TOKEN = if ($env:CLEAN_CORE_SERVICE_TOKEN) { $env:CLEAN_CORE_SERVICE_TOKEN } else { "dev-token" }
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl

    if ($PackageDir -ne "") {
        $agentID = "origin-coordinator-" + (Get-Date -Format "yyyyMMddHHmmssfff")
        $published = Publish-OriginCoordinatorPackage -BaseUrl $baseUrl -AgentID $agentID -Version "v1" -PackageDir $PackageDir
        if ($EvalFile -eq "") {
            $EvalFile = Find-AgentPackageEvalFile -PackageDir $PackageDir -Name "smoke"
        }
        $summary.package_dir = (Resolve-RepoRelativePath $PackageDir)
        $summary.eval_file = $EvalFile
    } else {
        $agentID = "origin-coordinator-" + (Get-Date -Format "yyyyMMddHHmmss")
        $published = Publish-OriginCoordinatorPackage -BaseUrl $baseUrl -AgentID $agentID -Version "v1"
    }
    $release = $published.Release
    $summary.package_version_id = $release.package_version_id
    $summary.agent_id = $published.AgentID
    $summary.agent_version = $published.Version

    $canary = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{
        package_version_id = $release.package_version_id
        canary_percent = 25
    }
    Assert-Equal $canary.status "canary" "canary status"

    $eval = Invoke-OriginCoordinatorSmokeEval -BaseUrl $baseUrl -PackageVersionID $release.package_version_id -AgentID $published.AgentID -Version $published.Version -EvalFile $EvalFile
    $summary.eval_suite_id = $eval.Suite.suite_id
    $summary.eval_run_id = $eval.Result.eval_run_id
    $summary.eval_passed = $eval.Result.passed
    $summary.eval_pass_rate = $eval.Result.pass_rate
    $summary.eval_tool_misuse_rate = $eval.Result.tool_misuse_rate
    if ($eval.Result.PSObject.Properties.Name -contains "failures") {
        $summary.eval_failures = $eval.Result.failures
    } else {
        $summary.eval_failures = @()
    }
    $evalResultPath = Join-Path $ReportDir "deepseek-eval-result.json"
    Write-E2EJson -Path $evalResultPath -Value $eval.Result
    & "$PSScriptRoot\eval_diagnostics.ps1" -EvalResultPath $evalResultPath -OutputDir $ReportDir -BaseUrl $baseUrl | Out-Null
    $summary.diagnostics_json = Join-Path $ReportDir "diagnostics.json"
    $summary.diagnostics_md = Join-Path $ReportDir "diagnostics.md"
    Assert-True ([bool]$eval.Result.passed) "real model eval suite should pass"
    Assert-Equal $eval.Result.tool_misuse_rate 0 "tool misuse rate"

    $stable = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{
        package_version_id = $release.package_version_id
    }
    Assert-Equal $stable.status "stable" "stable status"

    $traceID = "trace_deepseek_smoke_" + (Get-Date -Format "yyyyMMddHHmmssfff")
    $run = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -TraceID $traceID -Target @{
        agent_id = $published.AgentID
    } -Payload @{ input = Get-SmokeText "capability_question" }
    Assert-Equal $run.status "completed" "agent.run status"
    $summary.run_id = $run.run_id
    $summary.task_id = $run.task_id
    $summary.trace_id = $traceID
    if (($run.PSObject.Properties.Name -contains "reply") -and $null -ne $run.reply -and ($run.reply.PSObject.Properties.Name -contains "text")) {
        $summary.final_reply = $run.reply.text
    } else {
        $summary.final_reply = ""
    }

    $goNoGo = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/release/go-no-go"
    $summary.go_no_go = $goNoGo.decision

    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "deepseek-smoke-report.json") -Value $summary
    Write-Host "DeepSeek smoke passed. report=$ReportDir package=$($summary.package_version_id) eval=$($summary.eval_run_id)"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "deepseek-smoke-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
