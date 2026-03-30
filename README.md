# SleepJiraBot

Telegram bot for Jira Cloud integration. Authenticate via OAuth 2.0, track issues, get notifications, run sprint reports, and schedule automated JQL reports — all from Telegram.

## Features

- **Jira OAuth 2.0** — secure authentication with encrypted token storage (AES-256-GCM)
- **Issue management** — view, comment, transition, and assign issues
- **Subscriptions** — real-time notifications on issue/project changes (polling + webhooks)
- **Sprint reports** — view sprint boards and progress
- **Scheduled reports** — cron-based JQL/sprint reports delivered to chats
- **Multilingual** — English and Russian
- **Daily standups** — quick view of assigned issues for you or teammates

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
| `/sprint [PROJECT] [BOARD] [SPRINT]` | Sprint board |
| `/filters` | Jira saved filters |
| `/watch` / `/unwatch` | Subscribe/unsubscribe to changes |
| `/subscriptions` | List active subscriptions |
| `/schedule <cron>` | Create scheduled report |
| `/unschedule` / `/schedules` | Manage scheduled reports |
| `/defaults` | Set default project/board |
| `/lang` | Switch language |
| `/help` | Help |

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
| `JIRA_REDIRECT_URI` | no | `http://localhost:8080/callback` | OAuth callback URL |
| `JIRA_WEBHOOK_SECRET` | yes | — | Webhook signature verification (HMAC-SHA256) |
| `MONGO_URI` | no | `mongodb://localhost:27017` | MongoDB connection string |
| `MONGO_DB` | no | `sleepjirabot` | Database name |
| `CALLBACK_ADDR` | no | `:8080` | HTTP server address |
| `POLL_INTERVAL` | no | `2m` | Subscription polling interval |
| `LOG_LEVEL` | no | `info` | Log level (debug/info/warn/error) |

Generate encryption key:

```bash
openssl rand -hex 32
```

## Architecture

```
cmd/bot/main.go          # Entry point — starts all services concurrently
internal/
  config/                # Environment-based configuration
  crypto/                # AES-256-GCM token encryption
  jira/                  # OAuth 2.0 flow + Jira Cloud REST API client
  telegram/              # Bot handlers, menus, conversation state
  poller/                # Periodic subscription polling
  scheduler/             # Cron-based scheduled reports
  webhook/               # Jira webhook event processing
  storage/               # MongoDB repositories (User, Subscription, Schedule)
  locale/                # i18n (en, ru)
  format/                # Telegram MarkdownV2 formatting
  middleware/            # Rate limiting
  logger/                # Zerolog setup
```

The bot runs 4 concurrent services:

1. **Telegram bot** — long-polling for commands and callbacks
2. **Issue poller** — periodically queries Jira for subscription updates
3. **Scheduler** — cron-based report delivery
4. **HTTP server** — OAuth callbacks (`/callback`) and Jira webhooks (`/webhook`)

Graceful shutdown on SIGINT/SIGTERM.

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
3. Add scopes: `read:jira-work`, `write:jira-work`, `read:jira-user`
4. Set callback URL to your `JIRA_REDIRECT_URI`
5. Copy Client ID and Secret to `.env`

## License

MIT
