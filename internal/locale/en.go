package locale

var en = map[string]string{
	// General
	"error.generic":       "Something went wrong. Please try again.",
	"error.not_connected": "You are not connected to Jira. Use /connect first.",
	"action.cancelled":    "Action cancelled.",
	"unknown_command":     "Unknown command. Use /start to see the menu.",

	// Start / Help
	"start.welcome": "👋 *Welcome to SleepJiraBot!*\n\nI help you work with Jira Cloud right from Telegram.\nChoose an action below:",
	"help.text": "*Available commands:*\n\n" +
		"*Account:*\n" +
		"/connect — Connect your Jira Cloud account\n" +
		"/disconnect — Disconnect your Jira account\n" +
		"/me — Show your Jira profile\n" +
		"/lang — Change language\n\n" +
		"*Issues:*\n" +
		"/daily — Generate daily standup report\n" +
		"/issue `PROJ-123` — View issue details\n" +
		"/filters — Browse favourite filters\n" +
		"/list `JQL` — Search issues by JQL\n" +
		"/comment `PROJ-123 text` — Add a comment\n" +
		"/transition `PROJ-123` — Change issue status\n" +
		"/assign `PROJ-123` — Assign issue to yourself\n\n" +
		"*Notifications:*\n" +
		"/watch `PROJ` — Subscribe to project notifications\n" +
		"/unwatch — Remove all subscriptions in this chat\n" +
		"/subscriptions — List active subscriptions\n\n" +
		"*Reports:*\n" +
		"/sprint `PROJ` — Sprint report with metrics\n" +
		"/schedule `cron | name | JQL` — Create a scheduled report\n" +
		"/unschedule — Remove all schedules in this chat\n" +
		"/schedules — List active schedules\n\n" +
		"Or use /start to open the button menu.",

	// Language
	"lang.choose":  "Choose language:",
	"lang.changed": "Language changed to *English*.",

	// Menu
	"menu.main":    "📌 *Main Menu*\nChoose an action:",
	"menu.issues":  "📋 *Issues*\nChoose an action:",
	"menu.notif":   "🔔 *Notifications*\nChoose an action:",
	"menu.reports": "📊 *Reports*\nChoose an action:",
	"menu.profile": "👤 *Profile*\nChoose an action:",

	// Menu buttons
	"btn.profile":         "👤 Profile",
	"btn.my_profile":      "📄 My Jira Profile",
	"btn.connect_jira":    "🔗 Connect Jira",
	"btn.disconnect_jira": "🔌 Disconnect Jira",
	"btn.issues":          "📋 Issues",
	"btn.notifications":   "🔔 Notifications",
	"btn.reports":         "📊 Reports",
	"btn.view_issue":      "🔍 View Issue",
	"btn.my_issues":       "📄 My Issues",
	"btn.search_jql":      "🔎 Search (JQL)",
	"btn.comment":         "💬 Comment",
	"btn.transition":      "🔄 Transition",
	"btn.assign_to_me":    "✋ Assign to Me",
	"btn.back":            "◀️ Back",
	"btn.unwatch_all":     "🚫 Unwatch All",
	"btn.subscriptions":   "📋 Subscriptions",
	"btn.new_schedule":    "➕ New Schedule",
	"btn.remove_all":      "🗑 Remove All",
	"btn.schedules":       "📋 Schedules",
	"btn.cancel":          "❌ Cancel",
	"btn.menu":            "◀️ Menu",
	"btn.language":        "🌐 Language",
	"btn.defaults":        "⚙️ Default Project",
	"btn.issue_types":     "🏷 Issue Types",
	"btn.done_statuses":   "✅ Done Statuses",
	"btn.hold_statuses":   "⏸ Hold Statuses",

	// Connect
	"connect.click":            "Click the button below to connect your Jira Cloud account:",
	"connect.btn":              "🔗 Connect Jira",
	"connect.already":          "You are already connected to Jira. Use /disconnect first to reconnect.",
	"connect.success":          "Connected to Jira site *%s* successfully\\!\n\nUse /me to check your profile or /help to see available commands\\.",
	"connect.choose_site":      "You have access to multiple Jira sites. Please choose the one you want to connect:",
	"connect.site_expired":     "Site selection has expired. Please use /connect again.",
	"disconnect.success":       "Disconnected from Jira. Use /connect to link a new account.",
	"disconnect.failed":        "Failed to disconnect. Please try again.",
	"disconnect.not_linked":    "You are not connected to Jira. Use /connect to link your account.",
	"disconnect.token_expired": "Your Jira session has expired. You have been automatically disconnected.\n\nPlease use /connect to reconnect.",

	// Profile
	"me.title":  "*Your Jira Profile:*\n\nName: %s\nEmail: %s\nSite: %s",
	"me.failed": "Failed to fetch Jira profile. Try /connect again if the issue persists.",

	// Defaults
	"defaults.enter_project": "Enter project key (e.g. `PROJ`).\nSend `-` to clear defaults.",
	"defaults.choose_board":  "Choose a default board or type its name:",
	"defaults.saved":         "Default project set: *%s*, board: *%s*.",
	"defaults.cleared":       "Default project and board cleared.",
	"defaults.current":       "\n\nDefault project: *%s*, board: *%s*",
	"defaults.boards_failed": "Failed to load boards. Default project saved without board.",
	"defaults.project_saved": "Default project set: *%s* (no board selected).",

	// Issue
	"issue.usage":             "Usage: /issue PROJ-123",
	"issue.invalid_key":       "Invalid issue key format. Expected: PROJ-123",
	"issue.failed":            "Failed to get issue %s. Check the key and try again.",
	"issue.type":              "Type",
	"issue.status":            "Status",
	"issue.priority":          "Priority",
	"issue.assignee":          "Assignee",
	"issue.reporter":          "Reporter",
	"issue.due":               "Due",
	"issue.labels":            "Labels",
	"issue.unassigned":        "Unassigned",
	"issue.enter_key":         "Enter the issue key (e.g. `PROJ-123`):",
	"issue.invalid_key_short": "Invalid issue key. Expected: PROJ-123",

	// List / Search
	"list.no_issues":    "No issues found.",
	"list.found":        "*Found %d issues:*\n\n",
	"list.jql_too_long": "JQL query is too long (max %d characters).",
	"list.failed":       "Failed to search issues. Check your JQL and try again.",
	"list.enter_jql":    "Enter JQL query:",

	// Comment
	"comment.usage":      "Usage: /comment PROJ-123 Your comment text",
	"comment.too_long":   "Comment is too long (max %d characters).",
	"comment.added":      "Comment added to %s.",
	"comment.failed":     "Failed to add comment to %s.",
	"comment.enter_key":  "Enter the issue key (e.g. `PROJ-123`):",
	"comment.enter_text": "Enter your comment for *%s*:",

	// Transition
	"transition.usage":            "Usage: /transition PROJ-123",
	"transition.choose":           "Choose a transition for *%s*:",
	"transition.none":             "No available transitions for %s.",
	"transition.failed":           "Failed to get transitions for %s.",
	"transition.applied":          "Transition applied to *%s* successfully!",
	"transition.cb_applied":       "Transition applied to %s",
	"transition.cb_failed":        "Failed to transition",
	"transition.cb_not_connected": "Not connected to Jira",
	"transition.cb_invalid":       "Invalid transition data",
	"transition.enter_key":        "Enter the issue key (e.g. `PROJ-123`):",

	// Assign
	"assign.usage":     "Usage: /assign PROJ-123",
	"assign.success":   "Issue %s assigned to you.",
	"assign.failed":    "Failed to assign %s to you.",
	"assign.me_failed": "Failed to get your Jira profile.",
	"assign.enter_key": "Enter the issue key (e.g. `PROJ-123`):",

	// Subscriptions
	"btn.new_subscription": "➕ New Subscription",
	"btn.sub_my_new":       "📥 My New Issues",
	"btn.sub_my_mentions":  "💬 My Mentions",
	"btn.sub_my_watched":   "👁 Issues I Watch",
	"btn.sub_project":      "📁 Project Updates",
	"btn.sub_issue":        "🔔 Issue Updates",
	"btn.watch_issue":      "👁 Watch",
	"btn.sub_filter":       "🔍 Filter Updates",

	"sub.choose_type":     "*Choose subscription type:*",
	"sub.enter_project":   "Enter project key (e.g. `PROJ`):",
	"sub.enter_issue":     "Enter issue key (e.g. `PROJ-123`):",
	"sub.choose_filter":   "*Choose a filter:*",
	"sub.no_filters":      "You have no saved filters in Jira.",
	"sub.filters_failed":  "Failed to get your Jira filters.",
	"sub.already_exists":  "This subscription already exists.",
	"sub.failed":          "Failed to create subscription.",
	"sub.created":         "Subscription *%s* created!",
	"sub.created_project": "Subscription for project *%s* created!",
	"sub.created_issue":   "Subscription for issue *%s* created!",
	"sub.created_filter":  "Subscription for filter *%s* created!",

	"sub.type_my_new_issues":   "My New Issues",
	"sub.type_my_mentions":     "My Mentions",
	"sub.type_my_watched":      "Issues I Watch",
	"sub.type_project_updates": "Project Updates",
	"sub.type_issue_updates":   "Issue Updates",
	"sub.type_filter_updates":  "Filter Updates",

	"watch.invalid_project":       "Invalid project key. Use uppercase letters and digits, max 20 characters.",
	"watch.invalid_project_short": "Invalid project key. Use uppercase letters/digits, max 20 chars.",
	"unwatch.success":             "All subscriptions removed from this chat.",
	"unwatch.failed":              "Failed to remove subscriptions.",
	"subs.title":                  "*Active subscriptions:*\n\n",
	"subs.none":                   "No active subscriptions. Use the menu to create one.",
	"subs.failed":                 "Failed to get subscriptions.",

	// Schedule
	"schedule.usage": "Usage: /schedule `cron | name | JQL`\n\n" +
		"Examples:\n" +
		"`0 9 * * 1-5 | Morning briefing | assignee=currentUser() AND resolution=Unresolved`\n" +
		"`0 18 * * 5 | Weekly overdue | duedate < now() AND resolution=Unresolved`\n\n" +
		"Cron format: `minute hour day month weekday`",
	"schedule.invalid_format":  "Invalid format. Use: `cron | name | JQL`",
	"schedule.fields_required": "All fields are required: cron expression, report name, and JQL.",
	"schedule.name_too_long":   "Report name is too long (max %d characters).",
	"schedule.invalid_cron":    "Invalid cron expression: %s",
	"schedule.created":         "Schedule created!\n\nName: *%s*\nCron: `%s`\nJQL: `%s`",
	"schedule.failed":          "Failed to create schedule.",
	"schedule.enter":           "Enter schedule in format:\n`cron | name | JQL`\n\nExample:\n`0 9 * * 1-5 | Morning report | assignee=currentUser()`",
	"unschedule.success":       "All schedules removed from this chat.",
	"unschedule.failed":        "Failed to remove schedules.",
	"schedules.title":          "*Active schedules:*\n\n",
	"schedules.none":           "No active schedules. Use /schedule or the menu to create one.",
	"schedules.failed":         "Failed to get schedules.",

	// Scheduler (background reports)
	"report.not_connected": "Skipping scheduled report *%s*: Jira account not connected. Use /connect to link your account.",
	"report.failed":        "Failed to run scheduled report *%s*: %s",
	"report.default_name":  "Scheduled Report",
	"report.found":         "Found: %d issues",
	"report.no_issues":     "_No issues found._",
	"report.more":          "\n_...and %d more_",

	// Webhook notifications
	"notif.event":       "Jira event: %s",
	"notif.event_label": "Event",
	"notif.by":          "By",
	"notif.status":      "Status",
	"notif.assignee":    "Assignee",
	"notif.changed":     "Changed",
	"notif.comment_by":  "Comment by",

	// Poller notifications
	"notif.updates":    "👤 %s made updates in [%s](%s)",
	"notif.someone":    "Someone",
	"notif.summary":    "Summary",
	"notif.unassigned": "Unassigned",
	"notif.cleared":    "cleared",
	"notif.reporter":   "Reporter",
	"notif.priority":   "Priority",
	"notif.issue_type": "Type of issue",

	// Daily
	"btn.daily":             "📝 Daily",
	"daily.done":            "Done",
	"daily.doing":           "In Progress",
	"daily.plan":            "Planning",
	"daily.no_done":         "— none",
	"daily.no_doing":        "— none",
	"daily.failed":          "Failed to generate daily report.",
	"daily.enter_user":      "Enter user name to search:",
	"daily.choose_user":     "Multiple users found. Choose one:",
	"daily.user_not_found":  "User not found.",
	"daily.search_failed":   "Failed to search users.",
	"btn.daily_user":        "📝 Daily (other user)",
	"btn.daily_jql":         "📝 Daily JQL",
	"daily_jql.title":       "*Daily JQL Settings*\n\nCustom JQL queries for each daily block. Leave empty to use defaults.",
	"daily_jql.current":     "\n\n*Current settings:*\n*Done:* `%s`\n*Doing:* `%s`\n*Plan:* `%s`",
	"daily_jql.default":     "(default)",
	"daily_jql.btn_done":    "Edit Done JQL",
	"daily_jql.btn_doing":   "Edit Doing JQL",
	"daily_jql.btn_plan":    "Edit Plan JQL",
	"daily_jql.btn_reset":   "Reset All to Defaults",
	"daily_jql.enter_done":  "Enter JQL for *Done* block (issues you completed).\nSend `-` to reset to default.\n\nDefault: `status changed BY currentUser() AFTER \"yesterday\"`",
	"daily_jql.enter_doing": "Enter JQL for *Doing* block (issues in progress).\nSend `-` to reset to default.\n\nDefault: `assignee=currentUser() AND statusCategory=\"In Progress\"`",
	"daily_jql.enter_plan":  "Enter JQL for *Plan* block (planned issues).\nSend `-` to reset to default.\n\nDefault: none (empty section)",
	"daily_jql.saved":       "Daily JQL saved.",
	"daily_jql.reset":       "Daily JQL reset to defaults.",
	"daily.no_plan":         "— none",

	// Sprint
	"btn.sprint":               "🏃 Sprint Report",
	"sprint.enter_project":     "Enter project key (e.g. `PROJ`):",
	"sprint.choose_board":      "Choose a board or type its name:",
	"sprint.choose_sprint":     "Choose a sprint or type its name:",
	"sprint.no_boards":         "No boards found for this project.",
	"sprint.board_not_found":   "Board \"%s\" not found. Use /sprint PROJECT to see available boards.",
	"sprint.sprint_not_found":  "Sprint \"%s\" not found. Use /sprint PROJECT BOARD to see available sprints.",
	"sprint.no_sprints":        "No sprints found for this board.",
	"sprint.no_issues":         "No issues found in this sprint.",
	"sprint.boards_failed":     "Failed to get boards.",
	"sprint.sprints_failed":    "Failed to get sprints.",
	"sprint.report_failed":     "Failed to generate sprint report.",
	"sprint.report_title":      "Sprint Report",
	"sprint.total":             "Total issues",
	"sprint.done":              "Done",
	"sprint.in_progress":       "In Progress",
	"sprint.hold":              "On Hold",
	"sprint.todo":              "To Do",
	"sprint.by_type":           "By Issue Type",
	"sprint.by_assignee":       "By Assignee",
	"sprint.unassigned":        "Unassigned",
	"sprint.filtered":          "Filter: %s",
	"sprint.bug_ratio":         "Bug ratio",
	"sprint.by_priority":       "By Priority",
	"sprint.unestimated":       "Unestimated Issues",
	"sprint.overdue":           "Overdue Issues",
	"sprint.and_more":          "and %d more",
	"sprint.velocity":          "Velocity",
	"sprint.velocity_avg":      "avg(%d)",
	"sprint.scope_creep":       "Scope Creep",
	"sprint.carry_over":        "Carry-over",
	"sprint.commitment":        "Commitment",
	"sprint.cycle_time":        "Cycle Time",
	"sprint.cycle_time_avg":    "avg %s (%d issues)",
	"sprint.blocked_time":      "Blocked Time",
	"sprint.blocked_detail":    "total %s | avg %s (%d issues)",
	"sprint.forecast":          "Forecast",
	"sprint.forecast_on_track": "on track",
	"sprint.forecast_at_risk":  "at risk",
	"sprint.days_left":         "%d days left",

	// Issue Types settings
	"issuetypes.enter_project": "Enter project key to load issue types (e.g. `PROJ`):",
	"issuetypes.choose":        "Select issue types for sprint reports.\nSelected types will be used for metrics calculation.\nTap to toggle, then press Save.",
	"issuetypes.saved":         "Sprint report issue types saved: *%s*.",
	"issuetypes.cleared":       "Issue type filter cleared. All types will be included in reports.",
	"issuetypes.current":       "\nSprint issue types: *%s*",
	"issuetypes.none":          "All types",
	"issuetypes.failed":        "Failed to load issue types for this project.",
	"issuetypes.save_btn":      "💾 Save",
	"issuetypes.clear_btn":     "🗑 Clear filter",

	// Done statuses settings
	"donestatuses.enter_project": "Enter project key to load statuses (e.g. `PROJ`):",
	"donestatuses.choose":        "Select statuses that should count as *Done* in sprint reports.\nTap to toggle, then press Save.",
	"donestatuses.saved":         "Done statuses saved: *%s*.",
	"donestatuses.cleared":       "Done statuses reset to default (uses Jira status category).",
	"donestatuses.current":       "\nDone statuses: *%s*",
	"donestatuses.none":          "Default (Jira category)",
	"donestatuses.failed":        "Failed to load statuses for this project.",
	"donestatuses.save_btn":      "💾 Save",
	"donestatuses.clear_btn":     "🗑 Reset to default",

	// Hold statuses settings
	"holdstatuses.enter_project": "Enter project key to load statuses (e.g. `PROJ`):",
	"holdstatuses.choose":        "Select statuses that should count as *Hold/Blocked* in sprint reports.\nTap to toggle, then press Save.",
	"holdstatuses.saved":         "Hold statuses saved: *%s*.",
	"holdstatuses.cleared":       "Hold statuses reset to default.",
	"holdstatuses.current":       "\nHold statuses: *%s*",
	"holdstatuses.none":          "Default (Hold, On Hold, Blocked, Suspended)",
	"holdstatuses.failed":        "Failed to load statuses for this project.",
	"holdstatuses.save_btn":      "💾 Save",
	"holdstatuses.clear_btn":     "🗑 Reset to default",

	// Filters
	"btn.filters":          "📋 Filters",
	"filters.choose":       "*Choose a favourite filter:*",
	"filters.no_filters":   "You have no favourite filters in Jira.",
	"filters.failed":       "Failed to get favourite filters.",
	"filters.not_found":    "Filter not found.",
	"filters.issues_title": "*%s* (%d issues):\n\n",
	"filters.more":         "\n_...and %d more_",

	// Assignee field settings
	"btn.assignee_field":      "👤 Assignee Field",
	"assigneefield.choose":    "Select the field to use as assignee in reports.\nDefault is the standard *Assignee* field.\nShowing people-type fields:",
	"assigneefield.saved":     "Assignee field set to: *%s*.",
	"assigneefield.cleared":   "Assignee field reset to standard *Assignee*.",
	"assigneefield.current":   "\nAssignee field: *%s*",
	"assigneefield.default":   "Assignee (standard)",
	"assigneefield.failed":    "Failed to load fields from Jira.",
	"assigneefield.no_fields": "No people-type custom fields found.",
	"assigneefield.reset_btn": "🔄 Reset to default",

	// Story points field settings
	"btn.sp_field":      "🔢 Story Points Field",
	"spfield.choose":    "Select the field to use as Story Points in reports.\nBy default the bot checks common field names automatically.\nShowing number-type fields:",
	"spfield.saved":     "Story Points field set to: *%s*.",
	"spfield.cleared":   "Story Points field reset to auto-detect.",
	"spfield.current":   "\nStory Points field: *%s*",
	"spfield.default":   "Auto-detect",
	"spfield.failed":    "Failed to load fields from Jira.",
	"spfield.no_fields": "No number-type custom fields found.",
	"spfield.reset_btn": "🔄 Reset to auto-detect",

	// Admin
	"admin.not_authorized": "You are not authorized to access the admin panel.",
	"admin.menu":           "🛠 *Admin Panel*\nChoose an action:",
	"btn.admin":            "🛠 Admin",
	"btn.admin_stats":      "📊 Statistics",
	"btn.admin_users":      "👥 Users",
	"btn.admin_broadcast":  "📢 Broadcast",
	"btn.admin_poller":     "⚙️ Poller Status",
	"btn.admin_back":       "◀️ Back to Menu",

	"admin.stats": "*📊 Bot Statistics*\n\n" +
		"Users total: %d\n" +
		"Users connected: %d\n" +
		"Active subscriptions: %d\n" +
		"Active schedules: %d",

	"admin.users_title":       "*👥 Connected Users* (page %d):\n\n",
	"admin.users_empty":       "No users found.",
	"admin.user_entry":        "%d\\. `%d` — %s\n   Site: %s | Created: %s\n",
	"admin.user_disconnected": "%d\\. `%d` — disconnected\n   Created: %s\n",
	"btn.admin_prev":          "◀️ Prev",
	"btn.admin_next":          "Next ▶️",

	"admin.user_actions":         "*User* `%d`\n\nSite: %s\nConnected: %s\n\nChoose action:",
	"btn.admin_disconnect":       "🔌 Disconnect User",
	"btn.admin_delete":           "🗑 Delete User",
	"admin.user_disconnected_ok": "User `%d` disconnected.",
	"admin.user_deleted":         "User `%d` deleted with all subscriptions and schedules.",
	"admin.user_not_found":       "User not found.",

	"admin.broadcast_enter":   "Enter message to broadcast to all connected users:",
	"admin.broadcast_started": "Broadcast started. You will be notified when it finishes.",
	"admin.broadcast_done":    "Broadcast sent to %d users (%d failed).",
	"admin.broadcast_empty":   "No connected users to send to.",

	"admin.poller_status": "*⚙️ Poller Status*\n\n" +
		"Poll interval: %s\n" +
		"Batch window: %s\n" +
		"Pending notifications: %d\n" +
		"Last poll: %s",
	"admin.poller_never": "never",
}
