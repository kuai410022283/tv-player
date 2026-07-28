#!/bin/bash
# 编译 license-gen Windows 版可执行程序
# 用法: bash build_license_win.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "==> 编译 license-gen (Windows amd64) ..."
cd "$PROJECT_DIR"
GOOS=windows GOARCH=amd64 go build -o "$SCRIPT_DIR/license-gen.exe" ./cmd/license-gen/

echo "==> 编译完成: $SCRIPT_DIR/license-gen.exe"
ls -lh "$SCRIPT_DIR/license-gen.exe"