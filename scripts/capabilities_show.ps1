param(
    [Parameter(Mandatory = $true)] [string]$PackageDir,
    [ValidateSet("markdown", "json")] [string]$Format = "markdown",
    [string]$OutputPath = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$package = Read-AgentPackage -PackageDir $PackageDir
$skillsDir = Join-Path $package.PackageDir "skills"
$skills = @()
if (Test-Path $skillsDir) {
    foreach ($file in @(Get-ChildItem -Path $skillsDir -File -Recurse)) {
        $skills += [pscustomobject]@{
            path = $file.FullName
            name = $file.BaseName
            bytes = $file.Length
        }
    }
}

$report = [pscustomobject]@{
    agent_id = $package.AgentID
    version = $package.Version
    package_dir = $package.PackageDir
    tool_bindings = $package.ToolBindings
    allowed_tool_count = @($package.ToolBindings.allowed_tool_ids).Count
    exposed_tool_count = @($package.ToolBindings.exposed_tool_ids).Count
    denied_tool_count = @($package.ToolBindings.denied_tool_ids).Count
    skills = $skills
    notes = @(
        "This report shows package-declared visibility.",
        "Runtime-retrieved tool cards still depend on the running registry and WorkView retrieval."
    )
}

if ($Format -eq "json") {
    $content = $report | ConvertTo-Json -Depth 40
} else {
    $builder = [System.Text.StringBuilder]::new()
    [void]$builder.AppendLine("# Capability Visibility")
    [void]$builder.AppendLine()
    [void]$builder.AppendLine("- agent: $($package.AgentID)@$($package.Version)")
    [void]$builder.AppendLine("- package_dir: $($package.PackageDir)")
    [void]$builder.AppendLine()
    [void]$builder.AppendLine("## Tool bindings")
    [void]$builder.AppendLine()
    [void]$builder.AppendLine('```json')
    [void]$builder.AppendLine(($package.ToolBindings | ConvertTo-Json -Depth 20))
    [void]$builder.AppendLine('```')
    [void]$builder.AppendLine()
    [void]$builder.AppendLine("## Skills")
    [void]$builder.AppendLine()
    if ($skills.Count -eq 0) {
        [void]$builder.AppendLine("No local skill files found.")
    } else {
        foreach ($skill in $skills) {
            [void]$builder.AppendLine("- $($skill.name): $($skill.path)")
        }
    }
    [void]$builder.AppendLine()
    [void]$builder.AppendLine("## Notes")
    [void]$builder.AppendLine()
    foreach ($note in $report.notes) {
        [void]$builder.AppendLine("- $note")
    }
    $content = $builder.ToString()
}

if ($OutputPath -ne "") {
    if ([System.IO.Path]::IsPathRooted($OutputPath)) {
        $targetPath = $OutputPath
    } else {
        $targetPath = Join-Path (Get-Location) $OutputPath
    }
    [System.IO.File]::WriteAllText($targetPath, $content, [System.Text.UTF8Encoding]::new($false))
    Write-Host "Capability visibility written. path=$OutputPath"
} else {
    Write-Output $content
}
