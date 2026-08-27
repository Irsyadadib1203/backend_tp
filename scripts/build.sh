#!/usr/bin/env bash
# ==============================================================================
# Ultra-Lightweight & Fast Production Build Script for Linux VPS
# Flags:
#   -ldflags="-s -w" : Strip debug info and symbol table (cuts binary size by ~70%)
#   -trimpath        : Remove local absolute file paths from stack traces for security
# ==============================================================================

set -e

echo "🚀 Compiling TopUp Go Backend for Linux (amd64)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -trimpath \
    -o ./bin/server-linux \
    ./cmd/server/main.go

echo "✅ Build completed successfully: ./bin/server-linux"
ls -lh ./bin/server-linux

if command -v upx &> /dev/null; then
    echo "📦 Compressing binary with UPX..."
    upx --best ./bin/server-linux
    echo "🎉 UPX compression finished!"
    ls -lh ./bin/server-linux
fi
