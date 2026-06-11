param(
    [string]$BaseUrl = "",
    [int]$ProcessId = 0,
    [int]$Port = 0,
    [string]$ReportDir = "",
    [string]$AgentID = "test-agent",
    [string]$AgentVersion = "v1",
    [string]$TenantID = "tenant_perf",
    [string[]]$TenantIDs = @(),
    [string]$InputPrefix = "single node perf request",
    [int]$TotalRequests = 1000,
    [int]$Concurrency = 100,
    [int[]]$ConcurrencyStages = @(),
    [int]$SoakSeconds = 0,
    [int]$RecoveryProbeRequests = 1,
    [int]$SampleIntervalMs = 1000,
    [string]$Persistence = "",
    [string]$ModelProvider = "stub",
    [ValidateSet("sync", "async")] [string]$ExecutionMode = "sync",
    [int]$RunMaxConcurrent = 0,
    [int]$TenantRunMaxConcurrent = 0,
    [int]$AgentRunMaxConcurrent = 0,
    [int]$DBMaxOpenConns = 0,
    [int]$DBMaxIdleConns = 0,
    [int]$DBReadinessMaxOpenConns = 0,
    [int]$DBReadinessMaxIdleConns = 0,
    [int]$DBConnMaxLifetimeSeconds = 0,
    [int]$DBConnMaxIdleTimeSeconds = 0,
    [int]$TimeoutSec = 120,
    [int]$UsageEvidenceSampleLimit = 20,
    [int]$DetailedResultLimit = 10000,
    [int]$FailedResultLimit = 200,
    [int]$LatencySampleLimit = 1000000,
    [switch]$CollectUsageEvidence,
    [switch]$RunMigrations,
    [switch]$KeepServer,
    [switch]$ValidateOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"
Add-Type -AssemblyName System.Net.Http

function New-AgentRunRequest {
    param(
        [string]$BaseUrl,
        [string]$TenantID,
        [string]$AgentID,
        [string]$AgentVersion,
        [int]$Index,
        [string]$ServiceToken,
        [string]$InputPrefix
    )
    $input = if ($InputPrefix.Contains("{index}")) {
        $InputPrefix.Replace("{index}", [string]$Index)
    } else {
        "$InputPrefix $Index"
    }
    $traceID = "trace_perf_agent_run_{0}" -f $Index
    $body = @{
        command = "agent.run"
        trace_id = $traceID
        target = @{
            agent_id = $AgentID
            version = $AgentVersion
        }
        payload = @{
            input = $input
        }
        context = @{
            tenant_id = $TenantID
            user_id = "perf_user"
        }
    }
    $json = $body | ConvertTo-Json -Depth 40 -Compress
    $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Post, "$BaseUrl/v1/commands")
    $request.Content = [System.Net.Http.StringContent]::new($json, [System.Text.Encoding]::UTF8, "application/json")
    [void]$request.Headers.TryAddWithoutValidation("X-Tenant-ID", $TenantID)
    [void]$request.Headers.TryAddWithoutValidation("X-Caller-ID", "perf-runner")
    [void]$request.Headers.TryAddWithoutValidation("X-Caller-Type", "user")
    [void]$request.Headers.TryAddWithoutValidation("X-Roles", "runtime_caller")
    if (-not [string]::IsNullOrWhiteSpace($ServiceToken)) {
        [void]$request.Headers.TryAddWithoutValidation("Authorization", "Bearer $ServiceToken")
    }
    return [pscustomobject]@{
        Request = $request
        TraceID = $traceID
    }
}

function Get-PerfTenantIDForIndex {
    param(
        [string[]]$TenantIDs,
        [string]$Fallback,
        [int]$Index
    )
    if ($null -eq $TenantIDs -or $TenantIDs.Count -eq 0) {
        return $Fallback
    }
    $tenantIndex = $Index % $TenantIDs.Count
    return [string]$TenantIDs[$tenantIndex]
}

function Get-PerfRuntimeErrorCode {
    param(
        [AllowNull()] [string]$Body
    )
    if ([string]::IsNullOrWhiteSpace($Body)) {
        return ""
    }
    try {
        $json = $Body | ConvertFrom-Json
        $errorObj = Get-ObjectValue -Object $json -Key "error" -Default $null
        if ($null -ne $errorObj) {
            return [string](Get-ObjectValue -Object $errorObj -Key "code" -Default "")
        }
    } catch {
    }
    return ""
}

function Get-PerfRuntimeErrorMessage {
    param(
        [AllowNull()] [string]$Body
    )
    if ([string]::IsNullOrWhiteSpace($Body)) {
        return ""
    }
    try {
        $json = $Body | ConvertFrom-Json
        $errorObj = Get-ObjectValue -Object $json -Key "error" -Default $null
        if ($null -ne $errorObj) {
            return [string](Get-ObjectValue -Object $errorObj -Key "message" -Default "")
        }
    } catch {
    }
    return ""
}

function Complete-PerfHttpRequest {
    param(
        [Parameter(Mandatory = $true)] $Entry
    )
    $statusCode = 0
    $body = ""
    $errorMessage = ""
    try {
        $response = $Entry.Task.GetAwaiter().GetResult()
        $statusCode = [int]$response.StatusCode
        $body = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        $response.Dispose()
    } catch {
        $errorMessage = $_.Exception.Message
    } finally {
        $Entry.Stopwatch.Stop()
        $Entry.Request.Dispose()
    }
    $runStatus = ""
    $runID = ""
    $taskID = ""
    if ($body -ne "") {
        try {
            $json = $body | ConvertFrom-Json
            $runStatus = [string](Get-ObjectValue -Object $json -Key "status" -Default "")
            $runID = [string](Get-ObjectValue -Object $json -Key "run_id" -Default "")
            $taskID = [string](Get-ObjectValue -Object $json -Key "task_id" -Default "")
        } catch {
        }
    }
    $runtimeCode = Get-PerfRuntimeErrorCode -Body $body
    $runtimeMessage = Get-PerfRuntimeErrorMessage -Body $body
    $success = ($statusCode -ge 200 -and $statusCode -lt 300 -and $runtimeCode -eq "" -and $errorMessage -eq "")
    return [ordered]@{
        index = $Entry.Index
        trace_id = $Entry.TraceID
        status_code = $statusCode
        duration_ms = [Math]::Round($Entry.Stopwatch.Elapsed.TotalMilliseconds, 2)
        success = $success
        run_status = $runStatus
        run_id = $runID
        task_id = $taskID
        runtime_error_code = $runtimeCode
        runtime_error_message = $runtimeMessage
        error = $errorMessage
    }
}

function Get-PerfPercentile {
    param(
        [double[]]$Values,
        [double]$Percentile
    )
    if ($null -eq $Values -or $Values.Count -eq 0) {
        return $null
    }
    $sorted = @($Values | Sort-Object)
    $rank = [Math]::Ceiling(($Percentile / 100.0) * $sorted.Count) - 1
    if ($rank -lt 0) {
        $rank = 0
    }
    if ($rank -ge $sorted.Count) {
        $rank = $sorted.Count - 1
    }
    return [Math]::Round([double]$sorted[$rank], 2)
}

function New-PerfAggregateStats {
    return [ordered]@{
        total = 0L
        success = 0L
        failed = 0L
        rejected_count = 0L
        status_counts = [ordered]@{}
        runtime_error_code_counts = [ordered]@{}
        latency_values = (New-Object System.Collections.ArrayList)
        latency_samples_truncated = 0L
    }
}

function Add-PerfCount {
    param(
        [System.Collections.IDictionary]$Map,
        [string]$Key
    )
    if ([string]::IsNullOrWhiteSpace($Key)) {
        return
    }
    if (-not $Map.Contains($Key)) {
        $Map[$Key] = 0L
    }
    $Map[$Key] = [int64]$Map[$Key] + 1L
}

function Add-PerfAggregateResult {
    param(
        [System.Collections.IDictionary]$Stats,
        [System.Collections.IDictionary]$Result,
        [int]$LatencySampleLimit
    )
    $Stats["total"] = [int64]$Stats["total"] + 1L
    $isSuccess = [bool](Get-ObjectValue -Object $Result -Key "success" -Default $false)
    if ($isSuccess) {
        $Stats["success"] = [int64]$Stats["success"] + 1L
    } else {
        $Stats["failed"] = [int64]$Stats["failed"] + 1L
    }
    $statusCode = [string](Get-ObjectValue -Object $Result -Key "status_code" -Default "none")
    Add-PerfCount -Map $Stats["status_counts"] -Key $statusCode
    $runtimeCode = [string](Get-ObjectValue -Object $Result -Key "runtime_error_code" -Default "")
    if ($runtimeCode -ne "") {
        Add-PerfCount -Map $Stats["runtime_error_code_counts"] -Key $runtimeCode
    }
    if ($statusCode -eq "429" -or $runtimeCode -eq "ADMISSION_REJECTED") {
        $Stats["rejected_count"] = [int64]$Stats["rejected_count"] + 1L
    }
    $durationMs = Get-ObjectValue -Object $Result -Key "duration_ms" -Default $null
    if ($null -ne $durationMs) {
        $latencies = [System.Collections.ArrayList]$Stats["latency_values"]
        if ($LatencySampleLimit -le 0 -or $latencies.Count -lt $LatencySampleLimit) {
            [void]$latencies.Add([double]$durationMs)
        } else {
            $Stats["latency_samples_truncated"] = [int64]$Stats["latency_samples_truncated"] + 1L
        }
    }
}

function Add-PerfCompletedRequest {
    param(
        [Parameter(Mandatory = $true)] $Entry,
        [System.Collections.ArrayList]$Results,
        [System.Collections.ArrayList]$FailedResults,
        [System.Collections.IDictionary]$Stats,
        [int]$DetailedResultLimit,
        [int]$FailedResultLimit,
        [int]$LatencySampleLimit
    )
    $result = Complete-PerfHttpRequest -Entry $Entry
    Add-PerfAggregateResult -Stats $Stats -Result $result -LatencySampleLimit $LatencySampleLimit
    if ($DetailedResultLimit -le 0 -or $Results.Count -lt $DetailedResultLimit) {
        [void]$Results.Add($result)
    }
    if (-not [bool](Get-ObjectValue -Object $result -Key "success" -Default $false)) {
        if ($FailedResultLimit -le 0 -or $FailedResults.Count -lt $FailedResultLimit) {
            [void]$FailedResults.Add($result)
        }
    }
}

function ConvertTo-PerfAggregateSummary {
    param(
        [System.Collections.IDictionary]$Stats,
        [int]$LatencySampleLimit
    )
    $latencies = @($Stats["latency_values"] | ForEach-Object { [double]$_ })
    return [ordered]@{
        total_requests = [int64]$Stats["total"]
        success = [int64]$Stats["success"]
        failed = [int64]$Stats["failed"]
        status_counts = $Stats["status_counts"]
        runtime_error_code_counts = $Stats["runtime_error_code_counts"]
        rejected_count = [int64]$Stats["rejected_count"]
        p50_ms = Get-PerfPercentile -Values $latencies -Percentile 50
        p95_ms = Get-PerfPercentile -Values $latencies -Percentile 95
        p99_ms = Get-PerfPercentile -Values $latencies -Percentile 99
        max_ms = Get-PerfPercentile -Values $latencies -Percentile 100
        latency_sample_limit = $LatencySampleLimit
        latency_sample_count = $latencies.Count
        latency_samples_truncated = [int64]$Stats["latency_samples_truncated"]
    }
}

function Assert-OpenAICompatibleModelEnv {
    foreach ($required in @("CLEAN_CORE_MODEL_BASE_URL", "CLEAN_CORE_MODEL_API_KEY", "CLEAN_CORE_MODEL_NAME")) {
        if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($required))) {
            throw "missing required env $required for openai-compatible perf"
        }
    }
    if ($env:CLEAN_CORE_MODEL_API_KEY -eq "REPLACE_WITH_YOUR_DEEPSEEK_API_KEY") {
        throw "CLEAN_CORE_MODEL_API_KEY still contains the local template placeholder"
    }
}

function Add-OpenAICompatibleModelEnv {
    param(
        [hashtable]$ExtraEnv
    )
    foreach ($key in @(
        "CLEAN_CORE_MODEL_BASE_URL",
        "CLEAN_CORE_MODEL_API_KEY",
        "CLEAN_CORE_MODEL_NAME",
        "CLEAN_CORE_MODEL_MAX_TOKENS",
        "CLEAN_CORE_MODEL_TEMPERATURE",
        "CLEAN_CORE_MODEL_THINKING",
        "CLEAN_CORE_MODEL_REASONING_EFFORT"
    )) {
        $value = [Environment]::GetEnvironmentVariable($key)
        if (-not [string]::IsNullOrWhiteSpace($value)) {
            $ExtraEnv[$key] = $value
        }
    }
}

function Get-PerfUsageEvidenceSamples {
    param(
        [string]$BaseUrl,
        [hashtable]$Headers,
        [System.Collections.ArrayList]$Results,
        [int]$Limit
    )
    if ($Limit -le 0) {
        return @()
    }
    $samples = @()
    $seen = @{}
    foreach ($result in @($Results | Sort-Object { $_.index })) {
        if (-not [bool](Get-ObjectValue -Object $result -Key "success" -Default $false)) {
            continue
        }
        $traceID = [string](Get-ObjectValue -Object $result -Key "trace_id" -Default "")
        if ([string]::IsNullOrWhiteSpace($traceID) -or $seen.ContainsKey($traceID)) {
            continue
        }
        $seen[$traceID] = $true
        $encodedTraceID = [System.Uri]::EscapeDataString($traceID)
        $response = Get-OptionalJson -Uri "$BaseUrl/v1/usage/evidence?trace_id=$encodedTraceID" -Headers $Headers
        $evidence = Get-ObjectValue -Object $response -Key "usage_evidence" -Default $null
        if ($null -eq $evidence) {
            $samples += [ordered]@{
                trace_id = $traceID
                found = $false
            }
        } else {
            $samples += [ordered]@{
                trace_id = $traceID
                found = $true
                model_calls_total = [int](Get-ObjectValue -Object $evidence -Key "model_calls_total" -Default 0)
                model_failures_total = [int](Get-ObjectValue -Object $evidence -Key "model_failures_total" -Default 0)
                prompt_tokens_total = [int](Get-ObjectValue -Object $evidence -Key "prompt_tokens_total" -Default 0)
                completion_tokens_total = [int](Get-ObjectValue -Object $evidence -Key "completion_tokens_total" -Default 0)
                model_names = @((Get-ObjectValue -Object $evidence -Key "model_names" -Default @()))
            }
        }
        if ($samples.Count -ge $Limit) {
            break
        }
    }
    return $samples
}

function Get-PerfProcessTreeIds {
    param(
        [int]$RootProcessId
    )
    if ($RootProcessId -le 0) {
        return @()
    }
    $ids = New-Object System.Collections.ArrayList
    $queue = New-Object System.Collections.Queue
    [void]$ids.Add($RootProcessId)
    $queue.Enqueue($RootProcessId)
    while ($queue.Count -gt 0) {
        $parentID = [int]$queue.Dequeue()
        try {
            $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$parentID" -ErrorAction Stop)
        } catch {
            try {
                $children = @(Get-WmiObject Win32_Process -Filter "ParentProcessId=$parentID" -ErrorAction Stop)
            } catch {
                $children = @()
            }
        }
        foreach ($child in $children) {
            $childID = [int]$child.ProcessId
            if (-not $ids.Contains($childID)) {
                [void]$ids.Add($childID)
                $queue.Enqueue($childID)
            }
        }
    }
    return @($ids)
}

function Get-PerfProcessTotals {
    param(
        [int]$RootProcessId
    )
    $ids = @(Get-PerfProcessTreeIds -RootProcessId $RootProcessId)
    $cpuMs = 0.0
    $memoryBytes = 0.0
    $alive = @()
    foreach ($id in $ids) {
        try {
            $proc = Get-Process -Id $id -ErrorAction Stop
            $cpuMs += $proc.TotalProcessorTime.TotalMilliseconds
            $memoryBytes += $proc.WorkingSet64
            $alive += $id
        } catch {
        }
    }
    return [pscustomobject]@{
        CpuMs = $cpuMs
        MemoryMB = if ($alive.Count -gt 0) { [Math]::Round(($memoryBytes / 1MB), 2) } else { $null }
        ProcessIds = $alive
    }
}

function Add-PerfResourceSample {
    param(
        [System.Collections.ArrayList]$Samples,
        [int]$RootProcessId,
        [AllowNull()] $Previous,
        [string]$BaseUrl = "",
        [hashtable]$Headers = @{}
    )
    $now = Get-Date
    $cpuPercent = $null
    $queueDepth = $null
    $agentRunsRejectedTotal = $null
    $dbOpenConnections = $null
    $dbInUseConnections = $null
    $dbWaitCount = $null
    $dbWaitDurationMs = $null
    $dbReadinessOpenConnections = $null
    $dbReadinessInUseConnections = $null
    $dbReadinessWaitCount = $null
    $dbReadinessWaitDurationMs = $null
    $readinessStatus = $null
    $migrationStatus = $null
    if (-not [string]::IsNullOrWhiteSpace($BaseUrl)) {
        $metrics = Get-OptionalJson -Uri "$BaseUrl/metrics" -Headers $Headers
        if ($null -ne $metrics) {
            $readiness = Get-ObjectValue -Object $metrics -Key "readiness_status" -Default $null
            if ($null -ne $readiness) {
                $readinessStatus = [string]$readiness
            }
            $migration = Get-ObjectValue -Object $metrics -Key "migration_status" -Default $null
            if ($null -ne $migration) {
                $migrationStatus = [string]$migration
            }
            $running = Get-ObjectValue -Object $metrics -Key "agent_runs_running" -Default $null
            if ($null -ne $running) {
                $queueDepth = [int]$running
            }
            $rejected = Get-ObjectValue -Object $metrics -Key "agent_runs_rejected_total" -Default $null
            if ($null -ne $rejected) {
                $agentRunsRejectedTotal = [int64]$rejected
            }
            $dbOpen = Get-ObjectValue -Object $metrics -Key "db_open_connections" -Default $null
            if ($null -ne $dbOpen) {
                $dbOpenConnections = [int]$dbOpen
            }
            $dbInUse = Get-ObjectValue -Object $metrics -Key "db_in_use_connections" -Default $null
            if ($null -ne $dbInUse) {
                $dbInUseConnections = [int]$dbInUse
            }
            $dbWait = Get-ObjectValue -Object $metrics -Key "db_wait_count" -Default $null
            if ($null -ne $dbWait) {
                $dbWaitCount = [int64]$dbWait
            }
            $dbWaitDuration = Get-ObjectValue -Object $metrics -Key "db_wait_duration_ms" -Default $null
            if ($null -ne $dbWaitDuration) {
                $dbWaitDurationMs = [int64]$dbWaitDuration
            }
            $dbReadinessOpen = Get-ObjectValue -Object $metrics -Key "db_readiness_open_connections" -Default $null
            if ($null -ne $dbReadinessOpen) {
                $dbReadinessOpenConnections = [int]$dbReadinessOpen
            }
            $dbReadinessInUse = Get-ObjectValue -Object $metrics -Key "db_readiness_in_use_connections" -Default $null
            if ($null -ne $dbReadinessInUse) {
                $dbReadinessInUseConnections = [int]$dbReadinessInUse
            }
            $dbReadinessWait = Get-ObjectValue -Object $metrics -Key "db_readiness_wait_count" -Default $null
            if ($null -ne $dbReadinessWait) {
                $dbReadinessWaitCount = [int64]$dbReadinessWait
            }
            $dbReadinessWaitDuration = Get-ObjectValue -Object $metrics -Key "db_readiness_wait_duration_ms" -Default $null
            if ($null -ne $dbReadinessWaitDuration) {
                $dbReadinessWaitDurationMs = [int64]$dbReadinessWaitDuration
            }
        }
    }
    $totals = Get-PerfProcessTotals -RootProcessId $RootProcessId
    if ($null -ne $Previous -and $totals.ProcessIds.Count -gt 0) {
        $elapsedMs = ($now - $Previous.time).TotalMilliseconds
        if ($elapsedMs -gt 0) {
            $cpuPercent = [Math]::Round((($totals.CpuMs - $Previous.cpu_ms) / ($elapsedMs * [Environment]::ProcessorCount)) * 100.0, 2)
        }
    }
    if ($totals.ProcessIds.Count -gt 0) {
        $sample = [ordered]@{
            timestamp = $now.ToUniversalTime().ToString("o")
            cpu_percent = $cpuPercent
            memory_mb = $totals.MemoryMB
            queue_depth = $queueDepth
            agent_runs_rejected_total = $agentRunsRejectedTotal
            db_open_connections = $dbOpenConnections
            db_in_use_connections = $dbInUseConnections
            db_wait_count = $dbWaitCount
            db_wait_duration_ms = $dbWaitDurationMs
            db_readiness_open_connections = $dbReadinessOpenConnections
            db_readiness_in_use_connections = $dbReadinessInUseConnections
            db_readiness_wait_count = $dbReadinessWaitCount
            db_readiness_wait_duration_ms = $dbReadinessWaitDurationMs
            readiness_status = $readinessStatus
            migration_status = $migrationStatus
            process_ids = $totals.ProcessIds
        }
        [void]$Samples.Add($sample)
        return [pscustomobject]@{ time = $now; cpu_ms = $totals.CpuMs }
    }
    $sampleWithoutProcess = [ordered]@{
        timestamp = $now.ToUniversalTime().ToString("o")
        cpu_percent = $null
        memory_mb = $null
        queue_depth = $queueDepth
        agent_runs_rejected_total = $agentRunsRejectedTotal
        db_open_connections = $dbOpenConnections
        db_in_use_connections = $dbInUseConnections
        db_wait_count = $dbWaitCount
        db_wait_duration_ms = $dbWaitDurationMs
        db_readiness_open_connections = $dbReadinessOpenConnections
        db_readiness_in_use_connections = $dbReadinessInUseConnections
        db_readiness_wait_count = $dbReadinessWaitCount
        db_readiness_wait_duration_ms = $dbReadinessWaitDurationMs
        readiness_status = $readinessStatus
        migration_status = $migrationStatus
        process_ids = @()
    }
    [void]$Samples.Add($sampleWithoutProcess)
    return $Previous
}

function Get-OptionalJson {
    param(
        [string]$Uri,
        [hashtable]$Headers = @{}
    )
    try {
        return Invoke-RestMethod -Uri $Uri -Method Get -Headers $Headers -TimeoutSec 10
    } catch {
        return $null
    }
}

function New-PerfStagePlan {
    param(
        [int]$TotalRequests,
        [int]$DefaultConcurrency,
        [int[]]$Stages
    )
    if ($null -eq $Stages -or $Stages.Count -eq 0) {
        return @([ordered]@{
            stage = 1
            start_index = 0
            end_index = $TotalRequests - 1
            request_count = $TotalRequests
            concurrency = $DefaultConcurrency
        })
    }
    $count = $Stages.Count
    $base = [Math]::Floor($TotalRequests / $count)
    $remainder = $TotalRequests % $count
    $start = 0
    $plan = @()
    for ($i = 0; $i -lt $count; $i++) {
        $concurrency = [int]$Stages[$i]
        if ($concurrency -le 0) {
            throw "ConcurrencyStages must contain positive values"
        }
        $requestCount = [int]$base
        if ($i -lt $remainder) {
            $requestCount++
        }
        $end = $start + $requestCount - 1
        $plan += [ordered]@{
            stage = $i + 1
            start_index = $start
            end_index = $end
            request_count = $requestCount
            concurrency = $concurrency
        }
        $start = $end + 1
    }
    return $plan
}

function Get-PerfConcurrencyForIndex {
    param(
        [object[]]$StagePlan,
        [int]$Index,
        [int]$Fallback
    )
    foreach ($stage in @($StagePlan)) {
        if ($Index -ge [int]$stage.start_index -and $Index -le [int]$stage.end_index) {
            return [int]$stage.concurrency
        }
    }
    if ($StagePlan.Count -gt 0) {
        return [int]$StagePlan[$StagePlan.Count - 1].concurrency
    }
    return $Fallback
}

if ($ValidateOnly) {
    $probe = New-AgentRunRequest -BaseUrl "http://localhost:1" -TenantID "tenant_perf" -AgentID "test-agent" -AgentVersion "v1" -Index 1 -ServiceToken "token" -InputPrefix "validate request {index}"
    if ($probe.TraceID -ne "trace_perf_agent_run_1" -or $probe.Request.Method.Method -ne "POST") {
        throw "perf_single_node_agent_run validation failed"
    }
    $probe.Request.Dispose()
    & "$PSScriptRoot\perf_single_node_report.ps1" -ValidateOnly
    Write-Host "perf_single_node_agent_run validation passed"
    exit 0
}

if ($TotalRequests -le 0) {
    throw "TotalRequests must be positive"
}
if ($Concurrency -le 0) {
    throw "Concurrency must be positive"
}
if ($Concurrency -gt $TotalRequests) {
    $Concurrency = $TotalRequests
}
if ($SoakSeconds -lt 0) {
    throw "SoakSeconds must be non-negative"
}
if ($RecoveryProbeRequests -lt 0) {
    throw "RecoveryProbeRequests must be non-negative"
}
$TenantIDs = @($TenantIDs | ForEach-Object { [string]$_ } | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | ForEach-Object { $_.Trim() })
if ($TenantIDs.Count -eq 0) {
    $TenantIDs = @($TenantID)
}
if ($UsageEvidenceSampleLimit -lt 0) {
    throw "UsageEvidenceSampleLimit must be non-negative"
}
if ($DBMaxOpenConns -lt 0) {
    throw "DBMaxOpenConns must be non-negative"
}
if ($DBMaxIdleConns -lt 0) {
    throw "DBMaxIdleConns must be non-negative"
}
if ($DBMaxOpenConns -gt 0 -and $DBMaxIdleConns -gt $DBMaxOpenConns) {
    throw "DBMaxIdleConns must be less than or equal to DBMaxOpenConns"
}
if ($DBReadinessMaxOpenConns -lt 0) {
    throw "DBReadinessMaxOpenConns must be non-negative"
}
if ($DBReadinessMaxIdleConns -lt 0) {
    throw "DBReadinessMaxIdleConns must be non-negative"
}
if ($DBReadinessMaxOpenConns -gt 0 -and $DBReadinessMaxIdleConns -gt $DBReadinessMaxOpenConns) {
    throw "DBReadinessMaxIdleConns must be less than or equal to DBReadinessMaxOpenConns"
}
if ($DBConnMaxLifetimeSeconds -lt 0) {
    throw "DBConnMaxLifetimeSeconds must be non-negative"
}
if ($DBConnMaxIdleTimeSeconds -lt 0) {
    throw "DBConnMaxIdleTimeSeconds must be non-negative"
}
if ($DetailedResultLimit -lt 0) {
    throw "DetailedResultLimit must be non-negative"
}
if ($FailedResultLimit -lt 0) {
    throw "FailedResultLimit must be non-negative"
}
if ($LatencySampleLimit -lt 0) {
    throw "LatencySampleLimit must be non-negative"
}
if ($SampleIntervalMs -le 0) {
    $SampleIntervalMs = 1000
}
if ([string]::IsNullOrWhiteSpace($Persistence)) {
    if (-not [string]::IsNullOrWhiteSpace($env:CLEAN_CORE_DATABASE_URL)) {
        $Persistence = "postgres"
    } else {
        $Persistence = "in-memory"
    }
}
if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "perf-single-node"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

$root = Get-RepoRoot
$server = $null
$client = $null
$serviceToken = if ($env:CLEAN_CORE_SERVICE_TOKEN) { $env:CLEAN_CORE_SERVICE_TOKEN } else { "dev-token" }
$startedAt = (Get-Date)
$results = New-Object System.Collections.ArrayList
$failedResults = New-Object System.Collections.ArrayList
$stats = New-PerfAggregateStats
$recoveryResults = New-Object System.Collections.ArrayList
$samples = New-Object System.Collections.ArrayList
$usageEvidenceResults = @()
$stagePlan = @()
$rawPath = Join-Path $ReportDir "single-node-agent-run-raw.json"
$reportPath = Join-Path $ReportDir "single-node-perf-report.json"

try {
    if ($ModelProvider -eq "openai-compatible") {
        Assert-OpenAICompatibleModelEnv
        if (-not $CollectUsageEvidence) {
            $CollectUsageEvidence = $true
        }
    }
    $stagePlan = @(New-PerfStagePlan -TotalRequests $TotalRequests -DefaultConcurrency $Concurrency -Stages $ConcurrencyStages)
    if ($Persistence -eq "postgres" -and $RunMigrations) {
        if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_DATABASE_URL)) {
            throw "CLEAN_CORE_DATABASE_URL is required for postgres persistence"
        }
        Push-Location $root
        try {
            $go = Get-GoCommand
            $migrationUp = & $go run ./cmd/clean-core-server -migration up -migration-dir migrations 2>&1
            if ($LASTEXITCODE -ne 0) {
                throw "migration up failed: $($migrationUp -join "`n")"
            }
        } finally {
            Pop-Location
        }
    }

    if ([string]::IsNullOrWhiteSpace($BaseUrl)) {
        $extraEnv = @{
            CLEAN_CORE_MODEL_PROVIDER = $ModelProvider
            CLEAN_CORE_SERVICE_TOKEN = $serviceToken
            CLEAN_CORE_ENV = "local"
            CLEAN_CORE_LOG_LEVEL = "error"
            CLEAN_CORE_AGENT_RUN_EXECUTION_MODE = $ExecutionMode
        }
        if ($ModelProvider -eq "openai-compatible") {
            Add-OpenAICompatibleModelEnv -ExtraEnv $extraEnv
        }
        if ($Persistence -eq "postgres") {
            if ([string]::IsNullOrWhiteSpace($env:CLEAN_CORE_DATABASE_URL)) {
                throw "CLEAN_CORE_DATABASE_URL is required for postgres persistence"
            }
            $extraEnv["CLEAN_CORE_DATABASE_URL"] = $env:CLEAN_CORE_DATABASE_URL
        }
        if ($RunMaxConcurrent -gt 0) {
            $extraEnv["CLEAN_CORE_RUN_MAX_CONCURRENT"] = $RunMaxConcurrent
        }
        if ($TenantRunMaxConcurrent -gt 0) {
            $extraEnv["CLEAN_CORE_TENANT_RUN_MAX_CONCURRENT"] = $TenantRunMaxConcurrent
        }
        if ($AgentRunMaxConcurrent -gt 0) {
            $extraEnv["CLEAN_CORE_AGENT_RUN_MAX_CONCURRENT"] = $AgentRunMaxConcurrent
        }
        if ($DBMaxOpenConns -gt 0) {
            $extraEnv["CLEAN_CORE_DB_MAX_OPEN_CONNS"] = $DBMaxOpenConns
        }
        if ($DBMaxIdleConns -gt 0) {
            $extraEnv["CLEAN_CORE_DB_MAX_IDLE_CONNS"] = $DBMaxIdleConns
        }
        if ($DBReadinessMaxOpenConns -gt 0) {
            $extraEnv["CLEAN_CORE_DB_READINESS_MAX_OPEN_CONNS"] = $DBReadinessMaxOpenConns
        }
        if ($DBReadinessMaxIdleConns -gt 0) {
            $extraEnv["CLEAN_CORE_DB_READINESS_MAX_IDLE_CONNS"] = $DBReadinessMaxIdleConns
        }
        if ($DBConnMaxLifetimeSeconds -gt 0) {
            $extraEnv["CLEAN_CORE_DB_CONN_MAX_LIFETIME_SECONDS"] = $DBConnMaxLifetimeSeconds
        }
        if ($DBConnMaxIdleTimeSeconds -gt 0) {
            $extraEnv["CLEAN_CORE_DB_CONN_MAX_IDLE_TIME_SECONDS"] = $DBConnMaxIdleTimeSeconds
        }
        $server = Start-CleanCoreServer -Port $Port -LogPath (Join-Path $ReportDir "server.log") -ExtraEnv $extraEnv
        $BaseUrl = $server.BaseUrl
        $ProcessId = $server.Process.Id
    }
    $headers = Get-E2EHeaders -Roles "admin" -TenantID $TenantID -ServiceToken $serviceToken
    $readiness = Get-OptionalJson -Uri "$BaseUrl/v1/readiness/report" -Headers $headers
    if ($Persistence -eq "postgres") {
        if ($null -eq $readiness) {
            throw "postgres perf requires readiness report before load"
        }
        $readinessStatus = [string](Get-ObjectValue -Object $readiness -Key "status" -Default "")
        if ($readinessStatus -ne "ready") {
            throw "postgres perf requires readiness=ready before load; got $readinessStatus"
        }
    }
    $metricsBefore = Get-OptionalJson -Uri "$BaseUrl/metrics" -Headers $headers
    $previousSample = Add-PerfResourceSample -Samples $samples -RootProcessId $ProcessId -Previous $null -BaseUrl $BaseUrl -Headers $headers

    $client = [System.Net.Http.HttpClient]::new()
    $client.Timeout = [TimeSpan]::FromSeconds($TimeoutSec)
    $inflight = New-Object System.Collections.ArrayList
    $overall = [System.Diagnostics.Stopwatch]::StartNew()
    $nextSampleAt = (Get-Date).AddMilliseconds($SampleIntervalMs)
    $sentRequests = 0

    while ($sentRequests -lt $TotalRequests) {
        $currentConcurrency = Get-PerfConcurrencyForIndex -StagePlan $stagePlan -Index $sentRequests -Fallback $Concurrency
        while ($inflight.Count -ge $currentConcurrency) {
            $tasks = @($inflight | ForEach-Object { $_.Task })
            $completedIndex = [System.Threading.Tasks.Task]::WaitAny($tasks, 1000)
            if ($completedIndex -ge 0) {
                $entry = $inflight[$completedIndex]
                Add-PerfCompletedRequest -Entry $entry -Results $results -FailedResults $failedResults -Stats $stats -DetailedResultLimit $DetailedResultLimit -FailedResultLimit $FailedResultLimit -LatencySampleLimit $LatencySampleLimit
                $inflight.RemoveAt($completedIndex)
            }
            if ((Get-Date) -ge $nextSampleAt) {
                $previousSample = Add-PerfResourceSample -Samples $samples -RootProcessId $ProcessId -Previous $previousSample -BaseUrl $BaseUrl -Headers $headers
                $nextSampleAt = (Get-Date).AddMilliseconds($SampleIntervalMs)
            }
        }
        $requestTenantID = Get-PerfTenantIDForIndex -TenantIDs $TenantIDs -Fallback $TenantID -Index $sentRequests
        $requestInfo = New-AgentRunRequest -BaseUrl $BaseUrl -TenantID $requestTenantID -AgentID $AgentID -AgentVersion $AgentVersion -Index $sentRequests -ServiceToken $serviceToken -InputPrefix $InputPrefix
        $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
        $task = $client.SendAsync($requestInfo.Request)
        [void]$inflight.Add([pscustomobject]@{
            Index = $sentRequests
            TraceID = $requestInfo.TraceID
            Request = $requestInfo.Request
            Task = $task
            Stopwatch = $stopwatch
        })
        $sentRequests++
    }
    if ($SoakSeconds -gt 0) {
        $soakDeadline = (Get-Date).AddSeconds($SoakSeconds)
        while ((Get-Date) -lt $soakDeadline) {
            $currentConcurrency = Get-PerfConcurrencyForIndex -StagePlan $stagePlan -Index $sentRequests -Fallback $Concurrency
            while ($inflight.Count -ge $currentConcurrency) {
                $tasks = @($inflight | ForEach-Object { $_.Task })
                $completedIndex = [System.Threading.Tasks.Task]::WaitAny($tasks, 1000)
                if ($completedIndex -ge 0) {
                    $entry = $inflight[$completedIndex]
                    Add-PerfCompletedRequest -Entry $entry -Results $results -FailedResults $failedResults -Stats $stats -DetailedResultLimit $DetailedResultLimit -FailedResultLimit $FailedResultLimit -LatencySampleLimit $LatencySampleLimit
                    $inflight.RemoveAt($completedIndex)
                }
                if ((Get-Date) -ge $nextSampleAt) {
                    $previousSample = Add-PerfResourceSample -Samples $samples -RootProcessId $ProcessId -Previous $previousSample -BaseUrl $BaseUrl -Headers $headers
                    $nextSampleAt = (Get-Date).AddMilliseconds($SampleIntervalMs)
                }
            }
            $requestTenantID = Get-PerfTenantIDForIndex -TenantIDs $TenantIDs -Fallback $TenantID -Index $sentRequests
            $requestInfo = New-AgentRunRequest -BaseUrl $BaseUrl -TenantID $requestTenantID -AgentID $AgentID -AgentVersion $AgentVersion -Index $sentRequests -ServiceToken $serviceToken -InputPrefix $InputPrefix
            $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
            $task = $client.SendAsync($requestInfo.Request)
            [void]$inflight.Add([pscustomobject]@{
                Index = $sentRequests
                TraceID = $requestInfo.TraceID
                Request = $requestInfo.Request
                Task = $task
                Stopwatch = $stopwatch
            })
            $sentRequests++
        }
    }
    while ($inflight.Count -gt 0) {
        $tasks = @($inflight | ForEach-Object { $_.Task })
        $completedIndex = [System.Threading.Tasks.Task]::WaitAny($tasks, 1000)
        if ($completedIndex -ge 0) {
            $entry = $inflight[$completedIndex]
            Add-PerfCompletedRequest -Entry $entry -Results $results -FailedResults $failedResults -Stats $stats -DetailedResultLimit $DetailedResultLimit -FailedResultLimit $FailedResultLimit -LatencySampleLimit $LatencySampleLimit
            $inflight.RemoveAt($completedIndex)
        }
        if ((Get-Date) -ge $nextSampleAt) {
            $previousSample = Add-PerfResourceSample -Samples $samples -RootProcessId $ProcessId -Previous $previousSample -BaseUrl $BaseUrl -Headers $headers
            $nextSampleAt = (Get-Date).AddMilliseconds($SampleIntervalMs)
        }
    }
    $overall.Stop()
    $previousSample = Add-PerfResourceSample -Samples $samples -RootProcessId $ProcessId -Previous $previousSample -BaseUrl $BaseUrl -Headers $headers
    $metricsAfterLoad = Get-OptionalJson -Uri "$BaseUrl/metrics" -Headers $headers
    for ($i = 0; $i -lt $RecoveryProbeRequests; $i++) {
        $recoveryIndex = $sentRequests + $i
        $requestTenantID = Get-PerfTenantIDForIndex -TenantIDs $TenantIDs -Fallback $TenantID -Index $recoveryIndex
        $requestInfo = New-AgentRunRequest -BaseUrl $BaseUrl -TenantID $requestTenantID -AgentID $AgentID -AgentVersion $AgentVersion -Index $recoveryIndex -ServiceToken $serviceToken -InputPrefix $InputPrefix
        $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
        $task = $client.SendAsync($requestInfo.Request)
        [void]$recoveryResults.Add((Complete-PerfHttpRequest -Entry ([pscustomobject]@{
            Index = $sentRequests + $i
            TraceID = $requestInfo.TraceID
            Request = $requestInfo.Request
            Task = $task
            Stopwatch = $stopwatch
        })))
    }
    $metricsAfter = Get-OptionalJson -Uri "$BaseUrl/metrics" -Headers $headers
    if ($CollectUsageEvidence) {
        $usageEvidenceResults = @(Get-PerfUsageEvidenceSamples -BaseUrl $BaseUrl -Headers $headers -Results $results -Limit $UsageEvidenceSampleLimit)
    }
    $completedAt = Get-Date
    $summary = ConvertTo-PerfAggregateSummary -Stats $stats -LatencySampleLimit $LatencySampleLimit
    $resultsTruncated = [int64]$summary.total_requests - [int64]$results.Count
    if ($resultsTruncated -lt 0) {
        $resultsTruncated = 0
    }

    $raw = [ordered]@{
        status = "completed"
        started_at = $startedAt.ToUniversalTime().ToString("o")
        completed_at = $completedAt.ToUniversalTime().ToString("o")
        duration_ms = [Math]::Round($overall.Elapsed.TotalMilliseconds, 2)
        base_url = $BaseUrl
        requested_total_requests = $TotalRequests
        total_requests = [int64]$summary.total_requests
        concurrency = $Concurrency
        concurrency_stages = @($stagePlan)
        soak_seconds = $SoakSeconds
        recovery_probe_requests = $RecoveryProbeRequests
        agent_id = $AgentID
        agent_version = $AgentVersion
        tenant_id = $TenantID
        tenant_ids = @($TenantIDs)
        tenant_count = $TenantIDs.Count
        input_prefix = $InputPrefix
        model_provider = $ModelProvider
        model_base_url = if ($ModelProvider -eq "openai-compatible") { $env:CLEAN_CORE_MODEL_BASE_URL } else { "" }
        model_name = if ($ModelProvider -eq "openai-compatible") { $env:CLEAN_CORE_MODEL_NAME } else { "" }
        persistence = $Persistence
        execution_mode = $ExecutionMode
        run_migrations = [bool]$RunMigrations
        collect_usage_evidence = [bool]$CollectUsageEvidence
        usage_evidence_sample_limit = $UsageEvidenceSampleLimit
        run_max_concurrent = $RunMaxConcurrent
        tenant_run_max_concurrent = $TenantRunMaxConcurrent
        agent_run_max_concurrent = $AgentRunMaxConcurrent
        db_max_open_conns_configured = $DBMaxOpenConns
        db_max_idle_conns_configured = $DBMaxIdleConns
        db_readiness_max_open_conns_configured = $DBReadinessMaxOpenConns
        db_readiness_max_idle_conns_configured = $DBReadinessMaxIdleConns
        db_conn_max_lifetime_seconds_configured = $DBConnMaxLifetimeSeconds
        db_conn_max_idle_time_seconds_configured = $DBConnMaxIdleTimeSeconds
        detailed_result_limit = $DetailedResultLimit
        failed_result_limit = $FailedResultLimit
        results_truncated = $resultsTruncated
        latency_sample_limit = $LatencySampleLimit
        summary = $summary
        process_id = $ProcessId
        readiness_snapshot = $readiness
        metrics_before = $metricsBefore
        metrics_after_load = $metricsAfterLoad
        metrics_snapshot = $metricsAfter
        results = @($results | Sort-Object { $_.index })
        failed_results = @($failedResults | Sort-Object { $_.index })
        recovery_probe_results = @($recoveryResults | Sort-Object { $_.index })
        usage_evidence_results = @($usageEvidenceResults)
        samples = @($samples)
        raw_result_path = $rawPath
    }
    Write-E2EJson -Path $rawPath -Value $raw
    & "$PSScriptRoot\perf_single_node_report.ps1" -InputPath $rawPath -OutputPath $reportPath
    Write-Host "single-node agent.run perf completed. report=$reportPath raw=$rawPath"
} catch {
    $failedAt = Get-Date
    $summary = ConvertTo-PerfAggregateSummary -Stats $stats -LatencySampleLimit $LatencySampleLimit
    $resultsTruncated = [int64]$summary.total_requests - [int64]$results.Count
    if ($resultsTruncated -lt 0) {
        $resultsTruncated = 0
    }
    $raw = [ordered]@{
        status = "failed"
        started_at = $startedAt.ToUniversalTime().ToString("o")
        completed_at = $failedAt.ToUniversalTime().ToString("o")
        duration_ms = [Math]::Round(($failedAt - $startedAt).TotalMilliseconds, 2)
        base_url = $BaseUrl
        requested_total_requests = $TotalRequests
        total_requests = if ([int64]$summary.total_requests -gt 0) { [int64]$summary.total_requests } else { $TotalRequests }
        concurrency = $Concurrency
        concurrency_stages = if ($null -ne $stagePlan) { @($stagePlan) } else { @() }
        soak_seconds = $SoakSeconds
        recovery_probe_requests = $RecoveryProbeRequests
        agent_id = $AgentID
        agent_version = $AgentVersion
        tenant_id = $TenantID
        tenant_ids = @($TenantIDs)
        tenant_count = $TenantIDs.Count
        input_prefix = $InputPrefix
        model_provider = $ModelProvider
        model_base_url = if ($ModelProvider -eq "openai-compatible") { $env:CLEAN_CORE_MODEL_BASE_URL } else { "" }
        model_name = if ($ModelProvider -eq "openai-compatible") { $env:CLEAN_CORE_MODEL_NAME } else { "" }
        persistence = $Persistence
        execution_mode = $ExecutionMode
        run_migrations = [bool]$RunMigrations
        collect_usage_evidence = [bool]$CollectUsageEvidence
        usage_evidence_sample_limit = $UsageEvidenceSampleLimit
        run_max_concurrent = $RunMaxConcurrent
        tenant_run_max_concurrent = $TenantRunMaxConcurrent
        agent_run_max_concurrent = $AgentRunMaxConcurrent
        db_max_open_conns_configured = $DBMaxOpenConns
        db_max_idle_conns_configured = $DBMaxIdleConns
        db_readiness_max_open_conns_configured = $DBReadinessMaxOpenConns
        db_readiness_max_idle_conns_configured = $DBReadinessMaxIdleConns
        db_conn_max_lifetime_seconds_configured = $DBConnMaxLifetimeSeconds
        db_conn_max_idle_time_seconds_configured = $DBConnMaxIdleTimeSeconds
        detailed_result_limit = $DetailedResultLimit
        failed_result_limit = $FailedResultLimit
        results_truncated = $resultsTruncated
        latency_sample_limit = $LatencySampleLimit
        summary = $summary
        process_id = $ProcessId
        error = $_.Exception.Message
        results = @($results | Sort-Object { $_.index })
        failed_results = @($failedResults | Sort-Object { $_.index })
        recovery_probe_results = @($recoveryResults | Sort-Object { $_.index })
        usage_evidence_results = @($usageEvidenceResults)
        samples = @($samples)
        raw_result_path = $rawPath
    }
    Write-E2EJson -Path $rawPath -Value $raw
    Write-Error $_
    exit 1
} finally {
    if ($null -ne $client) {
        $client.Dispose()
    }
    if (-not $KeepServer -and $null -ne $server) {
        Stop-CleanCoreServer -Process $server.Process -BaseUrl $server.BaseUrl
    }
}
