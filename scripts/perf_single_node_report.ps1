param(
    [string]$InputPath = "",
    [string]$OutputPath = "",
    [switch]$ValidateOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

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

function Get-PerfObjectValue {
    param(
        [AllowNull()] $Object,
        [string]$Key,
        [AllowNull()] $Default = $null
    )
    if ($null -eq $Object) {
        return $Default
    }
    if ($Object -is [System.Collections.IDictionary]) {
        if ($Object.Contains($Key)) {
            return $Object[$Key]
        }
        return $Default
    }
    $property = $Object.PSObject.Properties[$Key]
    if ($null -ne $property) {
        return $property.Value
    }
    return $Default
}

function ConvertTo-SingleNodePerfReport {
    param(
        [Parameter(Mandatory = $true)] $Raw
    )
    $results = @(Get-PerfObjectValue -Object $Raw -Key "results" -Default @())
    $failedResults = @(Get-PerfObjectValue -Object $Raw -Key "failed_results" -Default @())
    $samples = @(Get-PerfObjectValue -Object $Raw -Key "samples" -Default @())
    $summary = Get-PerfObjectValue -Object $Raw -Key "summary" -Default $null
    $total = [int](Get-PerfObjectValue -Object $Raw -Key "total_requests" -Default $results.Count)
    $durationMs = [double](Get-PerfObjectValue -Object $Raw -Key "duration_ms" -Default 0)
    $latencies = @()
    $latencySampleCount = 0
    $latencySamplesTruncated = 0
    if ($null -ne $summary) {
        $total = [int](Get-PerfObjectValue -Object $summary -Key "total_requests" -Default $total)
        $success = [int](Get-PerfObjectValue -Object $summary -Key "success" -Default 0)
        $failed = [int](Get-PerfObjectValue -Object $summary -Key "failed" -Default ($total - $success))
        $statusCounts = Get-PerfObjectValue -Object $summary -Key "status_counts" -Default ([ordered]@{})
        $runtimeErrorCodeCounts = Get-PerfObjectValue -Object $summary -Key "runtime_error_code_counts" -Default ([ordered]@{})
        $rejected = [int](Get-PerfObjectValue -Object $summary -Key "rejected_count" -Default 0)
        $p50Ms = Get-PerfObjectValue -Object $summary -Key "p50_ms" -Default $null
        $p95Ms = Get-PerfObjectValue -Object $summary -Key "p95_ms" -Default $null
        $p99Ms = Get-PerfObjectValue -Object $summary -Key "p99_ms" -Default $null
        $maxMs = Get-PerfObjectValue -Object $summary -Key "max_ms" -Default $null
        $latencySampleCount = [int](Get-PerfObjectValue -Object $summary -Key "latency_sample_count" -Default 0)
        $latencySamplesTruncated = [int](Get-PerfObjectValue -Object $summary -Key "latency_samples_truncated" -Default 0)
    } else {
        $latencies = @($results | ForEach-Object {
            $value = Get-PerfObjectValue -Object $_ -Key "duration_ms" -Default $null
            if ($null -ne $value) {
                [double]$value
            }
        })
        $success = @($results | Where-Object { [bool](Get-PerfObjectValue -Object $_ -Key "success" -Default $false) }).Count
        $failed = $total - $success
        if ($failed -lt 0) {
            $failed = 0
        }
        $statusCounts = [ordered]@{}
        $runtimeErrorCodeCounts = [ordered]@{}
        $rejected = 0
        foreach ($result in $results) {
            $statusCode = [string](Get-PerfObjectValue -Object $result -Key "status_code" -Default "none")
            if (-not $statusCounts.Contains($statusCode)) {
                $statusCounts[$statusCode] = 0
            }
            $statusCounts[$statusCode] = [int]$statusCounts[$statusCode] + 1
            $runtimeCode = [string](Get-PerfObjectValue -Object $result -Key "runtime_error_code" -Default "")
            if ($runtimeCode -ne "") {
                if (-not $runtimeErrorCodeCounts.Contains($runtimeCode)) {
                    $runtimeErrorCodeCounts[$runtimeCode] = 0
                }
                $runtimeErrorCodeCounts[$runtimeCode] = [int]$runtimeErrorCodeCounts[$runtimeCode] + 1
            }
            if ($statusCode -eq "429" -or $runtimeCode -eq "ADMISSION_REJECTED") {
                $rejected++
            }
        }
        $p50Ms = Get-PerfPercentile -Values $latencies -Percentile 50
        $p95Ms = Get-PerfPercentile -Values $latencies -Percentile 95
        $p99Ms = Get-PerfPercentile -Values $latencies -Percentile 99
        $maxMs = Get-PerfPercentile -Values $latencies -Percentile 100
        $latencySampleCount = $latencies.Count
    }
    $rps = 0.0
    if ($durationMs -gt 0) {
        $rps = [Math]::Round(($total / ($durationMs / 1000.0)), 2)
    }
    $cpuSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "cpu_percent" -Default $null
        if ($null -ne $value) {
            [Math]::Round([double]$value, 2)
        }
    })
    $memorySeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "memory_mb" -Default $null
        if ($null -ne $value) {
            [Math]::Round([double]$value, 2)
        }
    })
    $queueSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "queue_depth" -Default $null
        if ($null -ne $value) {
            [int]$value
        }
    })
    $readinessStatusSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "readiness_status" -Default $null
        if ($null -ne $value -and [string]$value -ne "") {
            [string]$value
        }
    })
    $migrationStatusSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "migration_status" -Default $null
        if ($null -ne $value -and [string]$value -ne "") {
            [string]$value
        }
    })
    $readinessNotReadySamples = @($readinessStatusSeries | Where-Object { $_ -ne "ready" }).Count
    $migrationNotReadySamples = @($migrationStatusSeries | Where-Object { $_ -ne "ready" }).Count
    $dbInUseSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "db_in_use_connections" -Default $null
        if ($null -ne $value) {
            [int]$value
        }
    })
    $dbWaitSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "db_wait_count" -Default $null
        if ($null -ne $value) {
            [int64]$value
        }
    })
    $dbWaitDurationSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "db_wait_duration_ms" -Default $null
        if ($null -ne $value) {
            [int64]$value
        }
    })
    $dbReadinessInUseSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "db_readiness_in_use_connections" -Default $null
        if ($null -ne $value) {
            [int]$value
        }
    })
    $dbReadinessWaitSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "db_readiness_wait_count" -Default $null
        if ($null -ne $value) {
            [int64]$value
        }
    })
    $dbReadinessWaitDurationSeries = @($samples | ForEach-Object {
        $value = Get-PerfObjectValue -Object $_ -Key "db_readiness_wait_duration_ms" -Default $null
        if ($null -ne $value) {
            [int64]$value
        }
    })
    $recoveryResults = @(Get-PerfObjectValue -Object $Raw -Key "recovery_probe_results" -Default @())
    $recoverySuccess = @($recoveryResults | Where-Object { [bool](Get-PerfObjectValue -Object $_ -Key "success" -Default $false) }).Count
    $usageEvidenceResults = @(Get-PerfObjectValue -Object $Raw -Key "usage_evidence_results" -Default @())
    $usageEvidenceFound = @($usageEvidenceResults | Where-Object { [bool](Get-PerfObjectValue -Object $_ -Key "found" -Default $false) }).Count
    $usagePromptTokens = 0
    $usageCompletionTokens = 0
    $usageModelCalls = 0
    $usageModelFailures = 0
    foreach ($usage in $usageEvidenceResults) {
        $usagePromptTokens += [int](Get-PerfObjectValue -Object $usage -Key "prompt_tokens_total" -Default 0)
        $usageCompletionTokens += [int](Get-PerfObjectValue -Object $usage -Key "completion_tokens_total" -Default 0)
        $usageModelCalls += [int](Get-PerfObjectValue -Object $usage -Key "model_calls_total" -Default 0)
        $usageModelFailures += [int](Get-PerfObjectValue -Object $usage -Key "model_failures_total" -Default 0)
    }
    $metricsBefore = Get-PerfObjectValue -Object $Raw -Key "metrics_before" -Default $null
    $metricsAfterLoad = Get-PerfObjectValue -Object $Raw -Key "metrics_after_load" -Default $null
    $metrics = Get-PerfObjectValue -Object $Raw -Key "metrics_snapshot" -Default $null
    $dbWaitBefore = Get-PerfObjectValue -Object $metricsBefore -Key "db_wait_count" -Default $null
    $dbWaitAfter = Get-PerfObjectValue -Object $metrics -Key "db_wait_count" -Default $null
    $dbWaitDelta = $null
    if ($null -ne $dbWaitBefore -and $null -ne $dbWaitAfter) {
        $dbWaitDelta = [int64]$dbWaitAfter - [int64]$dbWaitBefore
    }
    $dbWaitDurationBefore = Get-PerfObjectValue -Object $metricsBefore -Key "db_wait_duration_ms" -Default $null
    $dbWaitDurationAfter = Get-PerfObjectValue -Object $metrics -Key "db_wait_duration_ms" -Default $null
    $dbWaitDurationDelta = $null
    if ($null -ne $dbWaitDurationBefore -and $null -ne $dbWaitDurationAfter) {
        $dbWaitDurationDelta = [int64]$dbWaitDurationAfter - [int64]$dbWaitDurationBefore
    }
    $dbReadinessWaitBefore = Get-PerfObjectValue -Object $metricsBefore -Key "db_readiness_wait_count" -Default $null
    $dbReadinessWaitAfter = Get-PerfObjectValue -Object $metrics -Key "db_readiness_wait_count" -Default $null
    $dbReadinessWaitDelta = $null
    if ($null -ne $dbReadinessWaitBefore -and $null -ne $dbReadinessWaitAfter) {
        $dbReadinessWaitDelta = [int64]$dbReadinessWaitAfter - [int64]$dbReadinessWaitBefore
    }
    $dbReadinessWaitDurationBefore = Get-PerfObjectValue -Object $metricsBefore -Key "db_readiness_wait_duration_ms" -Default $null
    $dbReadinessWaitDurationAfter = Get-PerfObjectValue -Object $metrics -Key "db_readiness_wait_duration_ms" -Default $null
    $dbReadinessWaitDurationDelta = $null
    if ($null -ne $dbReadinessWaitDurationBefore -and $null -ne $dbReadinessWaitDurationAfter) {
        $dbReadinessWaitDurationDelta = [int64]$dbReadinessWaitDurationAfter - [int64]$dbReadinessWaitDurationBefore
    }
    return [ordered]@{
        status = Get-PerfObjectValue -Object $Raw -Key "status" -Default "completed"
        started_at = Get-PerfObjectValue -Object $Raw -Key "started_at" -Default $null
        completed_at = Get-PerfObjectValue -Object $Raw -Key "completed_at" -Default $null
        requested_total_requests = [int](Get-PerfObjectValue -Object $Raw -Key "requested_total_requests" -Default $total)
        total_requests = $total
        detailed_result_limit = [int](Get-PerfObjectValue -Object $Raw -Key "detailed_result_limit" -Default 0)
        failed_result_limit = [int](Get-PerfObjectValue -Object $Raw -Key "failed_result_limit" -Default 0)
        results_recorded = $results.Count
        failed_results_recorded = $failedResults.Count
        results_truncated = [int](Get-PerfObjectValue -Object $Raw -Key "results_truncated" -Default 0)
        latency_sample_limit = [int](Get-PerfObjectValue -Object $Raw -Key "latency_sample_limit" -Default 0)
        latency_sample_count = $latencySampleCount
        latency_samples_truncated = $latencySamplesTruncated
        concurrency = [int](Get-PerfObjectValue -Object $Raw -Key "concurrency" -Default 0)
        concurrency_stages = @(Get-PerfObjectValue -Object $Raw -Key "concurrency_stages" -Default @())
        soak_seconds = [int](Get-PerfObjectValue -Object $Raw -Key "soak_seconds" -Default 0)
        success = $success
        failed = $failed
        status_counts = $statusCounts
        runtime_error_code_counts = $runtimeErrorCodeCounts
        failed_results = @($failedResults)
        rps = $rps
        p50_ms = $p50Ms
        p95_ms = $p95Ms
        p99_ms = $p99Ms
        max_ms = $maxMs
        cpu_percent_series = $cpuSeries
        memory_mb_series = $memorySeries
        metrics_http_requests_total = Get-PerfObjectValue -Object $metrics -Key "http_requests_total" -Default $null
        metrics_http_request_body_rejected_total = Get-PerfObjectValue -Object $metrics -Key "http_request_body_rejected_total" -Default $null
        metrics_agent_runs_total = Get-PerfObjectValue -Object $metrics -Key "agent_runs_total" -Default $null
        metrics_agent_runs_running = Get-PerfObjectValue -Object $metrics -Key "agent_runs_running" -Default $null
        metrics_agent_runs_rejected_total = Get-PerfObjectValue -Object $metrics -Key "agent_runs_rejected_total" -Default $null
        metrics_after_load_agent_runs_running = Get-PerfObjectValue -Object $metricsAfterLoad -Key "agent_runs_running" -Default $null
        db_open_connections = Get-PerfObjectValue -Object $metrics -Key "db_open_connections" -Default $null
        db_max_open_connections = Get-PerfObjectValue -Object $metrics -Key "db_max_open_connections" -Default $null
        db_in_use_connections = Get-PerfObjectValue -Object $metrics -Key "db_in_use_connections" -Default $null
        db_wait_count = Get-PerfObjectValue -Object $metrics -Key "db_wait_count" -Default $null
        db_wait_count_before = $dbWaitBefore
        db_wait_count_delta = $dbWaitDelta
        db_wait_duration_ms = Get-PerfObjectValue -Object $metrics -Key "db_wait_duration_ms" -Default $null
        db_wait_duration_ms_delta = $dbWaitDurationDelta
        db_readiness_open_connections = Get-PerfObjectValue -Object $metrics -Key "db_readiness_open_connections" -Default $null
        db_readiness_max_open_connections = Get-PerfObjectValue -Object $metrics -Key "db_readiness_max_open_connections" -Default $null
        db_readiness_in_use_connections = Get-PerfObjectValue -Object $metrics -Key "db_readiness_in_use_connections" -Default $null
        db_readiness_wait_count = Get-PerfObjectValue -Object $metrics -Key "db_readiness_wait_count" -Default $null
        db_readiness_wait_count_before = $dbReadinessWaitBefore
        db_readiness_wait_count_delta = $dbReadinessWaitDelta
        db_readiness_wait_duration_ms = Get-PerfObjectValue -Object $metrics -Key "db_readiness_wait_duration_ms" -Default $null
        db_readiness_wait_duration_ms_delta = $dbReadinessWaitDurationDelta
        readiness_status = Get-PerfObjectValue -Object $metrics -Key "readiness_status" -Default $null
        migration_status = Get-PerfObjectValue -Object $metrics -Key "migration_status" -Default $null
        readiness_status_series = $readinessStatusSeries
        migration_status_series = $migrationStatusSeries
        readiness_not_ready_samples = $readinessNotReadySamples
        migration_not_ready_samples = $migrationNotReadySamples
        queue_depth_series = $queueSeries
        queue_depth_max = Get-PerfPercentile -Values ([double[]]$queueSeries) -Percentile 100
        db_in_use_connections_series = $dbInUseSeries
        db_in_use_connections_max = Get-PerfPercentile -Values ([double[]]$dbInUseSeries) -Percentile 100
        db_wait_count_series = $dbWaitSeries
        db_wait_duration_ms_series = $dbWaitDurationSeries
        db_readiness_in_use_connections_series = $dbReadinessInUseSeries
        db_readiness_in_use_connections_max = Get-PerfPercentile -Values ([double[]]$dbReadinessInUseSeries) -Percentile 100
        db_readiness_wait_count_series = $dbReadinessWaitSeries
        db_readiness_wait_duration_ms_series = $dbReadinessWaitDurationSeries
        rejected_count = $rejected
        recovery_probe_requests = [int](Get-PerfObjectValue -Object $Raw -Key "recovery_probe_requests" -Default $recoveryResults.Count)
        recovery_probe_success = $recoverySuccess
        recovery_probe_failed = $recoveryResults.Count - $recoverySuccess
        collect_usage_evidence = [bool](Get-PerfObjectValue -Object $Raw -Key "collect_usage_evidence" -Default $false)
        usage_evidence_sample_limit = [int](Get-PerfObjectValue -Object $Raw -Key "usage_evidence_sample_limit" -Default 0)
        usage_evidence_samples = $usageEvidenceResults.Count
        usage_evidence_found = $usageEvidenceFound
        usage_model_calls_total = $usageModelCalls
        usage_model_failures_total = $usageModelFailures
        usage_prompt_tokens_total = $usagePromptTokens
        usage_completion_tokens_total = $usageCompletionTokens
        model_provider = Get-PerfObjectValue -Object $Raw -Key "model_provider" -Default ""
        model_name = Get-PerfObjectValue -Object $Raw -Key "model_name" -Default ""
        model_base_url = Get-PerfObjectValue -Object $Raw -Key "model_base_url" -Default ""
        persistence = Get-PerfObjectValue -Object $Raw -Key "persistence" -Default ""
        execution_mode = Get-PerfObjectValue -Object $Raw -Key "execution_mode" -Default ""
        run_migrations = [bool](Get-PerfObjectValue -Object $Raw -Key "run_migrations" -Default $false)
        run_max_concurrent = Get-PerfObjectValue -Object $Raw -Key "run_max_concurrent" -Default 0
        tenant_run_max_concurrent = Get-PerfObjectValue -Object $Raw -Key "tenant_run_max_concurrent" -Default 0
        agent_run_max_concurrent = Get-PerfObjectValue -Object $Raw -Key "agent_run_max_concurrent" -Default 0
        db_max_open_conns_configured = Get-PerfObjectValue -Object $Raw -Key "db_max_open_conns_configured" -Default 0
        db_max_idle_conns_configured = Get-PerfObjectValue -Object $Raw -Key "db_max_idle_conns_configured" -Default 0
        db_readiness_max_open_conns_configured = Get-PerfObjectValue -Object $Raw -Key "db_readiness_max_open_conns_configured" -Default 0
        db_readiness_max_idle_conns_configured = Get-PerfObjectValue -Object $Raw -Key "db_readiness_max_idle_conns_configured" -Default 0
        db_conn_max_lifetime_seconds_configured = Get-PerfObjectValue -Object $Raw -Key "db_conn_max_lifetime_seconds_configured" -Default 0
        db_conn_max_idle_time_seconds_configured = Get-PerfObjectValue -Object $Raw -Key "db_conn_max_idle_time_seconds_configured" -Default 0
        agent_id = Get-PerfObjectValue -Object $Raw -Key "agent_id" -Default ""
        agent_version = Get-PerfObjectValue -Object $Raw -Key "agent_version" -Default ""
        tenant_id = Get-PerfObjectValue -Object $Raw -Key "tenant_id" -Default ""
        tenant_ids = @(Get-PerfObjectValue -Object $Raw -Key "tenant_ids" -Default @())
        tenant_count = [int](Get-PerfObjectValue -Object $Raw -Key "tenant_count" -Default 1)
        base_url = Get-PerfObjectValue -Object $Raw -Key "base_url" -Default ""
        duration_ms = [Math]::Round($durationMs, 2)
        raw_result_path = Get-PerfObjectValue -Object $Raw -Key "raw_result_path" -Default ""
        notes = @(
            "db_* fields, queue_depth_series, db_wait_* deltas, and metrics_* counters are read from the server /metrics endpoint when available.",
            "recovery_probe_* records small post-load requests to confirm the server still accepts work after the pressure window.",
            "usage_* fields are sampled from /v1/usage/evidence and aggregate trace-based model token evidence for sampled successful requests."
        )
    }
}

if ($ValidateOnly) {
    $raw = [ordered]@{
        status = "completed"
        started_at = "2026-06-05T00:00:00Z"
        completed_at = "2026-06-05T00:00:01Z"
        total_requests = 3
        concurrency = 2
        duration_ms = 1000
        detailed_result_limit = 2
        failed_result_limit = 2
        results_truncated = 1
        latency_sample_limit = 3
        summary = [ordered]@{
            total_requests = 3
            success = 2
            failed = 1
            rejected_count = 1
            status_counts = [ordered]@{ "200" = 2; "429" = 1 }
            runtime_error_code_counts = [ordered]@{ ADMISSION_REJECTED = 1 }
            p50_ms = 20
            p95_ms = 30
            p99_ms = 30
            max_ms = 30
            latency_sample_count = 3
            latency_samples_truncated = 0
        }
        model_provider = "stub"
        model_name = "stub"
        model_base_url = ""
        persistence = "in-memory"
        execution_mode = "sync"
        run_max_concurrent = 0
        tenant_run_max_concurrent = 0
        agent_run_max_concurrent = 0
        db_readiness_max_open_conns_configured = 2
        db_readiness_max_idle_conns_configured = 2
        agent_id = "test-agent"
        agent_version = "v1"
        tenant_id = "tenant_perf"
        tenant_ids = @("tenant_perf", "tenant_perf_b")
        tenant_count = 2
        base_url = "http://localhost:0"
        results = @(
            [ordered]@{ status_code = 200; duration_ms = 10; success = $true },
            [ordered]@{ status_code = 200; duration_ms = 20; success = $true },
            [ordered]@{ status_code = 429; duration_ms = 30; success = $false; runtime_error_code = "ADMISSION_REJECTED" }
        )
        failed_results = @(
            [ordered]@{ status_code = 429; duration_ms = 30; success = $false; runtime_error_code = "ADMISSION_REJECTED" }
        )
        samples = @(
            [ordered]@{ cpu_percent = 12.5; memory_mb = 100.0; queue_depth = 0; db_in_use_connections = 0; db_wait_count = 0; db_wait_duration_ms = 0 },
            [ordered]@{ cpu_percent = 25.0; memory_mb = 101.0; queue_depth = 1; db_in_use_connections = 1; db_wait_count = 2; db_wait_duration_ms = 4; db_readiness_in_use_connections = 1; db_readiness_wait_count = 1; db_readiness_wait_duration_ms = 2; readiness_status = "ready"; migration_status = "ready" }
        )
        metrics_before = [ordered]@{ db_open_connections = 1; db_in_use_connections = 0; db_wait_count = 1; db_wait_duration_ms = 2 }
        metrics_snapshot = [ordered]@{ db_open_connections = 1; db_in_use_connections = 0; db_wait_count = 3; db_wait_duration_ms = 8; db_readiness_max_open_connections = 2; db_readiness_open_connections = 1; db_readiness_in_use_connections = 0; db_readiness_wait_count = 1; db_readiness_wait_duration_ms = 2 }
        recovery_probe_requests = 1
        recovery_probe_results = @([ordered]@{ status_code = 200; duration_ms = 10; success = $true })
        collect_usage_evidence = $true
        usage_evidence_sample_limit = 1
        usage_evidence_results = @([ordered]@{ trace_id = "trace_1"; found = $true; model_calls_total = 1; model_failures_total = 0; prompt_tokens_total = 11; completion_tokens_total = 7 })
    }
    $report = ConvertTo-SingleNodePerfReport -Raw $raw
    if ($report.total_requests -ne 3 -or $report.success -ne 2 -or $report.rejected_count -ne 1 -or $report.p95_ms -ne 30 -or $report.results_truncated -ne 1 -or $report.failed_results_recorded -ne 1 -or $report.tenant_count -ne 2 -or $report.latency_sample_count -ne 3 -or $report.db_wait_count_delta -ne 2 -or $report.queue_depth_max -ne 1 -or $report.readiness_not_ready_samples -ne 0 -or $report.migration_not_ready_samples -ne 0 -or $report.db_readiness_max_open_conns_configured -ne 2 -or $report.db_readiness_max_open_connections -ne 2 -or $report.db_readiness_in_use_connections_max -ne 1 -or $report.recovery_probe_success -ne 1 -or $report.usage_prompt_tokens_total -ne 11 -or $report.runtime_error_code_counts.ADMISSION_REJECTED -ne 1) {
        throw "single-node report validation failed"
    }
    Write-Host "perf_single_node_report validation passed"
    exit 0
}

if ([string]::IsNullOrWhiteSpace($InputPath)) {
    throw "InputPath is required unless -ValidateOnly is set."
}
if ([string]::IsNullOrWhiteSpace($OutputPath)) {
    $dir = Split-Path -Parent (Resolve-Path $InputPath)
    $OutputPath = Join-Path $dir "single-node-perf-report.json"
}

$rawText = Read-TextFileUtf8 -Path $InputPath
$raw = $rawText | ConvertFrom-Json
$report = ConvertTo-SingleNodePerfReport -Raw $raw
Write-E2EJson -Path $OutputPath -Value $report
Write-Host "single-node perf report written: $OutputPath"
