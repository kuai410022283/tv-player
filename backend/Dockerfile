FROM golang:1.21-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 注入版本号
ARG VERSION=dev
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags "-X main.Version=${VERSION}" \
    -o mediaplayer ./cmd/server

# ── 运行阶段 ─────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache sqlite-libs ca-certificates tzdata curl

# 创建非 root 用户
RUN addgroup -S mediaplayer && adduser -S mediaplayer -G mediaplayer

WORKDIR /app
COPY --from=builder /build/mediaplayer .
COPY --from=builder /build/web ./web
COPY --from=builder /build/config.yaml .

RUN mkdir -p /app/data && chown -R mediaplayer:mediaplayer /app

USER mediaplayer

EXPOSE 9527

# 健康检查
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -sf http://localhost:9527/ping || exit 1

CMD ["./mediaplayer"]
