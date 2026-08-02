#!/bin/bash

echo "🚀 开始编译 MediaPlayer 后端 (Windows AMD64)..."

# 设置编译环境变量
export GOOS=windows
export GOARCH=amd64
export CGO_ENABLED=0

# 构建 ldflags
LDFLAGS="-s -w -X main.Version=v1.0.0 -X github.com/mediaplayer/backend/internal/license.embeddedSecret=MediaPlayer-LAOK-v1.0.0"
if [ -n "$LICENSE_SECRET" ]; then
  LDFLAGS="$LDFLAGS -X github.com/mediaplayer/backend/internal/license.embeddedSecret=$LICENSE_SECRET"
  echo "🔑 使用自定义密钥种子编译"
fi

# 执行构建
go build -ldflags "$LDFLAGS" -o mediaplayer.exe ./cmd/server

if [ $? -ne 0 ]; then
    echo "❌ 编译失败，请检查上面的报错信息！"
    exit 1
else
    echo "✅ 编译成功！已生成可执行文件: mediaplayer.exe"
    echo "📦 你现在可以在 Windows 上运行它了。"
fi