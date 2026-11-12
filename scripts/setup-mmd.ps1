# 安装 PMX 转换依赖到 tools\mmd：mmd_tools（GitHub 发布包）与 opencc（PyPI wheel 解包）。
# 用法：powershell -ExecutionPolicy Bypass -File scripts\setup-mmd.ps1
$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
$dest = Join-Path $repo 'tools\mmd'
New-Item -ItemType Directory -Path $dest -Force | Out-Null

$mmdTools = Join-Path $dest 'mmd_tools'
$opencc = Join-Path $dest 'opencc'

$tmp = Join-Path ([IO.Path]::GetTempPath()) ('mmd-setup-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    if (-not (Test-Path -LiteralPath (Join-Path $mmdTools '__init__.py'))) {
        $zip = Join-Path $tmp 'mmd_tools.zip'
        Write-Host '[setup-mmd] 下载 mmd_tools v4.5.14 ...'
        Invoke-WebRequest -Uri 'https://github.com/MMD-Blender/blender_mmd_tools/releases/download/v4.5.14/mmd_tools-v4.5.14-bl4.2.zip' -OutFile $zip
        $extract = Join-Path $tmp 'mmd_tools_pkg'
        Expand-Archive -LiteralPath $zip -DestinationPath $extract -Force
        $pkg = Get-ChildItem -LiteralPath $extract -Directory | Where-Object { $_.Name -eq 'mmd_tools' } | Select-Object -First 1
        if (-not $pkg) { throw 'mmd_tools 压缩包里没有 mmd_tools 目录' }
        Copy-Item -LiteralPath $pkg.FullName -Destination $mmdTools -Recurse
    } else {
        Write-Host '[setup-mmd] mmd_tools 已存在，跳过'
    }

    if (-not (Test-Path -LiteralPath (Join-Path $opencc 'config\s2t.json'))) {
        Write-Host '[setup-mmd] 下载 opencc-python-reimplemented 0.1.7 ...'
        $meta = Invoke-RestMethod -Uri 'https://pypi.org/pypi/opencc-python-reimplemented/0.1.7/json'
        $wheelUrl = ($meta.urls | Where-Object { $_.packagetype -eq 'bdist_wheel' } | Select-Object -First 1).url
        if (-not $wheelUrl) { throw 'PyPI 没有找到 opencc 的 wheel 包' }
        $whl = Join-Path $tmp 'opencc.whl'
        Invoke-WebRequest -Uri $wheelUrl -OutFile $whl
        # .whl 就是 zip，但 Expand-Archive 只认 .zip 后缀。
        $openccZip = Join-Path $tmp 'opencc.zip'
        Copy-Item -LiteralPath $whl -Destination $openccZip
        $extract = Join-Path $tmp 'opencc_pkg'
        Expand-Archive -LiteralPath $openccZip -DestinationPath $extract -Force
        $pkg = Get-ChildItem -LiteralPath $extract -Directory | Where-Object { $_.Name -eq 'opencc' } | Select-Object -First 1
        if (-not $pkg) { throw 'opencc wheel 里没有 opencc 目录' }
        Copy-Item -LiteralPath $pkg.FullName -Destination $opencc -Recurse
    } else {
        Write-Host '[setup-mmd] opencc 已存在，跳过'
    }

    Write-Host '完成：'
    Write-Host ("  mmd_tools = " + $mmdTools)
    Write-Host ("  opencc    = " + $opencc)
    Write-Host 'PMX 转换还需要 Blender；启动服务后 GET /api/mmd/status 可自检。'
} finally {
    if (Test-Path -LiteralPath $tmp) {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}
