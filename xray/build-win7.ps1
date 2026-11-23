[CmdletBinding()]
param(
    [string] $SourceArchive,
    [string] $GoSdkArchive,
    [string] $OutputDirectory = (Join-Path $PSScriptRoot 'dist'),
    [switch] $ExperimentalAnyTls
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$xrayVersion = '26.11.9'
$goVersion = '1.27.4'
$workRoot = Join-Path ([IO.Path]::GetTempPath()) ('xray-win7-' + [Guid]::NewGuid().ToString('N'))

function Invoke-BuildCommand {
    param([string] $Command, [string[]] $Arguments)

    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command $($Arguments[0]) failed (exit $LASTEXITCODE)."
    }
}

function Expand-PinnedArchive {
    param([string] $Archive, [string] $Url, [string] $Sha256, [string] $Destination)

    if (-not $Archive) {
        $Archive = Join-Path $workRoot ([IO.Path]::GetFileName(([Uri] $Url).AbsolutePath))
        Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Archive
    }
    $resolved = (Resolve-Path -LiteralPath $Archive).ProviderPath
    $digest = (Get-FileHash -LiteralPath $resolved -Algorithm SHA256).Hash
    if ($digest -ne $Sha256) {
        throw "SHA-256 mismatch for $resolved. Expected $Sha256, got $digest."
    }
    [IO.Compression.ZipFile]::ExtractToDirectory($resolved, $Destination)
}

$environment = @{
    GOARCH = 'amd64'
    GOAMD64 = 'v1'
    GOENV = 'off'
    GOFLAGS = $null
    GOOS = 'windows'
    GOROOT = (Join-Path $workRoot 'go-win7')
    GOTOOLCHAIN = 'local'
    GOWORK = 'off'
    CGO_ENABLED = '0'
}
$previousEnvironment = @{}
foreach ($name in $environment.Keys) {
    $previousEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}

New-Item -ItemType Directory -Path $workRoot | Out-Null
try {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $sourceUrl = "https://codeload.github.com/XTLS/Xray-core/zip/refs/tags/v$xrayVersion"
    $goUrl = "https://github.com/XTLS/go-win7/releases/download/patched-$goVersion/go-for-win7-windows-amd64.zip"
    Expand-PinnedArchive $SourceArchive $sourceUrl 'eab6fad20dfabc862c2537552e136e195de93d166c9a0bab72e408b4295eb7dc' $workRoot
    Expand-PinnedArchive $GoSdkArchive $goUrl '2fccdac532798af3e7ffe73a18e25f0763d22d4c87f963075c0f59fcef8e3645' $environment.GOROOT

    foreach ($name in $environment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $environment[$name], 'Process')
    }
    $source = Join-Path $workRoot "Xray-core-$xrayVersion"
    $go = Join-Path $environment.GOROOT 'bin\go.exe'
    $outputName = 'xray.exe'
    $buildId = "v$xrayVersion"
    $buildFlags = @('-gcflags=all=-l=4')
    Push-Location -LiteralPath $source
    try {
        if ($ExperimentalAnyTls) {
            $git = Get-Command git -CommandType Application | Select-Object -First 1 -ExpandProperty Source
            $patch = Join-Path $PSScriptRoot "patches\anytls-v$xrayVersion-experimental.patch"
            Invoke-BuildCommand $git @('apply', '--check', $patch)
            Invoke-BuildCommand $git @('apply', $patch)
            $outputName = 'xray-anytls-auth-qa.exe'
            $buildId = "v$xrayVersion-anytls-auth-qa"
            $buildFlags = @()
        }

        $binary = Join-Path $workRoot $outputName
        Invoke-BuildCommand $go @('version')
        Invoke-BuildCommand $go @('mod', 'download')
        Invoke-BuildCommand $go @('mod', 'verify')
        $arguments = @('build', '-o', $binary, '-trimpath', '-buildvcs=false')
        $arguments += $buildFlags
        $arguments += @("-ldflags=-X github.com/xtls/xray-core/core.build=$buildId -s -w -buildid=", './main')
        Invoke-BuildCommand $go $arguments
    } finally {
        Pop-Location
    }

    New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
    $output = Join-Path ((Resolve-Path -LiteralPath $OutputDirectory).ProviderPath) $outputName
    Copy-Item -LiteralPath $binary -Destination $output -Force
    Get-FileHash -LiteralPath $output -Algorithm SHA256
} finally {
    foreach ($name in $environment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $previousEnvironment[$name], 'Process')
    }
    $resolvedWorkRoot = (Resolve-Path -LiteralPath $workRoot).ProviderPath
    $tempRoot = (Resolve-Path -LiteralPath ([IO.Path]::GetTempPath())).ProviderPath.TrimEnd('\')
    if ([IO.Path]::GetDirectoryName($resolvedWorkRoot) -eq $tempRoot -and
        [IO.Path]::GetFileName($resolvedWorkRoot) -match '^xray-win7-[0-9a-f]{32}$') {
        Remove-Item -LiteralPath $resolvedWorkRoot -Recurse -Force
    }
}
