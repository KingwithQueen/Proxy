[CmdletBinding()]
param(
    [string] $ToolsRoot,
    [string] $VisualStudioPath,
    [ValidateSet('x86', 'x64')]
    [string[]] $Architecture = @('x86', 'x64'),
    [switch] $SkipArchives
)

$ErrorActionPreference = 'Stop'
$repo = $PSScriptRoot
if (-not $PSBoundParameters.ContainsKey('ToolsRoot')) {
    $ToolsRoot = $env:CRAY_TOOLS_ROOT
    if (-not $ToolsRoot) { $ToolsRoot = Join-Path $repo '.tools\win7' }
}
if (-not $ToolsRoot -or -not (Test-Path -LiteralPath $ToolsRoot -PathType Container)) {
    throw "Build tools directory was not found: '$ToolsRoot'. See docs/Build-Win7.md."
}
$tools = (Resolve-Path -LiteralPath $ToolsRoot).Path
$sdk = Join-Path $tools 'dotnet-sdk-10.0.404'
$llvmBin = Join-Path $tools 'llvm-mingw-20261117-msvcrt-x86_64\bin'
$mbedtls = Join-Path $tools 'mbedtls-3.6.8'
$buildRoot = Join-Path $repo 'build'
$dist = Join-Path $repo 'dist'

function Invoke-BuildTool {
    param([string] $Tool, [string[]] $Arguments)

    & $Tool @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Tool failed (exit $LASTEXITCODE)."
    }
}

function Copy-NuGetNotices {
    param([string] $AssetsFile, [string] $Destination)

    $assets = Get-Content -LiteralPath $AssetsFile -Raw | ConvertFrom-Json
    $framework = ($assets.targets.PSObject.Properties | Select-Object -First 1).Value
    foreach ($entry in $framework.PSObject.Properties) {
        if (-not ($entry.Value.runtime -or $entry.Value.runtimeTargets -or $entry.Value.native)) {
            continue
        }
        $package = $assets.libraries.PSObject.Properties[$entry.Name].Value
        foreach ($folder in $assets.packageFolders.PSObject.Properties.Name) {
            $packageDirectory = Join-Path $folder $package.path
            if (-not (Test-Path -LiteralPath $packageDirectory -PathType Container)) {
                continue
            }
            $packageName = $entry.Name.Split('/')[0]
            foreach ($notice in (Get-ChildItem -LiteralPath $packageDirectory -File)) {
                if ($notice.Name -match '^(license|notice|third[._ -]?party[._ -]?notices)([.]|$)') {
                    $name = "$packageName-$($notice.Name)"
                    Copy-Item -LiteralPath $notice.FullName -Destination (Join-Path $Destination $name)
                }
            }
            break
        }
    }
}

if (-not $VisualStudioPath) {
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
    if (Test-Path -LiteralPath $vswhere -PathType Leaf) {
        $VisualStudioPath = & $vswhere -latest -products '*' -requires Microsoft.Component.MSBuild -property installationPath
    }
    if (-not $VisualStudioPath) {
        throw 'Visual Studio MSBuild was not found. Set -VisualStudioPath to your installation directory.'
    }
}

$msbuild = Join-Path $VisualStudioPath 'MSBuild\Current\Bin\MSBuild.exe'
$cmakeTools = Join-Path $VisualStudioPath 'Common7\IDE\CommonExtensions\Microsoft\CMake'
$cmake = Join-Path $cmakeTools 'CMake\bin\cmake.exe'
$ninja = Join-Path $cmakeTools 'Ninja\ninja.exe'
if (-not (Test-Path -LiteralPath $cmake -PathType Leaf)) {
    $cmake = (Get-Command cmake.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
}
if (-not (Test-Path -LiteralPath $ninja -PathType Leaf)) {
    $ninja = (Get-Command ninja.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
}
if (-not $cmake -or -not $ninja) {
    throw 'Install CMake and Ninja with Visual Studio or put their executables on PATH.'
}
$guiRoot = Join-Path $repo 'v2rayN-win7'
$gui = Join-Path $guiRoot 'v2rayN\v2rayN.csproj'
$guiLicense = Join-Path $guiRoot 'LICENSE'
$crayLicense = Join-Path $repo 'Cray\LICENSE'
$mbedtlsLicense = Join-Path $mbedtls 'LICENSE'
$usage = Join-Path $repo 'docs\Usage-Win7.md'
$packageDocs = @('Build-Win7.md', 'Xray-Win7.md', 'AnyTLS-Win7.md') | ForEach-Object {
    Join-Path $repo "docs\$_"
}
$compilers = @{
    x86 = Join-Path $llvmBin 'i686-w64-mingw32-clang.exe'
    x64 = Join-Path $llvmBin 'x86_64-w64-mingw32-clang.exe'
}

$requiredFiles = @($msbuild, $cmake, $ninja, "$sdk\dotnet.exe", "$mbedtls\CMakeLists.txt",
    $mbedtlsLicense, $gui, $guiLicense, $crayLicense, $usage, (Join-Path $repo 'Cray\CMakeLists.txt')) +
    $packageDocs + @($Architecture | ForEach-Object { $compilers[$_] })
foreach ($required in $requiredFiles) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Required build file is missing: $required"
    }
}

$staging = Join-Path $buildRoot ([Guid]::NewGuid().ToString('N'))
$guiOutput = Join-Path $staging 'gui'
New-Item -ItemType Directory -Path $guiOutput, $dist -Force | Out-Null

try {
    # Full Framework MSBuild preserves the upstream WinForms resource format.
    $previousSdkPath = $env:MSBuildSDKsPath
    $previousDotnetRoot = $env:DOTNET_ROOT
    $previousWorkloadResolver = $env:MSBuildEnableWorkloadResolver
    try {
        $env:DOTNET_ROOT = $sdk
        $env:MSBuildSDKsPath = Join-Path $sdk 'sdk\10.0.404\Sdks'
        $env:MSBuildEnableWorkloadResolver = 'false'
        $guiOutDir = $guiOutput.Replace('\', '/') + '/'
        $guiLog = Join-Path $buildRoot 'v2rayN-msbuild.log'
        Invoke-BuildTool $msbuild @(
            $gui, '-restore', '-nologo', '-nodeReuse:false', '-v:quiet', '-clp:ErrorsOnly',
            '-p:Configuration=Release', '-p:GenerateSatelliteAssembliesForCore=true',
            "-p:OutDir=$guiOutDir", '-fileLogger',
            "-fileLoggerParameters:LogFile=$guiLog;Encoding=UTF-8;Verbosity=normal"
        )
    } finally {
        $env:MSBuildSDKsPath = $previousSdkPath
        $env:DOTNET_ROOT = $previousDotnetRoot
        $env:MSBuildEnableWorkloadResolver = $previousWorkloadResolver
    }

    if (-not (Test-Path -LiteralPath (Join-Path $guiOutput 'v2rayN.exe') -PathType Leaf)) {
        throw 'The GUI build did not produce v2rayN.exe.'
    }

    $notices = Join-Path $staging 'licenses'
    New-Item -ItemType Directory -Path $notices | Out-Null
    Get-ChildItem -LiteralPath (Join-Path $repo 'licenses') -File | Copy-Item -Destination $notices
    Copy-Item -LiteralPath $guiLicense -Destination (Join-Path $notices 'GPL-3.0.txt')
    Copy-Item -LiteralPath $crayLicense -Destination (Join-Path $notices 'Cray-GPL-3.0.txt')
    Copy-Item -LiteralPath $mbedtlsLicense -Destination (Join-Path $notices 'Mbed-TLS.txt')
    $assetsFile = Join-Path (Split-Path -Parent $gui) 'obj\project.assets.json'
    Copy-NuGetNotices $assetsFile $notices

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    foreach ($arch in $Architecture) {
        $compiler = $compilers[$arch]
        $coreBuild = Join-Path $buildRoot "Cray-$arch"
        Invoke-BuildTool $cmake @(
            '-S', (Join-Path $repo 'Cray'), '-B', $coreBuild, '-G', 'Ninja',
            '-DCMAKE_BUILD_TYPE=Release', '-DCMAKE_SYSTEM_NAME=Windows',
            "-DCMAKE_C_COMPILER=$compiler", "-DCMAKE_MAKE_PROGRAM=$ninja",
            '-DCMAKE_C_FLAGS=-D_WIN32_WINNT=0x0601 -DWINVER=0x0601',
            "-DMBEDTLS_SOURCE_DIR=$mbedtls"
        )
        Invoke-BuildTool $cmake @('--build', $coreBuild)

        $name = "v2rayN-5.39-Cray-Win7-SP1-$arch"
        $package = Join-Path $staging $name
        $licenses = Join-Path $package 'licenses'
        $docs = Join-Path $package 'docs'
        New-Item -ItemType Directory -Path $package | Out-Null
        Get-ChildItem -LiteralPath $guiOutput | Copy-Item -Destination $package -Recurse
        Copy-Item -LiteralPath (Join-Path $coreBuild 'Cray.exe') -Destination $package
        Copy-Item -LiteralPath $usage -Destination (Join-Path $package 'README.md')
        New-Item -ItemType Directory -Path $docs | Out-Null
        Copy-Item -LiteralPath $packageDocs -Destination $docs
        Copy-Item -LiteralPath $notices -Destination $licenses -Recurse

        if ($SkipArchives) {
            Write-Output $package
            continue
        }

        $zip = Join-Path $staging "$name.zip"
        [IO.Compression.ZipFile]::CreateFromDirectory($package, $zip)
        $destination = Join-Path $dist "$name.zip"
        Move-Item -LiteralPath $zip -Destination $destination -Force
        Get-FileHash -LiteralPath $destination -Algorithm SHA256
    }
} finally {
    if (-not $SkipArchives -and (Test-Path -LiteralPath $staging)) {
        $checkedBuild = (Resolve-Path -LiteralPath $buildRoot).ProviderPath
        $checkedStaging = (Resolve-Path -LiteralPath $staging).ProviderPath
        if ([IO.Path]::GetDirectoryName($checkedStaging) -eq $checkedBuild) {
            Remove-Item -LiteralPath $checkedStaging -Recurse -Force
        }
    }
}
