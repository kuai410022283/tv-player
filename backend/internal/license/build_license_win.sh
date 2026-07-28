#!/bin/bash
# 编译 license-gen Windows 版可执行程序
# 用法: bash build_license_win.sh [密钥种子]
#
# 生产部署时指定自定义密钥种子：
#   bash build_license_win.sh "MPv2.0-你的密钥种子"
#
# 不指定则使用默认开发密钥。

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# 生产密钥种子，编译时注入到二进制中
# 与服务端 build_linux.sh 的 LICENSE_SECRET 必须一致
LICENSE_SECRET="MediaPlayer-LAOK-v1.0.0"

declare -a LDFLAGS=(-ldflags "-X github.com/mediaplayer/backend/internal/license.embeddedSecret=$LICENSE_SECRET")
if [ -n "$1" ]; then
  declare -a LDFLAGS=(-ldflags "-X github.com/mediaplayer/backend/internal/license.embeddedSecret=$1")
  echo "==> 使用命令行参数密钥种子编译"
fi

echo "==> 编译 license-gen (Windows amd64) ..."
cd "$PROJECT_DIR"
GOOS=windows GOARCH=amd64 go build "${LDFLAGS[@]}" -o "$SCRIPT_DIR/license-gen.exe" ./cmd/license-gen/

echo "==> 编译完成: $SCRIPT_DIR/license-gen.exe"
ls -lh "$SCRIPT_DIR/license-gen.exe"