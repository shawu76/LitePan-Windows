package strmdelete

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
	"litepan/internal/file"
	"litepan/internal/strm"
	"litepan/pkg/safego"
)

const (
	configKey  = "strm_delete_linkage_config"
	pendingKey = "strm_delete_linkage_pending"

	StrategyBlock   = "block"
	StrategyConfirm = "confirm"
)

// TaskConfig 是单个 STRM 任务的删除监控设置。
type TaskConfig struct {
	TaskID       int64  `json:"task_id"`
	Threshold    int    `json:"threshold"`
	Strategy     string `json:"strategy"`
	DelayMinutes int    `json:"delay_minutes"`
}

type Config struct {
	Enabled bool         `json:"enabled"`
	Items   []TaskConfig `json:"items"`
}

type TaskOption struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	LocalDir string `json:"local_dir"`
}

type Pending struct {
	ID        int64          `json:"id"`
	TaskID    int64          `json:"task_id"`
	TaskName  string         `json:"task_name"`
	AccountID int64          `json:"account_id"`
	Relative  string         `json:"relative_path"`
	Targets   []deleteTarget `json:"targets"`
	CreatedAt time.Time      `json:"created_at"`
}

type deleteTarget struct {
	Relative string `json:"relative_path"`
	RemoteID string `json:"remote_id"`
	ParentID string `json:"parent_id"`
	IsDir    bool   `json:"is_dir"`
}

type fileOperations interface {
	List(context.Context, int64, string, bool) ([]domain.FileItem, error)
	DeleteFiles(context.Context, int64, []string, string) error
}

type Status struct {
	Config  Config       `json:"config"`
	Tasks   []TaskOption `json:"tasks"`
	Pending []Pending    `json:"pending"`
}

type Options struct {
	Configs domain.ConfigRepository
	Tasks   domain.StrmTaskRepository
	Files   *file.Service
	Strm    *strm.Service
	StrmDir string
	Bus     *eventbus.Bus
	Log     *slog.Logger
}

type watchDir struct {
	taskID   int64
	root     string
	identity os.FileInfo
}

type candidate struct {
	taskID int64
	path   string
	isDir  bool
}

type candidateBatch struct {
	generation uint64
	items      map[string]candidate
	timer      *time.Timer
}

type Service struct {
	configs domain.ConfigRepository
	tasks   domain.StrmTaskRepository
	files   fileOperations
	strm    *strm.Service
	strmDir string
	bus     *eventbus.Bus
	log     *slog.Logger

	mu          sync.Mutex
	config      Config
	pending     []Pending
	watcher     *fsnotify.Watcher
	watched     map[string]watchDir
	roots       map[int64]string
	batches     map[int64]*candidateBatch
	generation  uint64
	revision    uint64
	warnedRoots map[int64]string
	started     bool
	paused      bool
	resume      chan struct{}
	refresh     chan struct{}
}

func New(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	var files fileOperations
	if opts.Files != nil {
		files = opts.Files
	}
	return &Service{
		configs:     opts.Configs,
		tasks:       opts.Tasks,
		files:       files,
		strm:        opts.Strm,
		strmDir:     opts.StrmDir,
		bus:         opts.Bus,
		log:         log,
		config:      defaultConfig(),
		watched:     make(map[string]watchDir),
		roots:       make(map[int64]string),
		batches:     make(map[int64]*candidateBatch),
		warnedRoots: make(map[int64]string),
		resume:      make(chan struct{}, 1),
		refresh:     make(chan struct{}, 1),
	}
}

const (
	defaultThreshold    = 100
	immediateSettleTime = 2 * time.Second
)

func defaultConfig() Config {
	return Config{Items: []TaskConfig{}}
}

func normalizeConfig(cfg Config) (Config, error) {
	seen := make(map[int64]struct{}, len(cfg.Items))
	items := make([]TaskConfig, 0, len(cfg.Items))
	for _, item := range cfg.Items {
		if item.TaskID <= 0 {
			continue
		}
		if _, ok := seen[item.TaskID]; ok {
			continue
		}
		norm, err := normalizeTaskConfig(item)
		if err != nil {
			return cfg, err
		}
		seen[item.TaskID] = struct{}{}
		items = append(items, norm)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TaskID < items[j].TaskID })
	cfg.Items = items
	if cfg.Enabled && len(items) == 0 {
		return cfg, domain.Errorf(domain.CodeValidation, "请至少选择一个 STRM 任务")
	}
	return cfg, nil
}

func normalizeTaskConfig(item TaskConfig) (TaskConfig, error) {
	if item.Threshold == 0 {
		item.Threshold = defaultThreshold
	}
	if item.Strategy == "" {
		item.Strategy = StrategyConfirm
	}
	if item.Threshold < 1 || item.Threshold > 100000 {
		return item, domain.Errorf(domain.CodeValidation, "保护阈值应在 1～100000 之间")
	}
	if item.DelayMinutes < 0 || item.DelayMinutes > 1440 {
		return item, domain.Errorf(domain.CodeValidation, "延迟时间应在 0～1440 分钟之间")
	}
	if item.Strategy != StrategyBlock && item.Strategy != StrategyConfirm {
		return item, domain.Errorf(domain.CodeValidation, "未知的超限处理方式")
	}
	return item, nil
}

func (s *Service) taskConfig(taskID int64) (TaskConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.config.Items {
		if item.TaskID == taskID {
			return item, true
		}
	}
	return TaskConfig{}, false
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.configs == nil || s.tasks == nil || s.files == nil || s.strm == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		s.log.Warn("加载 STRM 删除联动配置失败", "err", err)
	}
	go safego.Guard(s.log, "strm-delete.watch", func() { s.run(ctx) })
}

func (s *Service) load(ctx context.Context) error {
	cfg := defaultConfig()
	if raw, ok, err := s.configs.Get(ctx, configKey); err != nil {
		return err
	} else if ok && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return err
		}
	}
	var err error
	if cfg, err = normalizeConfig(cfg); err != nil {
		return err
	}
	var pending []Pending
	if raw, ok, err := s.configs.Get(ctx, pendingKey); err != nil {
		return err
	} else if ok && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &pending); err != nil {
			return err
		}
	}
	if pending == nil {
		pending = []Pending{}
	}
	s.mu.Lock()
	s.config = cfg
	s.pending = pending
	s.mu.Unlock()
	return nil
}

func (s *Service) run(ctx context.Context) {
	for ctx.Err() == nil {
		_ = s.watch(ctx)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-s.resume:
		}
	}
}

func (s *Service) watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.pauseWatch(ctx, err)
		return err
	}
	defer watcher.Close()
	s.mu.Lock()
	s.watcher = watcher
	s.mu.Unlock()
	return s.watchEvents(ctx, watcher.Events, watcher.Errors)
}

func (s *Service) pauseWatch(ctx context.Context, err error) {
	s.mu.Lock()
	for id, batch := range s.batches {
		if batch.timer != nil {
			batch.timer.Stop()
		}
		delete(s.batches, id)
	}
	s.watcher = nil
	s.watched = make(map[string]watchDir)
	s.roots = make(map[int64]string)
	s.paused = true
	s.mu.Unlock()
	if ctx.Err() == nil {
		s.log.Warn("STRM 删除监听异常，已暂停自动删除并取消本轮任务", "err", err)
		s.notify(ctx, "warning", "STRM 删除监控已暂停", "监听发生异常，已取消尚未执行的自动删除。请重新保存并启用监控设置恢复监听；异常期间的删除不会补执行。", 0, 0)
	}
}

func (s *Service) watchEvents(ctx context.Context, events <-chan fsnotify.Event, errs <-chan error) (err error) {
	watchCtx, cancel := context.WithCancel(ctx)
	defer func() {
		// timer.Stop 不会等待已经启动的回调，因此同时取消本轮上下文。
		cancel()
		s.pauseWatch(ctx, err)
	}()
	s.mu.Lock()
	s.paused = false
	s.mu.Unlock()
	if err := s.reconcile(watchCtx); err != nil {
		return err
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.reconcile(watchCtx); err != nil {
				return err
			}
		case <-s.refresh:
			if err := s.reconcile(watchCtx); err != nil {
				return err
			}
		case err, ok := <-errs:
			if !ok {
				return fmt.Errorf("文件监听错误通道已关闭")
			}
			if err != nil {
				return err
			}
		case evt, ok := <-events:
			if !ok {
				return fmt.Errorf("文件监听事件通道已关闭")
			}
			if err := s.handleEvent(watchCtx, evt); err != nil {
				return err
			}
		}
	}
}

func (s *Service) selectedTasks(ctx context.Context) (map[int64]*domain.StrmTask, error) {
	s.mu.Lock()
	cfg := s.config
	s.mu.Unlock()
	out := make(map[int64]*domain.StrmTask)
	if !cfg.Enabled {
		return out, nil
	}
	selected := make(map[int64]struct{}, len(cfg.Items))
	for _, item := range cfg.Items {
		selected[item.TaskID] = struct{}{}
	}
	tasks, err := s.tasks.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if _, ok := selected[task.ID]; ok {
			out[task.ID] = task
		}
	}
	return out, nil
}

func (s *Service) reconcile(ctx context.Context) error {
	tasks, err := s.selectedTasks(ctx)
	if err != nil {
		return err
	}
	desiredRoots := make(map[int64]string, len(tasks))
	for id, task := range tasks {
		desiredRoots[id] = filepath.Clean(strm.TaskOutputDir(s.strmDir, strm.TaskRelDir(task.GroupDir, task.OutputFolder)))
	}
	s.mu.Lock()
	watcher := s.watcher
	if watcher == nil {
		s.mu.Unlock()
		return nil
	}
	var staleRoots []string
	for id, previous := range s.roots {
		if root, ok := desiredRoots[id]; !ok || root != previous {
			staleRoots = append(staleRoots, previous)
		}
	}
	s.mu.Unlock()
	for _, root := range staleRoots {
		s.removeTree(root)
	}
	for id, root := range desiredRoots {
		st, err := os.Stat(root)
		if err != nil || !st.IsDir() {
			s.removeTree(root)
			s.warnUnwatchableRoot(id, root, err)
			continue
		}
		s.mu.Lock()
		delete(s.warnedRoots, id)
		info, watched := s.watched[root]
		s.mu.Unlock()
		if watched && info.taskID == id && info.identity != nil && os.SameFile(info.identity, st) {
			continue
		}
		s.removeTree(root)
		if err := s.addTree(ctx, id, root, root); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) removeTree(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, info := range s.watched {
		if !pathContains(root, path) {
			continue
		}
		if s.watcher != nil {
			_ = s.watcher.Remove(path)
		}
		delete(s.watched, path)
		if path == info.root {
			s.revision++
			delete(s.roots, info.taskID)
			if batch := s.batches[info.taskID]; batch != nil {
				if batch.timer != nil {
					batch.timer.Stop()
				}
				delete(s.batches, info.taskID)
			}
		}
	}
}

// warnUnwatchableRoot 对同一个任务的同一个根目录只告警一次，避免每分钟刷屏。
func (s *Service) warnUnwatchableRoot(taskID int64, root string, err error) {
	s.mu.Lock()
	if s.warnedRoots == nil {
		s.warnedRoots = map[int64]string{}
	}
	already := s.warnedRoots[taskID] == root
	s.warnedRoots[taskID] = root
	s.mu.Unlock()
	if already {
		return
	}
	reason := "目录不存在或不是目录"
	if err != nil {
		reason = err.Error()
	}
	s.log.Warn("STRM 删除联动未能监听任务目录，该任务不会触发远端删除",
		"task_id", taskID, "dir", root, "reason", reason)
}

func (s *Service) addTree(ctx context.Context, taskID int64, root, start string) error {
	return filepath.WalkDir(start, func(path string, entry os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		var identity os.FileInfo
		if path == root {
			identity, err = entry.Info()
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		_, exists := s.watched[path]
		if !exists && s.watcher != nil {
			if addErr := s.watcher.Add(path); addErr == nil {
				s.watched[path] = watchDir{taskID: taskID, root: root, identity: identity}
				if path == root {
					s.roots[taskID] = root
				}
			} else if os.IsNotExist(addErr) {
				return nil
			} else {
				return fmt.Errorf("监听 STRM 目录 %s 失败: %w", path, addErr)
			}
		}
		return nil
	})
}

func (s *Service) handleEvent(ctx context.Context, evt fsnotify.Event) error {
	s.mu.Lock()
	info, knownDir := s.watched[evt.Name]
	if !knownDir {
		info = s.watched[filepath.Dir(evt.Name)]
	}
	s.mu.Unlock()
	if info.taskID == 0 {
		return nil
	}
	s.log.Debug("STRM 删除监听收到文件事件", "task_id", info.taskID, "op", evt.Op.String(), "path", evt.Name)
	if evt.Op&fsnotify.Create != 0 {
		if st, err := os.Stat(evt.Name); err == nil && st.IsDir() {
			return s.addTree(ctx, info.taskID, info.root, evt.Name)
		}
		return nil
	}
	if evt.Op&(fsnotify.Remove|fsnotify.Rename) == 0 {
		return nil
	}
	if evt.Name == info.root {
		s.removeTree(info.root)
		return nil
	}
	if s.strm.IsTaskBusy(info.taskID) {
		// 程序自身搬移也必须移除失效的监听，但不产生删除候选。
		if knownDir {
			s.removeTree(evt.Name)
		}
		return nil
	}
	isDir := knownDir
	if !isDir && !strings.EqualFold(filepath.Ext(evt.Name), ".strm") {
		return nil
	}
	if knownDir {
		s.removeTree(evt.Name)
	}
	s.queue(ctx, candidate{taskID: info.taskID, path: evt.Name, isDir: isDir})
	return nil
}

func (s *Service) queue(ctx context.Context, c candidate) {
	c.path = filepath.Clean(c.path)
	s.mu.Lock()
	var item TaskConfig
	found := false
	if s.config.Enabled && !s.paused && ctx.Err() == nil {
		for _, configured := range s.config.Items {
			if configured.TaskID == c.taskID {
				item = configured
				found = true
				break
			}
		}
	}
	if !found {
		s.mu.Unlock()
		return
	}
	batch := s.batches[c.taskID]
	if batch == nil {
		batch = &candidateBatch{items: make(map[string]candidate)}
		s.batches[c.taskID] = batch
	}
	mergeCandidate(batch.items, c)
	s.generation++
	batch.generation = s.generation
	generation := batch.generation
	if batch.timer != nil {
		batch.timer.Stop()
	}
	delay := taskDelay(item.DelayMinutes)
	batch.timer = time.AfterFunc(delay, func() {
		safego.Guard(s.log, "strm-delete.process", func() { s.processBatch(ctx, c.taskID, generation) })
	})
	s.mu.Unlock()
}

func taskDelay(minutes int) time.Duration {
	if minutes <= 0 {
		return immediateSettleTime
	}
	return time.Duration(minutes) * time.Minute
}

func mergeCandidate(items map[string]candidate, incoming candidate) {
	for path, existing := range items {
		if pathContains(path, incoming.path) {
			if path == incoming.path && incoming.isDir {
				existing.isDir = true
				items[path] = existing
			}
			return
		}
		if pathContains(incoming.path, path) {
			delete(items, path)
		}
	}
	items[incoming.path] = incoming
}

func pathContains(parent, child string) bool {
	return child == parent || strings.HasPrefix(child, parent+string(filepath.Separator))
}

func (s *Service) notify(ctx context.Context, level, title, message string, accountID, refID int64) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(ctx, eventbus.NotificationCreated{Level: level, Category: domain.NotificationCategoryStrmDeleteConfirm, Title: title, Message: message, AccountID: accountID, RefID: refID})
}

func (s *Service) savePendingLocked(ctx context.Context) error {
	raw, err := json.Marshal(s.pending)
	if err != nil {
		return err
	}
	return s.configs.Set(ctx, pendingKey, string(raw))
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	tasks, err := s.tasks.List(ctx)
	if err != nil {
		return Status{}, err
	}
	options := make([]TaskOption, 0, len(tasks))
	for _, task := range tasks {
		options = append(options, TaskOption{ID: task.ID, Name: task.Name, LocalDir: strm.TaskRelDir(task.GroupDir, task.OutputFolder)})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := append([]Pending{}, s.pending...)
	return Status{Config: s.config, Tasks: options, Pending: pending}, nil
}

func (s *Service) UpdateConfig(ctx context.Context, cfg Config) (Status, error) {
	norm, err := normalizeConfig(cfg)
	if err != nil {
		return Status{}, err
	}
	raw, err := json.Marshal(norm)
	if err != nil {
		return Status{}, err
	}
	if err := s.configs.Set(ctx, configKey, string(raw)); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	s.config = norm
	s.revision++
	selected := make(map[int64]struct{}, len(norm.Items))
	for _, item := range norm.Items {
		selected[item.TaskID] = struct{}{}
	}
	for taskID, batch := range s.batches {
		if _, ok := selected[taskID]; !norm.Enabled || !ok {
			if batch.timer != nil {
				batch.timer.Stop()
			}
			delete(s.batches, taskID)
		}
	}
	if s.paused && norm.Enabled {
		select {
		case s.resume <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
	select {
	case s.refresh <- struct{}{}:
	default:
	}
	return s.Status(ctx)
}

func (s *Service) Cancel(ctx context.Context, id int64) error { return s.removePending(ctx, id) }

func (s *Service) removePending(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.pending {
		if s.pending[i].ID == id {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return s.savePendingLocked(ctx)
		}
	}
	return domain.Errorf(domain.CodeNotFound, "待确认删除记录不存在")
}
