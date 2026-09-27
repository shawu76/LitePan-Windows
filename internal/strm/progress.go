package strm

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/domain"
)

const (
	ScanPhaseScan            = "scanning"
	ScanPhaseMetadataCompare = "comparing_metadata"
	ScanPhaseMetadata        = "syncing_metadata"
	ScanPhaseMetadataUpload  = "uploading_metadata"
	ScanPhaseMetadataCleanup = "cleaning_metadata"
)

// metadataProgressUnset 是 MetadataTotal/MetadataDone 的哨兵值，表示不上报该字段；用负数，0 是合法进度。
const metadataProgressUnset = -1

type ScanProgressUpdate struct {
	Phase     string
	DirDelta  int
	FileDelta int
	Label     string
	// 为哨兵值时接收方保留原值；构造 update 要显式写入哨兵，否则 0 会被当成进度而清空。
	MetadataTotal int
	MetadataDone  int
}

type ScanProgressReporter func(ScanProgressUpdate)

type liveScanProgress struct {
	Phase         string
	CurrentLabel  string
	ScannedDirs   int
	ScannedFiles  int
	MetadataTotal int
	MetadataDone  int
	StartedAt     time.Time
}

type TaskListMeta struct {
	IsScanning        bool
	Phase             string
	CurrentLabel      string
	ScannedDirs       int
	ScannedFiles      int
	MetadataTotal     int
	MetadataDone      int
	StartedAt         time.Time
	CurrentDurationMs int64
	StaleRunning      bool
}

func (s *Service) beginLiveScan(taskID int64) ScanProgressReporter {
	s.mu.Lock()
	if s.scanProgress == nil {
		s.scanProgress = make(map[int64]liveScanProgress)
	}
	s.scanProgress[taskID] = liveScanProgress{
		Phase:     ScanPhaseScan,
		StartedAt: time.Now(),
	}
	s.mu.Unlock()
	return func(u ScanProgressUpdate) {
		if !scanProgressUpdateHasWork(u) {
			return
		}
		s.mu.Lock()
		p := s.scanProgress[taskID]
		p.ScannedDirs += u.DirDelta
		p.ScannedFiles += u.FileDelta
		if u.Phase != "" {
			p.Phase = u.Phase
		}
		if u.Label != "" {
			p.CurrentLabel = u.Label
		}
		if u.MetadataTotal >= 0 {
			p.MetadataTotal = u.MetadataTotal
		}
		if u.MetadataDone >= 0 {
			p.MetadataDone = u.MetadataDone
		}
		s.scanProgress[taskID] = p
		s.mu.Unlock()
	}
}

func scanProgressUpdateHasWork(u ScanProgressUpdate) bool {
	return u.DirDelta != 0 || u.FileDelta != 0 || u.Phase != "" || u.Label != "" ||
		u.MetadataTotal >= 0 || u.MetadataDone >= 0
}

func (s *Service) endLiveScan(taskID int64) {
	s.mu.Lock()
	delete(s.scanProgress, taskID)
	s.mu.Unlock()
}

func (s *Service) isTaskRunning(taskID int64) bool {
	s.mu.Lock()
	ok := s.running[taskID]
	s.mu.Unlock()
	return ok
}

func (s *Service) IsTaskRunning(taskID int64) bool {
	if s == nil {
		return false
	}
	return s.isTaskRunning(taskID)
}

func (s *Service) TaskListMeta(taskID int64, dbStatus string) TaskListMeta {
	s.mu.Lock()
	running := s.running[taskID]
	prog, hasProg := s.scanProgress[taskID]
	s.mu.Unlock()

	meta := TaskListMeta{IsScanning: running}
	if hasProg {
		meta.Phase = prog.Phase
		meta.CurrentLabel = prog.CurrentLabel
		meta.ScannedDirs = prog.ScannedDirs
		meta.ScannedFiles = prog.ScannedFiles
		meta.MetadataTotal = prog.MetadataTotal
		meta.MetadataDone = prog.MetadataDone
		meta.StartedAt = prog.StartedAt
		if running && !prog.StartedAt.IsZero() {
			meta.CurrentDurationMs = time.Since(prog.StartedAt).Milliseconds()
		}
	}
	if dbStatus == domain.StrmStatusRunning && !running {
		meta.StaleRunning = true
	}
	return meta
}

func reportScanProgress(r ScanProgressReporter, phase string, dirDelta, fileDelta int, label string) {
	if r == nil {
		return
	}
	r(ScanProgressUpdate{
		Phase:         phase,
		DirDelta:      dirDelta,
		FileDelta:     fileDelta,
		Label:         label,
		MetadataTotal: metadataProgressUnset,
		MetadataDone:  metadataProgressUnset,
	})
}

func reportMetadataProgress(r ScanProgressReporter, done, total int, label string) {
	reportMetadataActionProgress(r, ScanPhaseMetadata, done, total, label)
}

func reportMetadataActionProgress(r ScanProgressReporter, phase string, done, total int, label string) {
	if r == nil {
		return
	}
	u := ScanProgressUpdate{
		Phase:         phase,
		Label:         label,
		MetadataTotal: metadataProgressUnset,
		MetadataDone:  metadataProgressUnset,
	}
	if total >= 0 {
		u.MetadataTotal = total
	}
	if done >= 0 {
		u.MetadataDone = done
	}
	r(u)
}

func dirProgressLabel(relDirs []string) string {
	if len(relDirs) == 0 {
		return "/"
	}
	if len(relDirs) == 1 {
		return relDirs[0]
	}
	return relDirs[len(relDirs)-2] + " / " + relDirs[len(relDirs)-1]
}

func metadataProgressLabel(relPath string) string {
	p := filepath.ToSlash(strings.TrimSpace(relPath))
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

func (s *Service) FixStaleRunningAsync(taskID int64) {
	if s.isTaskRunning(taskID) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.repo.UpdateScan(ctx, taskID, domain.StrmScanPatch{
			Status: domain.StrmStatusActive,
		}); err != nil {
			s.log.Warn("STRM 修复残留运行状态失败", "task_id", taskID, "err", err)
		}
	}()
}

func (s *Service) recoverStaleRunningTasks(ctx context.Context) {
	tasks, err := s.repo.List(ctx)
	if err != nil {
		s.log.Warn("STRM 恢复残留运行状态失败", "err", err)
		return
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, task := range tasks {
		if task.Status != domain.StrmStatusRunning {
			continue
		}
		if err := s.repo.UpdateScan(persistCtx, task.ID, domain.StrmScanPatch{
			Status: domain.StrmStatusActive,
		}); err != nil {
			s.log.Warn("STRM 恢复残留运行任务失败", "task_id", task.ID, "err", err)
		}
	}
}
