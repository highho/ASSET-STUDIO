# 构建桌面套壳所需的本地服务与前端产物。
# 用法（仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File scripts\build-desktop.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\build-desktop.ps1 -SkipFrontend
#
# 只生成 desktop\bin 下的服务程序和 dist，不生成安装包；
# 安装包由 npm run desktop:dist 调用 electron-builder 完成。

param(
  [switch]$SkipFrontend,
  [switch]$SkipGo
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$server = Join-Path $root 'server'
$bin = Join-Path $root 'desktop\bin'
$goRoot = if ($env:ASSET_STUDIO_GO) { $env:ASSET_STUDIO_GO } else { 'C:\Users\36296\tools\go1.24.13\go' }

if (-not $SkipFrontend) {
  Push-Location $root
  try {
    npm run build
    if ($LASTEXITCODE -ne 0) { throw '前端构建失败' }
  } finally {
    Pop-Location
  }
}

if (-not $SkipGo) {
  if (-not (Test-Path -LiteralPath (Join-Path $goRoot 'bin\go.exe'))) {
    throw "找不到 Go：$goRoot（用 ASSET_STUDIO_GO 指定 go1.24 的根目录）"
  }

  $env:GOROOT = $goRoot
  $env:PATH = "$goRoot\bin;$env:PATH"
  $env:GOTOOLCHAIN = 'local'
  $env:GOPROXY = 'https://goproxy.cn,direct'
  $env:GOSUMDB = 'off'
  $env:CGO_ENABLED = '0'

  New-Item -ItemType Directory -Force -Path $bin | Out-Null
  Push-Location $server
  try {
    go build -trimpath -buildvcs=false -ldflags '-s -w' -o (Join-Path $bin 'asset-studio-server.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Go 服务构建失败' }
  } finally {
    Pop-Location
  }
}

$serverExe = Join-Path $bin 'asset-studio-server.exe'
if (-not (Test-Path -LiteralPath $serverExe)) {
  throw "桌面服务程序没有生成：$serverExe"
}

$size = [math]::Round((Get-Item -LiteralPath $serverExe).Length / 1MB, 1)
Write-Host "桌面服务：$serverExe（$size MB）"
Write-Host '下一步：npm run desktop:dist'
