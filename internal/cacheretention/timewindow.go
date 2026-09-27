package cacheretention

import (
	"time"

	"litepan/internal/domain"
	"litepan/pkg/timeutil"
)

func IsInTimeWindow(task *domain.CacheRetentionTask, now time.Time) bool {
	if task == nil || !task.TimeWindowEnabled {
		return true
	}
	return timeutil.InClockWindow(task.TimeStart, task.TimeEnd, now)
}
