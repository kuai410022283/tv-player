FROM golang:1.21-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o tvplayer ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache sqlite-libs ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/tvplayer .
COPY --from=builder /build/web ./web
COPY --from=builder /build/config.yaml .

RUN mkdir -p /app/data

EXPOSE 9527

CMD ["./tvplayer"]
