param(
    [string]$MigrationDir = "migrations",
    [string]$ManifestPath = "",
    [switch]$Update,
    [switch]$ValidateOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

. "$PSScriptRoot\e2e_common.ps1"

function Get-MigrationChecksumEntries {
    param(
        [string]$Dir
    )
    $resolved = Resolve-RepoRelativePath $Dir
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $entries = @()
    foreach ($file in @(Get-ChildItem -Path $resolved -Filter "*.sql" | Sort-Object Name)) {
        $separator = $file.Name.IndexOf("_")
        if ($separator -lt 1) {
            throw "migration file $($file.Name) must use version_name.sql format"
        }
        $version = $file.Name.Substring(0, $separator)
        $rest = $file.Name.Substring($separator + 1)
        if ([string]::IsNullOrWhiteSpace($rest)) {
            throw "migration file $($file.Name) must use version_name.sql format"
        }
        $bytes = Get-NormalizedMigrationChecksumBytes -Path $file.FullName
        $checksum = [System.BitConverter]::ToString($sha.ComputeHash($bytes)).Replace("-", "").ToLowerInvariant()
        $entries += [ordered]@{
            version = $version
            name = $rest.Substring(0, $rest.Length - 4)
            file = $file.Name
            checksum = $checksum
        }
    }
    return $entries
}

function Get-NormalizedMigrationChecksumBytes {
    param(
        [Parameter(Mandatory = $true)] [string]$Path
    )
    $bytes = [System.IO.File]::ReadAllBytes($Path)
    $stream = [System.IO.MemoryStream]::new()
    try {
        for ($i = 0; $i -lt $bytes.Length; $i++) {
            if ($bytes[$i] -eq 13) {
                if (($i + 1) -lt $bytes.Length -and $bytes[$i + 1] -eq 10) {
                    $i++
                }
                $stream.WriteByte(10)
            } else {
                $stream.WriteByte($bytes[$i])
            }
        }
        return $stream.ToArray()
    } finally {
        $stream.Dispose()
    }
}

function New-MigrationChecksumManifest {
    param(
        [string]$Dir
    )
    return [ordered]@{
        schema = "clean-core-migration-checksums.v1"
        migrations = @((Get-MigrationChecksumEntries -Dir $Dir))
    }
}

function Assert-MigrationChecksumManifest {
    param(
        $Current,
        $Expected
    )
    if ($Expected.schema -ne "clean-core-migration-checksums.v1") {
        throw "migration checksum manifest schema must be clean-core-migration-checksums.v1"
    }
    $expectedByVersion = @{}
    foreach ($entry in @($Expected.migrations)) {
        $expectedByVersion[[string]$entry.version] = $entry
    }
    $seen = @{}
    foreach ($entry in @($Current.migrations)) {
        $version = [string]$entry.version
        if (-not $expectedByVersion.ContainsKey($version)) {
            throw "migration $version is missing from checksum manifest. Add it intentionally with scripts/check_migration_checksums.ps1 -Update."
        }
        $expected = $expectedByVersion[$version]
        if ($expected.file -ne $entry.file -or $expected.name -ne $entry.name) {
            throw "migration $version manifest metadata mismatch: manifest=$($expected.file) current=$($entry.file)"
        }
        if ($expected.checksum -ne $entry.checksum) {
            throw "migration $version ($($entry.file)) checksum mismatch: manifest=$($expected.checksum) current=$($entry.checksum). Applied migrations are immutable; restore the original SQL or create a new migration file."
        }
        $seen[$version] = $true
    }
    foreach ($version in $expectedByVersion.Keys) {
        if (-not $seen.ContainsKey($version)) {
            throw "migration checksum manifest references missing migration $version"
        }
    }
}

if ([string]::IsNullOrWhiteSpace($ManifestPath)) {
    $ManifestPath = Join-Path (Resolve-RepoRelativePath $MigrationDir) "checksums.json"
} elseif (-not [System.IO.Path]::IsPathRooted($ManifestPath)) {
    $ManifestPath = Join-Path (Get-RepoRoot) $ManifestPath
}

$manifest = New-MigrationChecksumManifest -Dir $MigrationDir
if ($Update) {
    Write-E2EJson -Path $ManifestPath -Value $manifest
    Write-Host "migration checksum manifest updated: $ManifestPath"
    exit 0
}
if (-not (Test-Path $ManifestPath)) {
    throw "migration checksum manifest not found: $ManifestPath. Create it with scripts/check_migration_checksums.ps1 -Update."
}
$expected = (Read-TextFileUtf8 -Path $ManifestPath) | ConvertFrom-Json
Assert-MigrationChecksumManifest -Current $manifest -Expected $expected
if ($ValidateOnly) {
    Write-Host "migration checksum validation passed"
} else {
    Write-Host "migration checksums ok: $ManifestPath"
}
