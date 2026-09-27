package spacecleanup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"litepan/internal/domain"
	"litepan/internal/strm"
)

const (
	// scanLifetime 只作为扫描新鲜期信息（expires_at），报告一直保留到重新扫描覆盖或程序重启。
	scanLifetime     = 15 * time.Minute
	maxScanPlans     = 8
	backupTempMinAge = time.Hour
	// coverExtractTempMinAge 封面提取临时文件超过 1 小时未清理即视为异常中断残留。
	coverExtractTempMinAge = time.Hour
)

type Service struct {
	opts Options
	log  *slog.Logger

	mu    sync.Mutex
	scans map[string]scanPlan
}

func New(opts Options) (*Service, error) {
	if strings.TrimSpace(opts.DataDir) == "" || strings.TrimSpace(opts.StrmDir) == "" || opts.StrmTasks == nil {
		return nil, fmt.Errorf("space cleanup dependencies are incomplete")
	}
	log := slog.Default()
	if opts.Logs != nil {
		log = opts.Logs.Root()
	}
	return &Service{opts: opts, log: log, scans: make(map[string]scanPlan)}, nil
}

func (s *Service) Scan(ctx context.Context) (Report, error) {
	if s == nil {
		return Report{}, domain.Errorf(domain.CodeInternal, "垃圾清理服务未就绪")
	}
	now := time.Now().UTC()
	items := make([]planItem, 0, 32)

	tasks, err := s.opts.StrmTasks.List(ctx)
	if err != nil {
		return Report{}, err
	}
	activePaths := s.activeStrmPaths(tasks)
	strmItems, err := s.scanStrm(ctx, activePaths)
	if err != nil {
		return Report{}, domain.Wrap(domain.CodeInternal, err)
	}
	items = append(items, strmItems...)

	scrapeItems, err := s.scanScrapeIndexes(tasks)
	if err != nil {
		return Report{}, domain.Wrap(domain.CodeInternal, err)
	}
	items = append(items, scrapeItems...)
	items = append(items, s.scanUploadTemps()...)
	items = append(items, s.scanOfflineTemps(ctx)...)
	items = append(items, s.scanCoverExtractTemps()...)

	if s.opts.BackupTempScan != nil {
		entries, scanErr := s.opts.BackupTempScan(ctx, backupTempMinAge)
		if scanErr != nil {
			s.log.Warn("扫描备份临时目录失败", "err", scanErr)
		} else {
			for _, entry := range entries {
				items = append(items, planItem{
					Item: Item{
						ID:              itemID(kindBackupTemp, entry.Path),
						Category:        CategoryTemp,
						Name:            "备份恢复临时目录",
						Path:            entry.Path,
						Reason:          "创建、导入或恢复准备中断后留下，且未被待恢复计划引用",
						SizeBytes:       entry.SizeBytes,
						FileCount:       entry.FileCount,
						DirCount:        entry.DirCount,
						DefaultSelected: true,
						Risk:            RiskSafe,
					},
					Kind:       kindBackupTemp,
					TargetPath: entry.Path,
				})
			}
		}
	}

	logItems, err := s.scanLogs()
	if err != nil {
		s.log.Warn("扫描历史日志失败", "err", err)
	} else {
		items = append(items, logItems...)
	}
	items = append(items, s.scanCache(ctx)...)
	items = append(items, s.scanCoverExtractSession()...)

	if s.opts.DB != nil {
		garbage, dbErr := s.opts.DB.ScanGarbage(ctx)
		if dbErr != nil {
			s.log.Warn("扫描数据库残留失败", "err", dbErr)
		} else {
			for _, entry := range garbage {
				kind, risk, selected := kindDatabaseRows, RiskSafe, true
				reason := fmt.Sprintf("对应主数据已不存在，共 %d 条记录", entry.Count)
				if entry.Kind == "deprecated" {
					kind, risk, selected = kindDatabaseTables, RiskReview, false
					reason = fmt.Sprintf("当前版本已不再使用，共 %d 张表、%d 条记录", strings.Count(entry.Detail, "、")+1, entry.Count)
				}
				items = append(items, planItem{
					Item: Item{
						ID: itemID(kind, entry.Key), Category: CategoryDatabase, Name: entry.Name,
						Path: entry.Detail, Reason: reason, DefaultSelected: selected, Risk: risk,
					},
					Kind: kind, TargetPath: entry.Key,
				})
			}
		}
		reclaimable, dbErr := s.opts.DB.ReclaimableBytes(ctx)
		if dbErr != nil {
			s.log.Warn("读取数据库可回收空间失败", "err", dbErr)
		} else if reclaimable > 0 {
			items = append(items, planItem{
				Item: Item{
					ID:              itemID(kindDatabase, s.opts.DBPath),
					Category:        CategoryDatabase,
					Name:            "数据库空间整理",
					Path:            s.opts.DBPath,
					Reason:          "压缩 SQLite 空闲页；空闲页原本也可被后续写入重复利用",
					SizeBytes:       reclaimable,
					DefaultSelected: false,
					Risk:            RiskLocking,
				},
				Kind: kindDatabase,
			})
		}
	}

	scanID := uuid.NewString()
	plan := scanPlan{createdAt: now, expiresAt: now.Add(scanLifetime), items: make(map[string]planItem, len(items))}
	for _, item := range items {
		plan.items[item.ID] = item
	}
	report := buildReport(scanID, plan)

	s.mu.Lock()
	s.scans[scanID] = plan
	s.trimScansLocked()
	s.mu.Unlock()
	return report, nil
}

func buildReport(scanID string, plan scanPlan) Report {
	defs := []Group{
		{Key: CategoryStrm, Label: "STRM 残留", Description: "未关联目录、空目录、系统杂项和失效刮削索引"},
		{Key: CategoryTemp, Label: "临时文件", Description: "上传、离线下载及备份恢复遗留的本地临时数据"},
		{Key: CategoryLogs, Label: "历史日志", Description: "保留今天，清理今天之前的按日日志"},
		{Key: CategoryCache, Label: "缓存数据", Description: "元数据缓存和 FUSE 本地读缓存，清理后会按需重建"},
		{Key: CategoryDatabase, Label: "数据库整理", Description: "清理无主记录、已废弃表，并可压缩 SQLite 空闲页"},
	}
	byKey := make(map[string]*Group, len(defs))
	for i := range defs {
		defs[i].Items = make([]Item, 0)
		byKey[defs[i].Key] = &defs[i]
	}
	for _, planned := range plan.items {
		group := byKey[planned.Category]
		if group == nil {
			continue
		}
		group.Items = append(group.Items, planned.Item)
		group.Count++
		group.SizeBytes += planned.SizeBytes
		group.MemoryBytes += planned.MemoryBytes
	}
	report := Report{ScanID: scanID, ScannedAt: plan.createdAt, ExpiresAt: plan.expiresAt, Groups: defs}
	for i := range report.Groups {
		sort.Slice(report.Groups[i].Items, func(a, b int) bool {
			left, right := report.Groups[i].Items[a], report.Groups[i].Items[b]
			if left.Risk != right.Risk {
				return riskOrder(left.Risk) < riskOrder(right.Risk)
			}
			return left.Path < right.Path
		})
		report.TotalCount += report.Groups[i].Count
		report.TotalSizeBytes += report.Groups[i].SizeBytes
		report.TotalMemoryBytes += report.Groups[i].MemoryBytes
	}
	return report
}

func (s *Service) activeStrmPaths(tasks []*domain.StrmTask) []string {
	root := filepath.Clean(s.opts.StrmDir)
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		rel := strm.TaskRelDir(task.GroupDir, task.OutputFolder)
		if rel == "" {
			continue
		}
		path := strm.TaskOutputDir(root, rel)
		if path != "" && pathWithin(root, path) && filepath.Clean(path) != root {
			out = append(out, filepath.Clean(path))
		}
	}
	return out
}

func (s *Service) scanScrapeIndexes(tasks []*domain.StrmTask) ([]planItem, error) {
	active := make(map[int64]struct{}, len(tasks))
	for _, task := range tasks {
		if task != nil {
			active[task.ID] = struct{}{}
		}
	}
	root := filepath.Join(s.opts.DataDir, "strmscrape")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []planItem
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sqlite") {
			continue
		}
		idText := strings.TrimSuffix(name, ".sqlite")
		taskID, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || taskID <= 0 {
			continue
		}
		if _, ok := active[taskID]; ok {
			continue
		}
		base := filepath.Join(root, name)
		var bytes, files int64
		for _, path := range []string{base, base + "-wal", base + "-shm"} {
			if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() {
				bytes += info.Size()
				files++
			}
		}
		out = append(out, planItem{
			Item: Item{
				ID:              itemID(kindScrapeIndex, base),
				Category:        CategoryStrm,
				Name:            "失效刮削索引",
				Path:            base,
				Reason:          fmt.Sprintf("对应的 STRM 任务 %d 已不存在", taskID),
				SizeBytes:       bytes,
				FileCount:       files,
				DefaultSelected: true,
				Risk:            RiskSafe,
			},
			Kind:       kindScrapeIndex,
			TargetPath: base,
			RootPath:   root,
			TaskID:     taskID,
		})
	}
	return out, nil
}

// LatestReport 返回最近一次扫描报告，没有任何扫描时 ok=false，供前端刷新后恢复卡片状态。
func (s *Service) LatestReport() (Report, bool) {
	if s == nil {
		return Report{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 报告不自动过期，返回最近一次扫描，直到重新扫描覆盖或程序重启。
	var bestID string
	var bestCreated time.Time
	var bestPlan scanPlan
	for id, plan := range s.scans {
		if bestID == "" || plan.createdAt.After(bestCreated) {
			bestID, bestCreated, bestPlan = id, plan.createdAt, plan
		}
	}
	if bestID == "" {
		return Report{}, false
	}
	return buildReport(bestID, bestPlan), true
}

func (s *Service) trimScansLocked() {
	for len(s.scans) > maxScanPlans {
		var oldestID string
		var oldestAt time.Time
		for id, plan := range s.scans {
			if oldestID == "" || plan.createdAt.Before(oldestAt) {
				oldestID, oldestAt = id, plan.createdAt
			}
		}
		delete(s.scans, oldestID)
	}
}
