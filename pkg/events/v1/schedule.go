package eventsv1

import "strconv"

// ScheduleDue fires every time a cron schedule wakes and matches a
// ScheduledReport. It is intentionally emitted *before* JQL execution so
// a future scheduler-svc can hand the work off to a separate executor.
type ScheduleDue struct {
	ReportID   string `json:"report_id"`
	TelegramID int64  `json:"telegram_id"`
	ChatID     int64  `json:"chat_id"`
	JQL        string `json:"jql"`
	ReportName string `json:"report_name,omitempty"`
	FiredAt    int64  `json:"fired_at"`
}

func (ScheduleDue) Subject() string { return SubjectScheduleDue }

func (e ScheduleDue) IdempotencyKey() string {
	return "schedule.due:" + e.ReportID + ":" + strconv.FormatInt(e.FiredAt, 10)
}
