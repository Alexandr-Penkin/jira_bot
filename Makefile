.PHONY: run build docker-up docker-down docker-build docker-logs prod-up prod-down prod-build prod-logs

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

prod-up:
	docker compose -f docker-compose.prod.yml up -d

prod-down:
	docker compose -f docker-compose.prod.yml down

prod-build:
	docker compose -f docker-compose.prod.yml up -d --build

prod-logs:
	docker compose -f docker-compose.prod.yml logs -f
