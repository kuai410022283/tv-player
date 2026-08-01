FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 注入版本号
ARG VERSION=dev
# 授权密钥种子（与 license-gen 务必一致）
ARG LICENSE_SECRET=MediaPlayer-LAOK-v1.0.0
# TARGETOS / TARGETARCH / TARGETVARIANT 由 docker buildx 自动传入（多平台构建）
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

RUN GOARM=$([ "${TARGETVARIANT}" = "v7" ] && echo "7" || [ "${TARGETVARIANT}" = "v6" ] && echo "6" || echo "") \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.Version=${VERSION} -X github.com/mediaplayer/backend/internal/license.embeddedSecret=${LICENSE_SECRET}" \
    -o mediaplayer ./cmd/server

# ── 运行阶段 ─────────────────────────────────────────
FROM debian:bookworm-slim

# 安装运行时依赖 + JRE + Android 构建工具（glibc 原生，所有架构均有预编译包）
# zipalign: APK 4 字节对齐（由 android-platform-build 源码包提供）
# 注意: apktool 3.0.x 仅支持 aapt2，不再兼容系统 aapt(v1)
#       x86_64 平台使用 apktool 内置 aapt2（--use-aapt2），
#       ARM64 平台若需原生 aapt2 请手动安装或挂在工具链中
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata curl gosu openssl openjdk-17-jre-headless \
    zipalign \
    && rm -rf /var/lib/apt/lists/*

# 创建非 root 用户并指定固定的 UID/GID (1000)
RUN groupadd -g 1000 mediaplayer && useradd -u 1000 -g mediaplayer -d /app -s /bin/sh mediaplayer

WORKDIR /app
COPY --from=builder /build/mediaplayer .
COPY --from=builder /build/web ./web
COPY --from=builder /build/library .
COPY --from=builder /build/config.yaml .
COPY --from=builder /build/version .
COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

RUN mkdir -p /app/data && chown -R mediaplayer:mediaplayer /app

ENTRYPOINT ["/app/entrypoint.sh"]

EXPOSE 9527

# 健康检查
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -sf http://localhost:9527/ping || exit 1

CMD ["/app/mediaplayer"]