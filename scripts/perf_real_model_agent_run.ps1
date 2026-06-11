param(
    [string]$EnvFile = ".\.env",
    [string]$ReportDir = "",
    [int]$Port = 0,
    [string]$BaseUrl = "",
    [int]$ProcessId = 0,
    [string]$AgentID = "test-agent",
    [string]$AgentVersion = "v1",
    [string]$TenantID = "tenant_real_model_perf",
    [string]$InputPrefix = "Reply with one short acknowledgement only. Do not call tools. Request {index}.",
    [int]$TotalRequests = 20,
    [int]$Concurrency = 2,
    [ValidateSet("sync", "async")] [string]$ExecutionMode = "sync",
    [int]$TimeoutSec = 600,
    [int]$SampleIntervalMs = 1000,
    [int]$RecoveryProbeRequests = 1,
    [int]$UsageEvidenceSampleLimit = 20,
    [string]$Persistence = "",
    [int]$RunMaxConcurrent = 0,
    [int]$TenantRunMaxConcurrent = 0,
    [int]$AgentRunMaxConcurrent = 0,
    [switch]$RunMigrations,
    [switch]$KeepServer,
    [switch]$ValidateOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ValidateOnly) {
    & "$PSScriptRoot\perf_single_node_agent_run.ps1" -ValidateOnly
    Write-Host "perf_real_model_agent_run validation passed"
    exit 0
}

Import-E2EEnvFile -Path $EnvFile

foreach ($required in @("CLEAN_CORE_MODEL_BASE_URL", "CLEAN_CORE_MODEL_API_KEY", "CLEAN_CORE_MODEL_NAME")) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($required))) {
        throw "missing required env $required. Set it in $EnvFile or CI secrets before running real model perf."
    }
}
if ($env:CLEAN_CORE_MODEL_API_KEY -eq "REPLACE_WITH_YOUR_DEEPSEEK_API_KEY") {
    throw "CLEAN_CORE_MODEL_API_KEY still contains the local template placeholder."
}

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "real-model-perf"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

$runnerArgs = @{
    ReportDir = $ReportDir
    AgentID = $AgentID
    AgentVersion = $AgentVersion
    TenantID = $TenantID
    InputPrefix = $InputPrefix
    TotalRequests = $TotalRequests
    Concurrency = $Concurrency
    ModelProvider = "openai-compatible"
    ExecutionMode = $ExecutionMode
    TimeoutSec = $TimeoutSec
    SampleIntervalMs = $SampleIntervalMs
    RecoveryProbeRequests = $RecoveryProbeRequests
    UsageEvidenceSampleLimit = $UsageEvidenceSampleLimit
    CollectUsageEvidence = $true
}
if ($Port -gt 0) {
    $runnerArgs.Port = $Port
}
if (-not [string]::IsNullOrWhiteSpace($BaseUrl)) {
    $runnerArgs.BaseUrl = $BaseUrl
}
if ($ProcessId -gt 0) {
    $runnerArgs.ProcessId = $ProcessId
}
if (-not [string]::IsNullOrWhiteSpace($Persistence)) {
    $runnerArgs.Persistence = $Persistence
}
if ($RunMaxConcurrent -gt 0) {
    $runnerArgs.RunMaxConcurrent = $RunMaxConcurrent
}
if ($TenantRunMaxConcurrent -gt 0) {
    $runnerArgs.TenantRunMaxConcurrent = $TenantRunMaxConcurrent
}
if ($AgentRunMaxConcurrent -gt 0) {
    $runnerArgs.AgentRunMaxConcurrent = $AgentRunMaxConcurrent
}
if ($RunMigrations) {
    $runnerArgs.RunMigrations = $true
}
if ($KeepServer) {
    $runnerArgs.KeepServer = $true
}

& "$PSScriptRoot\perf_single_node_agent_run.ps1" @runnerArgs
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
Write-Host "real model agent.run perf completed. report=$ReportDir"
