.PHONY: build run clean docker test

build:
	cd backend && go build -o bin/mediaplayer ./cmd/server

run:
	cd backend && go run ./cmd/server

clean:
	rm -rf backend/bin backend/data

docker:
	cd backend && docker build -t mediaplayer .

docker-run:
	docker run -p 9527:9527 -v mediaplayer-data:/app/data mediaplayer

test:
	cd backend && go test ./...
