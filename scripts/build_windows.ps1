# Build relocatable LLVM kitchen-sink on Windows and bundle an xwin MSVC/ucrt sysroot
# so end users can compile with clang+lld without installing Visual Studio (cl) or MinGW.
#
# Builder machine still needs a bootstrap toolchain (VS Build Tools on GHA windows-latest).
$ErrorActionPreference = "Stop"

$VersionRaw = if ($env:LLVM_VERSION) { $env:LLVM_VERSION } elseif ($env:PACKAGE_VERSION) { $env:PACKAGE_VERSION } else { throw "set LLVM_VERSION" }
$BuildTarget = if ($env:BUILD_TARGET) { $env:BUILD_TARGET } else { "windows-amd64" }
$OutputDir = if ($env:OUTPUT_DIR) { $env:OUTPUT_DIR } else { Join-Path (Get-Location) "target\\$BuildTarget" }
$Upstream = if ($env:UPSTREAM_REPO) { $env:UPSTREAM_REPO } else { "https://github.com/llvm/llvm-project.git" }
$PackageName = if ($env:PACKAGE_NAME) { $env:PACKAGE_NAME } else { "llvm" }
$Jobs = if ($env:JOBS) { [int]$env:JOBS } else { [Environment]::ProcessorCount }
$LinkJobs = if ($env:LLVM_PARALLEL_LINK_JOBS) { $env:LLVM_PARALLEL_LINK_JOBS } else { "1" }
$Projects = if ($env:LLVM_ENABLE_PROJECTS) { $env:LLVM_ENABLE_PROJECTS } else { "clang;clang-tools-extra;lld;lldb;mlir;polly;bolt" }
$Runtimes = if ($env:LLVM_ENABLE_RUNTIMES) { $env:LLVM_ENABLE_RUNTIMES } else { "compiler-rt;libcxx;libcxxabi;libunwind;openmp" }
$Targets = if ($env:LLVM_TARGETS_TO_BUILD) { $env:LLVM_TARGETS_TO_BUILD } else { "Native" }
$SkipXwin = $env:SKIP_XWIN -eq "1"

switch ($BuildTarget) {
  "windows-amd64" { $ArchiveSuffix = "windows-amd64"; $XwinArch = "x86_64" }
  "windows-arm64" { $ArchiveSuffix = "windows-arm64"; $XwinArch = "aarch64" }
  default { throw "Unsupported BUILD_TARGET: $BuildTarget" }
}

$Work = Join-Path $env:TEMP "llvm-build"
$Src = Join-Path $Work "src"
$Build = Join-Path $Work "build"
$Stage = Join-Path $Work "stage"
$Prefix = Join-Path $Stage $PackageName

if (Test-Path $Work) { Remove-Item -Recurse -Force $Work }
New-Item -ItemType Directory -Force -Path $Src, $Build, $Prefix, $OutputDir | Out-Null

Write-Host "========================================="
Write-Host "Building $PackageName $VersionRaw ($BuildTarget)"
Write-Host "========================================="

# Resolve git ref
$Ref = "main"
if ($VersionRaw -in @("trunk", "main", "git-main") -or $VersionRaw.StartsWith("trunk-")) {
  $Ref = "main"
} else {
  $Ref = $VersionRaw
}

git clone --depth 1 --branch $Ref $Upstream $Src
if ($LASTEXITCODE -ne 0) {
  if ($Ref -eq "main") {
    git clone --depth 1 --branch master $Upstream $Src
    $Ref = "master"
  } else {
    throw "clone failed for $Ref"
  }
}
$GitSha = (git -C $Src rev-parse --short=12 HEAD).Trim()
if ($VersionRaw -in @("trunk", "main") -or $VersionRaw.StartsWith("trunk-")) {
  $ArtifactVersion = "trunk-$GitSha"
} else {
  $ArtifactVersion = $VersionRaw
}

Write-Host "ref=$Ref sha=$GitSha artifact=$ArtifactVersion"

# Configure + build (bootstrap with MSVC on the runner)
$CmakeArgs = @(
  "-G", "Ninja",
  "-S", (Join-Path $Src "llvm"),
  "-B", $Build,
  "-DCMAKE_BUILD_TYPE=Release",
  "-DCMAKE_INSTALL_PREFIX=$Prefix",
  "-DLLVM_ENABLE_PROJECTS=$Projects",
  "-DLLVM_ENABLE_RUNTIMES=$Runtimes",
  "-DLLVM_TARGETS_TO_BUILD=$Targets",
  "-DLLVM_ENABLE_ZLIB=ON",
  "-DLLVM_INSTALL_UTILS=ON",
  "-DLLVM_BUILD_TOOLS=ON",
  "-DLLVM_INCLUDE_TESTS=OFF",
  "-DLLVM_INCLUDE_EXAMPLES=OFF",
  "-DLLVM_INCLUDE_BENCHMARKS=OFF",
  "-DLLVM_INCLUDE_DOCS=OFF",
  "-DCLANG_INCLUDE_TESTS=OFF",
  "-DCLANG_DEFAULT_LINKER=lld",
  "-DLLVM_PARALLEL_LINK_JOBS=$LinkJobs"
)

cmake @CmakeArgs
if ($LASTEXITCODE -ne 0) { throw "cmake configure failed" }
cmake --build $Build --parallel $Jobs
if ($LASTEXITCODE -ne 0) { throw "cmake build failed" }
cmake --install $Build
if ($LASTEXITCODE -ne 0) { throw "cmake install failed" }

Remove-Item -Recurse -Force $Build -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force $Src -ErrorAction SilentlyContinue

# --- xwin sysroot: end-user compile without cl / mingw ---
if (-not $SkipXwin) {
  Write-Host "Fetching xwin MSVC+SDK sysroot for consumer self-host..."
  $XwinVersion = if ($env:XWIN_VERSION) { $env:XWIN_VERSION } else { "0.6.6-rc.2" }
  # pin via cargo-binstall or download release
  $XwinDir = Join-Path $Work "xwin-tool"
  New-Item -ItemType Directory -Force -Path $XwinDir | Out-Null
  $XwinUrl = "https://github.com/Jake-Shadle/xwin/releases/download/$XwinVersion/xwin-$XwinVersion-x86_64-pc-windows-msvc.zip"
  $XwinZip = Join-Path $XwinDir "xwin.zip"
  try {
    Invoke-WebRequest -Uri $XwinUrl -OutFile $XwinZip -UseBasicParsing
    Expand-Archive -Path $XwinZip -DestinationPath $XwinDir -Force
    $XwinExe = Get-ChildItem -Path $XwinDir -Filter xwin.exe -Recurse | Select-Object -First 1
    if (-not $XwinExe) { throw "xwin.exe not found in archive" }
    $Sysroot = Join-Path $Prefix "xwin"
    & $XwinExe.FullName splat --output $Sysroot --arch $XwinArch --include-debug-libs=false --include-debug-symbols=false
    if ($LASTEXITCODE -ne 0) {
      Write-Warning "xwin splat failed — package will still ship clang, but self-host smoke may need a machine SDK"
    } else {
      # Convenience: clang env helper
      $Activate = @"
@echo off
REM Use this kit without Visual Studio cl.exe or MinGW.
set "ROOT=%~dp0.."
set "PATH=%ROOT%\bin;%PATH%"
set "INCLUDE=%ROOT%\xwin\crt\include;%ROOT%\xwin\sdk\include\ucrt;%ROOT%\xwin\sdk\include\um;%ROOT%\xwin\sdk\include\shared;%INCLUDE%"
set "LIB=%ROOT%\xwin\crt\lib\%XwinArch%;%ROOT%\xwin\sdk\lib\ucrt\%XwinArch%;%ROOT%\xwin\sdk\lib\um\%XwinArch%;%LIB%"
echo LLVM kit activated: %ROOT%
"@
      # fix arch folder names - xwin layout uses x86_64
      $Activate = $Activate.Replace('%XwinArch%', $XwinArch)
      New-Item -ItemType Directory -Force -Path (Join-Path $Prefix "bin") | Out-Null
      Set-Content -Path (Join-Path $Prefix "bin\activate-xwin.cmd") -Value $Activate -Encoding ASCII

      $Readme = @"
Windows self-host notes
=======================
This package bundles:
  - clang / clang++ / lld / llvm-* / clang-tools / lldb / ...
  - xwin-splatted MSVC CRT + Windows SDK bits under xwin/

You do NOT need Visual Studio's cl.exe or MinGW on the machine to compile
simple C/C++ programs. You still need a Windows OS (UCRT is system-provided
on Windows 10+).

Example:
  call bin\activate-xwin.cmd
  clang -fuse-ld=lld -o hello.exe hello.c

Or pass --sysroot explicitly to the xwin tree (see smoke in CI).
Legal: CRT/SDK contents come from Microsoft via xwin; redistrib subject to MS terms.
"@
      Set-Content -Path (Join-Path $Prefix "WINDOWS_SELFHOST.txt") -Value $Readme -Encoding UTF8
    }
  } catch {
    Write-Warning "xwin setup failed: $_"
  }
}

$BuildInfo = @"
package=$PackageName
version=$ArtifactVersion
upstream_ref=$Ref
upstream_sha=$GitSha
build_target=$BuildTarget
projects=$Projects
runtimes=$Runtimes
targets=$Targets
distributor=actions-precompiled
built_at=$((Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ"))
xwin=$(-not $SkipXwin)
"@
Set-Content -Path (Join-Path $Prefix "BUILDINFO.txt") -Value $BuildInfo -Encoding UTF8

$ArchiveName = "$PackageName-$ArtifactVersion-$ArchiveSuffix.tar.gz"
$ArchivePath = Join-Path $OutputDir $ArchiveName
# Windows tar (bsdtar) can create gzip
Push-Location $Stage
tar -czf $ArchivePath $PackageName
if ($LASTEXITCODE -ne 0) { Pop-Location; throw "tar failed" }
Pop-Location
Write-Host "Done: $ArchivePath"
Get-Item $ArchivePath | Format-List Name, Length

if ($VersionRaw -in @("trunk", "main")) {
  $Alias = Join-Path $OutputDir "$PackageName-trunk-$ArchiveSuffix.tar.gz"
  Copy-Item $ArchivePath $Alias -Force
  Write-Host "Alias: $Alias"
}
