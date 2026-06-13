param(
    [int]$Port = 0,
    [string]$EnvFile = ".\.env",
    [string]$ReportDir = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

Import-E2EEnvFile -Path $EnvFile

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "context-strategy"
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
    model_provider = "stub"
    started_at = (Get-Date).ToUniversalTime().ToString("o")
}

try {
    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv @{
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = $env:CLEAN_CORE_SERVICE_TOKEN
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl

    $suffix = Get-Date -Format "yyyyMMddHHmmssfff"
    $agentID = "context-strategy-e2e-$suffix"
    $version = "v1"
    $longInput = ("context strategy compression evidence " * 240)

    $draft = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.draft.create" -Roles "optimizer" -Payload @{
        agent_id = $agentID
        version = $version
        prompt = "You are a context strategy e2e agent. Always return the platform Decision JSON."
        agents_md = "# Context Strategy E2E`nValidate context strategy preview diagnostics."
        strategies = @{
            context = @{
                mode = "balanced"
                recent_message_limit = 1
                retrieval_max_results = 1
                task_history_max_items = 0
                context_token_budget = 80
                enabled_sources = @("conversation_recent", "conversation_retrieval", "task_history", "memory_summary", "artifact_refs", "tool_results", "runtime_hook_context", "agent_plugin_context")
                compression = @{
                    enabled = $true
                    mode = "truncate"
                    trigger_ratio = 100
                    target_tokens = 40
                    preserve = @("latest user intent", "source refs")
                    forbid = @("new facts")
                }
            }
            output = @{
                output_mode = "decision_json"
                strict_json = $true
            }
        }
    }
    Assert-True ($draft.draft_id -ne "") "draft_id should not be empty"
    $summary.draft_id = $draft.draft_id
    $summary.agent_id = $agentID
    $summary.agent_version = $version

    $preview = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "prompt.preview" -Roles "optimizer" -Payload @{
        draft_id = $draft.draft_id
        input = $longInput
    }
    Write-E2EJson -Path (Join-Path $ReportDir "context-preview-initial.json") -Value $preview

    Assert-Equal $preview.effective_strategies.context.mode "balanced" "initial context mode"
    Assert-Equal $preview.effective_strategies.context.recent_message_limit 1 "initial recent message limit"
    Assert-Equal $preview.effective_strategies.context.retrieval_max_results 1 "initial retrieval limit"
    Assert-Equal $preview.effective_strategies.context.context_token_budget 80 "initial context token budget"
    Assert-True ($null -ne $preview.context_assembly_report) "initial context assembly report should exist"
    Assert-Equal $preview.context_assembly_report.mode "balanced" "initial context assembly report mode"
    Assert-Equal $preview.context_assembly_report.token_budget 80 "initial context assembly report token budget"
    Assert-True ($null -ne $preview.compression_report) "initial compression report should exist"
    Assert-Equal $preview.compression_report.mode "truncate" "initial compression mode"
    Assert-True ([bool]$preview.compression_report.applied) "truncate compression should be applied for long preview input"

    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.draft.patch_strategies" -Roles "optimizer" -Payload @{
        draft_id = $draft.draft_id
        strategies = @{
            context = @{
                mode = "long_context"
                recent_message_limit = 3
                retrieval_max_results = 2
                context_token_budget = 120
                enabled_sources = @("conversation_recent", "runtime_hook_context")
                compression = @{
                    enabled = $false
                    mode = "none"
                }
            }
            knowledge = @{
                enabled = $false
                search_mode = "bm25"
                inject_mode = "tool_only"
                max_results = 0
            }
            output = @{
                output_mode = "decision_json"
                strict_json = $true
            }
        }
    } | Out-Null

    $patchedPreview = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "prompt.preview" -Roles "optimizer" -Payload @{
        draft_id = $draft.draft_id
        input = "short context strategy preview"
    }
    Write-E2EJson -Path (Join-Path $ReportDir "context-preview-patched.json") -Value $patchedPreview

    Assert-Equal $patchedPreview.effective_strategies.context.mode "long_context" "patched context mode"
    Assert-Equal $patchedPreview.effective_strategies.context.recent_message_limit 3 "patched recent message limit"
    Assert-Equal $patchedPreview.effective_strategies.context.retrieval_max_results 2 "patched retrieval limit"
    Assert-Equal $patchedPreview.context_assembly_report.mode "long_context" "patched context assembly report mode"
    Assert-Equal $patchedPreview.context_assembly_report.token_budget 120 "patched context token budget"
    Assert-Equal $patchedPreview.effective_strategies.knowledge.search_mode "bm25" "patched knowledge search mode"
    Assert-Equal $patchedPreview.effective_strategies.knowledge.inject_mode "tool_only" "patched knowledge inject mode"
    Assert-Equal $patchedPreview.effective_strategies.output.output_mode "decision_json" "patched output mode"

    $summary.initial_prompt_bundle_hash = $preview.prompt_bundle_hash
    $summary.patched_prompt_bundle_hash = $patchedPreview.prompt_bundle_hash
    $summary.initial_strategy_hash = $preview.strategy_hash
    $summary.patched_strategy_hash = $patchedPreview.strategy_hash
    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "context-strategy-report.json") -Value $summary
    Write-Host "Context strategy e2e passed. report=$ReportDir draft=$($draft.draft_id)"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "context-strategy-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
