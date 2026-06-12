param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "clean-core-all-interfaces"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_SERVICE_TOKEN)) {
    $env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
}

$tenantID = "tenant_all_interfaces_e2e"
$otherTenantID = "tenant_all_interfaces_other"
$suffix = Get-Date -Format "yyyyMMddHHmmssfff"
$script:Coverage = [ordered]@{}
$script:Calls = New-Object System.Collections.Generic.List[object]

$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    tenant_id = $tenantID
    coverage = [ordered]@{}
    calls = @()
    call_count = 0
}

function ConvertTo-AIJsonText {
    param([AllowNull()] $Value)
    if ($null -eq $Value) {
        return ""
    }
    return $Value | ConvertTo-Json -Depth 80 -Compress
}

function Read-AIResponseBody {
    param([AllowNull()] $Response)
    if ($null -eq $Response) {
        return ""
    }
    try {
        $stream = $Response.GetResponseStream()
        if ($null -eq $stream) {
            return ""
        }
        $reader = [System.IO.StreamReader]::new($stream, [System.Text.Encoding]::UTF8)
        return $reader.ReadToEnd()
    } catch {
        return ""
    }
}

function Get-AIOpenAPIOperations {
    $specPath = Join-Path (Get-RepoRoot) "docs\openapi.clean-core.v1.json"
    $spec = Get-Content $specPath -Raw | ConvertFrom-Json
    $ops = New-Object System.Collections.Generic.List[string]
    foreach ($path in @($spec.paths.PSObject.Properties)) {
        foreach ($method in @($path.Value.PSObject.Properties)) {
            if ($method.Name -in @("get", "post", "put", "patch", "delete")) {
                $ops.Add(("{0} {1}" -f $method.Name.ToUpperInvariant(), $path.Name)) | Out-Null
            }
        }
    }
    return @($ops | Sort-Object -Unique)
}

function Register-AICoverage {
    param(
        [Parameter(Mandatory = $true)] [string]$Operation,
        [Parameter(Mandatory = $true)] [int]$StatusCode,
        [Parameter(Mandatory = $true)] [string]$Path
    )
    if (-not $script:Coverage.Contains($Operation)) {
        $script:Coverage[$Operation] = [ordered]@{
            status_code = $StatusCode
            concrete_path = $Path
            first_seen_at = (Get-Date).ToUniversalTime().ToString("o")
        }
    }
}

function Invoke-AIJson {
    param(
        [Parameter(Mandatory = $true)] [string]$BaseUrl,
        [Parameter(Mandatory = $true)] [string]$Method,
        [Parameter(Mandatory = $true)] [string]$Path,
        [AllowNull()] $Body = $null,
        [string]$Operation = "",
        [int[]]$ExpectedStatus = @(200),
        [string]$Roles = "admin",
        [string]$TenantID = $tenantID,
        [string]$CallerID = "all-interface-e2e",
        [string]$CallerType = "service",
        [hashtable]$Headers = @{}
    )
    $requestHeaders = Get-E2EHeaders -Roles $Roles -TenantID $TenantID -CallerID $CallerID -CallerType $CallerType
    foreach ($key in @($Headers.Keys)) {
        $requestHeaders[$key] = $Headers[$key]
    }
    $args = @{
        Uri = "$BaseUrl$Path"
        Method = $Method
        Headers = $requestHeaders
        TimeoutSec = 120
        UseBasicParsing = $true
    }
    if ($null -ne $Body) {
        $json = ConvertTo-AIJsonText $Body
        $args["Body"] = [System.Text.Encoding]::UTF8.GetBytes($json)
    }
    $statusCode = -1
    $raw = ""
    $parsed = $null
    try {
        $response = Invoke-WebRequest @args
        $statusCode = [int]$response.StatusCode
        $raw = [string]$response.Content
    } catch {
        if ($_.Exception.Response) {
            $statusCode = [int]$_.Exception.Response.StatusCode
            $raw = Read-AIResponseBody -Response $_.Exception.Response
        } elseif ($_.ErrorDetails -and $_.ErrorDetails.Message) {
            $raw = $_.ErrorDetails.Message
        } else {
            $raw = $_.Exception.Message
        }
    }
    if (-not [string]::IsNullOrWhiteSpace($raw)) {
        try {
            $parsed = $raw | ConvertFrom-Json
        } catch {
            $parsed = $null
        }
    }
    if ($Operation -ne "") {
        Register-AICoverage -Operation $Operation -StatusCode $statusCode -Path $Path
    }
    $entry = [ordered]@{
        operation = $Operation
        method = $Method.ToUpperInvariant()
        path = $Path
        status_code = $statusCode
    }
    if ($null -ne $Body -and $Body -is [System.Collections.IDictionary] -and $Body.Contains("command")) {
        $entry["command"] = [string]$Body["command"]
    }
    $script:Calls.Add([pscustomobject]$entry) | Out-Null
    if (-not ($ExpectedStatus -contains $statusCode)) {
        throw ("HTTP {0} {1} expected [{2}] actual={3} body={4}" -f $Method.ToUpperInvariant(), $Path, ($ExpectedStatus -join ","), $statusCode, $raw)
    }
    return [pscustomobject]@{
        StatusCode = $statusCode
        Body = $parsed
        Raw = $raw
    }
}

function Invoke-AICommand {
    param(
        [Parameter(Mandatory = $true)] [string]$BaseUrl,
        [Parameter(Mandatory = $true)] [string]$Command,
        [hashtable]$Payload = @{},
        [hashtable]$Target = @{},
        [hashtable]$Context = @{},
        [string]$TraceID = "",
        [string]$Roles = "admin",
        [string]$TenantID = $tenantID
    )
    if (-not $Context.ContainsKey("tenant_id")) {
        $Context["tenant_id"] = $TenantID
    }
    $body = @{
        command = $Command
        payload = $Payload
        context = $Context
    }
    if ($Target.Count -gt 0) {
        $body["target"] = $Target
    }
    if ($TraceID -ne "") {
        $body["trace_id"] = $TraceID
    }
    $response = Invoke-AIJson -BaseUrl $BaseUrl -Method "Post" -Path "/v1/commands" -Body $body -Operation "POST /v1/commands" -ExpectedStatus @(200) -Roles $Roles -TenantID $TenantID
    return $response.Body
}

function Assert-AITrue {
    param(
        [bool]$Condition,
        [string]$Message
    )
    if (-not $Condition) {
        throw $Message
    }
}

function Assert-AIStatus {
    param(
        [AllowNull()] $Value,
        [string]$Message
    )
    if ($null -eq $Value -or [string]$Value -eq "") {
        throw $Message
    }
}

function New-AIHost {
    param(
        [string]$Prefix,
        [string]$ReportDir
    )
    return Start-Job -ArgumentList $Prefix, $ReportDir -ScriptBlock {
        param($Prefix, $ReportDir)
        $listener = [System.Net.HttpListener]::new()
        $listener.Prefixes.Add($Prefix)
        $listener.Start()
        $logPath = Join-Path $ReportDir "interface-host.ndjson"
        try {
            while ($listener.IsListening) {
                $ctx = $listener.GetContext()
                $path = $ctx.Request.Url.AbsolutePath
                $ctx.Response.ContentType = "application/json; charset=utf-8"
                $status = 200
                $body = "{}"
                if ($path -eq "/healthz") {
                    $body = '{"status":"ok","service":"all-interface-host"}'
                } elseif ($path -eq "/openapi.json") {
                    $body = '{"openapi":"3.0.3","paths":{"/adapter/search":{"post":{"operationId":"adapter.search","summary":"Adapter search","requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"query":{"type":"string"}}}}},"responses":{"200":{"description":"OK"}}}}}}'
                } elseif ($path -eq "/.well-known/agent-card.json") {
                    $body = '{"name":"All Interface A2A Agent","url":"http://all-interface-a2a.local","description":"A2A mock agent for all-interface E2E."}'
                } elseif ($path -eq "/tools/catalog") {
                    $body = '{"tools":[{"tool_id":"all.remote.echo","group_id":"all.tools","operation":"echo","name":"All remote echo","description":"Remote echo for all-interface E2E.","when_to_use":["echo"],"input_schema":{"type":"object"},"output_schema":{"type":"object"},"risk_level":"low","visibility":"protected","version":"v1"}]}'
                } elseif ($path -eq "/tools/invoke") {
                    $reader = [System.IO.StreamReader]::new($ctx.Request.InputStream, [System.Text.Encoding]::UTF8)
                    $requestBody = $reader.ReadToEnd()
                    $request = $requestBody | ConvertFrom-Json
                    $entry = [ordered]@{
                        at = (Get-Date).ToUniversalTime().ToString("o")
                        path = $path
                        tool_id = [string]$request.tool_id
                        operation = [string]$request.operation
                    }
                    ($entry | ConvertTo-Json -Compress) | Add-Content -Path $logPath -Encoding UTF8
                    $body = (@{
                        output = @{
                            ok = $true
                            tool_id = [string]$request.tool_id
                            operation = [string]$request.operation
                            arguments = $request.arguments
                        }
                    } | ConvertTo-Json -Depth 20 -Compress)
                } elseif ($path -eq "/adapter/search") {
                    $reader = [System.IO.StreamReader]::new($ctx.Request.InputStream, [System.Text.Encoding]::UTF8)
                    $requestBody = $reader.ReadToEnd()
                    $request = $requestBody | ConvertFrom-Json
                    $entry = [ordered]@{
                        at = (Get-Date).ToUniversalTime().ToString("o")
                        path = $path
                        query = [string]$request.query
                    }
                    ($entry | ConvertTo-Json -Compress) | Add-Content -Path $logPath -Encoding UTF8
                    $body = (@{
                        output = @{
                            ok = $true
                            query = [string]$request.query
                            source = "http_api_adapter"
                        }
                    } | ConvertTo-Json -Depth 20 -Compress)
                } elseif ($path -eq "/mcp") {
                    $reader = [System.IO.StreamReader]::new($ctx.Request.InputStream, [System.Text.Encoding]::UTF8)
                    $requestBody = $reader.ReadToEnd()
                    $request = $requestBody | ConvertFrom-Json
                    if ([string]$request.method -eq "tools/list") {
                        $body = (@{
                            jsonrpc = "2.0"
                            id = $request.id
                            result = @{
                                tools = @(@{
                                    name = "sum"
                                    title = "All MCP sum"
                                    description = "Add two numbers through MCP."
                                    inputSchema = @{
                                        type = "object"
                                        properties = @{
                                            a = @{ type = "number" }
                                            b = @{ type = "number" }
                                        }
                                    }
                                    annotations = @{ readOnlyHint = $true }
                                })
                            }
                        } | ConvertTo-Json -Depth 40 -Compress)
                    } elseif ([string]$request.method -eq "tools/call") {
                        $args = $request.params.arguments
                        $total = [double]$args.a + [double]$args.b
                        $body = (@{
                            jsonrpc = "2.0"
                            id = $request.id
                            result = @{
                                structuredContent = @{ total = $total }
                                content = @(@{ type = "text"; text = "total=$total" })
                            }
                        } | ConvertTo-Json -Depth 40 -Compress)
                    } else {
                        $body = (@{
                            jsonrpc = "2.0"
                            id = $request.id
                            error = @{ code = -32601; message = "method not found" }
                        } | ConvertTo-Json -Depth 20 -Compress)
                    }
                } elseif ($path -eq "/tasks/get") {
                    $reader = [System.IO.StreamReader]::new($ctx.Request.InputStream, [System.Text.Encoding]::UTF8)
                    $requestBody = $reader.ReadToEnd()
                    $request = $requestBody | ConvertFrom-Json
                    $externalID = [string]$request.params.id
                    $body = (@{
                        jsonrpc = "2.0"
                        id = $request.id
                        result = @{
                            id = $externalID
                            status = @{ state = "working" }
                            metadata = @{
                                title = "A2A external task $externalID"
                                summary = "A2A summary for $externalID"
                            }
                        }
                    } | ConvertTo-Json -Depth 30 -Compress)
                } elseif ($path -eq "/message/send") {
                    $reader = [System.IO.StreamReader]::new($ctx.Request.InputStream, [System.Text.Encoding]::UTF8)
                    $requestBody = $reader.ReadToEnd()
                    $request = $requestBody | ConvertFrom-Json
                    $entry = [ordered]@{
                        at = (Get-Date).ToUniversalTime().ToString("o")
                        path = $path
                        method = [string]$request.method
                        task_id = [string]$request.params.taskId
                    }
                    ($entry | ConvertTo-Json -Compress) | Add-Content -Path $logPath -Encoding UTF8
                    $body = (@{
                        jsonrpc = "2.0"
                        id = $request.id
                        result = @{ id = "msg-all-interface" }
                    } | ConvertTo-Json -Depth 20 -Compress)
                } elseif ($path -eq "/runtime-hooks/catalog") {
                    $body = '{"provider_id":"all-hook-host","version":"v1","hooks":[{"hook_id":"all.remote.before","name":"All remote before model","phase":"before_model_call","status":"enabled","version":"v1","failure_policy":"ignore","patch_schema":{"type":"object"}}]}'
                } elseif ($path -eq "/runtime-hooks/invoke") {
                    $body = '{"status":"ok","patch":{"add_context_blocks":[{"id":"all-interface-hook","title":"All interface hook","content":"hook patch from all-interface host"}]}}'
                } else {
                    $status = 404
                    $body = '{"error":{"code":"not_found"}}'
                }
                $ctx.Response.StatusCode = $status
                $bytes = [System.Text.Encoding]::UTF8.GetBytes($body)
                $ctx.Response.OutputStream.Write($bytes, 0, $bytes.Length)
                $ctx.Response.Close()
            }
        } finally {
            $listener.Stop()
        }
    }
}

$server = $null
$hostJob = $null

try {
    $hostPrefix = "http://127.0.0.1:{0}/" -f (Get-FreeTcpPort)
    $hostJob = New-AIHost -Prefix $hostPrefix -ReportDir $ReportDir
    Start-Sleep -Milliseconds 300
    $summary.provider_host = $hostPrefix

    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv @{
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = $env:CLEAN_CORE_SERVICE_TOKEN
        CLEAN_CORE_METRICS_AUTH_MODE = "required"
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
        CLEAN_CORE_EXTERNAL_BRIDGE_PROVIDER = "a2a"
        CLEAN_CORE_EXTERNAL_BRIDGE_BASE_URL = $hostPrefix.TrimEnd("/")
    }
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl

    $agentID = "all-agent-$suffix"
    $collaboratorID = "all-collab-$suffix"
    $deleteAgentID = "all-delete-$suffix"
    $draftVersion = "v1"
    $stableVersion = "v2"
    $providerID = "all-provider-$suffix"
    $providerConnectionID = "$providerID-connection"
    $mcpProviderID = "all-mcp-$suffix"
    $mcpConnectionID = "$mcpProviderID-connection"
    $mcpToolID = "$mcpProviderID.sum"
    $adapterProviderID = "managed_http_api_adapter"
    $adapterConnectionID = "all-http-adapter-$suffix-connection"
    $adapterOperationID = "all.search.$suffix"
    $adapterToolID = "all-http-adapter-$suffix.search"
    $databaseProviderID = "managed_database_adapter"
    $databaseConnectionID = "all-db-adapter-$suffix-connection"
    $databaseOperationID = "all.customers.by_status.$suffix"
    $databaseToolID = "all-db-adapter-$suffix.customers.by_status"
    $toolGroupID = "all.tools.$suffix"
    $toolID = "all.local.echo.$suffix"
    $hookProviderID = "all-hook-host"
    $hookID = "all.remote.before"
    $goHookID = "all.go.before.$suffix"
    $skillID = "all.skill.$suffix"
    $exportedToolID = "all.exported.$suffix"
    $traceID = "trace_all_interfaces_$suffix"
    $toolTraceID = "trace_all_tool_$suffix"

    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/healthz" -Operation "GET /healthz" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/readyz" -Operation "GET /readyz" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/version" -Operation "GET /version" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/metrics" -Operation "GET /metrics" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/metrics" -Operation "GET /metrics unauthorized" -ExpectedStatus @(401) -Headers @{ Authorization = "Bearer wrong-token" } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/readiness/report" -Operation "GET /v1/readiness/report" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/release/go-no-go" -Operation "GET /v1/release/go-no-go" -ExpectedStatus @(200) | Out-Null

    $intakePolicy = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/intake/policies" -Operation "POST /v1/intake/policies" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        name = "All interface acknowledgement"
        status = "enabled"
        priority = 100
        channel = "web"
        match_type = "contains"
        pattern = "refund"
        reply_text = "Received. We are checking the request."
        reply_kind = "acknowledgement"
        continue_to_run = $true
    }
    $intakePolicyID = [string]$intakePolicy.Body.policy.policy_id
    Assert-AIStatus $intakePolicyID "intake policy create should return id"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/intake/policies" -Operation "GET /v1/intake/policies" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/intake/policies/$intakePolicyID" -Operation "GET /v1/intake/policies/{policy_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/intake/policies/$intakePolicyID" -Operation "PUT /v1/intake/policies/{policy_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        name = "All interface acknowledgement updated"
        status = "enabled"
        priority = 110
        channel = "web"
        match_type = "contains"
        pattern = "refund"
        reply_text = "Updated acknowledgement."
        reply_kind = "status_update"
        continue_to_run = $false
    } | Out-Null
    $preReply = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/intake/evaluate" -Operation "POST /v1/intake/evaluate" -ExpectedStatus @(200) -Roles "runtime_caller" -Body @{
        trace_id = "trace_intake_$suffix"
        channel = "web"
        input = "please refund order 123"
    }
    Assert-AITrue ([bool]$preReply.Body.pre_reply.matched) "intake evaluate should match policy"
    Assert-AITrue ([string]$preReply.Body.pre_reply.dispatch -eq "external_channel") "intake evaluate should only return external_channel dispatch"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/intake/policies/$intakePolicyID" -Operation "DELETE /v1/intake/policies/{policy_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null

    foreach ($policy in @(
        @{ group_id = "group-a"; subject_id = "admin"; actions = @("knowledge.create", "knowledge.search", "cross_group.policy"); resource_scopes = @("*") },
        @{ group_id = "group-b"; subject_id = "admin"; actions = @("knowledge.search", "cross_group.search", "cross_group.policy"); resource_scopes = @("*") }
    )) {
        Invoke-AICommand -BaseUrl $baseUrl -Command "permission.policy.upsert" -Roles "optimizer" -Payload $policy | Out-Null
    }

    $agentCreate = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents" -Operation "POST /v1/agents" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        agent_id = $agentID
        name = "All Interface Agent"
        description = "Agent for full HTTP interface E2E"
        owner_id = "all-interface"
        version = $draftVersion
        prompt = "You are an all-interface E2E agent. Reply ok."
        agents_md = "# All Interface Agent`n`nE2E coverage package."
        tool_bindings = @{
            allowed_tool_ids = @("echo")
            allowed_tool_group_ids = @()
            denied_tool_ids = @()
            exposed_tool_ids = @()
        }
        metadata = @{
            max_tool_calls = 2
            max_steps = 4
        }
    }
    $draftID = [string]$agentCreate.Body.draft.draft_id
    Assert-AIStatus $draftID "agent create should create draft"

    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents" -Operation "GET /v1/agents" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID" -Operation "GET /v1/agents/{agent_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Patch" -Path "/v1/agents/$agentID" -Operation "PATCH /v1/agents/{agent_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ description = "Patched all-interface agent"; status = "active" } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/drafts" -Operation "GET /v1/agents/{agent_id}/drafts" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/drafts/$draftID" -Operation "GET /v1/agents/{agent_id}/drafts/{draft_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/drafts/$draftID/validate" -Operation "POST /v1/agents/{agent_id}/drafts/{draft_id}/validate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/drafts/$draftID/review" -Operation "POST /v1/agents/{agent_id}/drafts/{draft_id}/review" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    $publishedV1 = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/drafts/$draftID/publish" -Operation "POST /v1/agents/{agent_id}/drafts/{draft_id}/publish" -ExpectedStatus @(200) -Roles "optimizer" -Body @{}
    Assert-AIStatus $publishedV1.Body.version.version.package_version_id "publish v1 should return package version"

    $stableDraft = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/drafts" -Operation "POST /v1/agents/{agent_id}/drafts" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        version = $stableVersion
        prompt = "Stable all-interface prompt."
        agents_md = "# Stable All Interface"
        metadata = @{ max_tool_calls = 4; max_steps = 4 }
    }
    $stableDraftID = [string]$stableDraft.Body.draft.draft_id
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/drafts/$stableDraftID/validate" -Operation "POST /v1/agents/{agent_id}/drafts/{draft_id}/validate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/drafts/$stableDraftID/review" -Operation "POST /v1/agents/{agent_id}/drafts/{draft_id}/review" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    $publishedV2 = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/drafts/$stableDraftID/publish" -Operation "POST /v1/agents/{agent_id}/drafts/{draft_id}/publish" -ExpectedStatus @(200) -Roles "optimizer" -Body @{}
    $packageVersionID = [string]$publishedV2.Body.version.version.package_version_id
    Assert-AIStatus $packageVersionID "publish v2 should return package version"
    Invoke-AICommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{ package_version_id = $packageVersionID; canary_percent = 25 } | Out-Null
    Invoke-AICommand -BaseUrl $baseUrl -Command "eval.run" -Roles "optimizer" -Target @{ agent_id = $agentID; version = $stableVersion } -Payload @{
        package_version_id = $packageVersionID
        input = "hello"
        final_reply_contains = @("ok")
        should_end_status = "completed"
        max_tool_calls = 0
    } | Out-Null
    Invoke-AICommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{ package_version_id = $packageVersionID } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/versions/$stableVersion/activate" -Operation "POST /v1/agents/{agent_id}/versions/{version}/activate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/versions" -Operation "GET /v1/agents/{agent_id}/versions" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/versions/$stableVersion" -Operation "GET /v1/agents/{agent_id}/versions/{version}" -ExpectedStatus @(200) | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections" -Operation "POST /v1/service-connections" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        connection_id = $providerConnectionID
        connection_type = "http_api"
        name = "All Interface ToolHost Connection"
        base_url = $hostPrefix.TrimEnd("/")
        status = "enabled"
        health_check_enabled = $true
        metadata = @{ openapi_path = "/openapi.json" }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections" -Operation "GET /v1/service-connections" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/templates" -Operation "GET /v1/service-connections/templates" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/$providerConnectionID" -Operation "GET /v1/service-connections/{connection_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/service-connections/$providerConnectionID" -Operation "PUT /v1/service-connections/{connection_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        connection_id = $providerConnectionID
        connection_type = "http_api"
        name = "All Interface ToolHost Connection Updated"
        base_url = $hostPrefix.TrimEnd("/")
        status = "enabled"
        health_check_enabled = $true
        metadata = @{ openapi_path = "/openapi.json" }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections/$providerConnectionID/test" -Operation "POST /v1/service-connections/{connection_id}/test" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections/$providerConnectionID/disable" -Operation "POST /v1/service-connections/{connection_id}/disable" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections/$providerConnectionID/enable" -Operation "POST /v1/service-connections/{connection_id}/enable" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/$providerConnectionID/resources" -Operation "GET /v1/service-connections/{connection_id}/resources" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$adapterProviderID/operations/from-resource" -Operation "POST /v1/tool-providers/{provider_id}/operations/from-resource" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        service_connection_id = $providerConnectionID
        resource_id = "POST /adapter/search"
        operation_id = "adapter.search.generated"
        tool_id = "adapter.search.generated"
        status = "draft"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/$providerConnectionID/health-events" -Operation "GET /v1/service-connections/{connection_id}/health-events" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/$providerConnectionID/impact" -Operation "GET /v1/service-connections/{connection_id}/impact" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/$providerConnectionID/usage" -Operation "GET /v1/service-connections/{connection_id}/usage" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/$providerConnectionID/usage?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z" -Operation "GET /v1/service-connections/{connection_id}/usage" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections/$providerConnectionID/secret-rotations" -Operation "POST /v1/service-connections/{connection_id}/secret-rotations" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        auth_type = "api_key"
        auth_ref = "secret://e2e/$providerConnectionID/rotated"
        reason = "all-interface smoke rotation"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/service-connections/$providerConnectionID/secret-rotations" -Operation "GET /v1/service-connections/{connection_id}/secret-rotations" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers" -Operation "POST /v1/tool-providers" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        provider_id = $providerID
        provider_type = "static_tool_host"
        name = "All Interface ToolHost"
        service_connection_id = $providerConnectionID
        status = "enabled"
        version = "v1"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-providers" -Operation "GET /v1/tool-providers" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-providers/$providerID" -Operation "GET /v1/tool-providers/{provider_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/tool-providers/$providerID" -Operation "PUT /v1/tool-providers/{provider_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        provider_id = $providerID
        provider_type = "static_tool_host"
        name = "All Interface ToolHost Updated"
        service_connection_id = $providerConnectionID
        status = "enabled"
        version = "v2"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$providerID/health?trace_id=$traceID" -Operation "POST /v1/tool-providers/{provider_id}/health" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$providerID/sync" -Operation "POST /v1/tool-providers/{provider_id}/sync" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-provider-governance?provider_id=$providerID" -Operation "GET /v1/tool-provider-governance" -ExpectedStatus @(200) | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections" -Operation "POST /v1/service-connections" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        connection_id = $mcpConnectionID
        connection_type = "http_api"
        name = "All Interface MCP Connection"
        base_url = "$($hostPrefix.TrimEnd('/'))/mcp"
        status = "enabled"
        health_check_enabled = $true
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers" -Operation "POST /v1/tool-providers" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        provider_id = $mcpProviderID
        provider_type = "mcp"
        name = "All Interface MCP"
        service_connection_id = $mcpConnectionID
        status = "enabled"
        version = "v1"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$mcpProviderID/health?trace_id=trace_mcp_health_$suffix" -Operation "POST /v1/tool-providers/{provider_id}/health" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    $mcpSync = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$mcpProviderID/sync" -Operation "POST /v1/tool-providers/{provider_id}/sync" -ExpectedStatus @(200) -Roles "optimizer" -Body @{}
    Assert-AITrue ($mcpSync.Raw -match [regex]::Escape($mcpToolID)) "MCP sync should install tool manifest"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Patch" -Path "/v1/agents/$agentID/tool-bindings" -Operation "PATCH /v1/agents/{agent_id}/tool-bindings" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        agent_version = $stableVersion
        allowed_tool_ids = @("echo", $mcpToolID)
        allowed_tool_group_ids = @()
        denied_tool_ids = @()
        exposed_tool_ids = @($mcpToolID)
    } | Out-Null
    $mcpResult = Invoke-AICommand -BaseUrl $baseUrl -Command "tools.invoke" -Roles "runtime_caller" -TraceID "trace_mcp_call_$suffix" -Target @{ agent_id = $agentID; version = $stableVersion } -Payload @{
        tool_id = $mcpToolID
        arguments = @{ a = 7; b = 5 }
    }
    Assert-AITrue ([string]$mcpResult.status -eq "succeeded") "MCP tools.invoke should succeed"
    Assert-AITrue ([double]$mcpResult.output.structuredContent.total -eq 12) "MCP tools.invoke should return structured total"

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections" -Operation "POST /v1/service-connections" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        connection_id = $adapterConnectionID
        connection_type = "http_api"
        name = "All Interface HTTP Adapter Connection"
        base_url = $hostPrefix.TrimEnd("/")
        status = "enabled"
        health_check_enabled = $true
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$adapterProviderID/operations" -Operation "POST /v1/tool-providers/{provider_id}/operations" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        operation_id = $adapterOperationID
        tool_id = $adapterToolID
        group_id = $toolGroupID
        name = "All adapter search"
        description = "Search through HTTP API adapter."
        when_to_use = @("adapter search")
        service_connection_id = $adapterConnectionID
        method = "POST"
        path = "/adapter/search"
        input_schema = @{ type = "object"; properties = @{ query = @{ type = "string" } }; required = @("query") }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        status = "enabled"
        version = "v1"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-providers/$adapterProviderID/operations" -Operation "GET /v1/tool-providers/{provider_id}/operations" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-providers/$adapterProviderID/operations/$adapterOperationID" -Operation "GET /v1/tool-providers/{provider_id}/operations/{operation_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/tool-providers/$adapterProviderID/operations/$adapterOperationID" -Operation "PUT /v1/tool-providers/{provider_id}/operations/{operation_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        operation_id = $adapterOperationID
        tool_id = $adapterToolID
        group_id = $toolGroupID
        name = "All adapter search updated"
        description = "Search through HTTP API adapter."
        when_to_use = @("adapter search")
        service_connection_id = $adapterConnectionID
        method = "POST"
        path = "/adapter/search"
        input_schema = @{ type = "object"; properties = @{ query = @{ type = "string" } }; required = @("query") }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        status = "enabled"
        version = "v2"
    } | Out-Null
    $adapterTest = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$adapterProviderID/operations/$adapterOperationID/test" -Operation "POST /v1/tool-providers/{provider_id}/operations/{operation_id}/test" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ arguments = @{ query = "adapter" } }
    Assert-AITrue ([string]$adapterTest.Body.output.source -eq "http_api_adapter") "adapter operation test should call HTTP endpoint"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$adapterProviderID/operations/$adapterOperationID/publish" -Operation "POST /v1/tool-providers/{provider_id}/operations/{operation_id}/publish" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$adapterProviderID/sync" -Operation "POST /v1/tool-providers/{provider_id}/sync" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/service-connections" -Operation "POST /v1/service-connections" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        connection_id = $databaseConnectionID
        connection_type = "database"
        name = "All Interface Database Connection"
        status = "enabled"
        base_url = "postgres://example.invalid/all-interface"
        health_check_enabled = $false
        metadata = @{ driver = "postgres" }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$databaseProviderID/operations" -Operation "POST /v1/tool-providers/{provider_id}/operations" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        operation_id = $databaseOperationID
        tool_id = $databaseToolID
        group_id = $toolGroupID
        name = "All database customers"
        description = "Read customers from a database connection."
        when_to_use = @("database customers")
        service_connection_id = $databaseConnectionID
        resource_id = "public.customers"
        query_template = "select id, name from customers where status = :status limit :limit"
        input_schema = @{ type = "object"; properties = @{ status = @{ type = "string" }; limit = @{ type = "integer" } }; required = @("status") }
        parameter_schema = @{ type = "object"; properties = @{ status = @{ type = "string" }; limit = @{ type = "integer" } }; required = @("status") }
        output_schema = @{ type = "object" }
        max_rows = 50
        read_only = $true
        risk_level = "low"
        visibility = "protected"
        status = "draft"
        version = "v1"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-providers/$databaseProviderID/operations" -Operation "GET /v1/tool-providers/{provider_id}/operations database adapter definitions" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-groups" -Operation "POST /v1/tool-groups" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        group_id = $toolGroupID
        name = "All Interface Tools"
        description = "All interface group"
        status = "enabled"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-groups" -Operation "GET /v1/tool-groups" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-groups/$toolGroupID" -Operation "GET /v1/tool-groups/{group_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/tool-groups/$toolGroupID" -Operation "PUT /v1/tool-groups/{group_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        group_id = $toolGroupID
        name = "All Interface Tools Updated"
        description = "Updated"
        status = "enabled"
        version = "v2"
    } | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-manifests" -Operation "POST /v1/tool-manifests" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        tool_id = $toolID
        group_id = $toolGroupID
        name = "All local echo"
        description = "All interface local tool"
        when_to_use = @("echo")
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        executor = @{ type = "static_tool_host"; provider_id = $providerID; operation = "echo" }
        status = "enabled"
        version = "v1"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-manifests" -Operation "GET /v1/tool-manifests" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tool-manifests/$toolID" -Operation "GET /v1/tool-manifests/{tool_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/tool-manifests/$toolID" -Operation "PUT /v1/tool-manifests/{tool_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        tool_id = $toolID
        group_id = $toolGroupID
        name = "All local echo updated"
        description = "Updated all interface local tool"
        when_to_use = @("echo")
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        executor = @{ type = "static_tool_host"; provider_id = $providerID; operation = "echo" }
        status = "enabled"
        version = "v2"
    } | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/tool-bindings" -Operation "PUT /v1/agents/{agent_id}/tool-bindings" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        tool_bindings = @{
            allowed_tool_ids = @("echo", $toolID, $adapterToolID, "all.remote.echo")
            allowed_tool_group_ids = @($toolGroupID, "all.tools")
            denied_tool_ids = @()
            exposed_tool_ids = @($toolID, $adapterToolID)
        }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/tool-bindings" -Operation "GET /v1/agents/{agent_id}/tool-bindings" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Patch" -Path "/v1/agents/$agentID/tool-bindings" -Operation "PATCH /v1/agents/{agent_id}/tool-bindings" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        tool_bindings = @{
            allowed_tool_ids = @("echo", $toolID, $adapterToolID, "all.remote.echo")
            allowed_tool_group_ids = @($toolGroupID, "all.tools")
            denied_tool_ids = @("origin.agent.delegate")
            exposed_tool_ids = @($toolID, $adapterToolID)
        }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/tool-bindings" -Operation "POST /v1/agents/{agent_id}/tool-bindings" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        tool_bindings = @{ allowed_tool_ids = @("echo", $toolID, $adapterToolID, "all.remote.echo"); allowed_tool_group_ids = @($toolGroupID, "all.tools"); denied_tool_ids = @(); exposed_tool_ids = @($toolID, $adapterToolID) }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/tool-bindings/governance" -Operation "GET /v1/agents/{agent_id}/tool-bindings/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/tool-bindings/versions" -Operation "GET /v1/agents/{agent_id}/tool-bindings/versions" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/tool-bindings/activate" -Operation "POST /v1/agents/{agent_id}/tool-bindings/activate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ agent_version = $stableVersion } | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/prompt-profile" -Operation "POST /v1/agents/{agent_id}/prompt-profile" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        identity_prompt = "All interface identity"
        system_prompt = "All interface system"
        developer_prompt = "All interface developer"
        agents_md = "# All Interface Profile"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/prompt-profile" -Operation "GET /v1/agents/{agent_id}/prompt-profile" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/prompt-profile" -Operation "PUT /v1/agents/{agent_id}/prompt-profile" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        identity_prompt = "All interface identity put"
        system_prompt = "All interface system put"
        developer_prompt = "All interface developer put"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Patch" -Path "/v1/agents/$agentID/prompt-profile" -Operation "PATCH /v1/agents/{agent_id}/prompt-profile" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        developer_prompt = "All interface developer patch"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/prompt-profile/preview" -Operation "POST /v1/agents/{agent_id}/prompt-profile/preview" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ input = "preview"; agent_version = $stableVersion } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/prompt-profile/governance" -Operation "GET /v1/agents/{agent_id}/prompt-profile/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/prompt-profile/versions" -Operation "GET /v1/agents/{agent_id}/prompt-profile/versions" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/prompt-profile/activate" -Operation "POST /v1/agents/{agent_id}/prompt-profile/activate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ agent_version = $stableVersion } | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/skills" -Operation "POST /v1/agents/{agent_id}/skills" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        skill_id = $skillID
        name = "All Interface Skill"
        description = "All interface skill"
        instruction = "Answer with ok."
        risk_level = "low"
        when_to_use = @("all interface")
        allowed_tools = @($toolID)
        recommended_tools = @($toolID)
        output_requirements = @("concise")
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/skills" -Operation "GET /v1/agents/{agent_id}/skills" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/skills/$skillID" -Operation "GET /v1/agents/{agent_id}/skills/{skill_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/skills/$skillID" -Operation "PUT /v1/agents/{agent_id}/skills/{skill_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        skill_id = $skillID
        name = "All Interface Skill Updated"
        description = "Updated"
        instruction = "Answer with ok."
        risk_level = "low"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/skills/governance" -Operation "GET /v1/agents/{agent_id}/skills/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/skills/$skillID/governance" -Operation "GET /v1/agents/{agent_id}/skills/{skill_id}/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/skills/$skillID/versions" -Operation "GET /v1/agents/{agent_id}/skills/{skill_id}/versions" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/skills/$skillID/activate" -Operation "POST /v1/agents/{agent_id}/skills/{skill_id}/activate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ agent_version = $stableVersion } | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents" -Operation "POST /v1/agents" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        agent_id = $collaboratorID
        name = "All Collaborator"
        owner_id = "all-interface"
        version = "v1"
        prompt = "Collaborator."
        metadata = @{ max_tool_calls = 0 }
    } | Out-Null
    $collabDraft = Invoke-AICommand -BaseUrl $baseUrl -Command "agent.package.draft.create" -Roles "optimizer" -Payload @{ agent_id = $collaboratorID; version = "v2"; prompt = "Collaborator v2"; metadata = @{ max_tool_calls = 0 } }
    Invoke-AICommand -BaseUrl $baseUrl -Command "agent.package.draft.validate" -Roles "optimizer" -Payload @{ draft_id = $collabDraft.draft_id } | Out-Null
    $collabPublished = Invoke-AICommand -BaseUrl $baseUrl -Command "agent.package.publish" -Roles "optimizer" -Payload @{ draft_id = $collabDraft.draft_id }
    Invoke-AICommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -Payload @{ package_version_id = $collabPublished.package_version_id; canary_percent = 100 } | Out-Null
    Invoke-AICommand -BaseUrl $baseUrl -Command "eval.run" -Roles "optimizer" -Target @{ agent_id = $collaboratorID; version = "v2" } -Payload @{ package_version_id = $collabPublished.package_version_id; input = "hello"; final_reply_contains = @("ok"); should_end_status = "completed"; max_tool_calls = 0 } | Out-Null
    Invoke-AICommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -Payload @{ package_version_id = $collabPublished.package_version_id } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/collaborators" -Operation "POST /v1/agents/{agent_id}/collaborators" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        agent_id = $collaboratorID
        name = "All Collaborator"
        description = "Collaborator"
        when_to_use = @("review")
        capabilities = @("review")
        allowed_handoff_modes = @("hybrid")
        default_handoff_mode = "hybrid"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/collaborators" -Operation "PUT /v1/agents/{agent_id}/collaborators" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        draft_id = $stableDraftID
        collaborators = @(@{
            agent_id = $collaboratorID
            version = "v2"
            name = "All Collaborator"
            description = "Collaborator draft replacement"
            when_to_use = @("review")
            capabilities = @("review")
            allowed_handoff_modes = @("hybrid")
            default_handoff_mode = "hybrid"
            status = "enabled"
        })
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/collaborators" -Operation "GET /v1/agents/{agent_id}/collaborators" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/collaborators/$collaboratorID" -Operation "GET /v1/agents/{agent_id}/collaborators/{collaborator_agent_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/collaborators/$collaboratorID" -Operation "POST /v1/agents/{agent_id}/collaborators/{collaborator_agent_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = "v2"
        name = "All Collaborator Posted"
        when_to_use = @("review")
        capabilities = @("review")
        allowed_handoff_modes = @("hybrid")
        default_handoff_mode = "hybrid"
        status = "enabled"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/collaborators/$collaboratorID" -Operation "PUT /v1/agents/{agent_id}/collaborators/{collaborator_agent_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        agent_id = $collaboratorID
        name = "All Collaborator Updated"
        when_to_use = @("review")
        capabilities = @("review")
        allowed_handoff_modes = @("hybrid")
        default_handoff_mode = "hybrid"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/collaborators/governance" -Operation "GET /v1/agents/{agent_id}/collaborators/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/collaborators/$collaboratorID/governance" -Operation "GET /v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/collaborators/$collaboratorID/versions" -Operation "GET /v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/versions" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/collaborators/$collaboratorID/activate" -Operation "POST /v1/agents/{agent_id}/collaborators/{collaborator_agent_id}/activate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ agent_version = $stableVersion } | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/exported-tools" -Operation "POST /v1/agents/{agent_id}/exported-tools" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        tool_id = $exportedToolID
        group_id = $toolGroupID
        operation = "echo"
        name = "All exported tool"
        description = "Exported agent tool"
        when_to_use = @("agent export")
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        status = "enabled"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/exported-tools" -Operation "PUT /v1/agents/{agent_id}/exported-tools" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        draft_id = $stableDraftID
        tools = @(@{
            tool_id = $exportedToolID
            group_id = $toolGroupID
            operation = "echo"
            name = "All exported tool"
            description = "Exported agent tool draft replacement"
            when_to_use = @("agent export")
            input_schema = @{ type = "object" }
            output_schema = @{ type = "object" }
            risk_level = "low"
            visibility = "protected"
            status = "enabled"
            version = "v1"
        })
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/exported-tools" -Operation "GET /v1/agents/{agent_id}/exported-tools" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/exported-tools/$exportedToolID" -Operation "GET /v1/agents/{agent_id}/exported-tools/{tool_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/exported-tools/$exportedToolID" -Operation "POST /v1/agents/{agent_id}/exported-tools/{tool_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        group_id = $toolGroupID
        operation = "echo"
        name = "All exported tool posted"
        description = "Exported agent tool posted"
        when_to_use = @("agent export")
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        status = "enabled"
        version = "v1"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/exported-tools/$exportedToolID" -Operation "PUT /v1/agents/{agent_id}/exported-tools/{tool_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        version = $stableVersion
        tool_id = $exportedToolID
        group_id = $toolGroupID
        operation = "echo"
        name = "All exported tool updated"
        description = "Exported agent tool updated"
        when_to_use = @("agent export")
        input_schema = @{ type = "object" }
        output_schema = @{ type = "object" }
        risk_level = "low"
        visibility = "protected"
        status = "enabled"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/exported-tools/governance" -Operation "GET /v1/agents/{agent_id}/exported-tools/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/exported-tools/$exportedToolID/governance" -Operation "GET /v1/agents/{agent_id}/exported-tools/{tool_id}/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/exported-tools/$exportedToolID/versions" -Operation "GET /v1/agents/{agent_id}/exported-tools/{tool_id}/versions" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/exported-tools/$exportedToolID/activate" -Operation "POST /v1/agents/{agent_id}/exported-tools/{tool_id}/activate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ agent_version = $stableVersion } | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/runtime-hook-providers" -Operation "POST /v1/runtime-hook-providers" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        provider_id = $hookProviderID
        provider_type = "static_hook_host"
        name = "All Hook Host"
        endpoint = $hostPrefix.TrimEnd("/")
        status = "enabled"
        health_status = "healthy"
        version = "v1"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-providers" -Operation "GET /v1/runtime-hook-providers" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-providers/$hookProviderID" -Operation "GET /v1/runtime-hook-providers/{provider_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/runtime-hook-providers/$hookProviderID" -Operation "PUT /v1/runtime-hook-providers/{provider_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        provider_id = $hookProviderID
        provider_type = "static_hook_host"
        name = "All Hook Host Updated"
        endpoint = $hostPrefix.TrimEnd("/")
        status = "enabled"
        health_status = "healthy"
        version = "v2"
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/runtime-hook-providers/$hookProviderID/health?trace_id=$traceID" -Operation "POST /v1/runtime-hook-providers/{provider_id}/health" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/runtime-hook-providers/$hookProviderID/catalog" -Operation "POST /v1/runtime-hook-providers/{provider_id}/catalog" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/runtime-hook-providers/$hookProviderID/catalog/sync" -Operation "POST /v1/runtime-hook-providers/{provider_id}/catalog/sync" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/runtime-hook-manifests" -Operation "POST /v1/runtime-hook-manifests" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        hook_id = $goHookID
        name = "All Go hook"
        phase = "before_model_call"
        status = "enabled"
        version = "v1"
        failure_policy = "ignore"
        config_schema = @{ type = "object" }
        patch_schema = @{ type = "object" }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-manifests" -Operation "GET /v1/runtime-hook-manifests" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-manifests/$goHookID" -Operation "GET /v1/runtime-hook-manifests/{hook_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/runtime-hook-manifests/$goHookID" -Operation "PUT /v1/runtime-hook-manifests/{hook_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        hook_id = $goHookID
        name = "All Go hook updated"
        phase = "before_model_call"
        status = "enabled"
        version = "v2"
        failure_policy = "ignore"
        patch_schema = @{ type = "object" }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-manifests/$goHookID/versions" -Operation "GET /v1/runtime-hook-manifests/{hook_id}/versions" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-manifests/$goHookID/versions/v2" -Operation "GET /v1/runtime-hook-manifests/{hook_id}/versions/{version}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/runtime-hook-manifests/$goHookID/versions/v2/activate" -Operation "POST /v1/runtime-hook-manifests/{hook_id}/versions/{version}/activate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/runtime-hooks" -Operation "PUT /v1/agents/{agent_id}/runtime-hooks" -ExpectedStatus @(200) -Roles "optimizer" -Body @{
        agent_version = $stableVersion
        hook_id = $goHookID
        provider_type = "go"
        phase = "before_model_call"
        enabled = $true
        failure_policy = "ignore"
        config = @{
            patch = @{
                add_context_blocks = @(@{ id = "all-hook"; title = "All hook"; content = "all hook context" })
            }
        }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/$agentID/runtime-hooks?agent_version=$stableVersion" -Operation "GET /v1/agents/{agent_id}/runtime-hooks" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents/$agentID/runtime-hooks/preview" -Operation "POST /v1/agents/{agent_id}/runtime-hooks/preview" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ agent_version = $stableVersion; phase = "before_model_call"; input = "preview"; trace_id = $traceID } | Out-Null

    $run = Invoke-AICommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -TraceID $traceID -Target @{ agent_id = $agentID; version = $stableVersion } -Payload @{ input = "hello all interfaces" }
    $taskID = [string]$run.task_id
    Assert-AIStatus $taskID "agent.run should return task_id"
    $toolResult = Invoke-AICommand -BaseUrl $baseUrl -Command "tools.invoke" -Roles "runtime_caller" -TraceID $toolTraceID -Target @{ agent_id = $agentID; version = $stableVersion } -Payload @{ tool_id = $toolID; arguments = @{ value = "ok" } }
    $toolCallID = [string]$toolResult.tool_call_id
    Assert-AIStatus $toolCallID "tools.invoke should return tool_call_id"
    $adapterToolResult = Invoke-AICommand -BaseUrl $baseUrl -Command "tools.invoke" -Roles "runtime_caller" -TraceID "trace_all_adapter_$suffix" -Target @{ agent_id = $agentID; version = $stableVersion } -Payload @{ tool_id = $adapterToolID; arguments = @{ query = "adapter" } }
    Assert-AITrue ([string]$adapterToolResult.status -eq "succeeded") "HTTP API adapter tools.invoke should succeed"
    Assert-AITrue ([string]$adapterToolResult.output.source -eq "http_api_adapter") "HTTP API adapter tools.invoke should return adapter output"

    $toolTrace = Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tools/$toolCallID/trace" -Operation "GET /v1/tools/{tool_call_id}/trace" -ExpectedStatus @(200)
    Assert-AIStatus ([string]$toolTrace.Body.tool_call.tool_version) "tool trace should include tool_call.tool_version"
    Assert-AIStatus ([string]$toolTrace.Body.tool_call.execution_profile) "tool trace should include tool_call.execution_profile"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/traces/$traceID" -Operation "GET /v1/traces/{trace_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/traces/$traceID/replay" -Operation "GET /v1/traces/{trace_id}/replay" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/metrics/governance?trace_id=$traceID" -Operation "GET /v1/metrics/governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/usage/evidence?trace_id=$traceID" -Operation "GET /v1/usage/evidence" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/audit" -Operation "GET /v1/audit" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-events?trace_id=$traceID" -Operation "GET /v1/runtime-hook-events" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-governance?trace_id=$traceID" -Operation "GET /v1/runtime-hook-governance" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/runtime-hook-approvals" -Operation "GET /v1/runtime-hook-approvals" -ExpectedStatus @(200) | Out-Null

    $task = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tasks/start" -Operation "POST /v1/tasks/start" -ExpectedStatus @(201) -Roles "runtime_caller" -Body @{
        agent_id = $agentID
        agent_version = $stableVersion
        title = "All interface task"
        objective = "Test every interface"
        trace_id = "trace_all_task_$suffix"
    }
    $resourceTaskID = [string]$task.Body.task.task_id
    Assert-AIStatus $resourceTaskID "tasks/start should return task"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tasks/$resourceTaskID" -Operation "GET /v1/tasks/{task_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AICommand -BaseUrl $baseUrl -Command "task.command" -Roles "runtime_caller" -Payload @{ task_id = $resourceTaskID; command = "create_plan"; objective = "Plan all interfaces"; steps = @(@{ title = "Call every interface" }, @{ title = "Verify report" }) } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tasks/$resourceTaskID/plan" -Operation "GET /v1/tasks/{task_id}/plan" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tasks/$resourceTaskID/timeline" -Operation "GET /v1/tasks/{task_id}/timeline" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/tasks/$resourceTaskID/recovery" -Operation "GET /v1/tasks/{task_id}/recovery" -ExpectedStatus @(200) | Out-Null

    $handoff = Invoke-AICommand -BaseUrl $baseUrl -Command "task.command" -Roles "runtime_caller" -TraceID "trace_all_handoff_$suffix" -Payload @{
        task_id = $resourceTaskID
        command = "create_handoff"
        to_agent_id = $collaboratorID
        to_agent_version = "v2"
        objective = "Review all-interface evidence"
        handoff_mode = "hybrid"
        reason = "interface coverage"
    }
    $handoffID = [string]$handoff.handoff.handoff_id
    Assert-AIStatus $handoffID "handoff create should return id"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/handoffs/$handoffID" -Operation "GET /v1/handoffs/{handoff_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/handoffs/$handoffID/trace" -Operation "GET /v1/handoffs/{handoff_id}/trace" -ExpectedStatus @(200) | Out-Null

    $externalTask = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tasks/start" -Operation "POST /v1/tasks/start" -ExpectedStatus @(201) -Roles "runtime_caller" -Body @{
        agent_id = $agentID
        agent_version = $stableVersion
        title = "External task"
        objective = "External binding"
    }
    $externalTaskID = [string]$externalTask.Body.task.task_id
    Invoke-AICommand -BaseUrl $baseUrl -Command "task.start" -Roles "runtime_caller" -Payload @{
        agent_id = $agentID
        agent_version = $stableVersion
        title = "Bound external task"
        objective = "Check external lookup"
    } -Context @{
        tenant_id = $tenantID
        collaboration = @{ provider = "array"; external_task_id = "ext-$suffix" }
    } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/external-tasks/array/ext-$suffix" -Operation "GET /v1/external-tasks/{provider}/{external_task_id}" -ExpectedStatus @(200) | Out-Null
    Assert-AIStatus $externalTaskID "external task setup should return task"

    Invoke-AICommand -BaseUrl $baseUrl -Command "task.start" -Roles "runtime_caller" -Payload @{
        agent_id = $agentID
        agent_version = $stableVersion
        title = "A2A bound external task"
        objective = "Check A2A external lookup"
    } -Context @{
        tenant_id = $tenantID
        collaboration = @{ provider = "a2a"; external_task_id = "a2a-ext-$suffix" }
    } | Out-Null
    $a2aExternal = Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/external-tasks/a2a/a2a-ext-$suffix" -Operation "GET /v1/external-tasks/{provider}/{external_task_id}" -ExpectedStatus @(200)
    Assert-AITrue ([string]$a2aExternal.Body.status -eq "working") "A2A external task lookup should return remote task status"

    $template = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/governance/templates" -Operation "POST /v1/governance/templates" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        name = "All Interface Governance"
        version = "v1"
        status = "active"
        gates = @(@{
            gate_id = "committee"
            name = "Committee"
            subject_type = "agent"
            review_mode = "multi"
            consensus_policy = "all"
            escalation_policy = "orchestrator"
            required_reviewers = 2
        })
    }
    $templateID = [string]$template.Body.template.template_id
    Invoke-AIJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/governance/templates" -Operation "PUT /v1/governance/templates" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        template_id = $templateID
        name = "All Interface Governance Updated"
        version = "v2"
        status = "active"
        gates = @(@{ gate_id = "committee"; review_mode = "multi"; consensus_policy = "all"; escalation_policy = "orchestrator"; required_reviewers = 2 })
    } | Out-Null
    $govRun = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/governance/runs" -Operation "POST /v1/governance/runs" -ExpectedStatus @(201) -Roles "optimizer" -Body @{
        template_id = $templateID
        subject_type = "agent"
        subject_id = $agentID
        trace_id = "trace_all_governance_$suffix"
    }
    $govRunID = [string]$govRun.Body.snapshot.process_run.run_id
    $gateRunID = [string]$govRun.Body.snapshot.gates[0].gate_run_id
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/governance/runs/$govRunID" -Operation "GET /v1/governance/runs/{run_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/governance/runs/$govRunID/gates/open" -Operation "POST /v1/governance/runs/{run_id}/gates/open" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ gate_run_id = $gateRunID; evidence_refs = @(@{ type = "trace"; trace_id = $traceID; summary = "trace evidence" }) } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/governance/gates/$gateRunID/reviews" -Operation "POST /v1/governance/gates/{gate_run_id}/reviews" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ reviewer_id = "reviewer-a"; decision = "approve"; reason = "ok" } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/governance/gates/$gateRunID/reviews" -Operation "POST /v1/governance/gates/{gate_run_id}/reviews" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ reviewer_id = "reviewer-b"; decision = "reject"; reason = "needs arbitration" } | Out-Null
    $escalated = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/governance/gates/$gateRunID/escalate" -Operation "POST /v1/governance/gates/{gate_run_id}/escalate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ issue = "split decision"; escalated_to = "orchestrator" }
    $conflictID = [string]$escalated.Body.snapshot.conflicts[0].conflict_id
    Assert-AIStatus $conflictID "governance escalate should return conflict"
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/governance/conflicts/$conflictID/arbitrate" -Operation "POST /v1/governance/conflicts/{conflict_id}/arbitrate" -ExpectedStatus @(200) -Roles "optimizer" -Body @{ decision = "approve"; reason = "arbitrated" } | Out-Null

    $kb = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/knowledge-bases" -Operation "POST /v1/knowledge-bases" -ExpectedStatus @(201) -Roles "admin" -Body @{
        owner_group_id = "group-a"
        name = "All Interface KB"
        visibility = "shared"
        index_type = "hybrid"
        source_type = "text"
    }
    $kbID = [string]$kb.Body.knowledge_base.knowledge_base_id
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/knowledge-bases" -Operation "GET /v1/knowledge-bases" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/knowledge-bases/$kbID" -Operation "GET /v1/knowledge-bases/{knowledge_base_id}" -ExpectedStatus @(200) | Out-Null
    $doc = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/knowledge-bases/$kbID/documents" -Operation "POST /v1/knowledge-bases/{knowledge_base_id}/documents" -ExpectedStatus @(201) -Roles "admin" -Body @{
        source_group_id = "group-a"
        title = "All Interface Launch"
        content = "Launch owner is alex@example.com and all interface coverage says ok."
        visibility = "shared"
    }
    $jobID = [string]$doc.Body.index_job.job_id
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/knowledge-bases/$kbID/index-jobs" -Operation "GET /v1/knowledge-bases/{knowledge_base_id}/index-jobs" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/knowledge-bases/$kbID/index-jobs/$jobID" -Operation "GET /v1/knowledge-bases/{knowledge_base_id}/index-jobs/{job_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/knowledge-search" -Operation "POST /v1/knowledge-search" -ExpectedStatus @(200) -Roles "admin" -Body @{ requester_group_id = "group-a"; query = "Launch"; search_mode = "hybrid"; limit = 3 } | Out-Null
    $share = Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/cross-group-share-policies" -Operation "POST /v1/cross-group-share-policies" -ExpectedStatus @(201) -Roles "admin" -Body @{
        source_group_id = "group-a"
        target_group_id = "group-b"
        knowledge_base_ids = @($kbID)
        redaction_policy = "mask_emails"
        status = "enabled"
    }
    $policyID = [string]$share.Body.policy.policy_id
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/cross-group-share-policies" -Operation "GET /v1/cross-group-share-policies" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/cross-group-share-policies/$policyID" -Operation "GET /v1/cross-group-share-policies/{policy_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Patch" -Path "/v1/cross-group-share-policies/$policyID" -Operation "PATCH /v1/cross-group-share-policies/{policy_id}" -ExpectedStatus @(200) -Roles "admin" -Body @{ source_group_id = "group-a"; target_group_id = "group-b"; knowledge_base_ids = @($kbID); redaction_policy = "summary_only"; status = "enabled" } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/cross-groups/search" -Operation "POST /v1/cross-groups/search" -ExpectedStatus @(200) -Roles "admin" -Body @{ request_group_id = "group-b"; source_group_id = "group-a"; query = "Launch"; limit = 3 } | Out-Null

    $suite = Invoke-AICommand -BaseUrl $baseUrl -Command "eval.suite.create" -Roles "optimizer" -Payload @{ name = "all-interface-suite"; min_pass_rate = 1; require_critical_pass = $true; require_safety_pass = $true } -Target @{ agent_id = $agentID; version = $stableVersion }
    $suiteID = [string]$suite.suite_id
    Invoke-AICommand -BaseUrl $baseUrl -Command "eval.suite.add_case" -Roles "optimizer" -Payload @{ suite_id = $suiteID; name = "ok"; input = "hello"; target = @{ agent_id = $agentID; version = $stableVersion }; final_reply_contains = @("ok"); should_end_status = "completed"; max_tool_calls = 0 } -Target @{ agent_id = $agentID; version = $stableVersion } | Out-Null
    $suiteRun = Invoke-AICommand -BaseUrl $baseUrl -Command "eval.suite.run" -Roles "optimizer" -Payload @{ suite_id = $suiteID } -Target @{ agent_id = $agentID; version = $stableVersion }
    $evalRunID = [string]$suiteRun.eval_run_id
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/evals/suites/$suiteID" -Operation "GET /v1/evals/suites/{suite_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/evals/results/$evalRunID" -Operation "GET /v1/evals/results/{eval_run_id}" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agent-packages/canary-hits?agent_id=$agentID" -Operation "GET /v1/agent-packages/canary-hits" -ExpectedStatus @(200) | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/agents/capabilities" -Operation "GET /v1/agents/capabilities" -ExpectedStatus @(200) | Out-Null

    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/agents/$agentID/skills/$skillID" -Operation "DELETE /v1/agents/{agent_id}/skills/{skill_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/agents/$agentID/collaborators/$collaboratorID" -Operation "DELETE /v1/agents/{agent_id}/collaborators/{collaborator_agent_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/agents/$agentID/exported-tools/$exportedToolID" -Operation "DELETE /v1/agents/{agent_id}/exported-tools/{tool_id}" -ExpectedStatus @(200) -Roles "optimizer" -Body @{} | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/agents/$agentID/prompt-profile" -Operation "DELETE /v1/agents/{agent_id}/prompt-profile" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/agents/$agentID/tool-bindings" -Operation "DELETE /v1/agents/{agent_id}/tool-bindings" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/cross-group-share-policies/$policyID" -Operation "DELETE /v1/cross-group-share-policies/{policy_id}" -ExpectedStatus @(200) -Roles "admin" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/runtime-hook-manifests/$goHookID" -Operation "DELETE /v1/runtime-hook-manifests/{hook_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/runtime-hook-providers/$hookProviderID" -Operation "DELETE /v1/runtime-hook-providers/{provider_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/tool-manifests/$adapterToolID" -Operation "DELETE /v1/tool-manifests/{tool_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/tool-manifests/$toolID" -Operation "DELETE /v1/tool-manifests/{tool_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/tool-groups/$toolGroupID" -Operation "DELETE /v1/tool-groups/{group_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/tool-providers/$mcpProviderID" -Operation "DELETE /v1/tool-providers/{provider_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/tool-providers/$providerID" -Operation "DELETE /v1/tool-providers/{provider_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/service-connections/$databaseConnectionID" -Operation "DELETE /v1/service-connections/{connection_id}" -ExpectedStatus @(204) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/service-connections/$adapterConnectionID" -Operation "DELETE /v1/service-connections/{connection_id}" -ExpectedStatus @(204) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/service-connections/$mcpConnectionID" -Operation "DELETE /v1/service-connections/{connection_id}" -ExpectedStatus @(204) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/service-connections/$providerConnectionID" -Operation "DELETE /v1/service-connections/{connection_id}" -ExpectedStatus @(204) -Roles "optimizer" | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents" -Operation "POST /v1/agents" -ExpectedStatus @(201) -Roles "optimizer" -Body @{ agent_id = $deleteAgentID; name = "Delete Agent"; owner_id = "all-interface" } | Out-Null
    Invoke-AIJson -BaseUrl $baseUrl -Method "Delete" -Path "/v1/agents/$deleteAgentID" -Operation "DELETE /v1/agents/{agent_id}" -ExpectedStatus @(200) -Roles "optimizer" | Out-Null

    $expected = Get-AIOpenAPIOperations
    $missing = @($expected | Where-Object { -not $script:Coverage.Contains($_) })
    if ($missing.Count -gt 0) {
        throw "all-interface coverage missing operations: $($missing -join '; ')"
    }

    $summary.status = "passed"
    $summary.agent_id = $agentID
    $summary.trace_id = $traceID
    $summary.tool_call_id = $toolCallID
    $summary.task_id = $resourceTaskID
    $summary.operation_count = $expected.Count
    $summary.call_count = $script:Calls.Count
    $summary.coverage = $script:Coverage
    $summary.calls = $script:Calls.ToArray()
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "all-interfaces-report.json") -Value $summary
    Write-Host "CleanCore all-interface E2E passed. operations=$($expected.Count) calls=$($script:Calls.Count) report=$ReportDir"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.coverage = $script:Coverage
    $summary.calls = $script:Calls.ToArray()
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "all-interfaces-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    if ($null -ne $hostJob) {
        Stop-Job $hostJob -ErrorAction SilentlyContinue | Out-Null
        Remove-Job $hostJob -ErrorAction SilentlyContinue | Out-Null
    }
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
