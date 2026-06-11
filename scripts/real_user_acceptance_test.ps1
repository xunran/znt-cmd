param(
    [string]$PackageDir = "agent_packages\origin-coordinator",
    [string]$EnvFile = ".\.env",
    [string]$ReportDir = "",
    [switch]$RunRealModel,
    [switch]$RunPostgres,
    [switch]$KeepDocker
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

if ($ReportDir -eq "") {
    $ReportDir = New-E2EReportDir -Name "real-user-acceptance"
} else {
    New-Item -ItemType Directory -Path $ReportDir -Force | Out-Null
}

$logDir = Join-Path $ReportDir "logs"
New-Item -ItemType Directory -Path $logDir -Force | Out-Null

$summary = [ordered]@{
    status = "started"
    report_dir = $ReportDir
    package_dir = (Resolve-RepoRelativePath $PackageDir)
    run_real_model = [bool]$RunRealModel
    run_postgres = [bool]$RunPostgres
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    steps = @()
}

function Write-Utf8 {
    param(
        [Parameter(Mandatory = $true)] [string]$Path,
        [Parameter(Mandatory = $true)] [string]$Text
    )
    [System.IO.File]::WriteAllText($Path, $Text, [System.Text.UTF8Encoding]::new($false))
}

function Append-Utf8 {
    param(
        [Parameter(Mandatory = $true)] [string]$Path,
        [Parameter(Mandatory = $true)] [string]$Text
    )
    [System.IO.File]::AppendAllText($Path, $Text, [System.Text.UTF8Encoding]::new($false))
}

function Safe-Name {
    param([Parameter(Mandatory = $true)] [string]$Name)
    return ($Name -replace "[^A-Za-z0-9_.-]", "_")
}

function Add-StepRecord {
    param($Step)
    $script:summary.steps += [pscustomobject]$Step
}

function Invoke-LocalStep {
    param(
        [Parameter(Mandatory = $true)] [string]$Name,
        [Parameter(Mandatory = $true)] [scriptblock]$Block
    )
    $step = [ordered]@{
        name = $Name
        status = "started"
        started_at = (Get-Date).ToUniversalTime().ToString("o")
    }
    try {
        $result = & $Block
        if ($null -ne $result) {
            $step.result = $result
        }
        $step.status = "passed"
    } catch {
        $step.status = "failed"
        $step.error = $_.Exception.Message
        Add-StepRecord $step
        throw
    }
    $step.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Add-StepRecord $step
}

function Invoke-ScriptStep {
    param(
        [Parameter(Mandatory = $true)] [string]$Name,
        [Parameter(Mandatory = $true)] [string]$Script,
        [string[]]$Args = @(),
        [switch]$ExpectFailure
    )
    $step = [ordered]@{
        name = $Name
        status = "started"
        started_at = (Get-Date).ToUniversalTime().ToString("o")
    }
    $logPath = Join-Path $logDir ((Safe-Name $Name) + ".log")
    $previousErrorAction = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = & powershell -NoProfile -ExecutionPolicy Bypass -File $Script @Args 2>&1
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorAction
    }
    Write-Utf8 -Path $logPath -Text (($output | Out-String).TrimEnd() + "`n")
    $step.log = $logPath
    $step.exit_code = $exitCode
    if ($ExpectFailure) {
        if ($exitCode -eq 0) {
            $step.status = "failed"
            $step.error = "expected failure but command passed"
            Add-StepRecord $step
            throw $step.error
        }
        $step.status = "passed"
        $step.expected_failure_observed = $true
    } else {
        if ($exitCode -ne 0) {
            $step.status = "failed"
            $step.error = "command failed with exit code $exitCode"
            Add-StepRecord $step
            throw ($step.error + ". See " + $logPath)
        }
        $step.status = "passed"
    }
    $step.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Add-StepRecord $step
}

function Copy-Package {
    param(
        [Parameter(Mandatory = $true)] [string]$Source,
        [Parameter(Mandatory = $true)] [string]$Destination
    )
    if (Test-Path $Destination) {
        Remove-Item -LiteralPath $Destination -Recurse -Force
    }
    Copy-Item -Path (Resolve-RepoRelativePath $Source) -Destination $Destination -Recurse -Force
}

function Wait-ComposePostgres {
    param([int]$TimeoutSeconds = 90)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $status = (& docker compose ps postgres --format json 2>$null | Out-String).Trim()
        if ($status -ne "") {
            try {
                $parsed = $status | ConvertFrom-Json
                $health = ""
                if ($parsed -is [array]) {
                    if ($parsed.Count -gt 0) {
                        $health = [string]$parsed[0].Health
                    }
                } else {
                    $health = [string]$parsed.Health
                }
                if ($health -eq "healthy") {
                    return
                }
            } catch {
            }
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    throw "postgres did not become healthy within ${TimeoutSeconds}s"
}

try {
    $userPackageDir = Join-Path $ReportDir "user-edited-package"
    $missingFileDir = Join-Path $ReportDir "negative-missing-file"
    $secretDir = Join-Path $ReportDir "negative-secret"

    Invoke-LocalStep "simulate-user-edits" {
        Copy-Package -Source $PackageDir -Destination $userPackageDir
        Append-Utf8 -Path (Join-Path $userPackageDir "developer.md") -Text "`nReal-user acceptance note: keep unsupported external execution explicit and concise.`n"
        return @{ package_dir = $userPackageDir }
    }

    Invoke-ScriptStep "package-validate" "$PSScriptRoot\package_validate.ps1" @(
        "-PackageDir", $userPackageDir,
        "-ReportPath", (Join-Path $ReportDir "package-validate.json")
    )

    Invoke-ScriptStep "prompt-lint" "$PSScriptRoot\prompt_lint.ps1" @(
        "-PackageDir", $userPackageDir,
        "-ReportPath", (Join-Path $ReportDir "prompt-lint.json")
    )

    Invoke-ScriptStep "prompt-preview" "$PSScriptRoot\prompt_preview.ps1" @(
        "-PackageDir", $userPackageDir,
        "-Input", (Get-SmokeText "capability_question"),
        "-OutputPath", (Join-Path $ReportDir "prompt-preview.md")
    )

    Invoke-ScriptStep "capabilities-show" "$PSScriptRoot\capabilities_show.ps1" @(
        "-PackageDir", $userPackageDir,
        "-OutputPath", (Join-Path $ReportDir "capabilities.md")
    )

    Invoke-ScriptStep "package-diff" "$PSScriptRoot\package_diff.ps1" @(
        "-LeftDir", (Resolve-RepoRelativePath $PackageDir),
        "-RightDir", $userPackageDir,
        "-OutputPath", (Join-Path $ReportDir "package-diff.md")
    )

    Invoke-LocalStep "prepare-experiment-file" {
        $experimentPath = Join-Path $ReportDir "experiment.json"
        $experiment = [ordered]@{
            name = "real-user-boundary-ab"
            base_package = $userPackageDir
            eval_suite = "eval/smoke.yaml"
            variants = @(
                [ordered]@{ name = "base" },
                [ordered]@{
                    name = "strict-boundary"
                    developer_append = "During real-user acceptance, prefer unsupported over speculative external execution."
                }
            )
            models = @([ordered]@{ name = "env-default"; provider = "openai-compatible"; temperature = 0 })
        }
        Write-E2EJson -Path $experimentPath -Value $experiment
        return @{ experiment_path = $experimentPath }
    }

    Invoke-ScriptStep "experiment-local" "$PSScriptRoot\experiment_run.ps1" @(
        "-ExperimentPath", (Join-Path $ReportDir "experiment.json"),
        "-ReportDir", (Join-Path $ReportDir "experiment-local")
    )

    Invoke-LocalStep "negative-missing-file-setup" {
        Copy-Package -Source $userPackageDir -Destination $missingFileDir
        Remove-Item -LiteralPath (Join-Path $missingFileDir "developer.md") -Force
        return @{ package_dir = $missingFileDir }
    }

    Invoke-ScriptStep "negative-missing-file" "$PSScriptRoot\package_validate.ps1" @(
        "-PackageDir", $missingFileDir,
        "-ReportPath", (Join-Path $ReportDir "negative-missing-file.json")
    ) -ExpectFailure

    Invoke-LocalStep "negative-secret-setup" {
        Copy-Package -Source $userPackageDir -Destination $secretDir
        Append-Utf8 -Path (Join-Path $secretDir "developer.md") -Text "`nFake test secret that must be blocked: sk-FAKE1234567890`n"
        return @{ package_dir = $secretDir }
    }

    Invoke-ScriptStep "negative-secret-lint" "$PSScriptRoot\prompt_lint.ps1" @(
        "-PackageDir", $secretDir,
        "-ReportPath", (Join-Path $ReportDir "negative-secret-lint.json")
    ) -ExpectFailure

    Invoke-ScriptStep "api-smoke-user-package" "$PSScriptRoot\e2e_api_smoke.ps1" @(
        "-PackageDir", $userPackageDir,
        "-ReportDir", (Join-Path $ReportDir "api-smoke")
    )

    if ($RunRealModel) {
        Invoke-ScriptStep "real-model-smoke-user-package" "$PSScriptRoot\e2e_deepseek_smoke.ps1" @(
            "-PackageDir", $userPackageDir,
            "-EnvFile", $EnvFile,
            "-ReportDir", (Join-Path $ReportDir "deepseek-smoke")
        )
    }

    if ($RunPostgres) {
        Invoke-LocalStep "postgres-start" {
            Push-Location (Get-RepoRoot)
            try {
                & docker compose up -d postgres
                if ($LASTEXITCODE -ne 0) {
                    throw "docker compose up -d postgres failed"
                }
                Wait-ComposePostgres
            } finally {
                Pop-Location
            }
            return @{ database_url = "postgres://clean_core:***@localhost:5432/clean_core?sslmode=disable" }
        }
        $env:CLEAN_CORE_DATABASE_URL = "postgres://clean_core:clean_core_dev@localhost:5432/clean_core?sslmode=disable"
    }

    $rcArgs = @(
        "-PackageDir", $userPackageDir
    )
    if ($RunPostgres) {
        $rcArgs += "-RunPostgres"
    }
    if ($RunRealModel) {
        $rcArgs += @("-RunRealModel", "-DeepSeekEnvFile", $EnvFile)
    }
    Invoke-ScriptStep "release-candidate-user-package" "$PSScriptRoot\release_candidate_check.ps1" $rcArgs

    $summary.status = "passed"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    throw
} finally {
    if ($RunPostgres -and -not $KeepDocker) {
        try {
            Push-Location (Get-RepoRoot)
            & docker compose down | Out-Null
        } catch {
        } finally {
            Pop-Location
        }
    }
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $ReportDir "real-user-acceptance-report.json") -Value $summary
    $md = [System.Text.StringBuilder]::new()
    [void]$md.AppendLine("# Real User Acceptance Report")
    [void]$md.AppendLine()
    [void]$md.AppendLine("- status: $($summary.status)")
    [void]$md.AppendLine("- report_dir: $ReportDir")
    [void]$md.AppendLine("- run_real_model: $RunRealModel")
    [void]$md.AppendLine("- run_postgres: $RunPostgres")
    [void]$md.AppendLine()
    [void]$md.AppendLine("| Step | Status | Log |")
    [void]$md.AppendLine("|---|---|---|")
    foreach ($step in @($summary.steps)) {
        $log = ""
        if ($step.PSObject.Properties.Name -contains "log") {
            $log = $step.log
        }
        [void]$md.AppendLine("| $($step.name) | $($step.status) | $log |")
    }
    [System.IO.File]::WriteAllText((Join-Path $ReportDir "real-user-acceptance-report.md"), $md.ToString(), [System.Text.UTF8Encoding]::new($false))
}

Write-Host "Real user acceptance completed. status=$($summary.status) report=$ReportDir"
