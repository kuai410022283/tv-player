.PHONY: build run clean docker test

build:
	cd backend && go build -o bin/tvplayer ./cmd/server

run:
	cd backend && go run ./cmd/server

clean:
	rm -rf backend/bin backend/data

docker:
	cd backend && docker build -t tvplayer .

docker-run:
	docker run -p 9527:9527 -v tvplayer-data:/app/data tvplayer

test:
	cd backend && go test ./...
