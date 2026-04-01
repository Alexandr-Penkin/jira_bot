package telegram

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"SleepJiraBot/internal/locale"
)

func mainMenuKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.profile"), "m:profile"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.issues"), "m:issues"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.notifications"), "m:notif"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.reports"), "m:reports"),
		),
	)
}

func profileMenuKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.my_profile"), "a:me"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.connect_jira"), "a:connect"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.disconnect_jira"), "a:disconnect"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.language"), "a:lang"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.defaults"), "a:defaults"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.issue_types"), "a:issuetypes"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.assignee_field"), "a:assigneefield"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sp_field"), "a:spfield"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.daily_jql"), "a:dailyjql"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:main"),
		),
	)
}

func issuesMenuKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.daily"), "a:daily"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.daily_user"), "a:daily_user"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.view_issue"), "a:issue"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.my_issues"), "a:list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.search_jql"), "a:listjql"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.filters"), "a:filters"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.comment"), "a:comment"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.transition"), "a:trans"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.assign_to_me"), "a:assign"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:main"),
		),
	)
}

func notifMenuKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.new_subscription"), "a:subscribe"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.subscriptions"), "a:subs"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.unwatch_all"), "a:unwatch"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:main"),
		),
	)
}

func reportsMenuKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.sprint"), "a:sprint"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.new_schedule"), "a:sched"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.remove_all"), "a:unsched"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.schedules"), "a:scheds"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.back"), "m:main"),
		),
	)
}

func issueActionsKeyboard(lang locale.Lang, issueKey string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.comment"), "issue_action:comment:"+issueKey),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.transition"), "issue_action:transition:"+issueKey),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.assign_to_me"), "issue_action:assign:"+issueKey),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.watch_issue"), "issue_action:watch:"+issueKey),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.menu"), "m:main"),
		),
	)
}

func cancelKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.cancel"), "a:cancel"),
		),
	)
}

func menuButtonKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.menu"), "m:main"),
		),
	)
}

func adminMenuKeyboard(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_stats"), "adm:stats"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_users"), "adm:users"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_broadcast"), "adm:broadcast"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_poller"), "adm:poller"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin_back"), "m:main"),
		),
	)
}

func mainMenuKeyboardAdmin(lang locale.Lang) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.profile"), "m:profile"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.issues"), "m:issues"),
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.notifications"), "m:notif"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.reports"), "m:reports"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(locale.T(lang, "btn.admin"), "m:admin"),
		),
	)
}
