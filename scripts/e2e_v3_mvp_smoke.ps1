param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "v3-mvp"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_SERVICE_TOKEN)) {
    $env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
}

function Invoke-V3Json {
    param(
        [Parameter(Mandatory = $true)] [string]$BaseUrl,
        [Parameter(Mandatory = $true)] [string]$Method,
        [Parameter(Mandatory = $true)] [string]$Path,
        [AllowNull()] $Body = $null,
        [string]$Roles = "admin",
        [switch]$AllowError
    )
    $args = @{
        Uri = "$BaseUrl$Path"
        Method = $Method
        Headers = (Get-E2EHeaders -Roles $Roles)
        TimeoutSec = 90
    }
    if ($null -ne $Body) {
        $json = $Body | ConvertTo-Json -Depth 40
        $args["Body"] = [System.Text.Encoding]::UTF8.GetBytes($json)
    }
    try {
        return [pscustomobject]@{
            StatusCode = 200
            Body = Invoke-RestMethod @args
            Error = ""
        }
    } catch {
        $statusCode = 0
        if ($_.Exception.Response) {
            $statusCode = [int]$_.Exception.Response.StatusCode
        }
        if (-not $AllowError) {
            $details = ""
            if ($_.ErrorDetails.Message) {
                $details = $_.ErrorDetails.Message
            }
            throw "$Method $Path failed status=$statusCode $details"
        }
        $errorBody = $null
        if ($_.ErrorDetails.Message) {
            try {
                $errorBody = $_.ErrorDetails.Message | ConvertFrom-Json
            } catch {
                $errorBody = $_.ErrorDetails.Message
            }
        }
        return [pscustomobject]@{
            StatusCode = $statusCode
            Body = $errorBody
            Error = $_.Exception.Message
        }
    }
}

function Assert-StatusCode {
    param(
        $Response,
        [int[]]$Expected,
        [string]$Message
    )
    if (-not ($Expected -contains [int]$Response.StatusCode)) {
        throw ("{0}: expected status in [{1}] actual={2} body={3}" -f $Message, ($Expected -join ","), $Response.StatusCode, ($Response.Body | ConvertTo-Json -Depth 20 -Compress))
    }
}

$server = $null
$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    model_provider = "stub"
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    coverage = [ordered]@{}
    known_gaps = @()
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
    $agentID = "v3-agent-$suffix"
    $traceID = "trace_v3_mvp_$suffix"

    $agent = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/agents" -Body @{
        agent_id = $agentID
        name = "V3 MVP Agent"
        description = "E2E V3 MVP smoke agent"
        owner_id = "owner-e2e"
        version = "v1"
        prompt = "You are a V3 MVP smoke agent. Return concise ok responses."
        agents_md = "# V3 MVP Agent`n`nUse configured profile, skills, tools, hooks, and knowledge boundaries."
        tool_bindings = @{
            allowed_tool_ids = @("echo")
            allowed_tool_group_ids = @()
            exposed_tool_ids = @()
            denied_tool_ids = @()
        }
    }
    Assert-True ($agent.Body.agent.agent_id -eq $agentID) "agent create should return agent id"
    Assert-True ($agent.Body.draft.draft_id -ne "") "agent create should create initial draft"
    $draftID = $agent.Body.draft.draft_id
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.draft.validate" -Roles "optimizer" -Payload @{
        draft_id = $draftID
    } | Out-Null
    $published = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.publish" -Roles "optimizer" -Payload @{
        draft_id = $draftID
    }
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{
        package_version_id = $published.package_version_id
        canary_percent = 25
    } | Out-Null
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "eval.run" -Roles "optimizer" -Target @{
        agent_id = $agentID
        version = "v1"
    } -Payload @{
        package_version_id = $published.package_version_id
        input = "hello"
        final_reply_contains = @("ok")
        should_end_status = "completed"
        max_tool_calls = 0
    } | Out-Null
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{
        package_version_id = $published.package_version_id
    } | Out-Null
    $summary.package_version_id = $published.package_version_id
    $summary.coverage.agent = "created"

    $profile = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/agents/$agentID/prompt-profile" -Body @{
        version = "v1"
        identity_prompt = "standalone identity for V3 MVP smoke"
        system_prompt = "standalone system for V3 MVP smoke"
        developer_prompt = "standalone developer for V3 MVP smoke"
        agents_md = "# V3 MVP Smoke`n`nStandalone prompt profile overlay."
    }
    Assert-True ($profile.Body.prompt_profile.source_kind -eq "profile") "prompt profile should be standalone profile source"
    $preview = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/agents/$agentID/prompt-profile/preview" -Body @{ input = "hello v3" }
    $previewJson = $preview.Body | ConvertTo-Json -Depth 40
    Assert-True ($previewJson -match "standalone developer for V3 MVP smoke") "prompt preview should use standalone prompt profile"
    $summary.coverage.prompt_profile = "overlay_previewed"

    $skill = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/agents/$agentID/skills" -Body @{
        skill_id = "v3.boundary"
        version = "v1"
        name = "V3 Boundary Skill"
        description = "Keeps V3 smoke answers inside configured capabilities."
        instruction = "Prefer capability boundaries and cite configured knowledge only."
        risk_level = "low"
        when_to_use = @("v3 mvp smoke")
        recommended_tools = @("echo")
        allowed_tools = @("echo")
        output_requirements = @("concise")
    }
    Assert-True ($skill.Body.skill.card.skill_id -eq "v3.boundary") "skill upsert should return V3 skill"
    $summary.coverage.skill = "upserted"

    $group = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/tool-groups" -Body @{
        group_id = "v3.tools"
        name = "V3 Tools"
        description = "Tools used by V3 MVP smoke."
    }
    Assert-True ($group.Body.group.group_id -eq "v3.tools") "tool group create should return group id"

    $toolProvider = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/tool-providers" -Body @{
        provider_id = "v3-local-tools"
        provider_type = "static_tool_host"
        name = "V3 Local Tools"
        endpoint = "http://127.0.0.1:1"
        health_status = "healthy"
    }
    Assert-True ($toolProvider.Body.provider.provider_id -eq "v3-local-tools") "tool provider create should return provider id"

    $manifest = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/tool-manifests" -Body @{
        tool_id = "v3.echo"
        group_id = "v3.tools"
        name = "V3 echo"
        description = "V3 static manifest smoke tool."
        when_to_use = @("v3 smoke")
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        executor = @{
            type = "static_tool_host"
            provider_id = "v3-local-tools"
            operation = "echo"
        }
        status = "enabled"
    }
    Assert-True ($manifest.Body.tool.tool_id -eq "v3.echo") "tool manifest create should return tool id"
    $toolBinding = Invoke-V3Json -BaseUrl $baseUrl -Method Put -Path "/v1/agents/$agentID/tool-bindings" -Body @{
        version = "v1"
        tool_bindings = @{
            allowed_tool_ids = @("echo", "v3.echo")
            allowed_tool_group_ids = @("v3.tools")
            denied_tool_ids = @("origin.agent.delegate")
            exposed_tool_ids = @()
        }
    }
    Assert-True (@($toolBinding.Body.tool_bindings.allowed_tool_group_ids) -contains "v3.tools") "tool binding should include group id"
    $summary.coverage.tooling = "provider_group_manifest_binding_created"

    $hookManifest = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/runtime-hook-manifests" -Body @{
        hook_id = "v3-before-model"
        name = "V3 before model"
        phase = "before_model_call"
        status = "enabled"
        failure_policy = "ignore"
    }
    Assert-True ($hookManifest.Body.hook.hook_id -eq "v3-before-model") "runtime hook manifest should be created"
    $hookBinding = Invoke-V3Json -BaseUrl $baseUrl -Method Put -Path "/v1/agents/$agentID/runtime-hooks" -Body @{
        agent_version = "v1"
        hook_id = "v3-before-model"
        provider_type = "go"
        phase = "before_model_call"
        enabled = $true
        failure_policy = "ignore"
        config = @{
            patch = @{
                add_context_blocks = @(@{
                    id = "v3-hook-context"
                    title = "V3 hook context"
                    content = "Runtime hook injected context for V3 MVP smoke."
                })
            }
        }
    }
    Assert-True ($hookBinding.Body.binding.hook_id -eq "v3-before-model") "runtime hook binding should be stored"
    $hookPreview = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/agents/$agentID/runtime-hooks/preview" -Body @{
        agent_version = "v1"
        hook_point = "before_model_call"
        input = "preview hook"
    }
    $hookPreviewJson = $hookPreview.Body | ConvertTo-Json -Depth 40
    Assert-True ($hookPreviewJson -match "Runtime hook injected context") "runtime hook preview should include configured patch"
    $summary.coverage.runtime_hooks = "manifest_binding_previewed"

    $run = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -TraceID $traceID -Target @{
        agent_id = $agentID
        version = "v1"
    } -Payload @{ input = "what can you do in this V3 smoke?" }
    Assert-Equal $run.status "completed" "agent.run should complete"
    $trace = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/traces/$traceID"
    $traceTypes = @($trace.events | ForEach-Object { $_.type })
    foreach ($required in @("input.received", "agent.loaded", "promptbundle.built", "model.called", "decision.validated", "response.sent")) {
        Assert-True ($traceTypes -contains $required) "trace should contain $required"
    }
    $hookEvents = Invoke-V3Json -BaseUrl $baseUrl -Method Get -Path "/v1/runtime-hook-events?trace_id=$traceID"
    $summary.coverage.agent_run_trace = "completed_with_trace"
    $summary.runtime_hook_event_count = @($hookEvents.Body.events).Count

    $kb = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/knowledge-bases" -Body @{
        owner_group_id = "group-a"
        name = "V3 Shared KB"
        visibility = "shared"
        index_type = "hybrid"
        source_type = "text"
    }
    $kbID = $kb.Body.knowledge_base.knowledge_base_id
    Assert-True ($kbID -ne "") "knowledge base id should be returned"
    Assert-Equal $kb.Body.knowledge_base.search_mode "hybrid" "knowledge base search mode"

    $doc = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/knowledge-bases/$kbID/documents" -Body @{
        source_group_id = "group-a"
        title = "Launch Contacts"
        content = "Launch owner is alex@example.com and release window is 123456."
        visibility = "shared"
    }
    Assert-Equal $doc.Body.index_job.status "completed" "knowledge ingestion job status"
    $jobID = $doc.Body.index_job.job_id
    $jobs = Invoke-V3Json -BaseUrl $baseUrl -Method Get -Path "/v1/knowledge-bases/$kbID/index-jobs"
    Assert-True (@($jobs.Body.index_jobs).Count -ge 1) "knowledge index jobs should list ingestion job"
    if ($jobID -ne "") {
        $job = Invoke-V3Json -BaseUrl $baseUrl -Method Get -Path "/v1/knowledge-bases/$kbID/index-jobs/$jobID"
        Assert-Equal $job.Body.index_job.status "completed" "knowledge index job detail status"
    }
    $ownSearch = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/knowledge-search" -Body @{
        requester_group_id = "group-a"
        query = "Launch"
        search_mode = "hybrid"
        limit = 3
    }
    Assert-True (@($ownSearch.Body.results).Count -ge 1) "knowledge search should return local group result"
    Assert-Equal $ownSearch.Body.results[0].search_mode "hybrid" "knowledge search result mode"
    $crossDenied = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/cross-groups/search" -Body @{
        request_group_id = "group-b"
        source_group_id = "group-a"
        query = "Launch"
    } -AllowError
    Assert-StatusCode $crossDenied @(400, 403) "cross-group search before explicit policy should be denied"
    $policy = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/cross-group-share-policies" -Body @{
        source_group_id = "group-a"
        target_group_id = "group-b"
        knowledge_base_ids = @($kbID)
        redaction_policy = "mask_emails"
        status = "enabled"
    }
    Assert-True ($policy.Body.policy.redaction_policy -eq "mask_emails") "cross-group share policy should be created with redaction"
    $crossStillDenied = Invoke-V3Json -BaseUrl $baseUrl -Method Post -Path "/v1/cross-groups/search" -Body @{
        request_group_id = "group-b"
        source_group_id = "group-a"
        query = "Launch"
    } -AllowError
    Assert-StatusCode $crossStillDenied @(400, 403) "cross-group search should still require explicit search permission"
    $summary.coverage.knowledge_cross_group = "kb_ingest_search_policy_and_http_permission_guards"
    $summary.known_gaps += "No REST/admin endpoint currently grants GroupPermissionPolicy for cross_group.search, so the full HTTP path for permission-granted redacted cross-group search is still covered only by service/server tests, not this E2E smoke."

    $goNoGo = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/release/go-no-go"
    Assert-True ($goNoGo.decision -ne "") "go/no-go should include decision"
    $readiness = Invoke-CleanCoreGet -BaseUrl $baseUrl -Path "/v1/readiness/report"
    Assert-True ($readiness.status -ne "") "readiness should include status"
    $summary.go_no_go = $goNoGo.decision
    $summary.readiness_status = $readiness.status

    $summary.status = "passed"
    $summary.agent_id = $agentID
    $summary.run_id = $run.run_id
    $summary.trace_id = $traceID
    $summary.knowledge_base_id = $kbID
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "v3-mvp-smoke-report.json") -Value $summary
    Write-Host "V3 MVP smoke passed. report=$ReportDir agent=$agentID trace=$traceID"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "v3-mvp-smoke-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
