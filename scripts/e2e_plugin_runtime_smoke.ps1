param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "plugin-runtime"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_SERVICE_TOKEN)) {
    $env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
}

$server = $null
$toolHostJob = $null
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

    $toolHostPrefix = "http://127.0.0.1:{0}/" -f (Get-FreeTcpPort)
    $toolHostJob = Start-Job -ArgumentList $toolHostPrefix -ScriptBlock {
        param($Prefix)
        $listener = [System.Net.HttpListener]::new()
        $listener.Prefixes.Add($Prefix)
        $listener.Start()
        try {
            while ($listener.IsListening) {
                $ctx = $listener.GetContext()
                $path = $ctx.Request.Url.AbsolutePath
                $ctx.Response.ContentType = "application/json; charset=utf-8"
                if ($path -eq "/tools/catalog") {
                    $body = '{"tools":[{"tool_id":"crm.remote.lookup","operation":"lookup","name":"CRM remote lookup","description":"Lookup CRM records through a tool host.","when_to_use":["crm remote lookup"],"input_schema":{"type":"object"},"output_schema":{"type":"object"},"risk_level":"low","visibility":"protected","version":"v1"}]}'
                } else {
                    $reader = [System.IO.StreamReader]::new($ctx.Request.InputStream, [System.Text.Encoding]::UTF8)
                    $requestBody = $reader.ReadToEnd()
                    $safeBody = $requestBody.Replace("\", "\\").Replace('"', '\"')
                    $body = '{"output":{"operation":"lookup","received":"' + $safeBody + '"}}'
                }
                $bytes = [System.Text.Encoding]::UTF8.GetBytes($body)
                $ctx.Response.OutputStream.Write($bytes, 0, $bytes.Length)
                $ctx.Response.Close()
            }
        } finally {
            $listener.Stop()
        }
    }
    Start-Sleep -Milliseconds 300
    $summary.tool_host = $toolHostPrefix

    $manifest = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "tool.manifest.upsert" -Roles "optimizer" -Payload @{
        tool_id = "http.customer.echo"
        name = "HTTP customer echo"
        description = "Echoes customer arguments through HTTP."
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
        executor = @{
            type = "http_direct"
            url = $toolHostPrefix.TrimEnd("/") + "/tools/invoke"
        }
    }
    Assert-Equal $manifest.tool_id "http.customer.echo" "http manifest tool_id"
    Assert-True ($manifest.execution_profile -match '"domain_id":"http"') "http manifest execution profile"

    $provider = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "tool.provider.upsert" -Roles "optimizer" -Payload @{
        provider_id = "crm-host"
        provider_type = "static_tool_host"
        name = "CRM Host"
        endpoint = $toolHostPrefix.TrimEnd("/")
    }
    Assert-Equal $provider.provider_id "crm-host" "provider id"

    $synced = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "tool.provider.sync" -Roles "optimizer" -Payload @{
        provider_id = "crm-host"
    }
    Assert-True (@($synced).Count -ge 1) "provider sync should install at least one tool"

    $toolList = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "tool.manifest.list" -Roles "optimizer"
    $toolListJson = $toolList | ConvertTo-Json -Depth 40
    Assert-True ($toolListJson -match "http.customer.echo") "tool manifest list should include http tool"
    Assert-True ($toolListJson -match "crm.remote.lookup") "tool manifest list should include synced tool"

    $agentSuffix = Get-Date -Format "yyyyMMddHHmmssfff"
    $providerAgentID = "crm-agent-$agentSuffix"
    $callerAgentID = "caller-agent-$agentSuffix"

    $providerDraft = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.draft.create" -Roles "optimizer" -Payload @{
        agent_id = $providerAgentID
        version = "v1"
        prompt = "CRM provider prompt"
        tool_bindings = @{ exposed_tool_ids = @("crm-agent.customer.summary", "http.customer.echo", "crm.remote.lookup") }
    }
    $exportDraft = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.exported_tool.add" -Roles "optimizer" -Payload @{
        draft_id = $providerDraft.draft_id
        tool_id = "crm-agent.customer.summary"
        name = "Customer summary"
        description = "Summarize customer records."
        when_to_use = @("customer history")
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
    }
    Assert-Equal $exportDraft.draft_id $providerDraft.draft_id "export draft id"

    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.draft.validate" -Roles "optimizer" -Payload @{ draft_id = $providerDraft.draft_id } | Out-Null
    $providerRelease = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.publish" -Roles "optimizer" -Payload @{ draft_id = $providerDraft.draft_id }
    Assert-Equal $providerRelease.status "published" "provider package publish"

    $callerDraft = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.draft.create" -Roles "optimizer" -Payload @{
        agent_id = $callerAgentID
        version = "v1"
        prompt = "Caller prompt"
        tool_bindings = @{ allowed_tool_ids = @("crm-agent.customer.summary", "http.customer.echo", "crm.remote.lookup") }
    }
    Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "agent.package.collaborator.add" -Roles "optimizer" -Payload @{
        draft_id = $callerDraft.draft_id
        agent_id = $providerAgentID
        name = "CRM Agent"
        description = "Handles customer history."
        when_to_use = @("customer history")
        capabilities = @("crm")
    } | Out-Null

    $preview = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "prompt.preview" -Roles "optimizer" -Payload @{
        draft_id = $callerDraft.draft_id
        input = "customer history"
    }
    $previewJson = $preview | ConvertTo-Json -Depth 40
    Assert-True ($previewJson -match "retrieved collaborator card") "preview should include collaborator card"
    Assert-True ($previewJson -match "crm-agent.customer.summary") "preview should include exported agent tool"

    $agentTool = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "tools.invoke" -Roles "runtime_caller" -TraceID ("trace_plugin_agent_tool_" + $agentSuffix) -Target @{
        agent_id = $providerAgentID
        version = "v1"
    } -Payload @{
        tool_id = "crm-agent.customer.summary"
        arguments = @{ customer_id = "c_123" }
    }
    Assert-Equal $agentTool.status "succeeded" "agent exported tool invoke"

    $httpTool = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "tools.invoke" -Roles "runtime_caller" -TraceID ("trace_plugin_http_tool_" + $agentSuffix) -Target @{
        agent_id = $providerAgentID
        version = "v1"
    } -Payload @{
        tool_id = "http.customer.echo"
        arguments = @{ customer_id = "c_123" }
    }
    Assert-Equal $httpTool.status "succeeded" "http dynamic tool invoke"

    $syncedTool = Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "tools.invoke" -Roles "runtime_caller" -TraceID ("trace_plugin_synced_tool_" + $agentSuffix) -Target @{
        agent_id = $providerAgentID
        version = "v1"
    } -Payload @{
        tool_id = "crm.remote.lookup"
        arguments = @{ customer_id = "c_456" }
    }
    Assert-Equal $syncedTool.status "succeeded" "provider synced tool invoke"

    $missingTargetDenied = $false
    try {
        Invoke-CleanCoreCommand -BaseUrl $baseUrl -Command "origin.agent.delegate" -Roles "runtime_caller" -Target @{
            agent_id = $callerAgentID
            version = "v1"
        } -Payload @{
            parent_task_id = "task_missing"
            objective = "delegate without target"
        } | Out-Null
    } catch {
        $missingTargetDenied = ($_.Exception.Message -match "to_agent_id")
    }
    Assert-True $missingTargetDenied "delegate without to_agent_id should be rejected"

    $summary.status = "passed"
    $summary.provider_agent_id = $providerAgentID
    $summary.caller_agent_id = $callerAgentID
    $summary.exported_tool_result = $agentTool.status
    $summary.http_tool_result = $httpTool.status
    $summary.synced_tool_result = $syncedTool.status
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "plugin-runtime-report.json") -Value $summary
    Write-Host "Plugin/runtime smoke passed. report=$ReportDir"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "plugin-runtime-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if ($null -ne $toolHostJob) {
        Stop-Job $toolHostJob -ErrorAction SilentlyContinue | Out-Null
        Remove-Job $toolHostJob -ErrorAction SilentlyContinue | Out-Null
    }
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
