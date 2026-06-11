param(
    [int]$Port = 0,
    [string]$ReportDir = "",
    [switch]$KeepServer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "clean-core-async-resilience"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_SERVICE_TOKEN)) {
    $env:CLEAN_CORE_SERVICE_TOKEN = "dev-token"
}
if ([string]::IsNullOrWhiteSpace($env:GOCACHE)) {
    $env:GOCACHE = Join-Path (Get-RepoRoot) ".gocache"
}

$server = $null
$slowHostProcess = $null
$fastHostProcess = $null
$slowInvokeJob = $null
$tenantID = "tenant_async_resilience"
$suffix = Get-Date -Format "yyyyMMddHHmmssfff"
$agentID = "async-resilience-agent-$suffix"
$toolGroupID = "crm.async.resilience"
$slowToolID = "crm.async.slow"
$fastToolID = "crm.async.lookup"
$slowDelayMs = 1500
$script:CurrentStep = ""

$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    tenant_id = $tenantID
    agent_id = $agentID
    execution_mode = "async"
    model_provider = "stub"
    slow_tool_delay_ms = $slowDelayMs
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    steps = [ordered]@{}
}

function ConvertTo-AsyncJsonText {
    param(
        [AllowNull()] $Value
    )
    if ($null -eq $Value) {
        return "{}"
    }
    return $Value | ConvertTo-Json -Depth 80 -Compress
}

function Read-AsyncResponseBody {
    param(
        [AllowNull()] $Response
    )
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

function Invoke-AsyncJson {
    param(
        [Parameter(Mandatory = $true)] [string]$BaseUrl,
        [Parameter(Mandatory = $true)] [string]$Method,
        [Parameter(Mandatory = $true)] [string]$Path,
        [AllowNull()] $Body = $null,
        [string]$Roles = "admin",
        [string]$TenantID = $tenantID,
        [string]$CallerID = "async-resilience-e2e",
        [string]$CallerType = "service",
        [hashtable]$Headers = @{},
        [switch]$AllowError,
        [int]$TimeoutSec = 120
    )
    $requestHeaders = Get-E2EHeaders -Roles $Roles -TenantID $TenantID -CallerID $CallerID -CallerType $CallerType
    foreach ($key in @($Headers.Keys)) {
        $requestHeaders[$key] = $Headers[$key]
    }
    $args = @{
        Uri = "$BaseUrl$Path"
        Method = $Method
        Headers = $requestHeaders
        TimeoutSec = $TimeoutSec
        UseBasicParsing = $true
    }
    if ($null -ne $Body) {
        $args["Body"] = [System.Text.Encoding]::UTF8.GetBytes((ConvertTo-AsyncJsonText $Body))
    }
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    try {
        $response = Invoke-WebRequest @args
        $stopwatch.Stop()
        $raw = [string]$response.Content
        $parsed = $null
        if (-not [string]::IsNullOrWhiteSpace($raw)) {
            $parsed = $raw | ConvertFrom-Json
        }
        return [pscustomobject]@{
            StatusCode = [int]$response.StatusCode
            Body = $parsed
            Raw = $raw
            DurationMs = [Math]::Round($stopwatch.Elapsed.TotalMilliseconds, 2)
        }
    } catch {
        $stopwatch.Stop()
        $statusCode = -1
        $raw = ""
        if ($_.Exception.Response) {
            $statusCode = [int]$_.Exception.Response.StatusCode
            $raw = Read-AsyncResponseBody -Response $_.Exception.Response
            if ([string]::IsNullOrWhiteSpace($raw) -and $_.ErrorDetails -and $_.ErrorDetails.Message) {
                $raw = $_.ErrorDetails.Message
            }
        } elseif ($_.ErrorDetails -and $_.ErrorDetails.Message) {
            $raw = $_.ErrorDetails.Message
        } else {
            $raw = $_.Exception.Message
        }
        if (-not $AllowError) {
            throw "HTTP $Method $Path failed status=$statusCode body=$raw"
        }
        $parsed = $null
        if (-not [string]::IsNullOrWhiteSpace($raw)) {
            try {
                $parsed = $raw | ConvertFrom-Json
            } catch {
                $parsed = $null
            }
        }
        return [pscustomobject]@{
            StatusCode = $statusCode
            Body = $parsed
            Raw = $raw
            DurationMs = [Math]::Round($stopwatch.Elapsed.TotalMilliseconds, 2)
        }
    }
}

function Invoke-AsyncCommand {
    param(
        [Parameter(Mandatory = $true)] [string]$BaseUrl,
        [Parameter(Mandatory = $true)] [string]$Command,
        [hashtable]$Payload = @{},
        [hashtable]$Target = @{},
        [hashtable]$Context = @{},
        [string]$TraceID = "",
        [string]$Roles = "admin",
        [string]$TenantID = $tenantID,
        [string]$CallerID = "async-resilience-e2e",
        [string]$CallerType = "service",
        [hashtable]$Headers = @{},
        [switch]$AllowError
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
    $response = Invoke-AsyncJson -BaseUrl $BaseUrl -Method "Post" -Path "/v1/commands" -Body $body -Roles $Roles -TenantID $TenantID -CallerID $CallerID -CallerType $CallerType -Headers $Headers -AllowError:$AllowError
    if ($AllowError) {
        return $response
    }
    return $response.Body
}

function Assert-AsyncTrue {
    param(
        [bool]$Condition,
        [string]$Message
    )
    if (-not $Condition) {
        throw $Message
    }
}

function Assert-AsyncEqual {
    param(
        [AllowNull()] $Actual,
        [AllowNull()] $Expected,
        [string]$Message
    )
    if ($Actual -ne $Expected) {
        throw ("{0}: expected={1} actual={2}" -f $Message, $Expected, $Actual)
    }
}

function Mark-AsyncStep {
    param(
        [Parameter(Mandatory = $true)] [string]$ID,
        [Parameter(Mandatory = $true)] [string]$Name,
        [hashtable]$Evidence = @{}
    )
    $summary.steps[$ID] = [ordered]@{
        status = "passed"
        name = $Name
        evidence = $Evidence
        completed_at = (Get-Date).ToUniversalTime().ToString("o")
    }
    Write-Host "$ID passed: $Name"
}

function Start-AsyncToolHostProcess {
    param(
        [string]$Prefix,
        [string]$Name,
        [string]$ToolID,
        [string]$GroupID,
        [string]$Operation,
        [int]$DelayMs,
        [string]$ReportDir
    )
    $uri = [System.Uri]$Prefix
    $addr = "{0}:{1}" -f $uri.Host, $uri.Port
    $root = Get-RepoRoot
    $go = Get-GoCommand
    $args = @(
        "run",
        "./scripts/e2e_toolhost_static.go",
        "-addr", $addr,
        "-name", $Name,
        "-tool-id", $ToolID,
        "-group-id", $GroupID,
        "-operation", $Operation,
        "-delay-ms", ([string]$DelayMs),
        "-log-path", (Join-Path $ReportDir "$Name-toolhost-invocations.ndjson")
    )
    return Start-Process -FilePath $go -ArgumentList $args -WorkingDirectory $root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $ReportDir "$Name-toolhost.out.log") -RedirectStandardError (Join-Path $ReportDir "$Name-toolhost.err.log")
}

function Wait-ForToolHostLog {
    param(
        [string]$Path,
        [int]$TimeoutMs = 3000
    )
    $deadline = (Get-Date).AddMilliseconds($TimeoutMs)
    do {
        if (Test-Path $Path) {
            $item = Get-Item -Path $Path
            if ($item.Length -gt 0) {
                return
            }
        }
        Start-Sleep -Milliseconds 50
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for ToolHost log $Path"
}

function Wait-AsyncToolHostReady {
    param(
        [string]$BaseUrl,
        $Process,
        [int]$TimeoutSeconds = 45
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        if ($null -ne $Process -and $Process.HasExited) {
            throw "ToolHost process exited before readiness at $BaseUrl exit_code=$($Process.ExitCode)"
        }
        try {
            $response = Invoke-RestMethod -Uri "$BaseUrl/healthz" -Method Get -TimeoutSec 2
            if ($response.status -eq "ok") {
                return
            }
        } catch {
        }
        Start-Sleep -Milliseconds 250
    } while ((Get-Date) -lt $deadline)
    throw "ToolHost did not become healthy at $BaseUrl within ${TimeoutSeconds}s process_id=$($Process.Id)"
}

try {
    $slowPrefix = "http://127.0.0.1:{0}/" -f (Get-FreeTcpPort)
    $fastPrefix = "http://127.0.0.1:{0}/" -f (Get-FreeTcpPort)
    $slowHostProcess = Start-AsyncToolHostProcess -Prefix $slowPrefix -Name "slow" -ToolID $slowToolID -GroupID $toolGroupID -Operation "slow" -DelayMs $slowDelayMs -ReportDir $ReportDir
    $fastHostProcess = Start-AsyncToolHostProcess -Prefix $fastPrefix -Name "fast" -ToolID $fastToolID -GroupID $toolGroupID -Operation "lookup" -DelayMs 0 -ReportDir $ReportDir
    Wait-AsyncToolHostReady -BaseUrl $slowPrefix.TrimEnd("/") -Process $slowHostProcess -TimeoutSeconds 45
    Wait-AsyncToolHostReady -BaseUrl $fastPrefix.TrimEnd("/") -Process $fastHostProcess -TimeoutSeconds 45
    $summary.slow_tool_host = $slowPrefix
    $summary.fast_tool_host = $fastPrefix

    $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv @{
        CLEAN_CORE_MODEL_PROVIDER = "stub"
        CLEAN_CORE_SERVICE_TOKEN = $env:CLEAN_CORE_SERVICE_TOKEN
        CLEAN_CORE_AGENT_RUN_EXECUTION_MODE = "async"
        CLEAN_CORE_ENV = "local"
        CLEAN_CORE_LOG_LEVEL = "error"
    }
    $baseUrl = $server.BaseUrl
    $summary.base_url = $baseUrl

    $script:CurrentStep = "ASYNC-E2E-01"
    $health = Invoke-AsyncJson -BaseUrl $baseUrl -Method "Get" -Path "/healthz" -TenantID $tenantID
    $ready = Invoke-AsyncJson -BaseUrl $baseUrl -Method "Get" -Path "/readyz" -TenantID $tenantID
    $readiness = Invoke-AsyncJson -BaseUrl $baseUrl -Method "Get" -Path "/v1/readiness/report" -TenantID $tenantID
    Assert-AsyncEqual $health.Body.status "ok" "healthz status"
    Assert-AsyncEqual $ready.Body.status "ready" "readyz status"
    Mark-AsyncStep "ASYNC-E2E-01" "Service starts in async agent.run mode" @{
        healthz = $health.Body.status
        readyz = $ready.Body.status
        readiness = $readiness.Body.status
    }

    $script:CurrentStep = "ASYNC-E2E-02"
    $agentCreate = Invoke-AsyncJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/agents" -Roles "optimizer" -TenantID $tenantID -CallerID "async-agent-builder" -Body @{
        agent_id = $agentID
        name = "Async Resilience Agent"
        description = "Async resilience E2E agent"
        version = "v1"
        prompt = "You are an async resilience test agent."
        agents_md = "# Async Resilience Agent"
        metadata = @{
            system_prompt = "Return concise test replies."
            developer_prompt = "Use only configured tools."
            max_steps = 3
            max_tool_calls = 2
        }
        tool_bindings = @{
            exposed_tool_ids = @()
            allowed_tool_ids = @()
        }
    }
    $draftID = [string]$agentCreate.Body.draft.draft_id
    Assert-AsyncEqual $agentCreate.StatusCode 201 "agent create status"
    Assert-AsyncTrue ($draftID -ne "") "agent create should include draft_id"
    Invoke-AsyncCommand -BaseUrl $baseUrl -Command "agent.package.draft.validate" -Roles "optimizer" -TenantID $tenantID -Payload @{ draft_id = $draftID } | Out-Null
    $release = Invoke-AsyncCommand -BaseUrl $baseUrl -Command "agent.package.publish" -Roles "optimizer" -TenantID $tenantID -Payload @{ draft_id = $draftID }
    $packageVersionID = [string]$release.package_version_id
    Invoke-AsyncCommand -BaseUrl $baseUrl -Command "agent.package.canary" -Roles "optimizer" -TenantID $tenantID -Payload @{
        package_version_id = $packageVersionID
        canary_percent = 25
    } | Out-Null
    Invoke-AsyncCommand -BaseUrl $baseUrl -Command "eval.run" -Roles "optimizer" -TenantID $tenantID -Target @{
        agent_id = $agentID
        version = "v1"
    } -Payload @{
        package_version_id = $packageVersionID
        input = "hello"
        final_reply_contains = @("ok")
        should_end_status = "completed"
        max_tool_calls = 0
    } | Out-Null
    $stable = Invoke-AsyncCommand -BaseUrl $baseUrl -Command "agent.package.stable" -Roles "optimizer" -TenantID $tenantID -Payload @{
        package_version_id = $packageVersionID
    }
    Assert-AsyncEqual $stable.status "stable" "stable status"
    Mark-AsyncStep "ASYNC-E2E-02" "Agent package is stable for async resilience probes" @{
        draft_id = $draftID
        package_version_id = $packageVersionID
    }

    $script:CurrentStep = "ASYNC-E2E-03"
    foreach ($provider in @(
        @{ provider_id = "async-slow-host-$suffix"; name = "Async Slow ToolHost"; endpoint = $slowPrefix.TrimEnd("/") },
        @{ provider_id = "async-fast-host-$suffix"; name = "Async Fast ToolHost"; endpoint = $fastPrefix.TrimEnd("/") }
    )) {
        Invoke-AsyncJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers" -Roles "optimizer" -TenantID $tenantID -Body @{
            provider_id = $provider.provider_id
            provider_type = "static_tool_host"
            name = $provider.name
            endpoint = $provider.endpoint
            timeout_ms = 5000
            retry_max = 0
        } | Out-Null
        Invoke-AsyncJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$($provider.provider_id)/health?trace_id=trace_async_health_$suffix" -Roles "optimizer" -TenantID $tenantID | Out-Null
        Invoke-AsyncJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-providers/$($provider.provider_id)/sync" -Roles "optimizer" -TenantID $tenantID | Out-Null
    }
    Invoke-AsyncJson -BaseUrl $baseUrl -Method "Post" -Path "/v1/tool-groups" -Roles "optimizer" -TenantID $tenantID -Body @{
        group_id = $toolGroupID
        name = "Async Resilience Tool Group"
        description = "Slow and fast ToolHost tools for async resilience."
        status = "enabled"
    } | Out-Null
    $activeBinding = Invoke-AsyncJson -BaseUrl $baseUrl -Method "Put" -Path "/v1/agents/$agentID/tool-bindings" -Roles "optimizer" -TenantID $tenantID -Body @{
        agent_version = "v1"
        tool_bindings = @{
            allowed_tool_group_ids = @($toolGroupID)
            exposed_tool_ids = @($slowToolID, $fastToolID)
            denied_tool_ids = @()
        }
    }
    Assert-AsyncTrue (@($activeBinding.Body.tool_bindings.exposed_tool_ids | ForEach-Object { [string]$_ }) -contains $slowToolID) "active binding should expose slow tool"
    Assert-AsyncTrue (@($activeBinding.Body.tool_bindings.exposed_tool_ids | ForEach-Object { [string]$_ }) -contains $fastToolID) "active binding should expose fast tool"
    Mark-AsyncStep "ASYNC-E2E-03" "Slow and fast ToolHosts are registered and bound" @{
        slow_tool_id = $slowToolID
        fast_tool_id = $fastToolID
        group_id = $toolGroupID
    }

    $script:CurrentStep = "ASYNC-E2E-04"
    $asyncRun = Invoke-AsyncCommand -BaseUrl $baseUrl -Command "agent.run" -Roles "runtime_caller" -TenantID $tenantID -CallerID "async-api-key" -CallerType "api_key" -TraceID "trace_async_prepared_$suffix" -Target @{
        agent_id = $agentID
        version = "v1"
    } -Payload @{
        input = "quick async prepared response"
    }
    Assert-AsyncEqual $asyncRun.status "created" "async agent.run should return prepared status"
    Assert-AsyncTrue ([string]$asyncRun.run_id -ne "") "async agent.run should include run_id"
    Mark-AsyncStep "ASYNC-E2E-04" "agent.run returns prepared run in async mode" @{
        run_id = $asyncRun.run_id
        task_id = $asyncRun.task_id
        status = $asyncRun.status
    }

    $script:CurrentStep = "ASYNC-E2E-05"
    $serviceToken = $env:CLEAN_CORE_SERVICE_TOKEN
    $slowInvokeJob = Start-Job -ArgumentList $baseUrl, $tenantID, $agentID, $slowToolID, $serviceToken, $suffix -ScriptBlock {
        param($BaseUrl, $TenantID, $AgentID, $ToolID, $ServiceToken, $Suffix)
        $headers = @{
            "Content-Type" = "application/json; charset=utf-8"
            "X-Tenant-ID" = $TenantID
            "X-Caller-ID" = "async-slow-background"
            "X-Caller-Type" = "api_key"
            "X-Roles" = "runtime_caller"
        }
        if ($ServiceToken -ne "") {
            $headers["Authorization"] = "Bearer $ServiceToken"
        }
        $body = @{
            command = "tools.invoke"
            trace_id = "trace_async_slow_$Suffix"
            target = @{
                agent_id = $AgentID
                version = "v1"
            }
            payload = @{
                tool_id = $ToolID
                arguments = @{ customer_id = "cust_slow"; probe = "slow" }
            }
            context = @{
                tenant_id = $TenantID
            }
        } | ConvertTo-Json -Depth 40 -Compress
        $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
        $response = Invoke-WebRequest -Uri "$BaseUrl/v1/commands" -Method Post -Headers $headers -Body ([System.Text.Encoding]::UTF8.GetBytes($body)) -UseBasicParsing -TimeoutSec 120
        $stopwatch.Stop()
        [pscustomobject]@{
            StatusCode = [int]$response.StatusCode
            DurationMs = [Math]::Round($stopwatch.Elapsed.TotalMilliseconds, 2)
            Body = [string]$response.Content
        }
    }
    $slowLogPath = Join-Path $ReportDir "slow-toolhost-invocations.ndjson"
    Wait-ForToolHostLog -Path $slowLogPath -TimeoutMs 3000

    $healthProbe = Invoke-AsyncJson -BaseUrl $baseUrl -Method "Get" -Path "/healthz" -Roles "runtime_caller" -TenantID $tenantID -CallerID "async-health-probe" -CallerType "api_key" -TimeoutSec 5
    $metricsProbe = Invoke-AsyncJson -BaseUrl $baseUrl -Method "Get" -Path "/metrics" -Roles "optimizer" -TenantID $tenantID -CallerID "async-metrics-probe" -CallerType "service" -TimeoutSec 5
    $fastStopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    $fastLookup = Invoke-AsyncCommand -BaseUrl $baseUrl -Command "tools.invoke" -Roles "runtime_caller" -TenantID $tenantID -CallerID "async-fast-probe" -CallerType "api_key" -TraceID "trace_async_fast_$suffix" -Target @{
        agent_id = $agentID
        version = "v1"
    } -Payload @{
        tool_id = $fastToolID
        arguments = @{ customer_id = "cust_fast"; probe = "fast" }
    }
    $fastStopwatch.Stop()
    Assert-AsyncEqual $healthProbe.Body.status "ok" "health probe during slow ToolHost call"
    Assert-AsyncEqual $metricsProbe.StatusCode 200 "metrics probe during slow ToolHost call"
    Assert-AsyncEqual $fastLookup.status "succeeded" "fast lookup during slow ToolHost call"
    Assert-AsyncTrue ($healthProbe.DurationMs -lt 1000) "healthz should not block behind slow ToolHost"
    Assert-AsyncTrue ($metricsProbe.DurationMs -lt 1000) "metrics should not block behind slow ToolHost"
    Assert-AsyncTrue ($fastStopwatch.Elapsed.TotalMilliseconds -lt 1000) "fast ToolHost invoke should not block behind unrelated slow ToolHost"

    Wait-Job $slowInvokeJob -Timeout 10 | Out-Null
    if ($slowInvokeJob.State -ne "Completed") {
        throw "slow ToolHost invoke did not complete"
    }
    $slowResult = Receive-Job $slowInvokeJob
    Assert-AsyncEqual $slowResult.StatusCode 200 "slow invoke status"
    $slowBody = $slowResult.Body | ConvertFrom-Json
    Assert-AsyncEqual $slowBody.status "succeeded" "slow invoke body status"
    Assert-AsyncTrue ([double]$slowResult.DurationMs -ge $slowDelayMs) "slow invoke should include configured provider delay"
    Mark-AsyncStep "ASYNC-E2E-05" "Slow ToolHost call does not block HTTP or unrelated fast invoke" @{
        healthz_duration_ms = $healthProbe.DurationMs
        metrics_duration_ms = $metricsProbe.DurationMs
        fast_lookup_duration_ms = [Math]::Round($fastStopwatch.Elapsed.TotalMilliseconds, 2)
        fast_lookup_status = $fastLookup.status
        slow_invoke_duration_ms = $slowResult.DurationMs
        slow_invoke_status = $slowBody.status
    }

    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "clean-core-async-resilience-report.json") -Value $summary
    Write-Host "CleanCore async resilience E2E passed. report=$ReportDir"
} catch {
    $summary.status = "failed"
    $summary.current_step = $script:CurrentStep
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "clean-core-async-resilience-report.json") -Value $summary
    Write-Error $_
    exit 1
} finally {
    foreach ($job in @($slowInvokeJob)) {
        if ($null -ne $job) {
            Stop-Job $job -ErrorAction SilentlyContinue | Out-Null
            Remove-Job $job -ErrorAction SilentlyContinue | Out-Null
        }
    }
    foreach ($process in @($slowHostProcess, $fastHostProcess)) {
        if ($null -ne $process -and -not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        }
    }
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
