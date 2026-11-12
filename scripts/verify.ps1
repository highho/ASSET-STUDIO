# 一条命令跑完阶段 5 的守门检查：Go 静态检查与单测、前端类型检查与单测、前端构建。
#
# 用法（在仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File scripts\verify.ps1
#
# 任何一步失败立即退出并返回非零码，可以直接挂到 CI 或 git hook 上。
# 机器上有多份 Go 时，用 ASSET_STUDIO_GO 指定要用的那一份（start-server.bat 用同一个变量）。

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$server = Join-Path $root 'server'

$goRoot = if ($env:ASSET_STUDIO_GO) { $env:ASSET_STUDIO_GO } else { 'C:\Users\36296\tools\go1.24.13\go' }
if (-not (Test-Path -LiteralPath (Join-Path $goRoot 'bin\go.exe'))) {
  throw "找不到 Go：$goRoot（用 ASSET_STUDIO_GO 指定 go1.24 的根目录）"
}

# 机器级 GOROOT 指向别的版本时必须 pin，否则 go 会跑到系统那份去
$env:GOROOT = $goRoot
$env:PATH = "$goRoot\bin;$env:PATH"
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOSUMDB = 'off'
$env:CGO_ENABLED = '0'

function Step([string]$name, [scriptblock]$body) {
  Write-Host "== $name"
  & $body
  if ($LASTEXITCODE -ne 0) {
    throw "$name 失败（exit $LASTEXITCODE）"
  }
}

Push-Location $server
try {
  Step 'go vet ./...' { go vet ./... }
  Step 'go test ./...' { go test ./... }
} finally {
  Pop-Location
}

Push-Location $root
try {
  Step 'npm run typecheck' { npm run typecheck }
  Step 'npm test' { npm test }
  Step 'vite build' { npx vite build }
} finally {
  Pop-Location
}

Write-Host '全部通过'
