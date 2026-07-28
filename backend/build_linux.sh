#!/bin/bash

echo "🚀 开始跨平台编译 MediaPlayer 后端 (Linux AMD64)..."

# 设置跨平台编译环境变量
export GOOS=linux
export GOARCH=amd64
# 关闭 CGO 以确保生成纯静态链接的二进制文件，避免在 Linux 上出现 glibc 版本依赖问题
export CGO_ENABLED=0

# 构建 ldflags
# -s -w: 剔除调试信息，减小体积
# -X main.Version: 注入版本号
# -X ...license.embeddedSecret: 注入自定义密钥种子（可选，通过环境变量 LICENSE_SECRET 传入）
# LDFLAGS="-s -w -X main.Version=v1.0.0"
LDFLAGS="-s -w -X main.Version=v1.0.0 -X github.com/mediaplayer/backend/internal/license.embeddedSecret=MediaPlayer-LAOK-v1.0.0"
if [ -n "$LICENSE_SECRET" ]; then
  LDFLAGS="$LDFLAGS -X github.com/mediaplayer/backend/internal/license.embeddedSecret=$LICENSE_SECRET"
  echo "🔑 使用自定义密钥种子编译"
fi

# 执行构建
go build -ldflags "$LDFLAGS" -o mediaplayer-linux-amd64 ./cmd/server

if [ $? -ne 0 ]; then
    echo "❌ 编译失败，请检查上面的报错信息！"
    exit 1
else
    echo "✅ 编译成功！已生成可执行文件: mediaplayer-linux-amd64"
    echo "📦 你现在可以将它上传到你的 Linux 服务器并运行了。"
fi
