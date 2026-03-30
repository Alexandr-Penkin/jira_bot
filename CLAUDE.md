# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

SleepJiraBot — a Telegram bot integrating with Jira Cloud. It provides OAuth-based Jira authentication, issue subscriptions (polling + webhooks), scheduled reports via cron, sprint views, and multilingual support (en/ru). Tokens are stored encrypted (AES-256-GCM) in MongoDB.

## Commands

```bash
# Build & Run
make run                # go run ./cmd/bot
make build              # go build -o bin/sleepjirabot ./cmd/bot

# Docker
make docker-build       # docker compose up -d --build
make docker-up          # docker compose up -d
make docker-down        # docker compose down
make docker-logs        # docker compose logs -f bot

# Test & Lint
go test ./...           # all tests
go test ./internal/jira # single package
golangci-lint run       # linter (.golangci.yml config)
```

## Architecture

Entry point: `cmd/bot/main.go` — starts 4 concurrent goroutines:
- **Telegram bot** (`internal/telegram`) — long-polling handler for commands and callbacks
- **Issue poller** (`internal/poller`) — periodically queries Jira for subscription updates
- **Scheduler** (`internal/scheduler`) — cron-based scheduled report delivery
- **HTTP callback server** (`internal/jira`, `internal/webhook`) — OAuth redirects + Jira webhook events

Key packages:
- `internal/config` — env-based config with required field validation
- `internal/crypto` — AES-256-GCM encryption for Jira OAuth tokens
- `internal/storage` — MongoDB repos: UserRepo, SubscriptionRepo, ScheduleRepo
- `internal/jira` — OAuth 2.0 client, token refresh, Jira Cloud REST API
- `internal/locale` — i18n (English, Russian)
- `internal/format` — Telegram MarkdownV2 escaping
- `internal/middleware` — rate limiting

## Configuration

Required env vars (see `.env.example`): `TELEGRAM_TOKEN`, `JIRA_CLIENT_ID`, `JIRA_CLIENT_SECRET`, `ENCRYPTION_KEY` (64 hex chars = 32 bytes).

Optional with defaults: `MONGO_URI` (mongodb://localhost:27017), `MONGO_DB` (sleepjirabot), `LOG_LEVEL` (info), `CALLBACK_ADDR` (:8080), `POLL_INTERVAL` (2m).

## Data Models

- **User**: TelegramUserID ↔ Jira credentials (encrypted tokens), language pref
- **Subscription**: links Telegram user/chat to Jira resource (project/issue/filter) with type (my_new_issues, my_mentions, etc.)
- **ScheduledReport**: cron expression + JQL filter → Telegram chat delivery

## Go Version

Go 1.25 (per go.mod). Docker builds with golang:1.24-alpine → alpine:3.21.
