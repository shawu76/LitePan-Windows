package strm

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	enhancedDirCacheBatchSize   = 100
	enhancedDirResolveRetryWait = 250 * time.Millisecond
)

type unresolvedEnhancedDir struct {
	fileCount int
	examples  []string
}

func describeEnhancedDirs(entries []driver.FullListEntry) map[string]unresolvedEnhancedDir {
	details := make(map[string]unresolvedEnhancedDir, 64)
	for _, entry := range entries {
		pid := strings.TrimSpace(entry.ParentID)
		if pid == "" || pid == "0" {
			continue
		}
		detail := details[pid]
		detail.fileCount++
		if name := strings.TrimSpace(entry.Name); name != "" && len(detail.examples) < 3 {
			detail.examples = append(detail.examples, name)
		}
		details[pid] = detail
	}
	return details
}

func useEnhancedScan(ctx context.Context, task *domain.StrmTask, deps ScanDeps, runMode string) (bool, error) {
	if !deps.Settings.Tool115TreeEnabled {
		return false, nil
	}
	if deps.Files == nil || deps.DirCache == nil {
		return false, nil
	}
	if runMode == domain.StrmRunModeBranch {
		return false, nil
	}
	if task.BranchCheckEnabled && runMode != domain.StrmRunModeFull {
		return false, nil
	}
	return deps.Files.SupportsFullList(ctx, task.AccountID)
}

// scanEnhancedTask 以 cur=0 全量清单替代逐目录递归：
// 拉清单 → pid→路径 翻译（缓存 + get_info 补漏）→ 构建候选 → 复用 finalizeScan 生成/同步/清理。
func scanEnhancedTask(
	ctx context.Context,
	task *domain.StrmTask,
	deps ScanDeps,
	root string,
	rules scanRules,
	failures *FailureCollector,
) (ScanResult, error) {
	var result ScanResult
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}

	entries, err := deps.Files.ListAllFiles(ctx, task.AccountID, task.ParentID)
	if err != nil {
		return result, err
	}
	dirDetails := describeEnhancedDirs(entries)
	dirPaths, unresolved, err := resolveDirPaths(ctx, deps, task.AccountID, dirDetails)
	if err != nil {
		return result, err
	}
	// 清单来自任务根，但缓存路径可能已过时；先核实矛盾，不能据此静默漏扫并删除本地文件。
	rootSegs := splitRemotePath(task.Path)
	pathConflict := ""
	if len(entries) == 0 {
		pathConflict = "115 全量清单为空，无法确认远端目录是否真实为空，本次已停止本地清理"
		log.Warn("115 STRM 增强返回空清单，本次不清理本地文件",
			"task_id", task.ID, "task_name", task.Name, "account_id", task.AccountID, "parent_id", task.ParentID)
	}
	for pid, oldPath := range dirPaths {
		if _, ok := relDirsOf(oldPath, "check", rootSegs); ok {
			continue
		}
		freshPath, resolveErr := resolveDirPathWithRetry(ctx, deps, task.AccountID, pid)
		if resolveErr != nil {
			if isNotFoundError(resolveErr) {
				unresolved[pid] = dirDetails[pid]
				if _, deleteErr := deps.DirCache.DeleteByIDs(ctx, task.AccountID, []string{pid}); deleteErr != nil {
					return result, deleteErr
				}
				continue
			}
			return result, fmt.Errorf("核实 STRM 目录路径失败（目录 ID %s，任务根 %s）: %w", pid, task.Path, resolveErr)
		}
		if _, ok := relDirsOf(freshPath, "check", rootSegs); !ok {
			pathConflict = "全量清单中的目录路径与任务根不一致，本次已停止本地清理，请检查任务目录和路径映射"
			log.Warn("STRM 扫描目录路径不一致", "task_id", task.ID, "account_id", task.AccountID,
				"directory_id", pid, "task_path", task.Path, "cached_path", oldPath, "resolved_path", freshPath)
			continue
		}
		dirPaths[pid] = freshPath
		if err := deps.DirCache.UpsertBatch(ctx, []domain.StrmDirCacheEntry{{
			AccountID: task.AccountID, DirID: pid, DirPath: freshPath, LastSeenAt: time.Now(),
		}}); err != nil {
			return result, err
		}
	}
	if len(unresolved) == 0 && pathConflict == "" {
		if derr := pruneDirCache(ctx, deps, task, entries); derr != nil {
			log.Warn("STRM 目录缓存清理失败", "account_id", task.AccountID, "err", derr.Error())
		}
	} else if len(unresolved) > 0 {
		log.Info("115 STRM 增强检测到失效目录，本次跳过映射清理", "task_id", task.ID,
			"task_name", task.Name, "account_id", task.AccountID, "directory_count", len(unresolved))
	}
	if len(unresolved) > 0 {
		ids := make([]string, 0, len(unresolved))
		for id := range unresolved {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			detail := unresolved[id]
			reason := fmt.Sprintf("115 返回目录不存在，已跳过关联的 %d 个文件", detail.fileCount)
			if len(detail.examples) > 0 {
				reason += "；文件示例：" + strings.Join(detail.examples, "、")
			}
			log.Info("115 STRM 增强跳过失效目录", "task_id", task.ID, "task_name", task.Name,
				"account_id", task.AccountID, "directory_id", id, "file_count", detail.fileCount,
				"examples", detail.examples)
			failures.Add(ScanFailureStrm, "远端目录 ID "+id, reason)
		}
	}

	harvest := newScanHarvest()
	state := harvest.state
	state.cleanupBlockedReason = pathConflict
	state.cleanupScopes = []cleanupScope{{recursive: true}}
	state.remoteChildren = nil // 清单不含空目录，禁用目录级清理避免误删
	if len(unresolved) > 0 {
		totalFiles := 0
		for _, detail := range unresolved {
			totalFiles += detail.fileCount
		}
		state.cleanupBlockedReason = fmt.Sprintf("115 全量清单中有 %d 个目录无法解析，已跳过关联的 %d 个文件；为避免误删，本次不执行任何本地清理", len(unresolved), totalFiles)
	}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		pid := strings.TrimSpace(e.ParentID)
		if _, missing := unresolved[pid]; missing {
			continue
		}
		if matchesKeywordRules(e.Name, rules.excludeFiles) {
			continue
		}
		relDirs, ok := relDirsOf(dirPaths[pid], e.Name, rootSegs)
		if !ok {
			continue // 远端路径不在任务根范围内，忽略
		}
		if matchesDirKeywordRules(relDirs, rules.excludeDirs) {
			continue
		}
		recordMetadataDirectory(state.metadataDirs, e.ParentID, relDirs)
		classified := rules.classify(e.FileID, e.Name, e.Size, relDirs)
		if classified.hasMedia {
			harvest.candidates = append(harvest.candidates, classified.media)
			harvest.dirHasMedia[dirKey(relDirs)] = true
			markSubtreeMedia(harvest.subtreeHasMedia, relDirs)
			continue
		}
		if classified.hasMetadata {
			harvest.metadataItems = append(harvest.metadataItems, classified.metadata)
		}
	}

	log.Info("STRM 增强扫描完成", "task_id", task.ID, "task_name", task.Name,
		"account_id", task.AccountID, "remote_files", len(entries),
		"candidates", len(harvest.candidates), "mode", "full-list")

	return finalizeScan(ctx, task, deps, harvest, false, rules, root, failures)
}

func matchesDirKeywordRules(dirs, rules []string) bool {
	for _, dir := range dirs {
		if matchesKeywordRules(dir, rules) {
			return true
		}
	}
	return false
}

// pruneDirCache 清理“任务根范围内、本次清单未出现”的 pid→路径 记录：
// 目录被删除（含其所有子路径）后，下次增强扫描即被清除，不需要定时任务。
// 清单为空、任务根为网盘根、或本次清单覆盖的目录数相对缓存明显缩水时跳过：
// 空清单与缩水清单都可能来自 115 分页异常/限流导致的拉取不完整，误清映射只会放大路径漂移。
func pruneDirCache(ctx context.Context, deps ScanDeps, task *domain.StrmTask, entries []driver.FullListEntry) error {
	if deps.DirCache == nil || len(entries) == 0 {
		return nil
	}
	prefix := strings.Trim(strings.ReplaceAll(strings.TrimSpace(task.Path), "\\", "/"), "/")
	if prefix == "" {
		return nil
	}
	existing, err := deps.DirCache.ListByPathPrefix(ctx, task.AccountID, prefix)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if pid := strings.TrimSpace(e.ParentID); pid != "" {
			seen[pid] = struct{}{}
		}
	}
	// 规模保护：本次清单实际覆盖的目录数不足已有缓存的一半时，判定为拉取不完整，
	// 跳过清理，避免把未扫到的目录误判为“已删除”而清掉映射。
	if len(seen) > 0 && len(seen)*2 < len(existing) {
		return nil
	}
	var stale []string
	for _, rec := range existing {
		if _, ok := seen[rec.DirID]; !ok {
			stale = append(stale, rec.DirID)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if _, err := deps.DirCache.DeleteByIDs(ctx, task.AccountID, stale); err != nil {
		return err
	}
	return nil
}

// resolveDirPaths 返回 pid→完整远端路径 映射：
// 优先查 SQLite 缓存，未命中的调驱动 ResolveDirPath 反查并落库。
func resolveDirPaths(ctx context.Context, deps ScanDeps, accountID int64, details map[string]unresolvedEnhancedDir) (map[string]string, map[string]unresolvedEnhancedDir, error) {
	out := make(map[string]string, 64)
	unresolved := make(map[string]unresolvedEnhancedDir)
	if deps.DirCache == nil || deps.Files == nil {
		return out, unresolved, nil
	}
	if len(details) == 0 {
		return out, unresolved, nil
	}
	ids := make([]string, 0, len(details))
	for pid := range details {
		ids = append(ids, pid)
	}
	sort.Strings(ids)
	hit, err := deps.DirCache.GetBatch(ctx, accountID, ids)
	if err != nil {
		return nil, nil, err
	}
	var missing []string
	for _, pid := range ids {
		if p, ok := hit[pid]; ok {
			out[pid] = p
		} else {
			missing = append(missing, pid)
		}
	}
	if len(missing) == 0 {
		return out, unresolved, nil
	}
	now := time.Now()
	var fresh []domain.StrmDirCacheEntry
	flush := func() error {
		if len(fresh) == 0 {
			return nil
		}
		if err := deps.DirCache.UpsertBatch(ctx, fresh); err != nil {
			return err
		}
		fresh = fresh[:0]
		return nil
	}
	for _, pid := range missing {
		p, rerr := resolveDirPathWithRetry(ctx, deps, accountID, pid)
		if rerr != nil {
			if isNotFoundError(rerr) {
				unresolved[pid] = details[pid]
				continue
			}
			if flushErr := flush(); flushErr != nil {
				return nil, nil, flushErr
			}
			return nil, nil, rerr
		}
		out[pid] = p
		fresh = append(fresh, domain.StrmDirCacheEntry{
			AccountID: accountID, DirID: pid, DirPath: p, LastSeenAt: now,
		})
		if len(fresh) >= enhancedDirCacheBatchSize {
			if err := flush(); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	return out, unresolved, nil
}

func resolveDirPathWithRetry(ctx context.Context, deps ScanDeps, accountID int64, dirID string) (string, error) {
	path, err := deps.Files.ResolveDirPath(ctx, accountID, dirID)
	if err == nil || !isNotFoundError(err) {
		return path, err
	}
	timer := time.NewTimer(enhancedDirResolveRetryWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
	}
	return deps.Files.ResolveDirPath(ctx, accountID, dirID)
}

func isNotFoundError(err error) bool {
	appErr, ok := domain.AsAppError(err)
	return ok && appErr.Code == domain.CodeNotFound
}

// relDirsOf 把远端完整路径裁掉任务根前缀，得到本地相对目录。
// 文件直接在任务根下时返回空切片；远端路径不在任务根内时返回 false。
// relDirsOf 切出 dirPath 相对任务根的子目录段。
//
// dirPath 是远端路径字符串（段间用 "/" 分隔），rootSegs 是任务根的目录段。
// 目录名允许自带斜杠（例：一个名为 abc/def/ghi 的目录），它拼出来的路径和三层目录
// 完全一样，切法见 segmentsBelowRoot。对不上就返回 false，走“路径不一致”的保护。
func relDirsOf(dirPath, fileName string, rootSegs []string) ([]string, bool) {
	if strings.TrimSpace(fileName) == "" {
		return nil, false
	}
	return segmentsBelowRoot(splitRemotePath(dirPath), rootSegs)
}

func splitRemotePath(p string) []string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
