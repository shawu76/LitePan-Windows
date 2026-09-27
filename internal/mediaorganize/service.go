package mediaorganize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/file"
	"litepan/internal/mediaorganize/rules"
	"litepan/internal/mediaorganize/tmdb"
	"litepan/internal/settings"
)

const logLimit = 800

var ErrTaskAborted = errors.New("media organize task aborted")

type LogEntry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
}

type Service struct {
	repo     domain.MediaOrganizeTaskRepository
	files    *file.Service
	settings *settings.Service
	dataDir  string
	log      *slog.Logger

	planner  PlannerBuilder
	executor ExecutorApplier

	mu              sync.Mutex
	taskLogs        map[string][]LogEntry
	taskProgress    map[string]map[string]any
	running         map[string]struct{}
	stopRequests    map[string]struct{}
	runningAccounts map[string]int64
	// pendingDelete 记录运行期间被请求删除的任务。
	pendingDelete map[string]struct{}
}

type ServiceOptions struct {
	Repo     domain.MediaOrganizeTaskRepository
	Files    *file.Service
	Settings *settings.Service
	DataDir  string
	Log      *slog.Logger
	Planner  PlannerBuilder
	Executor ExecutorApplier
}

func NewService(opts ServiceOptions) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	p := opts.Planner
	if p == nil {
		p = StubPlanner{}
	}
	e := opts.Executor
	if e == nil {
		e = StubExecutor{}
	}
	return &Service{
		repo:            opts.Repo,
		files:           opts.Files,
		settings:        opts.Settings,
		dataDir:         opts.DataDir,
		log:             log,
		planner:         p,
		executor:        e,
		taskLogs:        make(map[string][]LogEntry),
		taskProgress:    make(map[string]map[string]any),
		running:         make(map[string]struct{}),
		stopRequests:    make(map[string]struct{}),
		runningAccounts: make(map[string]int64),
		pendingDelete:   make(map[string]struct{}),
	}
}

func (s *Service) ListTasks(ctx context.Context) ([]*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	return s.repo.List(ctx)
}

func (s *Service) GetTask(ctx context.Context, id string) (*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	return s.repo.Get(ctx, id)
}

func (s *Service) CreateTask(ctx context.Context, task *domain.MediaOrganizeTask) (*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	if err := s.validateTaskForSave(task); err != nil {
		return nil, err
	}
	if task.Status == "" {
		task.Status = domain.MediaOrganizeStatusIdle
	}
	if err := s.repo.Create(ctx, task); err != nil {
		return nil, err
	}
	return s.repo.Get(ctx, task.ID)
}

func (s *Service) UpdateTask(ctx context.Context, task *domain.MediaOrganizeTask) error {
	if s.repo == nil {
		return domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	if err := s.validateTaskForSave(task); err != nil {
		return err
	}
	if err := s.repo.Update(ctx, task); err != nil {
		return err
	}
	return s.deletePlanFile(task.ID)
}

func (s *Service) validateTaskForSave(task *domain.MediaOrganizeTask) error {
	if task == nil {
		return domain.Errorf(domain.CodeValidation, "无效媒体整理任务")
	}
	if strings.TrimSpace(task.TaskName) == "" {
		return domain.Errorf(domain.CodeValidation, "任务名称不能为空")
	}
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil || accountID <= 0 {
		return domain.Errorf(domain.CodeValidation, "请选择网盘账号")
	}
	return validateTaskActionConfig(cfg)
}

func validateTaskActionConfig(cfg map[string]any) error {
	actionType := strings.ToLower(strings.TrimSpace(stringFromAny(cfg["action_type"])))
	if actionType == "" {
		actionType = "move"
	}
	if actionType == "move" && strings.TrimSpace(stringFromAny(cfg["target_root"])) == "" {
		return domain.Errorf(domain.CodeValidation, "move 模式下目标根目录不能为空")
	}
	if actionType == "rename" && strings.TrimSpace(stringFromAny(cfg["rename_marker"])) == "" {
		return domain.Errorf(domain.CodeValidation, "原地重命名必须设置标识：tmdb / 自定义 / off（不写入文件名，靠规范结构判断跳过）")
	}
	return nil
}

func (s *Service) DeleteTask(ctx context.Context, id string) (stopping bool, err error) {
	task, err := s.GetTask(ctx, id)
	if err != nil {
		return false, err
	}
	if s.IsRunning(id) {
		// 先请求停止、再在同一把锁内登记待删除；顺序反了协程收不到停止请求，登记不原子会丢删除请求。
		s.RequestStop(id)
		if s.deferDeleteIfRunning(id) {
			task.Status = domain.MediaOrganizeStatusStopping
			if uerr := s.repo.Update(ctx, task); uerr != nil {
				s.log.Warn("标记整理任务停止中失败", "task_id", id, "err", uerr)
			}
			return true, nil
		}
	}
	// 无执行协程（含状态残留）：直接删除。
	s.discardStop(id)
	if err := s.repo.Delete(ctx, id); err != nil {
		return false, err
	}
	_ = s.deletePlanFile(id)
	s.clearLogs(id)
	return false, nil
}

// deferDeleteIfRunning 在同一把锁内判断存活并登记待删除，避免竞态丢失删除请求。
func (s *Service) deferDeleteIfRunning(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.running[taskID]; !ok {
		return false
	}
	if s.pendingDelete == nil {
		s.pendingDelete = make(map[string]struct{})
	}
	s.pendingDelete[taskID] = struct{}{}
	return true
}

// finalizePendingDelete 在协程退出后清理待删除的任务数据；重复调用是安全的空操作。
func (s *Service) finalizePendingDelete(taskID string) bool {
	s.mu.Lock()
	_, ok := s.pendingDelete[taskID]
	delete(s.pendingDelete, taskID)
	s.mu.Unlock()
	if !ok {
		return false
	}
	s.discardStop(taskID)
	_ = s.deletePlanFile(taskID)
	s.clearLogs(taskID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.repo.Delete(ctx, taskID); err != nil {
		s.log.Warn("整理任务停止后删除失败", "task_id", taskID, "err", err)
		return true
	}
	s.log.Info("整理任务已停止并删除", "task_id", taskID)
	return true
}

func (s *Service) PlanTask(ctx context.Context, taskID string) (map[string]any, error) {
	task, err := s.requireTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !s.beginRun(taskID, 0) {
		return nil, domain.Errorf(domain.CodeValidation, "任务正在执行中")
	}
	defer s.finishRun(taskID)

	s.discardStop(taskID)
	s.clearLogs(taskID)
	s.resetProgress(taskID)
	s.appendLog(taskID, "[MediaOrganize] 生成计划开始")

	settingsDict := SettingsDict(s.settings)
	ctx = s.withAPIDelay(ctx)

	task.Status = domain.MediaOrganizeStatusPlanning
	_ = s.repo.Update(ctx, task)

	var plan *Plan
	defer func() {
		task.Status = domain.MediaOrganizeStatusIdle
		_ = s.repo.Update(context.Background(), task)
	}()

	plan, err = s.buildPlan(ctx, taskID, task, settingsDict)
	if err != nil {
		return nil, err
	}
	if err := s.savePlan(taskID, plan); err != nil {
		return nil, err
	}
	s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 计划生成完成: %d 个动作, 跳过 %d 个", len(plan.Actions), len(plan.Skipped)))
	s.updateProgress(taskID, map[string]any{
		"stage":      "done",
		"actions":    len(plan.Actions),
		"skipped":    len(plan.Skipped),
		"updated_at": time.Now().Format("15:04:05"),
	})
	return map[string]any{
		"plan": plan,
		"summary": map[string]any{
			"actions": len(plan.Actions),
			"skipped": len(plan.Skipped),
		},
	}, nil
}

func (s *Service) ApplyTask(ctx context.Context, taskID string) (map[string]any, error) {
	task, cfg, accountID, err := s.prepareRun(ctx, taskID)
	if err != nil {
		return nil, err
	}
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可执行的计划，请先生成计划")
	}

	s.discardStop(taskID)
	s.appendLog(taskID, "[MediaOrganize] 开始执行计划")
	s.startRunner(taskID, accountID, func(runCtx context.Context) {
		s.applyPlanRunner(runCtx, taskID, plan, task, cfg, accountID)
	})
	return map[string]any{"task_id": taskID, "submitted": true}, nil
}

// withAPIDelay 按全局 API 间隔设置给上下文叠加请求延迟。
func (s *Service) withAPIDelay(ctx context.Context) context.Context {
	settingsDict := SettingsDict(s.settings)
	delayMS := intFromAny(settingsDict["api_request_interval_ms"], 300)
	return driver.WithExtraAPIDelay(ctx, delayMS)
}

func (s *Service) RunTask(ctx context.Context, taskID string) (map[string]any, error) {
	task, cfg, accountID, err := s.prepareRun(ctx, taskID)
	if err != nil {
		return nil, err
	}

	s.discardStop(taskID)
	s.clearLogs(taskID)
	s.appendLog(taskID, "[MediaOrganize] 任务已提交，开始生成计划")
	s.log.Info("整理任务开始执行", "task_id", taskID, "task_name", task.TaskName, "account_id", accountID)
	s.startRunner(taskID, accountID, func(runCtx context.Context) {
		settingsDict := SettingsDict(s.settings)
		runCtx = s.withAPIDelay(runCtx)

		task.Status = domain.MediaOrganizeStatusPlanning
		_ = s.repo.Update(runCtx, task)
		plan, buildErr := s.buildPlan(runCtx, taskID, task, settingsDict)
		if buildErr != nil {
			if errors.Is(buildErr, ErrTaskAborted) {
				s.appendLog(taskID, "[MediaOrganize] 任务已停止")
			} else {
				s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", buildErr))
			}
			task.Status = domain.MediaOrganizeStatusIdle
			task.LastRunAt = time.Now()
			_ = s.repo.Update(runCtx, task)
			return
		}
		if err := s.savePlan(taskID, plan); err != nil {
			s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", err))
			task.Status = domain.MediaOrganizeStatusIdle
			task.LastRunAt = time.Now()
			_ = s.repo.Update(runCtx, task)
			return
		}
		s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 计划已生成: %d 个动作, 跳过 %d 个", len(plan.Actions), len(plan.Skipped)))
		s.applyPlanRunner(runCtx, taskID, plan, task, cfg, accountID)
	})
	return map[string]any{"task_id": taskID, "submitted": true}, nil
}

func (s *Service) prepareRun(ctx context.Context, taskID string) (*domain.MediaOrganizeTask, map[string]any, int64, error) {
	task, err := s.requireTask(ctx, taskID)
	if err != nil {
		return nil, nil, 0, err
	}
	if s.IsRunning(taskID) {
		return nil, nil, 0, domain.Errorf(domain.CodeValidation, "任务正在执行中")
	}
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, nil, 0, err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil {
		return nil, nil, 0, err
	}
	return task, cfg, accountID, nil
}

func (s *Service) GetPlan(taskID string) (*Plan, error) {
	return s.loadPlan(taskID)
}

func (s *Service) DeletePlan(taskID string) error {
	return s.deletePlanFile(taskID)
}

func (s *Service) UpdatePlanAction(taskID, actionID, targetName string) (map[string]any, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可编辑的计划")
	}
	targetName = strings.TrimSpace(targetName)
	if targetName == "" {
		return nil, domain.Errorf(domain.CodeValidation, "目标名不能为空")
	}
	if rules.SanitizeFilename(targetName) != targetName {
		return nil, domain.Errorf(domain.CodeValidation, "目标名包含非法字符")
	}
	var target *PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].ID == actionID {
			target = &plan.Actions[i]
			break
		}
	}
	if target == nil {
		return nil, domain.Errorf(domain.CodeValidation, "找不到对应的动作")
	}
	if target.Kind != ActionKindRelocate {
		return nil, domain.Errorf(domain.CodeValidation, "仅支持编辑整理动作")
	}
	if target.Status == "done" || target.Status == "failed" {
		return nil, domain.Errorf(domain.CodeValidation, "此动作已执行，无法编辑")
	}
	if targetName == target.TargetName {
		return map[string]any{"action": target, "changed": false}, nil
	}
	target.TargetName = targetName
	target.Reason = target.Reason + " | 手动调整"
	if target.Metadata == nil {
		target.Metadata = map[string]any{}
	}
	target.Metadata["edited"] = true
	if err := s.savePlan(taskID, plan); err != nil {
		return nil, err
	}
	return map[string]any{"action": target, "changed": true}, nil
}

func (s *Service) DeletePlanAction(taskID, actionID string) (map[string]any, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可编辑的计划")
	}
	var target *PlanAction
	for i := range plan.Actions {
		if plan.Actions[i].ID == actionID {
			target = &plan.Actions[i]
			break
		}
	}
	if target == nil {
		return nil, domain.Errorf(domain.CodeValidation, "找不到对应的动作")
	}
	if target.Kind != ActionKindRelocate {
		return nil, domain.Errorf(domain.CodeValidation, "仅支持删除整理动作（保留依赖结构）")
	}
	if target.Status == "done" {
		return nil, domain.Errorf(domain.CodeValidation, "此动作已执行，无法删除")
	}
	filtered := make([]PlanAction, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		if action.ID != actionID {
			filtered = append(filtered, action)
		}
	}
	plan.Actions = filtered
	if err := s.savePlan(taskID, plan); err != nil {
		return nil, err
	}
	return map[string]any{"removed": actionID}, nil
}

func (s *Service) DeletePlanActions(taskID string, actionIDs []string) (map[string]any, error) {
	plan, err := s.loadPlan(taskID)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, domain.Errorf(domain.CodeValidation, "当前没有可编辑的计划")
	}
	wanted := make(map[string]struct{}, len(actionIDs))
	for _, id := range actionIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return map[string]any{"removed": []string{}, "skipped": []string{}}, nil
	}
	removed := make([]string, 0, len(wanted))
	removedSet := make(map[string]struct{}, len(wanted))
	filtered := make([]PlanAction, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		_, selected := wanted[action.ID]
		if selected && action.Kind == ActionKindRelocate && action.Status != "done" {
			removed = append(removed, action.ID)
			removedSet[action.ID] = struct{}{}
			continue
		}
		filtered = append(filtered, action)
	}
	skipped := make([]string, 0, len(wanted))
	for id := range wanted {
		if _, ok := removedSet[id]; !ok {
			skipped = append(skipped, id)
		}
	}
	if len(removed) > 0 {
		plan.Actions = filtered
		if err := s.savePlan(taskID, plan); err != nil {
			return nil, err
		}
	}
	return map[string]any{"removed": removed, "skipped": skipped}, nil
}

func (s *Service) GetLogs(taskID string) []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LogEntry(nil), s.taskLogs[taskID]...)
}

func (s *Service) GetProgress(taskID string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.taskProgress[taskID]
	if len(current) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(current))
	for k, v := range current {
		out[k] = v
	}
	return out
}

func (s *Service) RequestStop(taskID string) {
	s.mu.Lock()
	s.stopRequests[taskID] = struct{}{}
	s.mu.Unlock()
	s.appendLog(taskID, "[MediaOrganize] 已请求停止，当前操作完成后退出")
}

func (s *Service) IsRunning(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[taskID]
	return ok
}

func (s *Service) GetRunningAccountIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[int64]struct{}, len(s.runningAccounts))
	out := make([]int64, 0, len(s.runningAccounts))
	for _, accountID := range s.runningAccounts {
		if accountID <= 0 {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		out = append(out, accountID)
	}
	return out
}

func (s *Service) GuessFilename(name string) map[string]any {
	parsed := rules.NormalizeParsedMedia(rules.ParseFilenameStrict(name))
	return parsed.ToMap()
}

func (s *Service) ValidateTMDB(ctx context.Context, overrides map[string]any) (map[string]any, error) {
	merged := SettingsDict(s.settings)
	for k, v := range overrides {
		merged[k] = v
	}
	apiKey := strings.TrimSpace(stringFromAny(merged["tmdb_api_key"]))
	if apiKey == "" {
		return nil, domain.Errorf(domain.CodeValidation, "请先填写 TMDB API Key 再测试")
	}
	language := stringFromAny(merged["tmdb_language"])
	if language == "" {
		language = "zh-CN"
	}
	client := tmdb.NewClient(tmdb.Options{
		APIKey:        apiKey,
		Language:      language,
		ProxyURL:      buildProxyURL(merged),
		APIBaseHost:   stringFromAny(merged["tmdb_api_host"]),
		ImageBaseHost: stringFromAny(merged["tmdb_image_host"]),
	})
	apiOK := client.ValidateConnection(ctx)
	image := client.ValidateImageConnection(ctx)
	return map[string]any{
		"ok":           apiOK && image.OK,
		"api_ok":       apiOK,
		"image_ok":     image.OK,
		"image_status": image.StatusCode,
		"language":     language,
		"proxy_used":   buildProxyURL(merged) != "",
	}, nil
}

func (s *Service) loadTaskConfig(task *domain.MediaOrganizeTask) (map[string]any, error) {
	if task == nil {
		return nil, domain.Errorf(domain.CodeValidation, "无效媒体整理任务")
	}
	cfg := map[string]any{}
	if len(task.Config) > 0 {
		if err := json.Unmarshal(task.Config, &cfg); err != nil {
			return nil, domain.Errorf(domain.CodeValidation, "任务配置解析失败")
		}
	}
	cfg = NormalizeTaskConfig(cfg)
	if task.AccountID > 0 {
		cfg["account_id"] = strconv.FormatInt(task.AccountID, 10)
	}
	return cfg, nil
}

func (s *Service) buildPlan(ctx context.Context, taskID string, task *domain.MediaOrganizeTask, settingsDict map[string]any) (*Plan, error) {
	cfg, err := s.loadTaskConfig(task)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(task, cfg)
	if err != nil {
		return nil, err
	}
	if err := validateTaskActionConfig(cfg); err != nil {
		return nil, err
	}
	_ = accountID
	plan, err := s.planner.Build(ctx, taskID, task, cfg, settingsDict, PlannerHooks{
		Log:       func(msg string) { s.appendLog(taskID, msg) },
		CheckStop: func() error { return s.checkStop(taskID) },
		Progress:  func(info map[string]any) { s.updateProgress(taskID, info) },
	})
	if err != nil {
		if errors.Is(err, ErrTaskAborted) {
			return nil, err
		}
		return nil, domain.Errorf(domain.CodeInternal, "计划生成失败: %v", err)
	}
	if plan.Diagnostics == nil {
		plan.Diagnostics = map[string]any{}
	}
	plan.Diagnostics["account_id"] = strconv.FormatInt(accountID, 10)
	return plan, nil
}

func (s *Service) applyPlanRunner(ctx context.Context, taskID string, plan *Plan, task *domain.MediaOrganizeTask, cfg map[string]any, accountID int64) {
	settingsDict := SettingsDict(s.settings)
	ctx = s.withAPIDelay(ctx)

	aborted := false
	task.Status = domain.MediaOrganizeStatusRunning
	_ = s.repo.Update(ctx, task)

	err := s.executor.Apply(ctx, plan, taskID, accountID, cfg, settingsDict, ExecutorHooks{
		Log:       func(msg string) { s.appendLog(taskID, msg) },
		CheckStop: func() error { return s.checkStop(taskID) },
	})
	if errors.Is(err, ErrTaskAborted) {
		aborted = true
		s.appendLog(taskID, "[MediaOrganize] 收到停止请求，已停止执行")
	} else if err != nil {
		s.appendLog(taskID, fmt.Sprintf("[MediaOrganize] 任务异常: %v", err))
	}

	s.discardStop(taskID)
	// 运行期间被请求删除：不写统计与完成日志，直接清掉任务数据。
	if s.finalizePendingDelete(taskID) {
		return
	}
	summary := summarizePlan(plan, aborted)
	summaryBytes, _ := json.Marshal(summary)
	task.Status = domain.MediaOrganizeStatusIdle
	task.LastRunAt = time.Now()
	task.LastRunResult = summaryBytes
	_ = s.repo.Update(context.Background(), task)
	_ = s.deletePlanFile(taskID)
	s.appendLog(taskID, "[MediaOrganize] 任务完成："+formatSummaryZh(summary))
	s.log.Info("整理任务执行完成",
		"task_id", taskID,
		"task_name", task.TaskName,
		"account_id", accountID,
		"result", formatSummaryZh(summary),
	)
}

func (s *Service) startRunner(taskID string, accountID int64, fn func(context.Context)) {
	if !s.beginRun(taskID, accountID) {
		return
	}

	go func() {
		defer s.finishRun(taskID)
		fn(context.Background())
	}()
}

func (s *Service) beginRun(taskID string, accountID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.running[taskID]; ok {
		return false
	}
	s.running[taskID] = struct{}{}
	if accountID > 0 {
		s.runningAccounts[taskID] = accountID
	}
	return true
}

func (s *Service) finishRun(taskID string) {
	s.mu.Lock()
	delete(s.running, taskID)
	delete(s.runningAccounts, taskID)
	s.mu.Unlock()
	// 提前退出时仍要兑现运行期间的删除请求。
	s.finalizePendingDelete(taskID)
}

func (s *Service) requireTask(ctx context.Context, taskID string) (*domain.MediaOrganizeTask, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "媒体整理仓储未就绪")
	}
	task, err := s.repo.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (s *Service) resolveAccountID(task *domain.MediaOrganizeTask, cfg map[string]any) (int64, error) {
	if task != nil && task.AccountID > 0 {
		return task.AccountID, nil
	}
	if id := CfgAccountID(cfg); id > 0 {
		return id, nil
	}
	return 0, domain.Errorf(domain.CodeValidation, "任务未配置账号")
}

func (s *Service) planDir() string {
	return filepath.Join(s.dataDir, "media_organize_plans")
}

func (s *Service) planPath(taskID string) string {
	return filepath.Join(s.planDir(), taskID+".json")
}

func (s *Service) savePlan(taskID string, plan *Plan) error {
	if err := os.MkdirAll(s.planDir(), 0o755); err != nil {
		return fmt.Errorf("create plan dir: %w", err)
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.planDir(), taskID+"-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, s.planPath(taskID))
}

func (s *Service) loadPlan(taskID string) (*Plan, error) {
	data, err := os.ReadFile(s.planPath(taskID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParsePlan(data)
}

func (s *Service) deletePlanFile(taskID string) error {
	err := os.Remove(s.planPath(taskID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Service) appendLog(taskID, message string) {
	if taskID == "" {
		return
	}
	entry := LogEntry{Time: time.Now().Format("15:04:05"), Message: message}
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := append(s.taskLogs[taskID], entry)
	if len(buf) > logLimit {
		buf = buf[len(buf)-logLimit:]
	}
	s.taskLogs[taskID] = buf
}

func (s *Service) clearLogs(taskID string) {
	s.mu.Lock()
	s.taskLogs[taskID] = nil
	s.mu.Unlock()
}

func (s *Service) updateProgress(taskID string, info map[string]any) {
	if taskID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.taskProgress[taskID]
	if current == nil {
		current = map[string]any{}
	}
	for k, v := range info {
		current[k] = v
	}
	current["updated_at"] = time.Now().Format("15:04:05")
	s.taskProgress[taskID] = current
}

func (s *Service) resetProgress(taskID string) {
	s.mu.Lock()
	delete(s.taskProgress, taskID)
	s.mu.Unlock()
}

func (s *Service) checkStop(taskID string) error {
	s.mu.Lock()
	_, ok := s.stopRequests[taskID]
	s.mu.Unlock()
	if ok {
		return ErrTaskAborted
	}
	return nil
}

func (s *Service) discardStop(taskID string) {
	s.mu.Lock()
	delete(s.stopRequests, taskID)
	s.mu.Unlock()
}

// IsActiveStatus 判断任务是否处于运行中/规划中/停止中等活跃状态。
func IsActiveStatus(status string) bool {
	switch status {
	case domain.MediaOrganizeStatusRunning, domain.MediaOrganizeStatusPlanning, domain.MediaOrganizeStatusStopping:
		return true
	default:
		return false
	}
}

func summarizePlan(plan *Plan, aborted bool) map[string]any {
	if plan == nil {
		return map[string]any{"stopped": aborted}
	}
	// 规划冲突同时保留动作和诊断记录，统计以动作结果为准。
	seen := make(map[string]bool)
	total, renamed, moved, failed := 0, 0, 0, 0
	skipped, normalSkipped, pending := 0, 0, 0
	for _, action := range plan.Actions {
		if action.Kind != ActionKindRelocate {
			continue
		}
		total++
		if action.SourceID != "" {
			seen[action.SourceID] = true
		}
		switch action.Status {
		case "done":
			if action.SourceParentID == action.TargetParentID {
				renamed++
			} else {
				moved++
			}
		case "skipped":
			skipped++
			if isNormalSkip(action.Error, action.Reason) {
				normalSkipped++
			}
		case "failed":
			failed++
		default:
			// 未执行的动作单独计数，保证 total 等于各分桶之和。
			pending++
		}
	}
	for _, item := range plan.Skipped {
		id := stringFromAny(item["file_id"])
		if id != "" && seen[id] {
			continue
		}
		if id != "" {
			seen[id] = true
		}
		total++
		skipped++
		if isNormalSkip("", stringFromAny(item["reason"])) {
			normalSkipped++
		}
	}
	return map[string]any{
		"total":            total,
		"renamed":          renamed,
		"moved":            moved,
		"skipped":          skipped,
		"normal_skipped":   normalSkipped,
		"abnormal_skipped": skipped - normalSkipped,
		"failed":           failed,
		"pending":          pending,
		"stopped":          aborted,
	}
}

// formatSummaryZh 把整理统计 map 渲染成中文可读的一行日志（不影响 summary 底层字段）。
func formatSummaryZh(summary map[string]any) string {
	n := func(key string) int {
		if v, ok := summary[key].(int); ok {
			return v
		}
		return 0
	}
	line := fmt.Sprintf(
		"共 %d 项，成功 %d（重命名 %d / 移动 %d），跳过 %d（无需处理 %d / 需关注 %d），失败 %d，未执行 %d",
		n("total"), n("renamed")+n("moved"), n("renamed"), n("moved"),
		n("skipped"), n("normal_skipped"), n("abnormal_skipped"), n("failed"), n("pending"),
	)
	if stopped, _ := summary["stopped"].(bool); stopped {
		line += "（已中止）"
	}
	return line
}

func isNormalSkip(errText, reason string) bool {
	text := errText
	if text == "" {
		text = reason
	}
	text = strings.TrimSpace(text)
	// 限定原因格式，避免冲突文件名含“已整理”等字样时被误判。
	return text == "已整理" || text == "已是目标名" ||
		text == "目标已存在同名（未开启覆盖）" || text == "执行期间目标已存在同名" ||
		strings.HasPrefix(text, "已并入「") ||
		(strings.HasPrefix(text, "已合并到「") &&
			(strings.HasSuffix(text, "（同一部作品，文件已自动并入，空目录将清理）") ||
				strings.HasSuffix(text, "；源目录内文件将自动搬入该目录"))) ||
		(strings.HasPrefix(text, "作品已在「") && strings.HasSuffix(text, "整理，文件已自动并入")) ||
		strings.HasPrefix(text, "目录非空（")
}

func buildProxyURL(settingsDict map[string]any) string {
	return tmdb.BuildProxyURL(proxyConfigFrom(settingsDict, ""))
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func intFromAny(v any, fallback int) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err == nil {
			return n
		}
	}
	return fallback
}
