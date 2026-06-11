param(
    [Parameter(Mandatory = $true)] [string]$PackageDir,
    [string]$Input = "What can you do?",
    [ValidateSet("markdown", "json")] [string]$Format = "markdown",
    [string]$OutputPath = "",
    [string]$BaseUrl = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

$previewSource = "local"
if ($BaseUrl -ne "") {
    $preview = Invoke-ServerPromptPreview -BaseUrl $BaseUrl -PackageDir $PackageDir -Input $Input
    $previewSource = "server"
} else {
    $preview = New-OriginPromptBundlePreview -PackageDir $PackageDir -Input $Input
}

if ($Format -eq "json") {
    $content = $preview | ConvertTo-Json -Depth 40
} else {
    if ($previewSource -eq "server") {
        $content = Format-ServerPromptPreviewMarkdown -Preview $preview
    } else {
        $content = Format-PromptPreviewMarkdown -Preview $preview
    }
}

if ($OutputPath -ne "") {
    if ([System.IO.Path]::IsPathRooted($OutputPath)) {
        $targetPath = $OutputPath
    } else {
        $targetPath = Join-Path (Get-Location) $OutputPath
    }
    [System.IO.File]::WriteAllText($targetPath, $content, [System.Text.UTF8Encoding]::new($false))
    $hashValue = Get-ObjectValue $preview "prompt_bundle_hash" (Get-ObjectValue $preview "prompt_bundle_hash" "")
    Write-Host "Prompt preview written. source=$previewSource path=$OutputPath hash=$hashValue"
} else {
    Write-Output $content
}
