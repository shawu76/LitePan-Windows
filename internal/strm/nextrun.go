package strm

import (
	"time"

	"litepan/internal/domain"
	"litepan/pkg/timeutil"
)

// NextRunAt 估算启用任务的下次扫描时间，不早于 now。
// ok=false 表示该任务不参与自动调度：手动调度、未启用或正在执行。
func (s *Service) NextRunAt(task *domain.StrmTask, now time.Time) (time.Time, bool) {
	if task == nil || !ShouldAutoSchedule(task) {
		return time.Time{}, false
	}
	if task.Status != domain.StrmStatusActive {
		return time.Time{}, false
	}

	interval := s.effectiveScanIntervalMinutes(task)

	due := now
	if !task.LastScan.IsZero() {
		due = task.LastScan.Add(time.Duration(interval) * time.Minute)
	}
	candidate := nextWithinTimeWindow(task, due)

	// 不返回已经错过的时间：窗口内已到期视为立即执行，窗口外顺延到下一次开窗。
	if !candidate.After(now) {
		return nextWithinTimeWindow(task, now), true
	}
	return candidate, true
}

// nextWithinTimeWindow 在开了时间窗口且候选时间落在窗口外时，顺延到下一个窗口起点。
func nextWithinTimeWindow(task *domain.StrmTask, candidate time.Time) time.Time {
	if task == nil || !task.TimeWindowEnabled || timeutil.InClockWindow(task.TimeStart, task.TimeEnd, candidate) {
		return candidate
	}
	sh, sm, ok := timeutil.ParseClock(task.TimeStart)
	if !ok {
		return candidate
	}
	next := time.Date(candidate.Year(), candidate.Month(), candidate.Day(), sh, sm, 0, 0, candidate.Location())
	if !next.After(candidate) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
