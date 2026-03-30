# SleepJiraBot

Telegram-бот для интеграции с Jira Cloud. Авторизация через OAuth 2.0, отслеживание задач, уведомления, спринт-отчёты и автоматические JQL-отчёты по расписанию — всё прямо из Telegram.

## Возможности

- **Jira OAuth 2.0** — безопасная авторизация с шифрованием токенов (AES-256-GCM)
- **Управление задачами** — просмотр, комментирование, смена статуса, назначение
- **Подписки** — уведомления об изменениях задач/проектов (поллинг + вебхуки)
- **Спринт-отчёты** — просмотр спринт-бордов и прогресса
- **Отчёты по расписанию** — автоматическая отправка JQL/спринт-отчётов по cron
- **Мультиязычность** — английский и русский
- **Дейли-стендапы** — быстрый обзор назначенных задач для себя или коллег

## Команды бота

| Команда | Описание |
|---------|----------|
| `/start`, `/menu` | Главное меню |
| `/connect` / `/disconnect` | Авторизация в Jira |
| `/me` | Текущий профиль |
| `/issue <KEY>` | Просмотр задачи |
| `/daily [username]` | Дейли-отчёт |
| `/list [JQL]` | Поиск задач |
| `/comment <KEY> <текст>` | Добавить комментарий |
| `/transition <KEY>` | Сменить статус задачи |
| `/assign <KEY>` | Назначить задачу на себя |
| `/sprint [ПРОЕКТ] [БОРД] [СПРИНТ]` | Спринт-борд |
| `/filters` | Сохранённые фильтры Jira |
| `/watch` / `/unwatch` | Подписаться/отписаться от изменений |
| `/subscriptions` | Список активных подписок |
| `/schedule <cron>` | Создать отчёт по расписанию |
| `/unschedule` / `/schedules` | Управление расписаниями |
| `/defaults` | Настроить проект/борд по умолчанию |
| `/lang` | Сменить язык |
| `/help` | Помощь |

## Быстрый старт

### Требования

- Go 1.25+
- MongoDB 7+
- Токен Telegram-бота ([@BotFather](https://t.me/BotFather))
- OAuth 2.0 приложение Jira Cloud ([developer.atlassian.com](https://developer.atlassian.com/console/myapps/))

### Локальный запуск

```bash
cp .env.example .env
# Заполните .env своими данными

make run
```

### Docker

```bash
cp .env.example .env
# Заполните .env своими данными

make docker-build
```

Запускает бота и MongoDB 7 через Docker Compose.

## Конфигурация

| Переменная | Обязательна | По умолчанию | Описание |
|------------|-------------|--------------|----------|
| `TELEGRAM_TOKEN` | да | — | Токен Telegram Bot API |
| `JIRA_CLIENT_ID` | да | — | Client ID приложения Jira OAuth 2.0 |
| `JIRA_CLIENT_SECRET` | да | — | Client Secret приложения Jira OAuth 2.0 |
| `ENCRYPTION_KEY` | да | — | 64 hex-символа (32 байта) для AES-256-GCM |
| `JIRA_REDIRECT_URI` | нет | `http://localhost:8080/callback` | URL обратного вызова OAuth |
| `JIRA_WEBHOOK_SECRET` | нет | — | Секрет для проверки подписи вебхуков |
| `MONGO_URI` | нет | `mongodb://localhost:27017` | Строка подключения к MongoDB |
| `MONGO_DB` | нет | `sleepjirabot` | Имя базы данных |
| `CALLBACK_ADDR` | нет | `:8080` | Адрес HTTP-сервера |
| `POLL_INTERVAL` | нет | `2m` | Интервал поллинга подписок |
| `LOG_LEVEL` | нет | `info` | Уровень логирования (debug/info/warn/error) |

Генерация ключа шифрования:

```bash
openssl rand -hex 32
```

## Архитектура

```
cmd/bot/main.go          # Точка входа — запускает все сервисы параллельно
internal/
  config/                # Конфигурация через переменные окружения
  crypto/                # Шифрование токенов AES-256-GCM
  jira/                  # OAuth 2.0 + клиент Jira Cloud REST API
  telegram/              # Обработчики команд, меню, состояние диалогов
  poller/                # Периодический поллинг подписок
  scheduler/             # Отчёты по расписанию (cron)
  webhook/               # Обработка вебхук-событий Jira
  storage/               # MongoDB-репозитории (User, Subscription, Schedule)
  locale/                # Локализация (en, ru)
  format/                # Форматирование Telegram MarkdownV2
  middleware/            # Rate limiting
  logger/                # Настройка Zerolog
```

Бот запускает 4 параллельных сервиса:

1. **Telegram-бот** — long-polling для команд и callback-кнопок
2. **Поллер задач** — периодический опрос Jira по подпискам
3. **Планировщик** — отправка отчётов по cron-расписанию
4. **HTTP-сервер** — OAuth-callback (`/callback`) и Jira-вебхуки (`/webhook`)

Корректное завершение по SIGINT/SIGTERM.

## Разработка

```bash
make build              # Сборка бинарника в bin/sleepjirabot
go test ./...           # Запуск всех тестов
golangci-lint run       # Линтер
```

### Docker-команды

```bash
make docker-build       # Сборка и запуск контейнеров
make docker-up          # Запуск контейнеров
make docker-down        # Остановка контейнеров
make docker-logs        # Логи бота в реальном времени
```

## Настройка Jira-приложения

1. Перейдите на [developer.atlassian.com/console/myapps](https://developer.atlassian.com/console/myapps/)
2. Создайте новое OAuth 2.0 приложение
3. Добавьте scopes: `read:jira-work`, `write:jira-work`, `read:jira-user`
4. Укажите callback URL, совпадающий с `JIRA_REDIRECT_URI`
5. Скопируйте Client ID и Secret в `.env`

## Лицензия

MIT
