package strmdelete

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

func (s *Service) processBatch(ctx context.Context, taskID int64, generation uint64) {
	s.mu.Lock()
	batch := s.batches[taskID]
	if ctx.Err() != nil || s.paused || batch == nil || batch.generation != generation {
		s.mu.Unlock()
		return
	}
	delete(s.batches, taskID)
	items := make([]candidate, 0, len(batch.items))
	for _, item := range batch.items {
		items = append(items, item)
	}
	s.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })
	if err := s.processCandidates(ctx, taskID, items); err != nil && ctx.Err() == nil {
		s.log.Warn("STRM 删除批次未完成，已停止后续删除", "task_id", taskID, "err", err)
	}
}

func (s *Service) processCandidates(ctx context.Context, taskID int64, items []candidate) error {
	if ctx.Err() != nil || s.strm.IsTaskBusy(taskID) {
		return ctx.Err()
	}
	s.mu.Lock()
	revision := s.revision
	enabled := s.config.Enabled && !s.paused
	s.mu.Unlock()
	if !enabled {
		return nil
	}
	cfg, ok := s.taskConfig(taskID)
	if !ok {
		return nil
	}
	task, err := s.tasks.Get(ctx, taskID)
	if err != nil {
		return err
	}
	root := taskRoot(s.strmDir, task)
	s.mu.Lock()
	rootInfo := s.watched[root]
	s.mu.Unlock()
	targets := make([]deleteTarget, 0, len(items))
	lists := make(map[string][]domain.FileItem)
	seen := make(map[string]bool)
	for _, c := range items {
		if _, err := os.Stat(c.path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		rel, err := filepath.Rel(root, c.path)
		if err != nil || !validRelative(rel) {
			return domain.Errorf(domain.CodeValidation, "删除候选不属于当前任务目录")
		}
		rel = filepath.ToSlash(rel)
		target, parentID, err := s.resolveTarget(ctx, task, rel, c.isDir, lists)
		if err != nil {
			return err
		}
		if seen[target.ID] {
			continue
		}
		seen[target.ID] = true
		targets = append(targets, deleteTarget{Relative: rel, RemoteID: target.ID, ParentID: parentID, IsDir: c.isDir})
	}
	if len(targets) == 0 {
		return nil
	}
	// 先完整反查批次，再累计统计；任何目标不明确时，整批不执行。
	count := 0
	for _, target := range targets {
		n := 1
		if target.IsDir {
			n, err = s.countMedia(ctx, task, target.RemoteID, cfg.Threshold-count)
			if err != nil {
				return err
			}
		}
		count += n
		if count > cfg.Threshold {
			break
		}
	}
	if err := s.validateAutomatic(ctx, task, cfg, rootInfo.identity, revision, targets); err != nil {
		return err
	}
	if count > cfg.Threshold {
		if cfg.Strategy == StrategyConfirm {
			return s.addPending(ctx, task, targets, cfg.Threshold)
		}
		s.notify(ctx, "warning", "STRM 删除已拦截", fmt.Sprintf("%s 本批删除的媒体文件总量已超过保护阈值 %d，整批未删除网盘源文件。", targetSummary(targets), cfg.Threshold), task.AccountID, 0)
		return nil
	}
	if err := s.deleteTargets(ctx, task, targets, func() error {
		return s.validateAutomatic(ctx, task, cfg, rootInfo.identity, revision, targets)
	}); err != nil {
		return err
	}
	s.log.Info("STRM 删除已联动远端", "task_id", task.ID, "targets", len(targets), "media_count", count)
	return nil
}

func taskRoot(base string, task *domain.StrmTask) string {
	return filepath.Clean(strm.TaskOutputDir(base, strm.TaskRelDir(task.GroupDir, task.OutputFolder)))
}

func validRelative(rel string) bool {
	rel = filepath.Clean(rel)
	return rel != "" && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Service) validateLocal(ctx context.Context, task *domain.StrmTask, targets []deleteTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.strm.IsTaskBusy(task.ID) {
		return domain.Errorf(domain.CodeValidation, "STRM 任务正在执行，已停止删除")
	}
	current, err := s.tasks.Get(ctx, task.ID)
	if err != nil {
		return err
	}
	if current.AccountID != task.AccountID || current.ParentID != task.ParentID || taskRoot(s.strmDir, current) != taskRoot(s.strmDir, task) {
		return domain.Errorf(domain.CodeValidation, "STRM 任务配置已变化，已停止删除")
	}
	root := taskRoot(s.strmDir, task)
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return domain.Errorf(domain.CodeValidation, "任务根目录不可用，已停止删除")
	}
	for _, target := range targets {
		rel := filepath.FromSlash(target.Relative)
		if !validRelative(rel) {
			return domain.Errorf(domain.CodeValidation, "删除目标路径无效")
		}
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil || !os.IsNotExist(err) {
			return domain.Errorf(domain.CodeValidation, "本地路径已经恢复或无法确认状态，已停止整批删除")
		}
	}
	return nil
}

func (s *Service) validateAutomatic(ctx context.Context, task *domain.StrmTask, cfg TaskConfig, identity os.FileInfo, revision uint64, targets []deleteTarget) error {
	s.mu.Lock()
	enabled := s.config.Enabled && !s.paused && s.revision == revision
	info, watched := s.watched[taskRoot(s.strmDir, task)]
	s.mu.Unlock()
	current, selected := s.taskConfig(task.ID)
	if !enabled || !selected || current != cfg || !watched || info.taskID != task.ID {
		return domain.Errorf(domain.CodeValidation, "监控设置或监听状态已变化，已停止删除")
	}
	if st, err := os.Stat(info.root); err != nil || identity == nil || info.identity == nil || !os.SameFile(st, identity) || !os.SameFile(info.identity, identity) {
		return domain.Errorf(domain.CodeValidation, "任务根目录已变化，已停止删除")
	}
	return s.validateLocal(ctx, task, targets)
}

// 按父目录合并删除请求；网盘不提供跨请求事务，失败后不继续后面的组。
func (s *Service) deleteTargets(ctx context.Context, task *domain.StrmTask, targets []deleteTarget, validate func() error) error {
	groups := make(map[string][]string)
	parents := make([]string, 0)
	for _, target := range targets {
		if _, ok := groups[target.ParentID]; !ok {
			parents = append(parents, target.ParentID)
		}
		groups[target.ParentID] = append(groups[target.ParentID], target.RemoteID)
	}
	for _, parent := range parents {
		if err := validate(); err != nil {
			return err
		}
		if err := s.files.DeleteFiles(ctx, task.AccountID, groups[parent], parent); err != nil {
			return err
		}
	}
	return nil
}

func targetSummary(targets []deleteTarget) string {
	if len(targets) == 1 {
		return targets[0].Relative
	}
	return fmt.Sprintf("%s 等 %d 个目标", targets[0].Relative, len(targets))
}

func (s *Service) addPending(ctx context.Context, task *domain.StrmTask, targets []deleteTarget, threshold int) error {
	s.mu.Lock()
	for _, item := range s.pending {
		if item.TaskID == task.ID && slices.Equal(item.Targets, targets) {
			s.mu.Unlock()
			return nil
		}
	}
	id := time.Now().UnixMilli()
	for _, existing := range s.pending {
		if existing.ID >= id {
			id = existing.ID + 1
		}
	}
	item := Pending{ID: id, TaskID: task.ID, TaskName: task.Name, AccountID: task.AccountID, Relative: targetSummary(targets), Targets: targets, CreatedAt: time.Now()}
	s.pending = append(s.pending, item)
	if err := s.savePendingLocked(ctx); err != nil {
		s.pending = s.pending[:len(s.pending)-1]
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		paths = append(paths, target.Relative)
	}
	s.notify(ctx, "warning", "STRM 删除等待确认", fmt.Sprintf("本批删除的媒体文件总量已超过保护阈值 %d，整批尚未删除。请确认是否删除以下网盘源文件或目录：\n%s", threshold, strings.Join(paths, "\n")), task.AccountID, id)
	return nil
}

func (s *Service) Confirm(ctx context.Context, id int64) error {
	s.mu.Lock()
	var item *Pending
	for _, pending := range s.pending {
		if pending.ID == id {
			copy := pending
			item = &copy
			break
		}
	}
	s.mu.Unlock()
	if item == nil {
		return domain.Errorf(domain.CodeNotFound, "待确认删除记录不存在")
	}
	if len(item.Targets) == 0 {
		return domain.Errorf(domain.CodeValidation, "待确认记录缺少目标，请取消后重新触发")
	}
	task, err := s.tasks.Get(ctx, item.TaskID)
	if err != nil {
		return err
	}
	if task.AccountID != item.AccountID {
		return domain.Errorf(domain.CodeValidation, "任务账号已变化，请取消后重新触发")
	}
	if err := s.validateLocal(ctx, task, item.Targets); err != nil {
		return err
	}
	lists := make(map[string][]domain.FileItem)
	for _, target := range item.Targets {
		resolved, parentID, err := s.resolveTarget(ctx, task, target.Relative, target.IsDir, lists)
		if err != nil || resolved.ID != target.RemoteID || parentID != target.ParentID {
			return domain.Errorf(domain.CodeValidation, "远端路径已变化，请取消后重新触发")
		}
	}
	if err := s.deleteTargets(ctx, task, item.Targets, func() error {
		return s.validateLocal(ctx, task, item.Targets)
	}); err != nil {
		return err
	}
	return s.removePending(ctx, id)
}

func (s *Service) resolveTarget(ctx context.Context, task *domain.StrmTask, rel string, isDir bool, lists map[string][]domain.FileItem) (*domain.FileItem, string, error) {
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	parentID := task.ParentID
	for i, name := range parts {
		items, ok := lists[parentID]
		if !ok {
			var err error
			items, err = s.files.List(ctx, task.AccountID, parentID, true)
			if err != nil {
				return nil, "", err
			}
			lists[parentID] = items
		}
		last := i == len(parts)-1
		var matches []domain.FileItem
		for _, item := range items {
			if last && !isDir && !s.strm.IsTaskMediaFile(task, item.Name) {
				continue
			}
			matched := targetNameMatches(item, name, last, isDir)
			if matched && ((!last && item.IsDir) || (last && item.IsDir == isDir)) {
				matches = append(matches, item)
			}
		}
		if len(matches) != 1 {
			return nil, "", domain.Errorf(domain.CodeNotFound, "路径不存在或不唯一：%s", strings.Join(parts[:i+1], "/"))
		}
		if last {
			item := matches[0]
			return &item, parentID, nil
		}
		parentID = matches[0].ID
	}
	return nil, "", domain.Errorf(domain.CodeNotFound, "远端目标不存在")
}

func targetNameMatches(item domain.FileItem, localName string, last, targetIsDir bool) bool {
	if last && item.IsDir != targetIsDir {
		return false
	}
	if last && !targetIsDir {
		localStem := strings.TrimSuffix(localName, filepath.Ext(localName))
		remoteStem := strings.TrimSuffix(item.Name, filepath.Ext(item.Name))
		return strings.EqualFold(remoteStem, localStem) || strings.EqualFold(strm.SafeStem(remoteStem), localStem)
	}
	return item.Name == localName || strm.SafeName(item.Name) == localName
}

func (s *Service) countMedia(ctx context.Context, task *domain.StrmTask, rootID string, limit int) (int, error) {
	queue := []string{rootID}
	count := 0
	visited := make(map[string]bool)
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			return 0, domain.Errorf(domain.CodeValidation, "远端目录树存在重复引用")
		}
		visited[id] = true
		items, err := s.files.List(ctx, task.AccountID, id, true)
		if err != nil {
			return 0, err
		}
		for _, item := range items {
			if item.IsDir {
				queue = append(queue, item.ID)
				continue
			}
			if s.strm.IsTaskMediaFile(task, item.Name) {
				count++
				if count > limit {
					return count, nil
				}
			}
		}
	}
	return count, nil
}
