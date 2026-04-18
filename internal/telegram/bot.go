package telegram

import (
	"context"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"net/http"

	"SleepJiraBot/internal/jira"
	"SleepJiraBot/internal/poller"
	"SleepJiraBot/internal/preferences"
	"SleepJiraBot/internal/storage"
)

const semAcquireTimeout = 10 * time.Second

const maxConcurrentUpdates = 20

type Bot struct {
	api     *tgbotapi.BotAPI
	handler *Handler
	log     zerolog.Logger
}

func NewBot(token string, oauth *jira.OAuthClient, jiraClient *jira.Client, userRepo *storage.UserRepo, prefs preferences.Provider, subRepo *storage.SubscriptionRepo, scheduleRepo *storage.ScheduleRepo, webhookMgr *jira.WebhookManager, templateRepo *storage.TemplateRepo, log zerolog.Logger, adminID int64, httpClient *http.Client) (*Bot, error) {
	api, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		return nil, err
	}

	log.Info().Str("bot", api.Self.UserName).Msg("authorized on Telegram")

	return &Bot{
		api:     api,
		handler: NewHandler(api, oauth, jiraClient, userRepo, prefs, subRepo, scheduleRepo, webhookMgr, templateRepo, log, adminID),
		log:     log,
	}, nil
}

func (b *Bot) API() *tgbotapi.BotAPI {
	return b.api
}

func (b *Bot) SetCallbackServer(cs *jira.CallbackServer) {
	b.handler.SetCallbackServer(cs)
}

func (b *Bot) SetPollerRef(p *poller.Poller) {
	b.handler.SetPollerRef(p)
}

func (b *Bot) SetWebhookStats(repo *storage.WebhookRepo, eventsFn func() int64) {
	b.handler.SetWebhookStats(repo, eventsFn)
}

func (b *Bot) SetOnScheduleChange(fn func()) {
	b.handler.SetOnScheduleChange(fn)
}

// UseMongoStateStore swaps the default in-memory FSM store for a
// Mongo-backed one so conversation progress survives process restarts.
// Opt-in via PERSIST_CONVERSATION_STATES; must be called before Start.
func (b *Bot) UseMongoStateStore(ctx context.Context, db *mongo.Database, log zerolog.Logger) error {
	store, err := NewMongoStateStore(ctx, db, log)
	if err != nil {
		return err
	}
	b.handler.useStateStore(store)
	return nil
}

func (b *Bot) Start(ctx context.Context) {
	b.handler.states.StartCleanup(ctx)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	b.log.Info().Msg("started polling for updates")

	sem := make(chan struct{}, maxConcurrentUpdates)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			b.log.Info().Msg("stopping bot polling")
			b.api.StopReceivingUpdates()
			wg.Wait()
			return
		case update := <-updates:
			if update.Message == nil && update.CallbackQuery == nil {
				continue
			}
			timer := time.NewTimer(semAcquireTimeout)
			select {
			case sem <- struct{}{}:
				timer.Stop()
				wg.Add(1)
				go func(upd tgbotapi.Update) {
					defer func() {
						<-sem
						wg.Done()
					}()
					b.handler.HandleUpdate(ctx, upd)
				}(update)
			case <-timer.C:
				b.log.Warn().Msg("update handler pool full, dropping update")
			case <-ctx.Done():
				timer.Stop()
				wg.Wait()
				return
			}
		}
	}
}
