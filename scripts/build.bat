@echo off
REM ==============================================================================
REM Production Build Script for Windows / Cross Compile
REM ==============================================================================

echo [1/2] Building optimized binary for Windows...
go build -ldflags="-s -w" -trimpath -o ./bin/server.exe ./cmd/server/main.go
if %errorlevel% neq 0 exit /b %errorlevel%
echo [1/2] Windows build success: ./bin/server.exe

echo [2/2] Building optimized binary for Linux VPS (amd64)...
set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0
go build -ldflags="-s -w" -trimpath -o ./bin/server-linux ./cmd/server/main.go
if %errorlevel% neq 0 exit /b %errorlevel%
echo [2/2] Linux VPS build success: ./bin/server-linux

echo Done! All binaries compiled with -s -w -trimpath.
