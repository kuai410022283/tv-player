.PHONY: build run clean docker docker-run test deps

# ==========================
# Backend (Go) Commands
# ==========================
build:
	cd backend && go build -o bin/mediaplayer ./cmd/server

run:
	cd backend && go run ./cmd/server

test:
	cd backend && go test ./...

deps:
	cd backend && go mod tidy && go mod download

# ==========================
# Docker Commands
# ==========================
docker:
	cd backend && docker build -t mediaplayer .

docker-run:
	docker run -d -p 9527:9527 -v mediaplayer-data:/app/data --name mediaplayer-server mediaplayer

# ==========================
# Utility Commands
# ==========================
clean:
ifeq ($(OS),Windows_NT)
	-rmdir /S /Q backend\bin backend\data 2>nul || exit 0
else
	-rm -rf backend/bin backend/data
endif
