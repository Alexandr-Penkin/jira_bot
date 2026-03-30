.PHONY: run build docker-up docker-down docker-build docker-logs

run:
	go run ./cmd/bot

build:
	go build -o bin/sleepjirabot ./cmd/bot

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-build:
	docker compose up -d --build

docker-logs:
	docker compose logs -f bot
