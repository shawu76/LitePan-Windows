package offlinedownload

import (
	"context"
	"sort"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

const nativePollMinInterval = 30 * time.Second
const nativePollMaxInterval = 5 * time.Minute

type nativePollState struct {
	next        time.Time
	interval    time.Duration
	lastAttempt time.Time
	refreshing  bool
	failed      bool
}

func isActiveNativeTask(task *Task) bool {
	return task.ProviderKind != ProviderBuiltin && !isTerminal(task.Status) && (task.ProviderTaskID != "" || task.InfoHash != "")
}

// 页面刷新和后台检查共用账号级节流，避免重复请求网盘。
func (s *Service) Refresh(ctx context.Context, accountID int64, force bool) error {
	s.mu.Lock()
	accounts := make(map[int64]struct{})
	for _, task := range s.tasks {
		if isActiveNativeTask(task) && (accountID == 0 || task.AccountID == accountID) {
			accounts[task.AccountID] = struct{}{}
		}
	}
	s.mu.Unlock()
	ids := make([]int64, 0, len(accounts))
	for id := range accounts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var firstErr error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.refreshNativeAccount(ctx, id, force); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) refreshNativeAccount(ctx context.Context, accountID int64, force bool) error {
	s.mu.Lock()
	state := s.nativePolls[accountID]
	if state == nil {
		state = &nativePollState{}
		s.nativePolls[accountID] = state
	}
	now := time.Now()
	if state.refreshing || (now.Before(state.next) && (!force || state.failed)) || now.Sub(state.lastAttempt) < 3*time.Second {
		s.mu.Unlock()
		return nil
	}
	refs := make([]driver.OfflineTaskRef, 0)
	seen := make(map[string]bool)
	for _, task := range s.tasks {
		if task.AccountID == accountID && isActiveNativeTask(task) && !seen[task.refKey()] {
			seen[task.refKey()] = true
			refs = append(refs, driver.OfflineTaskRef{ProviderTaskID: task.ProviderTaskID, InfoHash: task.InfoHash})
		}
	}
	if len(refs) == 0 {
		delete(s.nativePolls, accountID)
		s.mu.Unlock()
		return nil
	}
	state.refreshing = true
	state.lastAttempt = now
	s.mu.Unlock()
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var updates []driver.OfflineTaskUpdate
	err := s.exec.Run(queryCtx, accountID, func(drv driver.Driver) error {
		refresher, err := driverexec.Require[driver.OfflineTaskRefresher](drv)
		if err != nil {
			return err
		}
		updates, err = refresher.RefreshOfflineTasks(queryCtx, refs)
		return err
	})
	progressed := false
	if err == nil {
		progressed = s.applyUpdates(accountID, updates)
	}
	s.mu.Lock()
	state.refreshing = false
	state.failed = err != nil
	state.interval = nativePollDelay(state.interval, progressed, err)
	state.next = time.Now().Add(state.interval)
	s.mu.Unlock()
	s.wakeNativePoll()
	return err
}

func nativePollDelay(previous time.Duration, progressed bool, err error) time.Duration {
	delay := nativePollMinInterval
	if !progressed && previous > 0 {
		delay = min(previous*2, nativePollMaxInterval)
	}
	if err != nil {
		delay = max(delay, time.Minute)
		if appErr, ok := domain.AsAppError(err); ok {
			switch appErr.Code {
			case domain.CodeRateLimited:
				delay = max(delay, nativePollMaxInterval)
			case domain.CodeAuthExpired, domain.CodePermissionDenied, domain.CodeNotImplement:
				delay = 15 * time.Minute
			}
		}
	}
	return delay
}

func (s *Service) wakeNativePoll() {
	select {
	case s.nativeWake <- struct{}{}:
	default:
	}
}

func (s *Service) runNativePoll(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.Refresh(ctx, 0, false); err != nil && ctx.Err() == nil {
			s.log.Warn("原生离线下载状态检查失败", "err", err)
		}
		s.mu.Lock()
		active := make(map[int64]bool)
		for _, task := range s.tasks {
			if isActiveNativeTask(task) {
				active[task.AccountID] = true
			}
		}
		var next time.Time
		for id, state := range s.nativePolls {
			if !active[id] && !state.refreshing {
				delete(s.nativePolls, id)
				continue
			}
			if active[id] && !state.refreshing && (next.IsZero() || state.next.Before(next)) {
				next = state.next
			}
		}
		s.mu.Unlock()
		if next.IsZero() {
			select {
			case <-ctx.Done():
				return
			case <-s.nativeWake:
			}
			continue
		}
		timer := time.NewTimer(max(time.Until(next), 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.nativeWake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
