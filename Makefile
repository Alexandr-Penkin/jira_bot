# Every Phase 1–6b service is gated behind a compose profile so default
# `docker compose up` still boots only the monolith. The release/restart-all
# targets below activate the full fleet by enabling every profile at once.
COMPOSE_PROFILES := webhook-svc identity-svc subscription-svc scheduler-svc preferences-svc telegram-svc redis
COMPOSE_PROFILE_FLAGS := $(foreach p,$(COMPOSE_PROFILES),--profile $(p))
COMPOSE := docker compose $(COMPOSE_PROFILE_FLAGS)
# docker-compose.prod.yml only redeclares bot and webhook-svc with the
# caddy_net alias. Every other service (telegram-svc, identity-svc, etc.)
# lives in docker-compose.yml behind profiles. Both files target the same
# Compose project (= directory name) so containers share one fleet — but
# rebuilding the full set takes two invocations: $(COMPOSE) for the base
# + profiles, then $(COMPOSE_PROD) for the overlay.
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
# `restart` restarts whatever is currently running in the default compose.
# `restart-all` covers every Phase 1–6b profile so operators do not need to
# remember which --profile flags were active.
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
# `release` rebuilds + starts only services currently declared in the default
# compose (monolith fleet). `release-all` additionally enables every profile,
# bringing the full Phase 1–6b microservices set online in one command.
release:
	docker compose up -d --build

release-all:
	$(COMPOSE) up -d --build

prod-restart:
	$(COMPOSE_PROD) restart

# `prod-release` rebuilds the whole prod fleet:
#   1. base compose with every profile enabled → telegram-svc,
#      identity-svc, subscription-svc, scheduler-svc, preferences-svc,
#      webhook-svc, redis (containers named jira_bot-<svc>-1).
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
