param(
    [switch]$RunRace,
    [switch]$RunPostgres,
    [switch]$RunRealModel,
    [string]$DeepSeekEnvFile = "",
    [string]$PackageDir = "",
    [switch]$StrictContractTests
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$root = Get-RepoRoot
$reportDir = New-E2EReportDir -Name "release-candidate"
$originalDatabaseURL = $env:CLEAN_CORE_DATABASE_URL
$summary = [ordered]@{
    status = "started"
    report_dir = $reportDir
    started_at = (Get-Date).ToUniversalTime().ToString("o")
    steps = @()
}

function Invoke-Step {
    param(
        [string]$Name,
        [scriptblock]$Block
    )
    $step = [ordered]@{
        name = $Name
        status = "started"
        started_at = (Get-Date).ToUniversalTime().ToString("o")
    }
    try {
        & $Block
        $step.status = "passed"
    } catch {
        $step.status = "failed"
        $step.error = $_.Exception.Message
        $summary.steps += [pscustomobject]$step
        throw
    }
    $step.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    $summary.steps += [pscustomobject]$step
}

function Invoke-WithDatabaseUrl {
    param(
        [AllowNull()] [string]$DatabaseUrl,
        [Parameter(Mandatory = $true)] [scriptblock]$Block
    )
    $previous = $env:CLEAN_CORE_DATABASE_URL
    try {
        if ([string]::IsNullOrWhiteSpace($DatabaseUrl)) {
            Remove-Item Env:CLEAN_CORE_DATABASE_URL -ErrorAction SilentlyContinue
        } else {
            $env:CLEAN_CORE_DATABASE_URL = $DatabaseUrl
        }
        & $Block
    } finally {
        if ([string]::IsNullOrWhiteSpace($previous)) {
            Remove-Item Env:CLEAN_CORE_DATABASE_URL -ErrorAction SilentlyContinue
        } else {
            $env:CLEAN_CORE_DATABASE_URL = $previous
        }
    }
}

try {
    Push-Location $root
    try {
        Invoke-Step "gofmt-check" {
            $go = Get-GoCommand
            $gofmt = Join-Path (Split-Path -Parent $go) "gofmt.exe"
            if (-not (Test-Path $gofmt)) {
                throw "gofmt not found next to $go"
            }
            $before = @(& $gofmt -l ./cmd ./internal ./pkg)
            if ($before.Count -gt 0) {
                throw "gofmt needed for: $($before -join ', ')"
            }
        }
        Invoke-Step "go-vet" {
            $go = Get-GoCommand
            & $go vet ./...
        }
        Invoke-Step "go-test" {
            $go = Get-GoCommand
            & $go test ./... -count=1
        }
        if ($RunRace) {
            Invoke-Step "go-test-race" {
                $go = Get-GoCommand
                & $go test ./... -race -count=1
            }
        }
    } finally {
        Pop-Location
    }

    Invoke-Step "contract-verification" {
        $args = @()
        if ($StrictContractTests) {
            $args += "-StrictTestCoverage"
        }
        & "$PSScriptRoot\verify_contracts.ps1" @args
    }

    if ($PackageDir -ne "") {
        Invoke-Step "package-validate" {
            & "$PSScriptRoot\package_validate.ps1" -PackageDir $PackageDir -ReportPath (Join-Path $reportDir "package-validate.json")
        }
        Invoke-Step "prompt-lint" {
            & "$PSScriptRoot\prompt_lint.ps1" -PackageDir $PackageDir -ReportPath (Join-Path $reportDir "prompt-lint.json")
        }
        Invoke-Step "prompt-preview" {
            & "$PSScriptRoot\prompt_preview.ps1" -PackageDir $PackageDir -Input (Get-SmokeText "capability_question") -OutputPath (Join-Path $reportDir "prompt-preview.md")
        }
        Invoke-Step "capabilities-show" {
            & "$PSScriptRoot\capabilities_show.ps1" -PackageDir $PackageDir -OutputPath (Join-Path $reportDir "capabilities.md")
        }
    }

    Invoke-Step "api-smoke" {
        Invoke-WithDatabaseUrl "" {
            if ($PackageDir -ne "") {
                & "$PSScriptRoot\e2e_api_smoke.ps1" -ReportDir (Join-Path $reportDir "api-smoke") -PackageDir $PackageDir
            } else {
                & "$PSScriptRoot\e2e_api_smoke.ps1" -ReportDir (Join-Path $reportDir "api-smoke")
            }
        }
    }

    Invoke-Step "clean-core-mmr-service-e2e" {
        Invoke-WithDatabaseUrl "" {
            & "$PSScriptRoot\e2e_clean_core_mmr_service.ps1" -ReportDir (Join-Path $reportDir "clean-core-mmr-service")
        }
    }

    Invoke-Step "clean-core-all-interfaces-e2e" {
        Invoke-WithDatabaseUrl "" {
            & "$PSScriptRoot\e2e_clean_core_all_interfaces.ps1" -ReportDir (Join-Path $reportDir "clean-core-all-interfaces")
        }
    }

    if ($RunPostgres) {
        Invoke-Step "postgres-release" {
            if ([string]::IsNullOrWhiteSpace($originalDatabaseURL)) {
                throw "RunPostgres requires CLEAN_CORE_DATABASE_URL"
            }
            Invoke-WithDatabaseUrl $originalDatabaseURL {
                if ($PackageDir -ne "") {
                    & "$PSScriptRoot\e2e_postgres_release.ps1" -ReportDir (Join-Path $reportDir "postgres-release") -PackageDir $PackageDir
                } else {
                    & "$PSScriptRoot\e2e_postgres_release.ps1" -ReportDir (Join-Path $reportDir "postgres-release")
                }
            }
        }
    }

    if ($RunRealModel) {
        Invoke-Step "real-model-smoke" {
            Invoke-WithDatabaseUrl "" {
                $realArgs = @{
                    ReportDir = (Join-Path $reportDir "deepseek-smoke")
                }
                if ($DeepSeekEnvFile -ne "") {
                    $realArgs["EnvFile"] = $DeepSeekEnvFile
                }
                if ($PackageDir -ne "") {
                    $realArgs["PackageDir"] = $PackageDir
                }
                & "$PSScriptRoot\e2e_deepseek_smoke.ps1" @realArgs
            }
        }
    }

    $summary.status = "passed"
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $reportDir "release-candidate-report.json") -Value $summary
    Write-Host "Release candidate check passed. report=$reportDir"
} catch {
    $summary.status = "failed"
    $summary.error = $_.Exception.Message
    $summary.completed_at = (Get-Date).ToUniversalTime().ToString("o")
    Write-E2EJson -Path (Join-Path $reportDir "release-candidate-report.json") -Value $summary
    Write-Error $_
    exit 1
}
