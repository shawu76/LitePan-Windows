package strm

import (
	"context"
	"errors"
	"sort"
	"time"

	"litepan/internal/auth"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/settings"
)

func (s *Service) scheduleOnce(ctx context.Context) {
	if s.StartupRemaining() > 0 {
		return
	}
	tasks, err := s.repo.List(ctx)
	if err != nil {
		s.log.Warn("读取 STRM 调度列表失败", "err", err)
		return
	}
	for _, task := range s.queuedTasks(tasks, time.Now()) {
		s.runTaskAsync(task)
	}
}

type queuedRun struct {
	accountID int64
	order     uint64
}

// 已到期的任务保留排队顺序，完成后再次到期只能排到队尾。
func (s *Service) enqueueRunLocked(task *domain.StrmTask) {
	if s.running[task.ID] {
		return
	}
	if s.waitingRuns == nil {
		s.waitingRuns = make(map[int64]queuedRun)
	}
	if _, exists := s.waitingRuns[task.ID]; !exists {
		s.nextRunOrder++
		s.waitingRuns[task.ID] = queuedRun{accountID: task.AccountID, order: s.nextRunOrder}
	}
}

func (s *Service) queuedTasks(tasks []*domain.StrmTask, now time.Time) []*domain.StrmTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	eligible := make(map[int64]*domain.StrmTask)
	for _, task := range tasks {
		if task.Status != domain.StrmStatusActive || s.running[task.ID] {
			continue
		}
		_, pending := s.pendingRun[task.ID]
		if !ShouldAutoSchedule(task) && !pending {
			continue
		}
		if !pending && !IsInTimeWindow(task, now) {
			continue
		}
		_, queued := s.waitingRuns[task.ID]
		if !pending && !queued && !s.dirtyAccounts[task.AccountID] && !task.LastScan.IsZero() && now.Sub(task.LastScan) < time.Duration(s.effectiveScanIntervalMinutes(task))*time.Minute {
			continue
		}
		s.enqueueRunLocked(task)
		eligible[task.ID] = task
	}
	clear(s.dirtyAccounts)
	result := make([]*domain.StrmTask, 0, len(eligible))
	for id := range s.waitingRuns {
		if task, ok := eligible[id]; ok {
			result = append(result, task)
		} else {
			delete(s.waitingRuns, id)
			delete(s.pendingRun, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return s.waitingRuns[result[i].ID].order < s.waitingRuns[result[j].ID].order })
	return result
}

func (s *Service) runTaskAsync(task *domain.StrmTask) {
	if s.StartupRemaining() > 0 {
		return
	}
	if s.isOrganizeBusy(task.AccountID) {
		return
	}
	if s.isRetentionBusy(task.AccountID) {
		return
	}
	releaseFiles, ok := s.TryBeginTaskFileOperation(task.ID)
	if !ok {
		return
	}
	taskConcurrency := s.settings.Int(settings.KeyStrmTaskConcurrency)
	s.mu.Lock()
	if !s.canStartTaskLocked(task, taskConcurrency) {
		s.mu.Unlock()
		releaseFiles()
		return
	}
	s.running[task.ID] = true
	delete(s.waitingRuns, task.ID)
	if task.AccountID > 0 {
		s.runningAccounts[task.AccountID] = struct{}{}
	}
	parent := s.appCtx
	runMode := s.pendingRun[task.ID]
	if runMode == "" {
		runMode = domain.StrmRunModeAuto
	}
	s.mu.Unlock()
	s.log.Info("strm 任务开始执行",
		"task_id", task.ID,
		"task_name", task.Name,
		"account_id", task.AccountID,
		"parent_id", task.ParentID,
		"run_mode", runMode,
	)

	go func() {
		defer releaseFiles()
		defer func() {
			s.mu.Lock()
			s.clearTaskRunState(task.ID, task.AccountID)
			s.mu.Unlock()
		}()
		runCtx, cancel := taskRunContext(parent)
		defer cancel()
		s.mu.Lock()
		s.taskCancels[task.ID] = cancel
		s.mu.Unlock()
		ctx := runCtx
		ctx = driver.WithExtraAPIDelay(ctx, task.ApiInterval)
		reportProgress := s.beginLiveScan(task.ID)
		defer s.endLiveScan(task.ID)

		_ = s.updateScanPersist(task.ID, domain.StrmScanPatch{
			Status:       domain.StrmStatusRunning,
			PausedReason: "",
			ErrorMessage: "",
		})
		token, err := s.ensureToken(ctx)
		if err != nil {
			s.log.Error("STRM 任务令牌准备失败",
				"task_id", task.ID,
				"task_name", task.Name,
				"account_id", task.AccountID,
				"error", err.Error(),
			)
			if auth.IsAuthError(err) {
				if pauseErr := s.PauseTask(ctx, task.ID, domain.PauseReasonAuthFailure, err.Error()); pauseErr != nil {
					s.log.Warn("STRM 任务暂停状态保存失败", "task_id", task.ID, "error", pauseErr)
				}
			} else {
				_ = s.finalizeScanPersist(task.ID, domain.StrmScanPatch{
					Status:         domain.StrmStatusActive,
					ErrorMessage:   err.Error(),
					LastScan:       time.Now(),
					LastScanStatus: "failed",
				})
			}
			return
		}
		result, err := ScanTask(ctx, task, ScanDeps{
			Files:       s.files,
			Branches:    s.branches,
			DirCache:    s.dirCache,
			Playback:    s.playback,
			StrmDir:     s.strmDir,
			BaseURL:     s.scanBaseURL(),
			Token:       token,
			SignEnabled: s.settings.Bool(settings.KeyStrmSignatureEnabled),
			Secret:      s.secret,
			Settings:    s.scanSettings(),
			Log:         s.log,
			OnProgress:  reportProgress,
		}, runMode)
		patch := scanPatchAfterRun(err, result)
		patch.LastScan = time.Now()
		if err != nil && !errors.Is(err, context.Canceled) {
			s.log.Error("STRM 任务执行失败",
				"task_id", task.ID,
				"task_name", task.Name,
				"account_id", task.AccountID,
				"parent_id", task.ParentID,
				"error", err.Error(),
			)
			if auth.IsAuthError(err) {
				if pauseErr := s.PauseTask(ctx, task.ID, domain.PauseReasonAuthFailure, err.Error()); pauseErr != nil {
					s.log.Warn("STRM 任务暂停状态保存失败", "task_id", task.ID, "error", pauseErr)
				}
				return
			}
		} else if errors.Is(err, context.Canceled) {
			s.log.Info("STRM 任务已停止", "task_id", task.ID)
		} else {
			s.log.Info("strm 任务执行完成",
				"task_id", task.ID,
				"task_name", task.Name,
				"scanned", result.ScannedCount,
				"generated", result.GeneratedCount,
				"updated", result.UpdatedCount,
				"removed", result.RemovedCount,
				"failures", len(result.Failures),
			)
		}
		if err := s.finalizeScanPersist(task.ID, patch); err != nil {
			s.log.Warn("更新 STRM 扫描状态失败", "task_id", task.ID, "err", err)
		}
		if err == nil || errors.Is(err, context.Canceled) {
			s.notifyScanFailures(task, result.Failures)
			if err == nil && result.Protected {
				s.notifyScanProtected(task, result.ProtectReason)
			}
		}
	}()
}

func (s *Service) canStartTaskLocked(task *domain.StrmTask, taskConcurrency int) bool {
	if task == nil || s.running[task.ID] || len(s.running) >= taskConcurrency {
		return false
	}
	if current, queued := s.waitingRuns[task.ID]; queued {
		for _, waiting := range s.waitingRuns {
			if waiting.accountID == task.AccountID && waiting.order < current.order {
				return false
			}
		}
	}
	_, accountRunning := s.runningAccounts[task.AccountID]
	return task.AccountID <= 0 || !accountRunning
}

func taskRunContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithCancel(parent)
}

func (s *Service) updateScanPersist(taskID int64, patch domain.StrmScanPatch) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.repo.UpdateScan(ctx, taskID, patch)
}

func (s *Service) finalizeScanPersist(taskID int64, patch domain.StrmScanPatch) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	task, err := s.repo.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status == domain.StrmStatusPaused {
		patch.Status = task.Status
		patch.PausedReason = task.PausedReason
		if patch.ErrorMessage == "" {
			patch.ErrorMessage = task.ErrorMessage
		}
	}
	return s.repo.UpdateScan(ctx, taskID, patch)
}
