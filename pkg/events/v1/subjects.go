// Package eventsv1 defines the v1 domain event contract for SleepJiraBot.
//
// Phase 0 of the microservices split publishes these events alongside the
// existing monolith so downstream services extracted in later phases can
// consume a stable contract without changes to producers.
//
// Subject scheme: sjb.<context>.<aggregate>.<event>.v1
package eventsv1

// JetStream stream names. One stream per bounded context; subjects scoped
// to the stream's context prefix. Adding a new event under an existing
// context does not require schema or stream changes.
const (
	StreamIdentity     = "SJB_IDENTITY"
	StreamPreferences  = "SJB_PREFERENCES"
	StreamSubscription = "SJB_SUBSCRIPTION"
	StreamWebhook      = "SJB_WEBHOOK"
	StreamSchedule     = "SJB_SCHEDULE"
	StreamNotify       = "SJB_NOTIFY"
)

// Subject filters per stream.
const (
	SubjectsIdentity     = "sjb.identity.>"
	SubjectsPreferences  = "sjb.preferences.>"
	SubjectsSubscription = "sjb.subscription.>"
	SubjectsWebhook      = "sjb.webhook.>"
	SubjectsSchedule     = "sjb.schedule.>"
	SubjectsNotify       = "sjb.notify.>"
)

// Concrete event subjects (all v1).
const (
	SubjectUserAuthenticated = "sjb.identity.user.authenticated.v1"
	SubjectTokensRefreshed   = "sjb.identity.tokens.refreshed.v1"
	SubjectUserDeauthorized  = "sjb.identity.user.deauthorized.v1"

	SubjectLanguageChanged = "sjb.preferences.language.changed.v1"
	SubjectDefaultsChanged = "sjb.preferences.defaults.changed.v1"

	SubjectSubscriptionCreated = "sjb.subscription.created.v1"
	SubjectSubscriptionDeleted = "sjb.subscription.deleted.v1"
	SubjectChangeDetected      = "sjb.subscription.change_detected.v1"

	SubjectWebhookReceived   = "sjb.webhook.received.v1"
	SubjectWebhookNormalized = "sjb.webhook.normalized.v1"

	SubjectScheduleDue = "sjb.schedule.due.v1"

	SubjectNotifyRequested = "sjb.notify.requested.v1"
	SubjectNotifyDelivered = "sjb.notify.delivered.v1"
	SubjectNotifyFailed    = "sjb.notify.failed.v1"
)
