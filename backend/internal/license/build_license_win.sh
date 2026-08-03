#!/bin/bash
# 编译 license-gen (Windows/macOS) + Android APK 授权客户端
# 用法: bash build_license_win.sh [密钥种子]
#
# 生产部署时指定自定义密钥种子：
#   bash build_license_win.sh "MPv2.0-你的密钥种子"
#
# 不指定则使用默认生产密钥。
# 前置条件（仅 APK 编译需要）:
#   go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init
#   Android SDK + NDK 已安装，ANDROID_HOME 已设置

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
ANDROID_DIR="$SCRIPT_DIR/android"
CMD_PATH="./cmd/license-gen/"

# 修复 JAVA_HOME 指向错误的问题（去掉末尾的 \bin）
export JAVA_HOME="D:/Program Files/Android/Android Studio/jbr"

# 确保 gobind 等工具在 PATH 中（gomobile 内部调用）
GOPATH_UNIX=$(cygpath -u "$(go env GOPATH)")
export PATH="$PATH:$GOPATH_UNIX/bin"

# 生产密钥种子，编译时注入到二进制中
# 与服务端 build_linux.sh 的 LICENSE_SECRET 必须一致
LICENSE_SECRET="MediaPlayer-LAOK-v1.0.0"

declare -a LDFLAGS=(-ldflags "-X github.com/mediaplayer/backend/internal/license.embeddedSecret=$LICENSE_SECRET")
if [ -n "$1" ]; then
  declare -a LDFLAGS=(-ldflags "-X github.com/mediaplayer/backend/internal/license.embeddedSecret=$1")
  echo "==> 使用命令行参数密钥种子编译"
fi

# ── 1. 编译 license-gen (Windows) ──────────────────────

echo ""
echo "==> [1/3] 编译 license-gen (Windows amd64) ..."
cd "$PROJECT_DIR"
GOOS=windows GOARCH=amd64 go build "${LDFLAGS[@]}" -o "$SCRIPT_DIR/license-gen.exe" $CMD_PATH

echo "  -> 编译完成: $SCRIPT_DIR/license-gen.exe"
ls -lh "$SCRIPT_DIR/license-gen.exe"

# ── 2. 编译 license-gen (macOS) ────────────────────────

echo ""
echo "==> [2/3] 编译 license-gen (macOS amd64 + arm64) ..."
cd "$PROJECT_DIR"

GOOS=darwin GOARCH=amd64 go build "${LDFLAGS[@]}" -o "$SCRIPT_DIR/license-gen-darwin-amd64" $CMD_PATH
echo "  -> 编译完成: $SCRIPT_DIR/license-gen-darwin-amd64"
ls -lh "$SCRIPT_DIR/license-gen-darwin-amd64"

GOOS=darwin GOARCH=arm64 go build "${LDFLAGS[@]}" -o "$SCRIPT_DIR/license-gen-darwin-arm64" $CMD_PATH
echo "  -> 编译完成: $SCRIPT_DIR/license-gen-darwin-arm64"
ls -lh "$SCRIPT_DIR/license-gen-darwin-arm64"

# ── 3. 编译 Android APK ────────────────────────────────

echo ""
echo "==> [3/3] 编译 Android APK 授权客户端 ..."

# 检查 gomobile 是否可用
GOMOBILE="$(go env GOPATH)/bin/gomobile"
if [ ! -f "$GOMOBILE" ]; then
  echo "  -> 跳过: gomobile 未安装 (go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init)"
  echo ""
  echo "================================================"
  echo "  编译完成!"
  echo "  license-gen.exe          : $SCRIPT_DIR/license-gen.exe"
  echo "  license-gen-darwin-amd64 : $SCRIPT_DIR/license-gen-darwin-amd64"
  echo "  license-gen-darwin-arm64 : $SCRIPT_DIR/license-gen-darwin-arm64"
  echo "  APK 编译跳过（缺少 gomobile）"
  echo "================================================"
  exit 0
fi

# 2.1 编译 Go → AAR
AAR_OUTPUT="$ANDROID_DIR/app/libs/licensegen.aar"
echo "  -> 编译 gomobile AAR ..."
cd "$PROJECT_DIR"
"$GOMOBILE" bind \
  -target=android \
  -androidapi 21 \
  "${LDFLAGS[@]}" \
  -o "$AAR_OUTPUT" \
  github.com/mediaplayer/backend/cmd/licensegen-mobile
echo "  -> AAR: $AAR_OUTPUT"

# 2.2 编译 APK
echo "  -> 编译 APK ..."
cd "$ANDROID_DIR"

# 确保 gradlew 具有可执行权限
chmod +x ./gradlew 2>/dev/null || true

# 加载签名配置（与主客户端共用同一签名）
MAIN_KEYSTORE="$PROJECT_DIR/../android/release.keystore"
if [ -f "$MAIN_KEYSTORE" ]; then
  export ANDROID_SIGNING_KEYSTORE="$MAIN_KEYSTORE"
  export ANDROID_SIGNING_PASSWORD="tvplayer"
  export ANDROID_SIGNING_KEY_ALIAS="tvplayer"
  export ANDROID_SIGNING_KEY_PASSWORD="tvplayer"
  echo "  -> 使用主客户端签名: $MAIN_KEYSTORE"
fi

./gradlew assembleRelease 2>&1 || {
  echo "  -> APK 编译失败，请检查 Android SDK 配置 (ANDROID_HOME)"
  exit 1
}

APK_OUTPUT="$ANDROID_DIR/app/build/outputs/apk/release/app-release.apk"
if [ -f "$APK_OUTPUT" ]; then
  cp "$APK_OUTPUT" "$SCRIPT_DIR/MediaPlayer授权.apk"
  echo ""
  echo "================================================"
  echo "  编译完成!"
  echo "  license-gen.exe          : $SCRIPT_DIR/license-gen.exe"
  echo "  license-gen-darwin-amd64 : $SCRIPT_DIR/license-gen-darwin-amd64"
  echo "  license-gen-darwin-arm64 : $SCRIPT_DIR/license-gen-darwin-arm64"
  echo "  MediaPlayer授权.apk      : $SCRIPT_DIR/MediaPlayer授权.apk"
  echo "================================================"
else
  echo "  -> APK 输出未找到，编译可能失败"
fi