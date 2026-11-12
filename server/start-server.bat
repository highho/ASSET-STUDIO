@echo off
chcp 65001 >NUL
setlocal
cd /d "%~dp0"

rem This project needs Go 1.23+ (see go.mod).
rem If go is not on PATH, point ASSET_STUDIO_GO at the Go root,
rem e.g. set ASSET_STUDIO_GO=C:\tools\go1.24.13\go
set "GO=go"
if defined ASSET_STUDIO_GO if exist "%ASSET_STUDIO_GO%\bin\go.exe" set "GO=%ASSET_STUDIO_GO%\bin\go.exe"
if not defined ASSET_STUDIO_GO where go >NUL 2>NUL || (
  echo [ASSET-STUDIO] go not found on PATH.
  echo               Install Go 1.23+, or set ASSET_STUDIO_GO to the Go root.
  pause
  exit /b 1
)

rem A machine-wide GOROOT pointing at another Go install breaks this one,
rem so pin it when ASSET_STUDIO_GO is used and clear it otherwise.
if defined ASSET_STUDIO_GO (
  set "GOROOT=%ASSET_STUDIO_GO%"
) else (
  set "GOROOT="
)

if not defined GOPROXY set "GOPROXY=https://goproxy.cn,direct"
set "GOTOOLCHAIN=local"
set "CGO_ENABLED=0"

echo [ASSET-STUDIO] using %GO%
echo Asset Studio: http://localhost:8080
echo Close this window or press Ctrl+C to stop.
"%GO%" run . -addr 127.0.0.1:8080 -data ./data
pause
