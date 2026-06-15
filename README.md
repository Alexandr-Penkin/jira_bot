# SleepJiraBot

Telegram bot for Jira Cloud integration. Authenticate via OAuth 2.0, track issues, get notifications, run sprint and kanban flow reports, schedule automated JQL reports, and get reminders from your calendar feed — all from Telegram.

## Features

- **Jira OAuth 2.0** — secure authentication with encrypted token storage (AES-256-GCM), auto-refresh, multi-site (Atlassian resources) support
- **Issue management** — view, comment, transition, and assign issues
- **Issue creation** — interactive wizard with Epic linking, Templates selector (tap-to-copy body), priority/assignee pickers, reusable templates, and quick-create syntax
- **Instant create (`/createfast`)** — turn any text or photo/file caption into a Jira issue with the attachment uploaded, Epic selection, and one-tap confirmation. Any plain message is treated as a `/createfast` shortcut
- **Subscriptions** — real-time notifications on issue/project changes via polling and signed Jira webhooks (HMAC-SHA256)
- **Sprint reports** — view sprint boards and progress
- **Kanban flow reports** — board-level flow metrics (throughput, cycle/lead time, WIP, work item age, flow efficiency) with 7/14/30-day periods; multi-board project selection; respects custom done/hold statuses from Profile
- **Scheduled reports** — cron-based JQL/sprint reports delivered to chats
- **Daily standups** — quick view of assigned issues for you or teammates, plus a scheduled daily standup subscription with timezone support
- **Calendar reminders** — subscribe an iCal/ICS "secret address" (Google/Outlook/Apple) from Profile → 📅 Calendar; the bot polls the feed, expands recurring events, and DMs you "new", "changed", and "starts in N min" reminders with a configurable lead time
- **Defaults** — per-user defaults for project, board, and issue type via `/defaults`
- **Admin tools** — `/admin` panel (stats, user management, broadcast, poller status), `/diagnose` for OAuth scope/API/webhook troubleshooting, and `/webhooks_resync` to reconcile Jira webhooks with the local DB
- **Multilingual** — English and Russian

## Bot Commands

| Command | Description |
|---------|-------------|
| `/start`, `/menu` | Main menu |
| `/connect` / `/disconnect` | Jira authentication |
| `/me` | Current profile |
| `/issue <KEY>` | View issue details |
| `/daily [username]` | Daily standup report |
| `/list [JQL]` | Search issues |
| `/comment <KEY> <text>` | Add comment |
| `/transition <KEY>` | Change issue status |
| `/assign <KEY>` | Assign issue to self |
| `/create` | Interactive issue creation wizard |
| `/create <PROJECT> <Type> \| <Summary> \| <Description>` | Quick-create issue |
| `/createfast [summary]` | Instant create — text or photo/file with caption becomes an issue with attachment |
| `/sprint [PROJECT] [BOARD] [SPRINT]` | Sprint board |
| `/kanban [PROJECT] [DAYS]` | Kanban flow report (throughput, cycle time, WIP) |
| `/filters` | Jira saved filters |
| `/watch` / `/unwatch` | Subscribe/unsubscribe to changes |
| `/subscriptions` | List active subscriptions |
| `/schedule <cron>` | Create scheduled report |
| `/unschedule` / `/schedules` | Manage scheduled reports |
| `/defaults` | Set default project, board, and issue type |
| `/lang` | Switch language |
| `/help` | Help |
| `/admin` | Admin panel — stats, users, broadcast, poller status (admin only) |
| `/diagnose [user_id]` | Troubleshoot Jira OAuth scopes, API probes, and webhook drift (admin only) |
| `/webhooks_resync [user_id]` | Reconcile Jira webhooks with the local DB (admin only) |

> Calendar reminders are managed from the button menu (Profile → 📅 Calendar), not via a slash command. The daily standup subscription lives under Reports → ⏰ Daily Subscription.

## Quick Start

### Prerequisites

- Go 1.25+
- MongoDB 7+
- Telegram bot token ([@BotFather](https://t.me/BotFather))
- Jira Cloud OAuth 2.0 app ([developer.atlassian.com](https://developer.atlassian.com/console/myapps/))

### Local Setup

```bash
cp .env.example .env
# Edit .env with your credentials

make run
```

### Docker

```bash
cp .env.example .env
# Edit .env with your credentials

make docker-build
```

This starts the bot and MongoDB 7 via Docker Compose.

## Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TELEGRAM_TOKEN` | yes | — | Telegram Bot API token |
| `JIRA_CLIENT_ID` | yes | — | Jira OAuth 2.0 client ID |
| `JIRA_CLIENT_SECRET` | yes | — | Jira OAuth 2.0 client secret |
| `ENCRYPTION_KEY` | yes | — | 64 hex chars (32 bytes) for AES-256-GCM |
| `ENCRYPTION_KEY_PREVIOUS` | no | — | Old key used as a decrypt fallback during key rotation |
| `JIRA_REDIRECT_URI` | no | `http://localhost:8080/callback` | OAuth callback URL |
| `JIRA_WEBHOOK_URL` | no | — | Public HTTPS endpoint Jira posts webhook events to |
| `JIRA_WEBHOOK_SECRET` | no | — | Webhook signature verification (HMAC-SHA256) |
| `ALLOW_UNSIGNED_WEBHOOKS` | no | `false` | Accept unsigned webhook POSTs (use only behind network ACL / proxy auth) |
| `ADMIN_TELEGRAM_ID` | no | — | Telegram user ID granted admin commands |
| `MONGO_URI` | no | `mongodb://localhost:27017` | MongoDB connection string |
| `MONGO_DB` | no | `sleepjirabot` | Database name |
| `CALLBACK_ADDR` | no | `:8080` | HTTP server address |
| `POLL_INTERVAL` | no | `30s` | Subscription polling interval |
| `BATCH_WINDOW` | no | `1m` | Notification batching window |
| `PROXY_URL` | no | — | SOCKS5 proxy for all outbound connections |
| `LOG_LEVEL` | no | `info` | Log level (debug/info/warn/error) |

Generate encryption key:

```bash
openssl rand -hex 32
```

### Calendar feed

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CALENDAR_POLL_INTERVAL` | no | `5m` | How often each ICS feed is refetched |
| `CALENDAR_LOOKAHEAD` | no | `24h` | Recurring-event (RRULE) expansion window |
| `CALENDAR_FETCH_TIMEOUT` | no | `30s` | ICS fetch timeout |
| `CALENDAR_DEFAULT_REMINDER_MIN` | no | `15` | Default reminder lead time in minutes |
| `CALENDAR_NEW_HORIZON` | no | `1h` | Cap on which freshly-discovered events trigger a "🆕 New event" ping |

The calendar poller runs in `cmd/bot`; multi-replica deployments converge on one reminder via `DEDUP_REDIS_URL`.

### Microservices

The fleet shares one `.env` and communicates over NATS JetStream (mandatory). The cross-service variables — `NATS_URL`, `IDENTITY_SVC_URL`, `PREFERENCES_SVC_URL`, `WEBHOOK_SVC_URL`, `INTERNAL_AUTH_TOKEN`, `DEDUP_REDIS_URL`, the per-service `*_SVC_ADDR` listen addresses, and the `OTEL_*` knobs — are documented in `.env.example`.

## Architecture

SleepJiraBot runs as a fleet of microservices that share MongoDB and a NATS
JetStream event bus. There is no monolith — each concern runs in exactly one
service:

| Service (`cmd/`) | Owns | Port |
|------------------|------|------|
| `bot` | OAuth `/callback`, web pages, calendar poller | `:8080` |
| `identity-svc` | Jira token lease + refresh (sole refresh owner) | `:9080` |
| `preferences-svc` | user preferences | `:9082` |
| `subscription-svc` | Jira polling | `:8082` |
| `scheduler-svc` | cron reports | `:8083` |
| `webhook-svc` | Jira webhook ingress + 24h refresher | `:8081` |
| `telegram-svc` | Telegram update-handling + notification delivery | `:8084` |

`subscription-svc`, `scheduler-svc`, `webhook-svc`, and `bot`'s calendar poller
publish `NotifyRequested` events; `telegram-svc` is the only Telegram sender.
Every Jira-token consumer leases tokens from `identity-svc`, so refresh races
are impossible by construction. Each service exposes `/healthz` + `/readyz` and
shuts down gracefully on SIGINT/SIGTERM.

```
cmd/                     # One main.go per service (see table above)
internal/
  config/                # Environment-based configuration
  crypto/                # AES-256-GCM token encryption
  jira/                  # OAuth 2.0 flow, callback server, SiteConnector, REST client
  telegram/              # Bot handlers, menus, conversation state
  poller/                # Periodic subscription polling
  scheduler/             # Cron-based scheduled reports
  webhook/               # Jira webhook event processing
  webhookstats/          # Remote webhook delivery-stats fetcher
  calendar/              # ICS fetch + parse + RRULE expansion
  calendarpoller/        # Calendar feed polling and reminder dispatch
  daily/                 # Daily standup report builder
  identity/              # Token lease provider + HTTP server
  storage/               # MongoDB repositories (User, Subscription, Schedule, …)
  preferences/           # User preferences (lang, defaults, field mappings)
  notifydedup/           # Notification deduplication (in-memory or Redis)
  locale/                # i18n (en, ru)
  format/                # Telegram MarkdownV2 formatting
  middleware/            # Rate limiting
  proxy/                 # SOCKS5 dialer
  logger/                # Zerolog setup
pkg/                     # Cross-service contracts: events/v1, natsx, notifier,
                         # identityv1/client, preferencesv1/client, telemetry, health
```

`docker compose up` boots the whole fleet (plus NATS and Redis); Mongo lives on
an external network. See the [Docker Commands](#docker-commands) below.

## Development

```bash
make build              # Build binary to bin/sleepjirabot
go test ./...           # Run all tests
golangci-lint run       # Lint
```

### Docker Commands

```bash
make docker-build       # Build and start containers
make docker-up          # Start containers
make docker-down        # Stop containers
make docker-logs        # Stream bot logs
```

## Jira App Setup

1. Go to [developer.atlassian.com/console/myapps](https://developer.atlassian.com/console/myapps/)
2. Create a new OAuth 2.0 app
3. Add the **granular** Jira scopes the bot requests (issues, comments, attachments, projects, priorities, filters, webhooks, etc.) plus `offline_access`. Do **not** add the classic `read:jira-work` / `write:jira-work` / `read:jira-user` scopes — mixing them produces a "hybrid" token that fails with *"scope does not match"*. Run `/diagnose` to verify the granted set against what the API needs.
4. Set callback URL to your `JIRA_REDIRECT_URI`
5. Copy Client ID and Secret to `.env`

## License

MIT
