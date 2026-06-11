param(
    [switch]$StrictTestCoverage
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$root = Get-RepoRoot
$failures = New-Object System.Collections.Generic.List[string]
$warnings = New-Object System.Collections.Generic.List[string]

function Add-Failure {
    param([string]$Message)
    $failures.Add($Message) | Out-Null
}

function Add-Warning {
    param([string]$Message)
    $warnings.Add($Message) | Out-Null
}

function Read-JsonFile {
    param([string]$Path)
    return Get-Content $Path -Raw | ConvertFrom-Json
}

$serverPath = Join-Path $root "internal\server\server.go"
$openAPIPath = Join-Path $root "docs\openapi.clean-core.v1.json"
$configPath = Join-Path $root "config.example.json"
$composePath = Join-Path $root "docker-compose.yml"
$helmValuesPath = Join-Path $root "deploy\helm\clean-core\values.yaml"
$helmConfigMapPath = Join-Path $root "deploy\helm\clean-core\templates\configmap.yaml"
$helmSecretPath = Join-Path $root "deploy\helm\clean-core\templates\secret.yaml"
$serverTestPath = Join-Path $root "internal\server\server_test.go"

$server = Get-Content $serverPath -Raw
$openapi = Read-JsonFile $openAPIPath
$config = Read-JsonFile $configPath
$serverTests = Get-Content $serverTestPath -Raw

$allowedBody = [regex]::Match($server, 'func allowedCommand[\s\S]*?default:').Value
$allowedCommands = [regex]::Matches($allowedBody, '"([a-z0-9_.]+)"') |
    ForEach-Object { $_.Groups[1].Value } |
    Sort-Object -Unique
$openAPICommands = @($openapi.components.schemas.AgentEnvelope.properties.command.enum) | Sort-Object -Unique

$missingFromOpenAPI = @($allowedCommands | Where-Object { $_ -notin $openAPICommands })
$missingFromServer = @($openAPICommands | Where-Object { $_ -notin $allowedCommands })

foreach ($command in $missingFromOpenAPI) {
    Add-Failure "server command '$command' is missing from OpenAPI AgentEnvelope command enum"
}
foreach ($command in $missingFromServer) {
    Add-Failure "OpenAPI command '$command' is missing from server allowedCommand"
}

$dispatchBody = [regex]::Match($server, 'func dispatchCommand[\s\S]*?default:').Value
$dispatchCommands = [regex]::Matches($dispatchBody, '"([a-z0-9_.]+)"') |
    ForEach-Object { $_.Groups[1].Value } |
    Sort-Object -Unique
foreach ($command in $allowedCommands) {
    if ($command -notin $dispatchCommands) {
        Add-Failure "allowed command '$command' is not handled by dispatchCommand"
    }
    if ($serverTests -notmatch [regex]::Escape($command)) {
        Add-Warning "server command '$command' has no direct string evidence in internal/server/server_test.go"
    }
}

$configMap = @{
    service_name = "CLEAN_CORE_SERVICE_NAME"
    version = "CLEAN_CORE_VERSION"
    env = "CLEAN_CORE_ENV"
    http_addr = "CLEAN_CORE_HTTP_ADDR"
    log_level = "CLEAN_CORE_LOG_LEVEL"
    database_url = "CLEAN_CORE_DATABASE_URL"
    readiness = "CLEAN_CORE_READINESS"
    service_token = "CLEAN_CORE_SERVICE_TOKEN"
    model_provider = "CLEAN_CORE_MODEL_PROVIDER"
    model_base_url = "CLEAN_CORE_MODEL_BASE_URL"
    model_api_key = "CLEAN_CORE_MODEL_API_KEY"
    model_name = "CLEAN_CORE_MODEL_NAME"
    model_max_tokens = "CLEAN_CORE_MODEL_MAX_TOKENS"
    model_temperature = "CLEAN_CORE_MODEL_TEMPERATURE"
    model_thinking = "CLEAN_CORE_MODEL_THINKING"
    model_reasoning_effort = "CLEAN_CORE_MODEL_REASONING_EFFORT"
    external_bridge_base_url = "CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL"
    external_bridge_token = "CLEAN_CORE_EXTERNAL_BRIDGE_TOKEN"
    disabled_agent_ids = "CLEAN_CORE_DISABLED_AGENT_IDS"
    disabled_tool_ids = "CLEAN_CORE_DISABLED_TOOL_IDS"
    disable_handoff = "CLEAN_CORE_DISABLE_HANDOFF"
    disable_external_tools_invoke = "CLEAN_CORE_DISABLE_EXTERNAL_TOOLS_INVOKE"
}

$configText = Get-Content $configPath -Raw
$configGo = Get-Content (Join-Path $root "internal\app\config\config.go") -Raw
$composeText = Get-Content $composePath -Raw
$helmValuesText = Get-Content $helmValuesPath -Raw
$helmConfigMapText = Get-Content $helmConfigMapPath -Raw
$helmSecretText = Get-Content $helmSecretPath -Raw

foreach ($entry in $configMap.GetEnumerator()) {
    $jsonKey = $entry.Key
    $envKey = $entry.Value
    if (-not ($config.PSObject.Properties.Name -contains $jsonKey)) {
        Add-Failure "config.example.json is missing '$jsonKey'"
    }
    if ($configGo -notmatch [regex]::Escape($envKey)) {
        Add-Failure "config.go does not read env '$envKey'"
    }
}

$deploymentEnvText = $composeText + "`n" + $helmValuesText + "`n" + $helmConfigMapText + "`n" + $helmSecretText
$deploymentRequiredEnv = @(
    "CLEAN_CORE_ENV",
    "CLEAN_CORE_HTTP_ADDR",
    "CLEAN_CORE_LOG_LEVEL",
    "CLEAN_CORE_SERVICE_TOKEN",
    "CLEAN_CORE_DATABASE_URL",
    "CLEAN_CORE_MODEL_PROVIDER",
    "CLEAN_CORE_MODEL_BASE_URL",
    "CLEAN_CORE_MODEL_API_KEY",
    "CLEAN_CORE_MODEL_NAME",
    "CLEAN_CORE_MODEL_MAX_TOKENS",
    "CLEAN_CORE_MODEL_TEMPERATURE",
    "CLEAN_CORE_MODEL_THINKING",
    "CLEAN_CORE_MODEL_REASONING_EFFORT",
    "CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL",
    "CLEAN_CORE_EXTERNAL_BRIDGE_TOKEN"
)
foreach ($envKey in $deploymentRequiredEnv) {
    if ($deploymentEnvText -notmatch [regex]::Escape($envKey)) {
        Add-Failure "deployment config is missing '$envKey'"
    }
}

$requiredPaths = @(
    "/healthz",
    "/readyz",
    "/v1/commands",
    "/v1/agents",
    "/v1/agents/{agent_id}",
    "/v1/agents/{agent_id}/drafts",
    "/v1/agents/{agent_id}/drafts/{draft_id}",
    "/v1/agents/{agent_id}/drafts/{draft_id}/validate",
    "/v1/agents/{agent_id}/drafts/{draft_id}/review",
    "/v1/agents/{agent_id}/drafts/{draft_id}/publish",
    "/v1/agents/{agent_id}/versions",
    "/v1/agents/{agent_id}/versions/{version}",
    "/v1/agents/{agent_id}/versions/{version}/activate",
    "/v1/agents/{agent_id}/runtime-hooks",
    "/v1/agents/{agent_id}/prompt-profile",
    "/v1/agents/{agent_id}/prompt-profile/governance",
    "/v1/agents/{agent_id}/prompt-profile/versions",
    "/v1/agents/{agent_id}/prompt-profile/activate",
    "/v1/agents/{agent_id}/tool-bindings",
    "/v1/agents/{agent_id}/tool-bindings/governance",
    "/v1/agents/{agent_id}/tool-bindings/versions",
    "/v1/agents/{agent_id}/tool-bindings/activate",
    "/v1/agents/{agent_id}/skills",
    "/v1/agents/{agent_id}/skills/governance",
    "/v1/agents/{agent_id}/skills/{skill_id}",
    "/v1/agents/{agent_id}/skills/{skill_id}/governance",
    "/v1/agents/{agent_id}/skills/{skill_id}/versions",
    "/v1/agents/{agent_id}/skills/{skill_id}/activate",
    "/v1/agents/{agent_id}/collaborators",
    "/v1/agents/{agent_id}/collaborators/governance",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/governance",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/versions",
    "/v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/activate",
    "/v1/agents/{agent_id}/exported-tools",
    "/v1/agents/{agent_id}/exported-tools/governance",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}/governance",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}/versions",
    "/v1/agents/{agent_id}/exported-tools/{tool_id}/activate",
    "/v1/tool-providers",
    "/v1/tool-provider-governance",
    "/v1/tool-providers/{provider_id}/health",
    "/v1/tool-groups",
    "/v1/tool-manifests",
    "/v1/runtime-hook-providers",
    "/v1/runtime-hook-providers/{provider_id}/catalog",
    "/v1/runtime-hook-providers/{provider_id}/catalog/sync",
    "/v1/runtime-hook-providers/{provider_id}/health",
    "/v1/runtime-hook-manifests",
    "/v1/runtime-hook-manifests/{hook_id}",
    "/v1/runtime-hook-manifests/{hook_id}/versions",
    "/v1/runtime-hook-manifests/{hook_id}/versions/{version}",
    "/v1/runtime-hook-manifests/{hook_id}/versions/{version}/activate",
    "/v1/runtime-hook-events",
    "/v1/runtime-hook-governance",
    "/v1/runtime-hook-approvals",
    "/v1/knowledge-bases",
    "/v1/knowledge-bases/{knowledge_base_id}",
    "/v1/knowledge-bases/{knowledge_base_id}/documents",
    "/v1/knowledge-bases/{knowledge_base_id}/index-jobs",
    "/v1/knowledge-bases/{knowledge_base_id}/index-jobs/{job_id}",
    "/v1/knowledge-search",
    "/v1/cross-group-share-policies",
    "/v1/cross-group-share-policies/{policy_id}",
    "/v1/cross-groups/search",
    "/v1/tasks/start",
    "/v1/readiness/report",
    "/v1/release/go-no-go",
    "/v1/usage/evidence",
    "/v1/agent-packages/canary-hits",
    "/v1/evals/results/{eval_run_id}",
    "/v1/traces/{trace_id}",
    "/v1/traces/{trace_id}/replay",
    "/v1/handoffs/{handoff_id}",
    "/v1/handoffs/{handoff_id}/trace"
)
foreach ($path in $requiredPaths) {
    if (-not ($openapi.paths.PSObject.Properties.Name -contains $path)) {
        Add-Failure "OpenAPI is missing path '$path'"
    }
}

if ($StrictTestCoverage) {
    foreach ($warning in $warnings) {
        Add-Failure $warning
    }
    $warnings.Clear()
}

$report = [pscustomobject]@{
    status = if ($failures.Count -eq 0) { "passed" } else { "failed" }
    command_count = $allowedCommands.Count
    warnings = @($warnings)
    failures = @($failures)
}

$reportDir = New-E2EReportDir -Name "contract"
Write-E2EJson -Path (Join-Path $reportDir "contract-report.json") -Value $report

if ($warnings.Count -gt 0) {
    Write-Host "Contract warnings:"
    foreach ($warning in $warnings) {
        Write-Host "WARN: $warning"
    }
}
if ($failures.Count -gt 0) {
    Write-Host "Contract failures:"
    foreach ($failure in $failures) {
        Write-Host "FAIL: $failure"
    }
    Write-Host "Report: $reportDir"
    exit 1
}

Write-Host "Contract verification passed. commands=$($allowedCommands.Count) report=$reportDir"
