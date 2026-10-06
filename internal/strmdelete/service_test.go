package strmdelete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/strm"
)

type watchTestTaskRepo struct {
	domain.StrmTaskRepository
	get   func(context.Context, int64) (*domain.StrmTask, error)
	items []*domain.StrmTask
}

func (r *watchTestTaskRepo) List(context.Context) ([]*domain.StrmTask, error) { return r.items, nil }
func (r *watchTestTaskRepo) Get(ctx context.Context, id int64) (*domain.StrmTask, error) {
	return r.get(ctx, id)
}

type watchTestConfigRepo struct {
	domain.ConfigRepository
	set func(context.Context, string, string) error
}

func (r *watchTestConfigRepo) Set(ctx context.Context, key, val string) error {
	if r.set != nil {
		return r.set(ctx, key, val)
	}
	return nil
}

func startTestWatchLoop(t *testing.T, s *Service, parent context.Context) (chan fsnotify.Event, chan error, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	events, errs := make(chan fsnotify.Event), make(chan error)
	done, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		done <- s.watchEvents(ctx, events, errs)
	}()
	t.Cleanup(func() { cancel(); <-finished })
	select {
	case events <- fsnotify.Event{}:
	case <-time.After(time.Second):
		t.Fatal("事件监听未启动")
	}
	return events, errs, done
}

func TestWatcherErrorClearsBatchesAndRequiresExplicitResume(t *testing.T) {
	bus := eventbus.New(nil)
	defer func() {
		if err := bus.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	notices := make(chan eventbus.NotificationCreated, 2)
	eventbus.Subscribe(bus, func(_ context.Context, e eventbus.NotificationCreated) { notices <- e })
	s := New(Options{Tasks: &watchTestTaskRepo{}, Configs: &watchTestConfigRepo{}, Bus: bus})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, errs, done := startTestWatchLoop(t, s, ctx)
	cfg := Config{Enabled: true, Items: []TaskConfig{{TaskID: 1, Threshold: 10, Strategy: StrategyBlock, DelayMinutes: 10}}}
	s.mu.Lock()
	s.config = cfg
	s.mu.Unlock()
	s.queue(ctx, candidate{taskID: 1, path: filepath.Join(t.TempDir(), "old.strm")})
	s.mu.Lock()
	timer := s.batches[1].timer
	s.mu.Unlock()
	select {
	case errs <- fsnotify.ErrEventOverflow:
	case <-time.After(time.Second):
		t.Fatal("未接收监听错误")
	}
	if err := <-done; !errors.Is(err, fsnotify.ErrEventOverflow) {
		t.Fatalf("没有返回监听溢出错误: %v", err)
	}
	select {
	case e := <-notices:
		if e.Title != "STRM 删除监控已暂停" {
			t.Fatalf("异常通知不正确: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("没有发送暂停通知")
	}
	s.mu.Lock()
	if !s.paused || s.watcher != nil || len(s.batches) != 0 || len(s.watched) != 0 {
		t.Error("异常后仍保留监听或删除批次")
	}
	s.mu.Unlock()
	if timer.Stop() {
		t.Error("异常后没有停止原有定时器")
	}
	s.queue(ctx, candidate{taskID: 1, path: "new.strm"})
	s.mu.Lock()
	if len(s.batches) != 0 {
		t.Error("暂停期间仍接受删除候选")
	}
	s.mu.Unlock()
	if _, err := s.UpdateConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.resume:
	case <-time.After(time.Second):
		t.Fatal("保存设置未唤醒暂停的监听")
	}
	_, _, _ = startTestWatchLoop(t, s, ctx)
	s.mu.Lock()
	if s.paused || len(s.batches) != 0 {
		t.Error("恢复后重放了旧批次或仍处于暂停状态")
	}
	s.mu.Unlock()
}

func TestWatcherErrorCancelsAlreadyStartedBatch(t *testing.T) {
	entered := make(chan context.Context, 1)
	stopped := make(chan struct{})
	repo := &watchTestTaskRepo{get: func(ctx context.Context, _ int64) (*domain.StrmTask, error) {
		entered <- ctx
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}}
	s := New(Options{Tasks: repo, Strm: strm.NewService(strm.ServiceOptions{})})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, errs, done := startTestWatchLoop(t, s, ctx)
	root := t.TempDir()
	s.mu.Lock()
	s.config = Config{Enabled: true, Items: []TaskConfig{{TaskID: 1, Threshold: 10, Strategy: StrategyBlock}}}
	s.watched[root] = watchDir{taskID: 1, root: root}
	s.mu.Unlock()
	select {
	case events <- fsnotify.Event{Name: filepath.Join(root, "deleted.strm"), Op: fsnotify.Remove}:
	case <-time.After(time.Second):
		t.Fatal("未接收文件事件")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("批次未进入请求阶段")
	}
	select {
	case errs <- errors.New("监听异常"):
	case <-time.After(time.Second):
		t.Fatal("未接收监听错误")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("已经启动的批次未取消")
	}
	if err := <-done; err == nil {
		t.Fatal("未返回监听错误")
	}
}

func TestCancelledOldBatchCannotConsumeReplacementBatch(t *testing.T) {
	s := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	replacement := &candidateBatch{generation: 1, items: map[string]candidate{"new": {taskID: 1, path: "new"}}}
	s.batches[1] = replacement
	s.processBatch(ctx, 1, 1)
	if s.batches[1] != replacement {
		t.Fatal("已取消的旧回调消费了恢复后的新批次")
	}
	if err := s.processCandidates(ctx, 1, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的批次仍继续执行: %v", err)
	}
}

func TestRunRebuildsClosedWatcherAfterSavingConfig(t *testing.T) {
	s := New(Options{Tasks: &watchTestTaskRepo{}, Configs: &watchTestConfigRepo{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.run(ctx) }()
	defer func() { cancel(); <-done }()
	wait := func(previous *fsnotify.Watcher, paused bool) *fsnotify.Watcher {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			watcher, isPaused := s.watcher, s.paused
			s.mu.Unlock()
			if paused && isPaused || !paused && watcher != nil && watcher != previous {
				return watcher
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("监听未按预期暂停或恢复")
		return nil
	}
	watcher := wait(nil, false)
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	_ = wait(watcher, true)
	if _, err := s.UpdateConfig(ctx, Config{Enabled: true, Items: []TaskConfig{{TaskID: 1, Threshold: 10, Strategy: StrategyBlock}}}); err != nil {
		t.Fatal(err)
	}
	_ = wait(watcher, false)
}

func TestMergeCandidateKeepsHighestDeletedPath(t *testing.T) {
	taskID := int64(1)
	root := filepath.Join("strm", "动漫剧", "海贼王 (1999)")
	season := filepath.Join(root, "Season 1")
	items := map[string]candidate{}

	mergeCandidate(items, candidate{taskID: taskID, path: filepath.Join(season, "S01E01.strm")})
	mergeCandidate(items, candidate{taskID: taskID, path: filepath.Join(season, "S01E02.strm")})
	mergeCandidate(items, candidate{taskID: taskID, path: season, isDir: true})
	mergeCandidate(items, candidate{taskID: taskID, path: filepath.Join(root, "Season 2", "S02E01.strm")})
	mergeCandidate(items, candidate{taskID: taskID, path: root, isDir: true})

	if len(items) != 1 {
		t.Fatalf("候选数量 = %d，期望只保留作品目录", len(items))
	}
	got, ok := items[root]
	if !ok || !got.isDir {
		t.Fatalf("未保留作品目录候选：%+v", items)
	}

	mergeCandidate(items, candidate{taskID: taskID, path: filepath.Join(root, "Season 3", "S03E01.strm")})
	if len(items) != 1 {
		t.Fatalf("作品目录已入队后不应再加入子文件：%+v", items)
	}
}

func TestMonitorTiming(t *testing.T) {
	for _, tt := range []struct {
		name    string
		minutes int
		delay   time.Duration
	}{
		{"立即处理", 0, 2 * time.Second},
		{"延迟处理", 10, 10 * time.Minute},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeTaskConfig(TaskConfig{TaskID: 1, Threshold: 10, Strategy: StrategyConfirm, DelayMinutes: tt.minutes})
			if err != nil {
				t.Fatalf("配置被拒绝：%v", err)
			}
			if got.DelayMinutes != tt.minutes {
				t.Fatalf("延迟分钟 = %d，期望 %d", got.DelayMinutes, tt.minutes)
			}
			if delay := taskDelay(got.DelayMinutes); delay != tt.delay {
				t.Fatalf("归并等待时间 = %s，期望 %s", delay, tt.delay)
			}
		})
	}
}

type deleteTestFiles struct {
	tree       map[string][]domain.FileItem
	calls      map[string]int
	deleted    [][]string
	beforeList func(string) error
}

func (f *deleteTestFiles) List(ctx context.Context, _ int64, parent string, refresh bool) ([]domain.FileItem, error) {
	if !refresh {
		return nil, errors.New("反查使用了过期缓存")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.calls[parent]++
	if f.beforeList != nil {
		if err := f.beforeList(parent); err != nil {
			return nil, err
		}
	}
	return f.tree[parent], nil
}

func (f *deleteTestFiles) DeleteFiles(ctx context.Context, _ int64, ids []string, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.deleted = append(f.deleted, append([]string{}, ids...))
	return nil
}

func newDeleteTestService(t *testing.T, threshold int, strategy string) (*Service, *deleteTestFiles, *domain.StrmTask) {
	t.Helper()
	task := &domain.StrmTask{ID: 1, AccountID: 2, ParentID: "root", Name: "影视", OutputFolder: "library"}
	repo := &watchTestTaskRepo{items: []*domain.StrmTask{task}, get: func(context.Context, int64) (*domain.StrmTask, error) { return task, nil }}
	s := New(Options{Tasks: repo, Configs: &watchTestConfigRepo{}, Strm: strm.NewService(strm.ServiceOptions{}), StrmDir: t.TempDir()})
	s.config = Config{Enabled: true, Items: []TaskConfig{{TaskID: 1, Threshold: threshold, Strategy: strategy}}}
	root := taskRoot(s.strmDir, task)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	identity, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	s.watched[root] = watchDir{taskID: task.ID, root: root, identity: identity}
	files := &deleteTestFiles{tree: make(map[string][]domain.FileItem), calls: make(map[string]int)}
	s.files = files
	return s, files, task
}

func deletedDirectory(s *Service, task *domain.StrmTask, name string) candidate {
	return candidate{taskID: task.ID, path: filepath.Join(taskRoot(s.strmDir, task), name), isDir: true}
}

func mediaItems(n int) []domain.FileItem {
	items := make([]domain.FileItem, n)
	for i := range items {
		items[i] = domain.FileItem{ID: fmt.Sprint(i), Name: fmt.Sprintf("S01E%02d.mkv", i+1)}
	}
	return items
}

func TestBatchThresholdCountsAllDirectoriesBeforeDeleting(t *testing.T) {
	for _, tt := range []struct {
		name    string
		second  int
		blocked bool
	}{
		{"分别未超限但累计超限", 3, true}, {"恰好达到阈值", 2, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, files, task := newDeleteTestService(t, 5, StrategyBlock)
			files.tree["root"] = []domain.FileItem{{ID: "a", Name: "A", IsDir: true}, {ID: "b", Name: "B", IsDir: true}}
			files.tree["a"], files.tree["b"] = mediaItems(3), mediaItems(tt.second)
			if err := s.processCandidates(context.Background(), 1, []candidate{deletedDirectory(s, task, "A"), deletedDirectory(s, task, "B")}); err != nil {
				t.Fatal(err)
			}
			if tt.blocked && len(files.deleted) != 0 {
				t.Fatal("超限前已经删除了一部分目标")
			}
			if !tt.blocked && (len(files.deleted) != 1 || len(files.deleted[0]) != 2) {
				t.Fatalf("未按父目录合并删除: %+v", files.deleted)
			}
			if files.calls["root"] != 1 {
				t.Fatal("同批反查没有复用父目录列表")
			}
		})
	}
}

func TestBatchConfirmationPersistsAllTargetsAndStopsCountingEarly(t *testing.T) {
	s, files, task := newDeleteTestService(t, 2, StrategyConfirm)
	files.tree["root"] = []domain.FileItem{{ID: "a", Name: "A", IsDir: true}, {ID: "b", Name: "B", IsDir: true}, {ID: "c", Name: "C.mkv"}}
	files.tree["a"] = append(mediaItems(20), domain.FileItem{ID: "deep", Name: "deep", IsDir: true})
	var saved string
	s.configs = &watchTestConfigRepo{set: func(_ context.Context, key, raw string) error {
		if key == pendingKey {
			saved = raw
		}
		return nil
	}}
	c := deletedDirectory(s, task, "C.strm")
	c.isDir = false
	err := s.processCandidates(context.Background(), 1, []candidate{deletedDirectory(s, task, "A"), deletedDirectory(s, task, "B"), c})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.deleted) != 0 || len(s.pending) != 1 || len(s.pending[0].Targets) != 3 {
		t.Fatalf("未整批等待确认: %+v", s.pending)
	}
	if files.calls["b"] != 0 || files.calls["deep"] != 0 {
		t.Fatal("达到阈值+1后仍继续递归统计")
	}
	var restored []Pending
	if err := json.Unmarshal([]byte(saved), &restored); err != nil {
		t.Fatal(err)
	}
	s.pending = restored
	if err := s.Confirm(context.Background(), restored[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(s.pending) != 0 || len(files.deleted) != 1 || len(files.deleted[0]) != 3 {
		t.Fatalf("确认没有涵盖完整批次或文件目标: %+v", files.deleted)
	}
}

func TestBatchPreflightFailuresNeverPartiallyDelete(t *testing.T) {
	for _, kind := range []string{"反查缺失", "统计失败", "本地恢复", "根目录丢失", "监控停用", "重新保存配置", "取消上下文"} {
		t.Run(kind, func(t *testing.T) {
			s, files, task := newDeleteTestService(t, 10, StrategyBlock)
			files.tree["root"] = []domain.FileItem{{ID: "a", Name: "A", IsDir: true}, {ID: "b", Name: "B", IsDir: true}}
			files.tree["a"], files.tree["b"] = mediaItems(2), mediaItems(2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "反查缺失" {
				files.tree["root"] = files.tree["root"][:1]
			}
			files.beforeList = func(parent string) error {
				if parent != "b" {
					return nil
				}
				switch kind {
				case "统计失败":
					return errors.New("模拟网盘异常")
				case "本地恢复":
					return os.Mkdir(filepath.Join(taskRoot(s.strmDir, task), "A"), 0755)
				case "根目录丢失":
					return os.Remove(taskRoot(s.strmDir, task))
				case "监控停用":
					s.config.Enabled = false
				case "重新保存配置":
					_, err := s.UpdateConfig(ctx, s.config)
					return err
				case "取消上下文":
					cancel()
				}
				return nil
			}
			err := s.processCandidates(ctx, 1, []candidate{deletedDirectory(s, task, "A"), deletedDirectory(s, task, "B")})
			if err == nil {
				t.Fatal("未阻断不安全的批次")
			}
			if len(files.deleted) != 0 {
				t.Fatal("整批预检失败前已经删除部分目标")
			}
		})
	}
}

func TestConfirmRechecksEntireBatchBeforeFirstDelete(t *testing.T) {
	for _, kind := range []string{"远端已变化", "本地已恢复", "账号已变化"} {
		t.Run(kind, func(t *testing.T) {
			s, files, task := newDeleteTestService(t, 1, StrategyConfirm)
			files.tree["root"] = []domain.FileItem{{ID: "a", Name: "A", IsDir: true}, {ID: "b", Name: "B", IsDir: true}}
			files.tree["a"], files.tree["b"] = mediaItems(2), mediaItems(2)
			if err := s.processCandidates(context.Background(), 1, []candidate{deletedDirectory(s, task, "A"), deletedDirectory(s, task, "B")}); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "远端已变化":
				files.tree["root"][1].ID = "replacement"
			case "本地已恢复":
				if err := os.Mkdir(filepath.Join(taskRoot(s.strmDir, task), "B"), 0755); err != nil {
					t.Fatal(err)
				}
			case "账号已变化":
				task.AccountID++
			}
			if err := s.Confirm(context.Background(), s.pending[0].ID); err == nil {
				t.Fatal("已变化的批次仍允许确认删除")
			}
			if len(files.deleted) != 0 {
				t.Fatal("确认只预检一个目标就开始删除")
			}
		})
	}
}

func TestPendingSaveFailureDoesNotCreateUnconfirmableRecord(t *testing.T) {
	s, files, task := newDeleteTestService(t, 1, StrategyConfirm)
	s.configs = &watchTestConfigRepo{set: func(context.Context, string, string) error { return errors.New("写入失败") }}
	files.tree["root"] = []domain.FileItem{{ID: "a", Name: "A", IsDir: true}}
	files.tree["a"] = mediaItems(2)
	if err := s.processCandidates(context.Background(), 1, []candidate{deletedDirectory(s, task, "A")}); err == nil {
		t.Fatal("保存失败未返回错误")
	}
	if len(s.pending) != 0 || len(files.deleted) != 0 {
		t.Fatal("保存失败后留下待确认记录或执行了删除")
	}
}

func TestReconcileOnlyScansNewOrReplacedRoots(t *testing.T) {
	s, _, task := newDeleteTestService(t, 10, StrategyBlock)
	s.watched = make(map[string]watchDir)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	s.watcher = watcher
	defer func() {
		if err := watcher.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx := context.Background()
	root := taskRoot(s.strmDir, task)
	first := filepath.Join(root, "first")
	if err := os.Mkdir(first, 0755); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.watched[first]; !ok {
		t.Fatal("首次启动未注册子目录")
	}
	fresh := filepath.Join(root, "new")
	child := filepath.Join(fresh, "child")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.watched[fresh]; ok {
		t.Fatal("正常巡检仍在遍历全部子目录")
	}
	if err := s.handleEvent(ctx, fsnotify.Event{Name: fresh, Op: fsnotify.Create}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.watched[child]; !ok {
		t.Fatal("新增目录事件未增量注册子树")
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	s.queue(ctx, deletedDirectory(s, task, "stale"))
	if err := s.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.watched) != 1 || len(s.batches) != 0 {
		t.Fatal("根目录被替换后仍保留旧子树或删除批次")
	}
	st, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(st, s.watched[root].identity) {
		t.Fatal("未注册新根目录")
	}
	if err := s.handleEvent(ctx, fsnotify.Event{Name: root, Op: fsnotify.Remove}); err != nil {
		t.Fatal(err)
	}
	if len(s.watched) != 0 || len(s.batches) != 0 {
		t.Fatal("整任务目录删除被当作普通候选")
	}
}

func TestRunningBatchCannotUseReplacedRoot(t *testing.T) {
	s, files, task := newDeleteTestService(t, 10, StrategyBlock)
	files.tree["root"] = []domain.FileItem{{ID: "a", Name: "A", IsDir: true}}
	files.tree["a"] = mediaItems(2)
	files.beforeList = func(parent string) error {
		if parent != "a" {
			return nil
		}
		root := taskRoot(s.strmDir, task)
		if err := os.Rename(root, root+"-old"); err != nil {
			return err
		}
		if err := os.Mkdir(root, 0755); err != nil {
			return err
		}
		st, err := os.Stat(root)
		if err != nil {
			return err
		}
		s.removeTree(root)
		s.watched[root] = watchDir{taskID: task.ID, root: root, identity: st}
		return nil
	}
	if err := s.processCandidates(context.Background(), 1, []candidate{deletedDirectory(s, task, "A")}); err == nil {
		t.Fatal("已开始的旧批次复用了新根目录监听")
	}
	if len(files.deleted) != 0 {
		t.Fatal("根目录替换后仍删除了远端")
	}
}

func TestNativeWatcherAddsSubtreeAndQueuesDirectoryDeletion(t *testing.T) {
	s, _, task := newDeleteTestService(t, 10, StrategyBlock)
	s.watched = make(map[string]watchDir)
	s.config.Items[0].DelayMinutes = 10
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.watch(ctx) }()
	defer func() { cancel(); <-done }()
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			ok := check()
			s.mu.Unlock()
			if ok {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("真实文件系统事件未按预期处理")
	}
	root := taskRoot(s.strmDir, task)
	wait(func() bool { _, ok := s.watched[root]; return ok })
	child := filepath.Join(root, "series", "Season 02")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { _, ok := s.watched[child]; return ok })
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		b := s.batches[task.ID]
		return b != nil && len(b.items) == 1 && b.items[child].isDir
	})
}

func TestSubtreeRegistrationFailurePausesEntireWatcher(t *testing.T) {
	s, _, task := newDeleteTestService(t, 10, StrategyBlock)
	s.config.Items[0].DelayMinutes = 10
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	s.watcher = watcher
	child := filepath.Join(taskRoot(s.strmDir, task), "new")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	s.queue(context.Background(), deletedDirectory(s, task, "old"))
	events := make(chan fsnotify.Event, 1)
	events <- fsnotify.Event{Name: child, Op: fsnotify.Create}
	if err := s.watchEvents(context.Background(), events, make(chan error)); err == nil {
		t.Fatal("注册失败未传播给监听循环")
	}
	if !s.paused || len(s.batches) != 0 || len(s.watched) != 0 {
		t.Fatal("子目录无法监听时仍执行删除")
	}
}
