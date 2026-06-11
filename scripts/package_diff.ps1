param(
    [Parameter(Mandatory = $true)] [string]$LeftDir,
    [Parameter(Mandatory = $true)] [string]$RightDir,
    [string]$OutputPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$left = Read-AgentPackage -PackageDir $LeftDir
$right = Read-AgentPackage -PackageDir $RightDir
$files = @("package.yaml", "system.md", "developer.md", "prompt.md", "agents.md", "eval\smoke.yaml", "eval\release.yaml")

$builder = [System.Text.StringBuilder]::new()
[void]$builder.AppendLine("# AgentPackage Diff")
[void]$builder.AppendLine()
[void]$builder.AppendLine("- left: $($left.AgentID)@$($left.Version) $($left.PackageDir)")
[void]$builder.AppendLine("- right: $($right.AgentID)@$($right.Version) $($right.PackageDir)")
[void]$builder.AppendLine()

foreach ($file in $files) {
    $leftPath = Join-Path $left.PackageDir $file
    $rightPath = Join-Path $right.PackageDir $file
    if (-not (Test-Path $leftPath) -and -not (Test-Path $rightPath)) {
        continue
    }
    [void]$builder.AppendLine("## $file")
    [void]$builder.AppendLine()
    if (-not (Test-Path $leftPath)) {
        [void]$builder.AppendLine("Only exists in right package.")
        [void]$builder.AppendLine()
        continue
    }
    if (-not (Test-Path $rightPath)) {
        [void]$builder.AppendLine("Only exists in left package.")
        [void]$builder.AppendLine()
        continue
    }
    $leftLines = (Read-TextFileUtf8 -Path $leftPath) -split "`n"
    $rightLines = (Read-TextFileUtf8 -Path $rightPath) -split "`n"
    $diff = Compare-Object -ReferenceObject $leftLines -DifferenceObject $rightLines -IncludeEqual:$false
    if ($null -eq $diff -or @($diff).Count -eq 0) {
        [void]$builder.AppendLine("No changes.")
    } else {
        [void]$builder.AppendLine('```diff')
        foreach ($row in @($diff)) {
            $prefix = if ($row.SideIndicator -eq "=>") { "+" } else { "-" }
            [void]$builder.AppendLine($prefix + [string]$row.InputObject)
        }
        [void]$builder.AppendLine('```')
    }
    [void]$builder.AppendLine()
}

$content = $builder.ToString()
if ($OutputPath -ne "") {
    if ([System.IO.Path]::IsPathRooted($OutputPath)) {
        $targetPath = $OutputPath
    } else {
        $targetPath = Join-Path (Get-Location) $OutputPath
    }
    [System.IO.File]::WriteAllText($targetPath, $content, [System.Text.UTF8Encoding]::new($false))
    Write-Host "Package diff written. path=$OutputPath"
} else {
    Write-Output $content
}
