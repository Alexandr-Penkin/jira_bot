package notifydedup_test

import "SleepJiraBot/internal/notifydedup"

// Compile-time assertions that both impls satisfy Allower.
var (
	_ notifydedup.Allower = (*notifydedup.Guard)(nil)
	_ notifydedup.Allower = (*notifydedup.RedisGuard)(nil)
)
