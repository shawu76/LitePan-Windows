package strm

import (
	"context"
	"errors"
	"io"
	"litepan/internal/domain"
	"litepan/internal/settings"
	"litepan/internal/store"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPauseRunningTaskPersistsBeforeCancel(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "运行中", true: "请求已取消"}[alreadyCanceled], func(t *testing.T) {
			s, db := testService(t)
			ctx := context.Background()
			aid, err := db.Accounts.Create(ctx, &domain.Account{Name: "模拟账号", DriverType: "mock", Config: "{}", IsActive: true})
			if err != nil {
				t.Fatal(err)
			}
			id, err := db.StrmTasks.Create(ctx, &domain.StrmTask{Name: "模拟任务", AccountID: aid, Status: domain.StrmStatusRunning})
			if err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			s.running[id] = true
			s.runningAccounts[aid] = struct{}{}
			s.taskCancels[id] = func() {
				st, err := db.StrmTasks.Get(ctx, id)
				if err != nil || st.Status != domain.StrmStatusPaused {
					t.Errorf("取消执行前应已保存暂停状态：%+v，%v", st, err)
				}
				cancel()
			}
			if err := s.PauseTask(runCtx, id, domain.PauseReasonAuthFailure, "认证失效"); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(runCtx.Err(), context.Canceled) {
				t.Fatal("未取消执行")
			}
			// 模拟取消后的收尾，不能把暂停状态覆盖回正常。
			if err := s.finalizeScanPersist(id, scanPatchAfterRun(context.Canceled, ScanResult{})); err != nil {
				t.Fatal(err)
			}
			st, err := db.StrmTasks.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if st.Status != domain.StrmStatusPaused || st.PausedReason != string(domain.PauseReasonAuthFailure) || st.ErrorMessage != "认证失效" {
				t.Fatalf("暂停状态不正确：%+v", st)
			}
			if s.running[id] {
				t.Fatal("运行标记未清理")
			}
		})
	}
}

func TestQueuedTasksSixTasksDoNotStarve(t *testing.T) {
	s, _ := testService(t)
	now := time.Now()
	tasks := make([]*domain.StrmTask, 6)
	for i := range tasks {
		tasks[i] = &domain.StrmTask{ID: int64(i + 1), AccountID: 7, Status: domain.StrmStatusActive}
	}
	for want := int64(1); want <= 6; want++ {
		ready := s.queuedTasks(tasks, now)
		if len(ready) == 0 || ready[0].ID != want {
			t.Fatalf("等待任务应为 %d，实际 %v", want, ready)
		}
		s.mu.Lock()
		if !s.canStartTaskLocked(ready[0], 3) {
			t.Fatal("队首无法启动")
		}
		if len(ready) > 1 && s.canStartTaskLocked(ready[1], 3) {
			t.Fatal("后来的任务不能插队")
		}
		delete(s.waitingRuns, want)
		s.running[want] = true
		s.runningAccounts[7] = struct{}{}
		if len(ready) > 1 && s.canStartTaskLocked(ready[1], 3) {
			t.Fatal("同账号不能并行")
		}
		s.clearTaskRunState(want, 7)
		s.mu.Unlock()
		// 保持已完成任务仍然到期，模拟短间隔，不能抢占剩余任务。
	}
	if got := s.queuedTasks(tasks, now)[0].ID; got != 1 {
		t.Fatalf("第二轮队首=%d", got)
	}
}

func TestQueuedManualRunIgnoresIntervalAndWindow(t *testing.T) {
	s, _ := testService(t)
	task := &domain.StrmTask{ID: 1, AccountID: 7, Status: domain.StrmStatusActive, ScheduleMode: domain.StrmScheduleManual, LastScan: time.Now()}
	s.pendingRun[1] = domain.StrmRunModeFull
	s.enqueueRunLocked(task)
	for i := 0; i < 3; i++ {
		if got := s.queuedTasks([]*domain.StrmTask{task}, time.Now()); len(got) != 1 {
			t.Fatal("手动排队不应受扫描间隔限制")
		}
	}
	if len(s.waitingRuns) != 1 {
		t.Fatal("重复调度不应重复入队")
	}
	task.Status = domain.StrmStatusPaused
	if len(s.queuedTasks([]*domain.StrmTask{task}, time.Now())) != 0 || len(s.pendingRun) != 0 {
		t.Fatal("暂停应清除等待请求")
	}
}

func TestQueuedTasksWindowAndOtherAccounts(t *testing.T) {
	s, _ := testService(t)
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.Local)
	a := &domain.StrmTask{ID: 1, AccountID: 7, Status: domain.StrmStatusActive, TimeWindowEnabled: true, TimeStart: "11:00", TimeEnd: "12:00"}
	b := &domain.StrmTask{ID: 2, AccountID: 8, Status: domain.StrmStatusActive}
	s.queuedTasks([]*domain.StrmTask{a, b}, now)
	s.runningAccounts[7] = struct{}{}
	if !s.canStartTaskLocked(b, 3) {
		t.Fatal("其他账号不应被阻塞")
	}
	ready := s.queuedTasks([]*domain.StrmTask{a, b}, now.Add(time.Hour))
	if len(ready) != 1 || ready[0].ID != 2 {
		t.Fatal("自动任务不能在窗口外启动")
	}
	s.queuedTasks(nil, now)
	if len(s.waitingRuns) != 0 {
		t.Fatal("删除任务应清理排队状态")
	}
}

func testService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	settingsSvc, err := settings.New(ctx, st.Configs)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceOptions{
		Repo:     st.StrmTasks,
		Settings: settingsSvc,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return svc, st
}

type reciprocalRetentionBusy struct {
	other RunningAccountLister
}

func (r reciprocalRetentionBusy) GetRunningAccountIDs() []int64 {
	if r.other != nil {
		_ = r.other.GetRunningAccountIDs()
	}
	return []int64{7}
}

func TestRunTaskAsyncCrossBusyCheckNoDeadlock(t *testing.T) {
	svc, _ := testService(t)
	svc.SetRetentionBusyChecker(reciprocalRetentionBusy{other: svc})

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		svc.mu.Lock()
		time.Sleep(200 * time.Millisecond)
		svc.mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		task := &domain.StrmTask{ID: 1, AccountID: 7, LastScan: time.Now().Add(-2 * time.Hour)}
		svc.runTaskAsync(task)
	}()
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runTaskAsync cross busy check deadlocked")
	}
}

func TestStartupRemainingIncludesPostAuthDelayWhileGateBlocked(t *testing.T) {
	svc, _ := testService(t)
	svc.startupPending = true
	if got := svc.StartupRemaining(); got != int(strmStartupDelay.Seconds()) {
		t.Fatalf("startup remaining=%d want=%d", got, int(strmStartupDelay.Seconds()))
	}
}

func TestTaskRunContextHasNoFixedDeadline(t *testing.T) {
	ctx, cancel := taskRunContext(context.Background())
	if _, ok := ctx.Deadline(); ok {
		cancel()
		t.Fatal("STRM 任务不应有固定执行期限")
	}

	cancel()
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("取消任务后错误 = %v，期望 context.Canceled", ctx.Err())
	}
}

func TestTaskStartLimitMatchesLegacyScheduler(t *testing.T) {
	svc, _ := testService(t)

	svc.mu.Lock()
	svc.running[1] = true
	svc.runningAccounts[7] = struct{}{}
	if svc.canStartTaskLocked(&domain.StrmTask{ID: 2, AccountID: 7}, 3) {
		t.Fatal("同一账号的 STRM 任务应串行")
	}
	if !svc.canStartTaskLocked(&domain.StrmTask{ID: 2, AccountID: 8}, 3) {
		t.Fatal("不同账号且未达到全局上限时应允许并发")
	}
	svc.running[2] = true
	svc.running[3] = true
	if svc.canStartTaskLocked(&domain.StrmTask{ID: 4, AccountID: 9}, 3) {
		t.Fatal("达到全局任务并发上限后应等待")
	}
	svc.mu.Unlock()
}

func TestQueuedTasksUsesGlobalIntervalForLegacyTasks(t *testing.T) {
	svc, _ := testService(t)
	if err := svc.settings.Update(context.Background(), map[string]string{
		settings.KeyStrmDefaultScanInterval: "360",
	}); err != nil {
		t.Fatal(err)
	}
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		Status:       domain.StrmStatusActive,
		ScanInterval: 10, // 历史任务里固化过的旧值
		LastScan:     time.Now().Add(-20 * time.Minute),
	}
	if len(svc.queuedTasks([]*domain.StrmTask{task}, time.Now())) != 0 {
		t.Fatal("全局扫描间隔应优先于历史任务级间隔，20 分钟后不应触发")
	}
}

func TestQueuedTasksFallsBackToLegacyTaskIntervalWhenGlobalMissing(t *testing.T) {
	svc, _ := testService(t)
	svc.settings = nil
	task := &domain.StrmTask{
		ID:           1,
		AccountID:    1,
		Status:       domain.StrmStatusActive,
		ScanInterval: 10,
		LastScan:     time.Now().Add(-20 * time.Minute),
	}
	if len(svc.queuedTasks([]*domain.StrmTask{task}, time.Now())) != 1 {
		t.Fatal("全局配置缺失时应回退历史任务级间隔，避免升级后停调度")
	}
}

func TestNextRunAt(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, loc)
	svc := &Service{}

	base := func() *domain.StrmTask {
		return &domain.StrmTask{
			ID:           1,
			Name:         "电影 / 115 主库",
			Status:       domain.StrmStatusActive,
			ScheduleMode: domain.StrmScheduleWindow,
			ScanInterval: 360,
		}
	}

	t.Run("手动调度不参与", func(t *testing.T) {
		task := base()
		task.ScheduleMode = domain.StrmScheduleManual
		if _, ok := svc.NextRunAt(task, now); ok {
			t.Fatalf("manual 任务不应参与自动调度")
		}
	})

	t.Run("未启用不参与", func(t *testing.T) {
		task := base()
		task.Status = domain.StrmStatusPaused
		if _, ok := svc.NextRunAt(task, now); ok {
			t.Fatalf("未启用任务不应参与自动调度")
		}
	})

	t.Run("执行中的任务不排入下一轮", func(t *testing.T) {
		task := base()
		task.Status = domain.StrmStatusRunning
		if _, ok := svc.NextRunAt(task, now); ok {
			t.Fatalf("执行中的任务不应出现在下一轮计划里")
		}
	})

	t.Run("从未扫描视为立即到期", func(t *testing.T) {
		next, ok := svc.NextRunAt(base(), now)
		if !ok {
			t.Fatalf("应参与调度")
		}
		if !next.Equal(now) {
			t.Fatalf("期望 %v，实际 %v", now, next)
		}
	})

	t.Run("到期时间等于上次扫描加间隔", func(t *testing.T) {
		task := base()
		task.LastScan = time.Date(2026, 9, 11, 10, 30, 0, 0, loc)
		next, _ := svc.NextRunAt(task, now)
		want := time.Date(2026, 9, 11, 16, 30, 0, 0, loc)
		if !next.Equal(want) {
			t.Fatalf("期望 %v，实际 %v", want, next)
		}
	})

	t.Run("已到期且在窗口内时返回当前时间（不返回过去时间）", func(t *testing.T) {
		task := base()
		task.LastScan = time.Date(2026, 9, 11, 2, 0, 0, 0, loc) // 10 小时前，间隔 6 小时，已到期
		next, _ := svc.NextRunAt(task, now)
		if !next.Equal(now) {
			t.Fatalf("期望 %v，实际 %v", now, next)
		}
	})

	t.Run("错过时间窗口后顺延到下一个窗口起点（不返回过去时间）", func(t *testing.T) {
		task := base()
		task.TimeWindowEnabled = true
		task.TimeStart = "20:00"
		task.TimeEnd = "23:00"
		task.LastScan = time.Date(2026, 9, 10, 10, 0, 0, 0, loc) // 到期于昨天 16:00，早已错过
		next, _ := svc.NextRunAt(task, now)
		want := time.Date(2026, 9, 11, 20, 0, 0, 0, loc)
		if !next.Equal(want) {
			t.Fatalf("期望 %v，实际 %v", want, next)
		}
		if next.Before(now) {
			t.Fatalf("不应返回过去时间")
		}
	})

	t.Run("任务级间隔回退", func(t *testing.T) {
		task := base()
		task.ScanInterval = 60
		task.LastScan = time.Date(2026, 9, 11, 11, 30, 0, 0, loc)
		next, _ := svc.NextRunAt(task, now)
		want := time.Date(2026, 9, 11, 12, 30, 0, 0, loc)
		if !next.Equal(want) {
			t.Fatalf("期望 %v，实际 %v", want, next)
		}
	})

	t.Run("窗口外顺延到下一个窗口起点", func(t *testing.T) {
		task := base()
		task.TimeWindowEnabled = true
		task.TimeStart = "20:00"
		task.TimeEnd = "23:00"
		task.LastScan = time.Date(2026, 9, 11, 18, 0, 0, 0, loc) // 到期 24:00，落在窗口外
		next, _ := svc.NextRunAt(task, now)
		want := time.Date(2026, 9, 12, 20, 0, 0, 0, loc)
		if !next.Equal(want) {
			t.Fatalf("期望 %v，实际 %v", want, next)
		}
	})

	t.Run("跨午夜窗口顺延到当天起点", func(t *testing.T) {
		task := base()
		task.TimeWindowEnabled = true
		task.TimeStart = "22:00"
		task.TimeEnd = "06:00"
		task.LastScan = time.Date(2026, 9, 11, 2, 0, 0, 0, loc) // 到期 08:00，落在窗口外
		next, _ := svc.NextRunAt(task, now)
		want := time.Date(2026, 9, 11, 22, 0, 0, 0, loc)
		if !next.Equal(want) {
			t.Fatalf("期望 %v，实际 %v", want, next)
		}
	})

	t.Run("窗口内保持原时间", func(t *testing.T) {
		task := base()
		task.TimeWindowEnabled = true
		task.TimeStart = "00:00"
		task.TimeEnd = "23:59"
		task.LastScan = time.Date(2026, 9, 11, 16, 0, 0, 0, loc) // 到期 22:00，在窗口内
		next, _ := svc.NextRunAt(task, now)
		want := time.Date(2026, 9, 11, 22, 0, 0, 0, loc)
		if !next.Equal(want) {
			t.Fatalf("期望 %v，实际 %v", want, next)
		}
	})
}

func TestUpdateBranchRetentionPreservesPathAndRefreshesExpiry(t *testing.T) {
	svc, st := testService(t)
	svc.branches = st.StrmBranches
	ctx := t.Context()

	accountID, err := st.Accounts.Create(ctx, &domain.Account{
		Name:       "测试账号",
		DriverType: "localfs",
		IsActive:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := st.StrmTasks.Create(ctx, &domain.StrmTask{
		Name:      "电视剧",
		AccountID: accountID,
		ParentID:  "library",
		Path:      "/云影音/电视剧",
		Status:    domain.StrmStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := svc.CreateBranch(ctx, &domain.StrmBranch{
		TaskID:        taskID,
		ParentID:      "one-piece",
		Path:          "/云影音/电视剧/海贼王",
		Recursive:     true,
		RetentionDays: 30,
		BranchType:    domain.StrmBranchTypeTemporary,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ExpiresAt.IsZero() {
		t.Fatal("监控分支创建后应按保留天数设置过期时间")
	}

	days := 90
	updated, err := svc.UpdateBranch(ctx, taskID, branch.ID, BranchPatch{RetentionDays: &days}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ParentID != branch.ParentID || updated.Path != branch.Path || updated.RelativePath != branch.RelativePath {
		t.Fatalf("仅修改保留天数不应改变目录信息，更新前=%+v，更新后=%+v", branch, updated)
	}
	if !updated.Recursive || updated.BranchType != domain.StrmBranchTypeTemporary || updated.Source != "manual" {
		t.Fatalf("仅修改保留天数不应改变其他分支属性: %+v", updated)
	}
	if updated.RetentionDays != days {
		t.Fatalf("保留天数=%d，期望=%d", updated.RetentionDays, days)
	}
	expectedExpiry := branch.CreatedAt.Add(90 * 24 * time.Hour)
	if !updated.ExpiresAt.Equal(expectedExpiry) {
		t.Fatalf("过期时间=%v，期望按创建时间计算为 %v", updated.ExpiresAt, expectedExpiry)
	}

	permanent := 0
	updated, err = svc.UpdateBranch(ctx, taskID, branch.ID, BranchPatch{RetentionDays: &permanent}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ExpiresAt.IsZero() {
		t.Fatalf("永久保留应清空过期时间，实际=%v", updated.ExpiresAt)
	}
	if updated.Path != branch.Path {
		t.Fatalf("改为永久保留后路径被改变为 %q", updated.Path)
	}
}

func TestNormalizeTemporaryBranchExpiryUsesCreatedAt(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 8, 0, 0, 0, time.Local)
	branch := &domain.StrmBranch{
		RetentionDays: 90,
		CreatedAt:     createdAt,
	}

	if err := normalizeTemporaryBranchExpiry(branch, true); err != nil {
		t.Fatal(err)
	}
	expected := createdAt.Add(90 * 24 * time.Hour)
	if !branch.ExpiresAt.Equal(expected) {
		t.Fatalf("过期时间=%v，期望=%v", branch.ExpiresAt, expected)
	}
}

func createOutputMoveTask(t *testing.T, svc *Service, st *store.Store, groupDir string) *domain.StrmTask {
	t.Helper()
	ctx := context.Background()
	accountID, err := st.Accounts.Create(ctx, &domain.Account{
		Name:       "测试账号",
		DriverType: "LocalFs",
		Config:     "{}",
		IsActive:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateTask(ctx, &domain.StrmTask{
		Name:         "夸克电影",
		AccountID:    accountID,
		ParentID:     "0",
		Path:         "/电影",
		Recursive:    true,
		ScanMode:     domain.StrmScanModeIncrementalUpdate,
		OutputFolder: "夸克电影",
		GroupDir:     groupDir,
		ScheduleMode: domain.StrmScheduleWindow,
		Status:       domain.StrmStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestUpdateTaskMovesOutputDirectoryAndCleansEmptyParents(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "测试/电影")

	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	oldSeasonDir := filepath.Join(oldDir, "Season 1")
	if err := os.MkdirAll(oldSeasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldSeasonDir, "EP01.strm")
	if err := os.WriteFile(oldFile, []byte("https://example.test/1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, metadataPath := range []string{
		filepath.Join(filepath.Dir(oldDir), ".DS_Store"),
		filepath.Join(filepath.Dir(oldDir), "desktop.ini"),
		filepath.Join(filepath.Dir(filepath.Dir(oldDir)), ".directory"),
	} {
		if err := os.WriteFile(metadataPath, []byte("system metadata"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	input := *task
	input.GroupDir = ""
	updated, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.GroupDir != "" {
		t.Fatalf("分组目录 = %q，期望为空", updated.GroupDir)
	}

	newFile := filepath.Join(svc.strmDir, task.OutputFolder, "Season 1", "EP01.strm")
	content, err := os.ReadFile(newFile)
	if err != nil {
		t.Fatalf("新路径未保留 STRM 文件：%v", err)
	}
	if string(content) != "https://example.test/1\n" {
		t.Fatalf("移动后文件内容 = %q", string(content))
	}
	if _, err := os.Stat(filepath.Join(svc.strmDir, "测试")); !os.IsNotExist(err) {
		t.Fatalf("旧空分组目录应被清理，得到 err=%v", err)
	}
	if _, err := os.Stat(svc.strmDir); err != nil {
		t.Fatalf("STRM 根目录不应被删除：%v", err)
	}
}

func TestRemovableSystemMetadataNames(t *testing.T) {
	dir := t.TempDir()
	removable := []string{
		".DS_Store",
		"._poster.jpg",
		".localized",
		".LSOverride",
		".VolumeIcon.icns",
		"Icon\r",
		"Thumbs.db",
		"ehthumbs.db",
		"ehthumbs_vista.db",
		"desktop.ini",
		".directory",
		".hidden",
		".xdg-volume-info",
	}
	for _, name := range removable {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("metadata"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !isRemovableSystemMetadata(entry) {
			t.Errorf("系统元数据 %q 应可忽略", entry.Name())
		}
	}

	for _, name := range []string{".keep", ".env", ".nfs123", "用户文件.txt"} {
		t.Run(name, func(t *testing.T) {
			testDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(testDir, name), []byte("user data"), 0o644); err != nil {
				t.Fatal(err)
			}
			userEntries, err := os.ReadDir(testDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(userEntries) != 1 || isRemovableSystemMetadata(userEntries[0]) {
				t.Fatalf("用户文件 %q 不应被当作系统元数据", name)
			}
		})
	}
}

func TestUpdateTaskKeepsNonEmptyOldParent(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影/国产")
	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepFile := filepath.Join(svc.strmDir, "电影", "其他任务", "keep.strm")
	if err := os.MkdirAll(filepath.Dir(keepFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keepFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := *task
	input.GroupDir = ""
	if _, err := svc.UpdateTask(context.Background(), task.ID, &input); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keepFile); err != nil {
		t.Fatalf("清理旧空目录时不应影响其他任务：%v", err)
	}
	if _, err := os.Stat(filepath.Join(svc.strmDir, "电影", "国产")); !os.IsNotExist(err) {
		t.Fatalf("已变空的旧子目录应被清理，得到 err=%v", err)
	}
}

func TestUpdateTaskChangesPathWhenOldOutputDoesNotExist(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")

	input := *task
	input.GroupDir = ""
	updated, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.GroupDir != "" {
		t.Fatalf("旧输出目录不存在时仍应保存新路径，得到 %q", updated.GroupDir)
	}
	if _, err := os.Stat(TaskOutputDir(svc.strmDir, task.OutputFolder)); !os.IsNotExist(err) {
		t.Fatalf("保存配置不应预先创建空的任务目录，得到 err=%v", err)
	}
}

func TestUpdateTaskRejectsWhileTaskFileOperationIsBusy(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")
	release, ok := svc.TryBeginTaskFileOperation(task.ID)
	if !ok {
		t.Fatal("测试文件锁加锁失败")
	}
	defer release()

	input := *task
	input.GroupDir = ""
	_, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err == nil || !strings.Contains(err.Error(), "当前任务正在进行，请停止后再修改设置") {
		t.Fatalf("任务忙碌时应立即拒绝修改，得到 err=%v", err)
	}
	stored, getErr := svc.GetTask(context.Background(), task.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.GroupDir != "电影" {
		t.Fatalf("被拒绝后不应改变分组目录，得到 %q", stored.GroupDir)
	}
}

func TestUpdateTaskRejectsExistingDestination(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")
	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	newDir := TaskOutputDir(svc.strmDir, task.OutputFolder)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}

	input := *task
	input.GroupDir = ""
	_, err := svc.UpdateTask(context.Background(), task.ID, &input)
	if err == nil || !strings.Contains(err.Error(), "新的 STRM 输出目录已存在") {
		t.Fatalf("目标目录已存在时应拒绝合并，得到 err=%v", err)
	}
	stored, getErr := svc.GetTask(context.Background(), task.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.GroupDir != "电影" {
		t.Fatalf("目录冲突时不应保存新配置，得到 %q", stored.GroupDir)
	}
}

type failingStrmTaskUpdateRepo struct {
	domain.StrmTaskRepository
}

func (r failingStrmTaskUpdateRepo) Update(context.Context, *domain.StrmTask) error {
	return errors.New("模拟数据库保存失败")
}

func TestUpdateTaskRollsDirectoryBackWhenDatabaseUpdateFails(t *testing.T) {
	svc, st := testService(t)
	svc.strmDir = t.TempDir()
	task := createOutputMoveTask(t, svc, st, "电影")
	oldDir := TaskOutputDir(svc.strmDir, TaskRelDir(task.GroupDir, task.OutputFolder))
	oldFile := filepath.Join(oldDir, "movie.strm")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.repo = failingStrmTaskUpdateRepo{StrmTaskRepository: st.StrmTasks}

	input := *task
	input.GroupDir = ""
	if _, err := svc.UpdateTask(context.Background(), task.ID, &input); err == nil {
		t.Fatal("期望返回数据库保存错误")
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("数据库保存失败后应回移原目录：%v", err)
	}
	newDir := TaskOutputDir(svc.strmDir, task.OutputFolder)
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Fatalf("数据库保存失败后不应留下新目录，得到 err=%v", err)
	}
}

func TestTaskFileOperationLock(t *testing.T) {
	svc := NewService(ServiceOptions{})
	release, ok := svc.TryBeginTaskFileOperation(7)
	if !ok || !svc.IsTaskFileOperationBusy(7) {
		t.Fatal("首次加锁应成功并显示任务忙碌")
	}
	if _, ok := svc.TryBeginTaskFileOperation(7); ok {
		t.Fatal("同一任务不应同时执行扫描、当前目录生成或刮削")
	}
	if _, ok := svc.TryBeginTaskFileOperation(8); !ok {
		t.Fatal("不同任务可以独立加锁")
	}

	release()
	release()
	if svc.IsTaskFileOperationBusy(7) {
		t.Fatal("释放后任务不应继续显示忙碌")
	}
	if releaseAgain, ok := svc.TryBeginTaskFileOperation(7); !ok {
		t.Fatal("释放后应允许再次加锁")
	} else {
		releaseAgain()
	}
}
