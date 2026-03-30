package locale

var ru = map[string]string{
	// General
	"error.generic":       "Что-то пошло не так. Попробуйте ещё раз.",
	"error.not_connected": "Вы не подключены к Jira. Используйте /connect.",
	"action.cancelled":    "Действие отменено.",
	"unknown_command":     "Неизвестная команда. Используйте /start для вызова меню.",

	// Start / Help
	"start.welcome": "👋 *Добро пожаловать в SleepJiraBot!*\n\nЯ помогаю работать с Jira Cloud прямо из Telegram.\nВыберите действие:",
	"help.text": "*Доступные команды:*\n\n" +
		"*Аккаунт:*\n" +
		"/connect — Подключить Jira Cloud\n" +
		"/disconnect — Отключить Jira\n" +
		"/me — Показать профиль Jira\n" +
		"/lang — Сменить язык\n\n" +
		"*Задачи:*\n" +
		"/daily — Сгенерировать daily отчёт\n" +
		"/issue `PROJ-123` — Просмотр задачи\n" +
		"/filters — Избранные фильтры\n" +
		"/list `JQL` — Поиск задач по JQL\n" +
		"/comment `PROJ-123 текст` — Добавить комментарий\n" +
		"/transition `PROJ-123` — Сменить статус\n" +
		"/assign `PROJ-123` — Назначить на себя\n\n" +
		"*Уведомления:*\n" +
		"/watch `PROJ` — Подписаться на проект\n" +
		"/unwatch — Удалить все подписки\n" +
		"/subscriptions — Список подписок\n\n" +
		"*Отчёты:*\n" +
		"/sprint `PROJ` — Отчёт по спринту с метриками\n" +
		"/schedule `cron | имя | JQL` — Создать отчёт по расписанию\n" +
		"/unschedule — Удалить все расписания\n" +
		"/schedules — Список расписаний\n\n" +
		"Или нажмите /start для вызова меню с кнопками.",

	// Language
	"lang.choose":  "Выберите язык:",
	"lang.changed": "Язык изменён на *Русский*.",

	// Menu
	"menu.main":    "📌 *Главное меню*\nВыберите действие:",
	"menu.issues":  "📋 *Задачи*\nВыберите действие:",
	"menu.notif":   "🔔 *Уведомления*\nВыберите действие:",
	"menu.reports": "📊 *Отчёты*\nВыберите действие:",
	"menu.profile": "👤 *Профиль*\nВыберите действие:",

	// Menu buttons
	"btn.profile":         "👤 Профиль",
	"btn.my_profile":      "📄 Мой профиль Jira",
	"btn.connect_jira":    "🔗 Подключить Jira",
	"btn.disconnect_jira": "🔌 Отключить Jira",
	"btn.issues":          "📋 Задачи",
	"btn.notifications":   "🔔 Уведомления",
	"btn.reports":         "📊 Отчёты",
	"btn.view_issue":      "🔍 Просмотр задачи",
	"btn.my_issues":       "📄 Мои задачи",
	"btn.search_jql":      "🔎 Поиск (JQL)",
	"btn.comment":         "💬 Комментарий",
	"btn.transition":      "🔄 Сменить статус",
	"btn.assign_to_me":    "✋ Назначить на меня",
	"btn.back":            "◀️ Назад",
	"btn.unwatch_all":     "🚫 Отписаться от всех",
	"btn.subscriptions":   "📋 Подписки",
	"btn.new_schedule":    "➕ Новое расписание",
	"btn.remove_all":      "🗑 Удалить все",
	"btn.schedules":       "📋 Расписания",
	"btn.cancel":          "❌ Отмена",
	"btn.menu":            "◀️ Меню",
	"btn.language":        "🌐 Язык",
	"btn.defaults":        "⚙️ Проект по умолчанию",
	"btn.issue_types":     "🏷 Типы задач",

	// Connect
	"connect.click":         "Нажмите кнопку ниже, чтобы подключить Jira Cloud:",
	"connect.btn":           "🔗 Подключить Jira",
	"connect.already":       "Вы уже подключены к Jira. Сначала используйте /disconnect.",
	"connect.success":       "Подключено к Jira *%s* успешно\\!\n\nИспользуйте /me для проверки профиля или /help для списка команд\\.",
	"connect.choose_site":   "У вас есть доступ к нескольким сайтам Jira. Выберите, к какому подключиться:",
	"connect.site_expired":  "Время выбора сайта истекло. Используйте /connect снова.",
	"disconnect.success":    "Jira отключена. Используйте /connect для подключения нового аккаунта.",
	"disconnect.failed":     "Не удалось отключиться. Попробуйте ещё раз.",
	"disconnect.not_linked": "Вы не подключены к Jira. Используйте /connect.",

	// Profile
	"me.title":  "*Ваш профиль Jira:*\n\nИмя: %s\nEmail: %s\nСайт: %s",
	"me.failed": "Не удалось получить профиль Jira. Попробуйте /connect снова.",

	// Defaults
	"defaults.enter_project": "Введите ключ проекта (напр. `PROJ`).\nОтправьте `-` для сброса.",
	"defaults.choose_board":  "Выберите доску по умолчанию или введите её название:",
	"defaults.saved":         "Проект по умолчанию: *%s*, доска: *%s*.",
	"defaults.cleared":       "Проект и доска по умолчанию сброшены.",
	"defaults.current":       "\n\nПроект по умолчанию: *%s*, доска: *%s*",
	"defaults.boards_failed": "Не удалось загрузить доски. Проект сохранён без доски.",
	"defaults.project_saved": "Проект по умолчанию: *%s* (доска не выбрана).",

	// Issue
	"issue.usage":             "Использование: /issue PROJ-123",
	"issue.invalid_key":       "Неверный формат ключа. Ожидается: PROJ-123",
	"issue.failed":            "Не удалось получить задачу %s. Проверьте ключ и попробуйте снова.",
	"issue.type":              "Тип",
	"issue.status":            "Статус",
	"issue.priority":          "Приоритет",
	"issue.assignee":          "Исполнитель",
	"issue.reporter":          "Автор",
	"issue.due":               "Срок",
	"issue.labels":            "Метки",
	"issue.unassigned":        "Не назначен",
	"issue.enter_key":         "Введите ключ задачи (напр. `PROJ-123`):",
	"issue.invalid_key_short": "Неверный ключ задачи. Ожидается: PROJ-123",

	// List / Search
	"list.no_issues":    "Задачи не найдены.",
	"list.found":        "*Найдено %d задач:*\n\n",
	"list.jql_too_long": "JQL запрос слишком длинный (макс. %d символов).",
	"list.failed":       "Не удалось найти задачи. Проверьте JQL и попробуйте снова.",
	"list.enter_jql":    "Введите JQL запрос:",

	// Comment
	"comment.usage":      "Использование: /comment PROJ-123 Текст комментария",
	"comment.too_long":   "Комментарий слишком длинный (макс. %d символов).",
	"comment.added":      "Комментарий добавлен к %s.",
	"comment.failed":     "Не удалось добавить комментарий к %s.",
	"comment.enter_key":  "Введите ключ задачи (напр. `PROJ-123`):",
	"comment.enter_text": "Введите комментарий для *%s*:",

	// Transition
	"transition.usage":            "Использование: /transition PROJ-123",
	"transition.choose":           "Выберите переход для *%s*:",
	"transition.none":             "Нет доступных переходов для %s.",
	"transition.failed":           "Не удалось получить переходы для %s.",
	"transition.applied":          "Переход для *%s* выполнен!",
	"transition.cb_applied":       "Переход выполнен для %s",
	"transition.cb_failed":        "Не удалось выполнить переход",
	"transition.cb_not_connected": "Не подключён к Jira",
	"transition.cb_invalid":       "Неверные данные перехода",
	"transition.enter_key":        "Введите ключ задачи (напр. `PROJ-123`):",

	// Assign
	"assign.usage":     "Использование: /assign PROJ-123",
	"assign.success":   "Задача %s назначена на вас.",
	"assign.failed":    "Не удалось назначить %s на вас.",
	"assign.me_failed": "Не удалось получить ваш профиль Jira.",
	"assign.enter_key": "Введите ключ задачи (напр. `PROJ-123`):",

	// Subscriptions
	"btn.new_subscription": "➕ Новая подписка",
	"btn.sub_my_new":       "📥 Мои новые задачи",
	"btn.sub_my_mentions":  "💬 Мои упоминания",
	"btn.sub_my_watched":   "👁 Задачи под наблюдением",
	"btn.sub_project":      "📁 Обновления проекта",
	"btn.sub_issue":        "🔔 Обновления задачи",
	"btn.sub_filter":       "🔍 Обновления по фильтру",

	"sub.choose_type":     "*Выберите тип подписки:*",
	"sub.enter_project":   "Введите ключ проекта (напр. `PROJ`):",
	"sub.enter_issue":     "Введите ключ задачи (напр. `PROJ-123`):",
	"sub.choose_filter":   "*Выберите фильтр:*",
	"sub.no_filters":      "У вас нет сохранённых фильтров в Jira.",
	"sub.filters_failed":  "Не удалось получить фильтры Jira.",
	"sub.already_exists":  "Такая подписка уже существует.",
	"sub.failed":          "Не удалось создать подписку.",
	"sub.created":         "Подписка *%s* создана!",
	"sub.created_project": "Подписка на проект *%s* создана!",
	"sub.created_issue":   "Подписка на задачу *%s* создана!",
	"sub.created_filter":  "Подписка на фильтр *%s* создана!",

	"sub.type_my_new_issues":   "Мои новые задачи",
	"sub.type_my_mentions":     "Мои упоминания",
	"sub.type_my_watched":      "Задачи под наблюдением",
	"sub.type_project_updates": "Обновления проекта",
	"sub.type_issue_updates":   "Обновления задачи",
	"sub.type_filter_updates":  "Обновления по фильтру",

	"watch.invalid_project":       "Неверный ключ проекта. Используйте заглавные буквы и цифры, макс. 20 символов.",
	"watch.invalid_project_short": "Неверный ключ проекта. Заглавные буквы/цифры, макс. 20 символов.",
	"unwatch.success":             "Все подписки в этом чате удалены.",
	"unwatch.failed":              "Не удалось удалить подписки.",
	"subs.title":                  "*Активные подписки:*\n\n",
	"subs.none":                   "Нет активных подписок. Используйте меню для создания.",
	"subs.failed":                 "Не удалось получить подписки.",

	// Schedule
	"schedule.usage": "Использование: /schedule `cron | имя | JQL`\n\n" +
		"Примеры:\n" +
		"`0 9 * * 1-5 | Утренний отчёт | assignee=currentUser() AND resolution=Unresolved`\n" +
		"`0 18 * * 5 | Просроченные | duedate < now() AND resolution=Unresolved`\n\n" +
		"Формат cron: `минута час день месяц день_недели`",
	"schedule.invalid_format":  "Неверный формат. Используйте: `cron | имя | JQL`",
	"schedule.fields_required": "Все поля обязательны: cron выражение, имя отчёта и JQL.",
	"schedule.name_too_long":   "Имя отчёта слишком длинное (макс. %d символов).",
	"schedule.invalid_cron":    "Неверное cron выражение: %s",
	"schedule.created":         "Расписание создано!\n\nИмя: *%s*\nCron: `%s`\nJQL: `%s`",
	"schedule.failed":          "Не удалось создать расписание.",
	"schedule.enter":           "Введите расписание в формате:\n`cron | имя | JQL`\n\nПример:\n`0 9 * * 1-5 | Утренний отчёт | assignee=currentUser()`",
	"unschedule.success":       "Все расписания в этом чате удалены.",
	"unschedule.failed":        "Не удалось удалить расписания.",
	"schedules.title":          "*Активные расписания:*\n\n",
	"schedules.none":           "Нет активных расписаний. Используйте /schedule или меню.",
	"schedules.failed":         "Не удалось получить расписания.",

	// Scheduler (background reports)
	"report.not_connected": "Отчёт *%s* пропущен: Jira не подключена. Используйте /connect.",
	"report.failed":        "Ошибка отчёта *%s*: %s",
	"report.default_name":  "Отчёт по расписанию",
	"report.found":         "Найдено: %d задач",
	"report.no_issues":     "_Задачи не найдены._",
	"report.more":          "\n_...и ещё %d_",

	// Webhook notifications
	"notif.event":       "Событие Jira: %s",
	"notif.event_label": "Событие",
	"notif.by":          "Автор",
	"notif.status":      "Статус",
	"notif.assignee":    "Исполнитель",
	"notif.changed":     "Изменено",
	"notif.comment_by":  "Комментарий от",

	// Daily
	"btn.daily":            "📝 Daily",
	"daily.done":           "Сделал",
	"daily.doing":          "Делаю",
	"daily.plan":           "Планирую",
	"daily.no_done":        "— нет",
	"daily.no_doing":       "— нет",
	"daily.failed":         "Не удалось сгенерировать daily отчёт.",
	"daily.enter_user":     "Введите имя пользователя для поиска:",
	"daily.choose_user":    "Найдено несколько пользователей. Выберите:",
	"daily.user_not_found": "Пользователь не найден.",
	"daily.search_failed":  "Не удалось найти пользователей.",
	"btn.daily_user":       "📝 Daily (другой)",
	"btn.daily_jql":        "📝 Daily JQL",
	"daily_jql.title":      "*Настройки Daily JQL*\n\nПользовательские JQL-запросы для каждого блока daily. Оставьте пустым для значений по умолчанию.",
	"daily_jql.current":    "\n\n*Текущие настройки:*\n*Сделал:* `%s`\n*Делаю:* `%s`\n*Планирую:* `%s`",
	"daily_jql.default":    "(по умолчанию)",
	"daily_jql.btn_done":   "Изменить JQL Сделал",
	"daily_jql.btn_doing":  "Изменить JQL Делаю",
	"daily_jql.btn_plan":   "Изменить JQL Планирую",
	"daily_jql.btn_reset":  "Сбросить все",
	"daily_jql.enter_done": "Введите JQL для блока *Сделал* (завершённые задачи).\nОтправьте `-` для сброса.\n\nПо умолчанию: `status changed BY currentUser() AFTER \"yesterday\"`",
	"daily_jql.enter_doing": "Введите JQL для блока *Делаю* (задачи в работе).\nОтправьте `-` для сброса.\n\nПо умолчанию: `assignee=currentUser() AND statusCategory=\"In Progress\"`",
	"daily_jql.enter_plan":  "Введите JQL для блока *Планирую* (запланированные задачи).\nОтправьте `-` для сброса.\n\nПо умолчанию: нет (пустой раздел)",
	"daily_jql.saved":       "JQL для daily сохранён.",
	"daily_jql.reset":       "JQL для daily сброшен к значениям по умолчанию.",
	"daily.no_plan":         "— нет",

	// Sprint
	"btn.sprint":              "🏃 Отчёт по спринту",
	"sprint.enter_project":    "Введите ключ проекта (напр. `PROJ`):",
	"sprint.choose_board":     "Выберите доску или введите её название:",
	"sprint.choose_sprint":    "Выберите спринт или введите его название:",
	"sprint.no_boards":        "Доски не найдены для этого проекта.",
	"sprint.board_not_found":  "Доска \"%s\" не найдена. Используйте /sprint PROJECT, чтобы увидеть доступные доски.",
	"sprint.sprint_not_found": "Спринт \"%s\" не найден. Используйте /sprint PROJECT BOARD, чтобы увидеть доступные спринты.",
	"sprint.no_sprints":       "Спринты не найдены для этой доски.",
	"sprint.no_issues":        "Задачи в этом спринте не найдены.",
	"sprint.boards_failed":    "Не удалось получить доски.",
	"sprint.sprints_failed":   "Не удалось получить спринты.",
	"sprint.report_failed":    "Не удалось сгенерировать отчёт по спринту.",
	"sprint.report_title":     "Отчёт по спринту",
	"sprint.total":            "Всего задач",
	"sprint.done":             "Готово",
	"sprint.in_progress":      "В работе",
	"sprint.hold":             "В ожидании",
	"sprint.todo":             "К выполнению",
	"sprint.by_type":          "По типу задач",
	"sprint.by_assignee":      "По исполнителям",
	"sprint.unassigned":       "Не назначен",
	"sprint.filtered":         "Фильтр: %s",
	"sprint.bug_ratio":        "Соотношение багов",
	"sprint.by_priority":      "По приоритету",
	"sprint.unestimated":      "Без оценки",
	"sprint.overdue":          "Просроченные задачи",
	"sprint.and_more":         "и ещё %d",
	"sprint.velocity":         "Velocity",
	"sprint.velocity_avg":     "сред(%d)",
	"sprint.scope_creep":      "Scope Creep",
	"sprint.carry_over":       "Перенос",
	"sprint.commitment":       "Обязательства",
	"sprint.cycle_time":       "Cycle Time",
	"sprint.cycle_time_avg":   "сред %s (%d задач)",
	"sprint.blocked_time":     "Время блокировки",
	"sprint.blocked_detail":   "всего %s | сред %s (%d задач)",
	"sprint.forecast":         "Прогноз",
	"sprint.forecast_on_track": "по плану",
	"sprint.forecast_at_risk":  "под угрозой",
	"sprint.days_left":         "%d дн. осталось",

	// Настройка типов задач
	"issuetypes.enter_project": "Введите ключ проекта для загрузки типов задач (например `PROJ`):",
	"issuetypes.choose":        "Выберите типы задач для спринт-отчётов.\nВыбранные типы будут использоваться при расчёте метрик.\nНажмите для переключения, затем Сохранить.",
	"issuetypes.saved":         "Типы задач для отчётов сохранены: *%s*.",
	"issuetypes.cleared":       "Фильтр типов задач сброшен. Все типы будут учитываться в отчётах.",
	"issuetypes.current":       "\nТипы задач в отчётах: *%s*",
	"issuetypes.none":          "Все типы",
	"issuetypes.failed":        "Не удалось загрузить типы задач для этого проекта.",
	"issuetypes.save_btn":      "💾 Сохранить",
	"issuetypes.clear_btn":     "🗑 Сбросить фильтр",

	// Фильтры
	"btn.filters":          "📋 Фильтры",
	"filters.choose":       "*Выберите избранный фильтр:*",
	"filters.no_filters":   "У вас нет избранных фильтров в Jira.",
	"filters.failed":       "Не удалось получить избранные фильтры.",
	"filters.not_found":    "Фильтр не найден.",
	"filters.issues_title": "*%s* (%d задач):\n\n",
	"filters.more":         "\n_...и ещё %d_",

	// Настройка поля исполнителя
	"btn.assignee_field":          "👤 Поле исполнителя",
	"assigneefield.choose":        "Выберите поле для определения исполнителя в отчётах.\nПо умолчанию — стандартное поле *Исполнитель*.\nПоля с типом «пользователь»:",
	"assigneefield.saved":         "Поле исполнителя: *%s*.",
	"assigneefield.cleared":       "Поле исполнителя сброшено на стандартное *Исполнитель*.",
	"assigneefield.current":       "\nПоле исполнителя: *%s*",
	"assigneefield.default":       "Исполнитель (стандартное)",
	"assigneefield.failed":        "Не удалось загрузить поля из Jira.",
	"assigneefield.no_fields":     "Не найдено кастомных полей с типом «пользователь».",
	"assigneefield.reset_btn":     "🔄 Сбросить",
}
