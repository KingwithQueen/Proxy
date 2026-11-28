@echo off
setlocal

echo Checking Go toolchain...
where go >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Go toolchain not found in PATH. Please install Go 1.27+ from https://go.dev/dl/
    exit /b 1
)

go version

echo Building Jray for Windows (amd64)...
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -o jray.exe -trimpath -ldflags "-s -w -buildid=" ./main

if errorlevel 1 (
    echo [ERROR] Build failed.
    exit /b 1
)

echo.
echo [SUCCESS] Built jray.exe
.\jray.exe version
endlocal
