param(
    [Parameter(Mandatory = $true)] [string]$PackageDir,
    [Parameter(Mandatory = $true)] [string]$EvalFile,
    [int]$Port = 0,
    [string]$ReportDir = "",
    [string]$ModelProvider = "stub",
    [string]$EnvFile = ".\.env",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

Import-E2EEnvFile -Path $EnvFile

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "eval-suite"
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
    package_dir = (Resolve-RepoRelativePath $PackageDir)
    eval_file = (Resolve-RepoRelativePath $EvalFile)
    model_provider = $ModelProvider
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

try {
    $extraEnv = @{
        CLEAN_CORE_MODEL_PROVIDER = $ModelProvider
        CLEAN_CORE_SERVICE_TOKEN = $env:CLEAN_CORE_SERVICE_TOKEN
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
        CLEAN_CORE_CONVERSATION_JUDGE_MODE = if ($env:CLEAN_CORE_CONVERSATION_JUDGE_MODE) { $env:CLEAN_CORE_CONVERSATION_JUDGE_MODE } else { "hybrid" }
        CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS = if ($env:CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS) { $env:CLEAN_CORE_CONVERSATION_JUDGE_TIMEOUT_MS } else { "1500" }
        CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED = if ($env:CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED) { $env:CLEAN_CORE_CONVERSATION_RETRIEVAL_ENABLED } else { "true" }
        CLEAN_CORE_CONVERSATION_MAX_RETRIEVED = if ($env:CLEAN_CORE_CONVERSATION_MAX_RETRIEVED) { $env:CLEAN_CORE_CONVERSATION_MAX_RETRIEVED } else { "8" }
    }
    if ($ModelProvider -eq "stub") {
        foreach ($key in @("CLEAN_CORE_MODEL_BASE_URL", "CLEAN_CORE_MODEL_API_KEY", "CLEAN_CORE_MODEL_NAME", "CLEAN_CORE_MODEL_MAX_TOKENS", "CLEAN_CORE_MODEL_TEMPERATURE", "CLEAN_CORE_MODEL_THINKING", "CLEAN_CORE_MODEL_REASONING_EFFORT")) {
            $extraEnv[$key] = ""
        }
    }
    if ($ModelProvider -eq "openai-compatible") {
        foreach ($required in @("CLEAN_CORE_MODEL_BASE_URL", "CLEAN_CORE_MODEL_API_KEY", "CLEAN_CORE_MODEL_NAME")) {
            if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($required)) -or [Environment]::GetEnvironmentVariable($required) -eq "REPLACE_WITH_YOUR_DEEPSEEK_API_KEY") {
                throw "missing required real-model env $required"
            }
        }
        $extraEnv["CLEAN_CORE_MODEL_BASE_URL"] = $env:CLEAN_CORE_MODEL_BASE_URL
        $extraEnv["CLEAN_CORE_MODEL_API_KEY"] = $env:CLEAN_CORE_MODEL_API_KEY
        $extraEnv["CLEAN_CORE_MODEL_NAME"] = $env:CLEAN_CORE_MODEL_NAME
        $extraEnv["CLEAN_CORE_MODEL_MAX_TOKENS"] = if ($env:CLEAN_CORE_MODEL_MAX_TOKENS) { $env:CLEAN_CORE_MODEL_MAX_TOKENS } else { "2048" }
        $extraEnv["CLEAN_CORE_MODEL_TEMPERATURE"] = if ($env:CLEAN_CORE_MODEL_TEMPERATURE) { $env:CLEAN_CORE_MODEL_TEMPERATURE } else { "0" }
        $extraEnv["CLEAN_CORE_MODEL_THINKING"] = if ($env:CLEAN_CORE_MODEL_THINKING) { $env:CLEAN_CORE_MODEL_THINKING } else { "" }
        $extraEnv["CLEAN_CORE_MODEL_REASONING_EFFORT"] = if ($env:CLEAN_CORE_MODEL_REASONING_EFFORT) { $env:CLEAN_CORE_MODEL_REASONING_EFFORT } else { "" }
        $summary.model_base_url = $env:CLEAN_CORE_MODEL_BASE_URL
        $summary.model_name = $env:CLEAN_CORE_MODEL_NAME
    }

    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv $extraEnv
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl

    $agentID = "origin-coordinator-" + (Get-Date -Format "yyyyMMddHHmmssfff")
    $published = Publish-OriginCoordinatorPackage -BaseUrl $baseUrl -AgentID $agentID -Version "v1" -PackageDir $PackageDir
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

    $evalResultPath = Join-Path $ReportDir "eval-result.json"
    Write-E2EJson -Path $evalResultPath -Value $eval.Result
    & "$PSScriptRoot\eval_diagnostics.ps1" -EvalResultPath $evalResultPath -OutputDir $ReportDir -BaseUrl $baseUrl | Out-Null
    $summary.diagnostics_json = Join-Path $ReportDir "diagnostics.json"
    $summary.diagnostics_md = Join-Path $ReportDir "diagnostics.md"

    Assert-True ([bool]$eval.Result.passed) "eval suite should pass"
    Assert-Equal $eval.Result.tool_misuse_rate 0 "tool misuse rate"

    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "eval-suite-report.json") -Value $summary
    Write-Host "Eval suite passed. report=$ReportDir suite=$($summary.eval_suite_id) eval=$($summary.eval_run_id)"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "eval-suite-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
