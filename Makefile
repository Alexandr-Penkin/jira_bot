# SleepJiraBot runs as a full microservice fleet — `docker compose up`
# boots every service (bot, identity-svc, preferences-svc, subscription-svc,
# scheduler-svc, webhook-svc, telegram-svc + nats/redis). There is no
# monolith mode and no compose profiles.
COMPOSE := docker compose
# docker-compose.prod.yml is a thin overlay that redeclares the two
# publicly-exposed services (bot, webhook-svc) with the caddy_net alias for
# the Caddy reverse proxy; every other service stays internal on the base
# project network. Both files target the same Compose project (= directory
# name) so containers share one fleet — rebuilding the full set takes two
# invocations: $(COMPOSE) for the base, then $(COMPOSE_PROD) for the overlay.
COMPOSE_PROD := docker compose -f docker-compose.prod.yml

.PHONY: run build \
        docker-up docker-down docker-build docker-logs \
        prod-up prod-down prod-build prod-logs \
        restart restart-all restart-bot restart-webhook-svc restart-identity-svc \
        restart-subscription-svc restart-scheduler-svc restart-preferences-svc \
        restart-telegram-svc restart-redis restart-nats \
        release release-all prod-restart prod-release deploy

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
	$(COMPOSE_PROD) up -d

prod-down:
	$(COMPOSE_PROD) down

prod-build:
	$(COMPOSE_PROD) up -d --build

prod-logs:
	$(COMPOSE_PROD) logs -f

# ── Restart targets ────────────────────────────────────────────────────────
# `restart` restarts the whole fleet; `restart-all` is kept as an alias.
restart:
	docker compose restart

restart-all:
	$(COMPOSE) restart

restart-bot:
	docker compose restart bot

restart-webhook-svc:
	$(COMPOSE) restart webhook-svc

restart-identity-svc:
	$(COMPOSE) restart identity-svc

restart-subscription-svc:
	$(COMPOSE) restart subscription-svc

restart-scheduler-svc:
	$(COMPOSE) restart scheduler-svc

restart-preferences-svc:
	$(COMPOSE) restart preferences-svc

restart-telegram-svc:
	$(COMPOSE) restart telegram-svc

restart-redis:
	$(COMPOSE) restart redis

restart-nats:
	docker compose restart nats

# ── Release targets ────────────────────────────────────────────────────────
# `release` rebuilds + starts the full fleet; `release-all` is kept as an
# alias.
release:
	docker compose up -d --build

release-all:
	$(COMPOSE) up -d --build

prod-restart:
	$(COMPOSE_PROD) restart

# `prod-release` rebuilds the whole prod fleet:
#   1. base compose → the full fleet (bot, telegram-svc, identity-svc,
#      subscription-svc, scheduler-svc, preferences-svc, webhook-svc,
#      nats, redis) as containers named jira_bot-<svc>-1.
#   2. prod overlay, restricted to `bot` only → adds caddy_net for the
#      Caddy reverse proxy. webhook-svc in the overlay is intentionally
#      skipped because the base compose already owns its container; the
#      overlay's container_name would otherwise create a duplicate.
prod-release:
	$(COMPOSE) up -d --build
	$(COMPOSE_PROD) up -d --build bot

# `deploy` is the one-shot server-side redeploy: pull latest code,
# rebuild every service, then print fleet status.
deploy:
	git pull --ff-only
	$(COMPOSE) up -d --build --remove-orphans
	$(COMPOSE_PROD) up -d --build bot
	docker ps --format 'table {{.Names}}\t{{.Status}}'
