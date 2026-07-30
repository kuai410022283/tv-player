#!/bin/bash
# 编译 licensegen Android AAR
# 用法: bash build_aar.sh [密钥种子]
#
# 不指定密钥种子则使用生产密钥 MediaPlayer-LAOK-v1.0.0。
# AAR 输出到 android/app/libs/licensegen.aar。
#
# 前置条件:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   gomobile init
#   Android SDK + NDK 已安装

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
OUTPUT_DIR="$SCRIPT_DIR/android/app/libs"

# 生产密钥种子，与服务端 build_linux.sh 的 LICENSE_SECRET 必须一致
LICENSE_SECRET="MediaPlayer-LAOK-v1.0.0"
declare -a LDFLAGS=(-ldflags "-X github.com/mediaplayer/backend/internal/license.embeddedSecret=$LICENSE_SECRET")
if [ -n "$1" ]; then
  declare -a LDFLAGS=(-ldflags "-X github.com/mediaplayer/backend/internal/license.embeddedSecret=$1")
  echo "==> 使用命令行参数密钥种子"
fi

echo "==> 编译 licensegen AAR (Android) ..."

mkdir -p "$OUTPUT_DIR"
cd "$PROJECT_DIR"

gomobile bind \
  -target=android/arm,android/arm64 \
  "${LDFLAGS[@]}" \
  -o "$OUTPUT_DIR/licensegen.aar" \
  github.com/mediaplayer/backend/cmd/licensegen-mobile

echo "==> 编译完成: $OUTPUT_DIR/licensegen.aar"
ls -lh "$OUTPUT_DIR/licensegen.aar"