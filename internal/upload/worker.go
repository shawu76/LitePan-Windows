package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"litepan/internal/core/driverexec"
	"litepan/internal/domain"
	"litepan/internal/driver"
	"litepan/internal/eventbus"
	"litepan/internal/playback"
	"litepan/pkg/speedsmoother"
	"litepan/pkg/timeutil"
)

func (m *Manager) executeCrossTransferDownload(ctx context.Context, taskID string) bool {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	localPath := st.localPath
	sourceAccountID := st.SourceAccountID
	sourceFileID := st.SourceFileID
	totalBytes := st.TotalBytes
	m.mu.Unlock()

	if m.playback == nil {
		m.failTask(taskID, "播放服务未就绪")
		return false
	}
	if sourceAccountID <= 0 || strings.TrimSpace(sourceFileID) == "" {
		m.failTask(taskID, "跨盘源文件信息不完整")
		return false
	}
	if localPath == "" {
		m.failTask(taskID, "跨盘临时文件路径为空")
		return false
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		m.failTask(taskID, err.Error())
		return false
	}
	existingDownloaded := int64(0)
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		existingDownloaded = info.Size()
	}
	if totalBytes > 0 && existingDownloaded > totalBytes {
		existingDownloaded = 0
	}

	started := false
	m.patch(taskID, func(st *taskState) {
		if ctx.Err() != nil || st.Status != StatusPending {
			return
		}
		started = true
		st.Status = StatusRunning
		st.Phase = PhaseDownloading
		st.Progress = calcProgress(existingDownloaded, totalBytes)
		st.DownloadedBytes = existingDownloaded
		st.UploadedBytes = 0
		st.SpeedBytesPerSecond = 0
		if existingDownloaded > 0 {
			st.Message = "正在继续从源盘下载"
		} else {
			st.Message = "正在从源盘下载"
		}
		st.Error = ""
		st.resumeData = nil
		st.speed.Reset()
	})
	if !started {
		return false
	}

	res, err := m.playback.Resolve(ctx, sourceAccountID, sourceFileID, "", false, false)
	if err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, translateError(err.Error()))
		return false
	}
	if res.Link.URL == "" {
		m.finishCrossTransferDownloadError(ctx, taskID, "无法解析源盘下载地址")
		return false
	}
	if res.File.Size > 0 {
		totalBytes = res.File.Size
	} else if res.Link.Size > 0 {
		totalBytes = res.Link.Size
	}
	if totalBytes > 0 && existingDownloaded > totalBytes {
		existingDownloaded = 0
	}
	if totalBytes > 0 && existingDownloaded == totalBytes {
		return m.finishCrossTransferDownloadSuccess(ctx, taskID, totalBytes, totalBytes)
	}
	resumed := totalBytes > 0 && existingDownloaded > 0
	file, err := openCrossTransferTempFile(localPath, resumed)
	if err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
		return false
	}
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()

	if resumed {
		if _, err := file.Seek(existingDownloaded, io.SeekStart); err != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
			return false
		}
	}

	downloaded := existingDownloaded
	sessionDownloaded := int64(0)
	speed := speedsmoother.NewDefault()
	lastEmit := time.Now()
	progress := &crossTransferProgressWriter{writer: file, onWrite: func(n int64) {
		downloaded += n
		sessionDownloaded += n
		now := time.Now()
		if now.Sub(lastEmit) < progressInterval {
			return
		}
		message := "正在从源盘下载"
		if resumed {
			message = "正在继续从源盘下载"
		}
		m.updateDownloadProgress(taskID, downloaded, totalBytes, message, speed.Sample(sessionDownloaded, now, "download").Display)
		lastEmit = now
	}}
	if totalBytes > 0 {
		err = m.playback.CopyOriginalRange(ctx, progress, sourceAccountID, sourceFileID, res, existingDownloaded, totalBytes-1)
	} else {
		err = m.playback.CopyOriginalFull(ctx, progress, sourceAccountID, sourceFileID, res)
		totalBytes = downloaded
	}
	if err != nil && errors.Is(err, playback.ErrInvalidRangeResponse) {
		if closeErr := file.Close(); closeErr != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, closeErr.Error())
			return false
		}
		file, err = openCrossTransferTempFile(localPath, false)
		if err != nil {
			m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
			return false
		}
		resumed = false
		downloaded = 0
		sessionDownloaded = 0
		speed.Reset()
		lastEmit = time.Now()
		progress.writer = file
		err = m.playback.CopyOriginalFull(ctx, progress, sourceAccountID, sourceFileID, res)
	}
	if err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, translateError(err.Error()))
		return false
	}
	if downloaded <= 0 {
		m.finishCrossTransferDownloadError(ctx, taskID, "源盘下载为空文件")
		return false
	}
	if totalBytes <= 0 {
		totalBytes = downloaded
	}
	if downloaded != totalBytes {
		m.finishCrossTransferDownloadError(ctx, taskID, fmt.Sprintf("源盘下载不完整：已下载 %d 字节，预期 %d 字节", downloaded, totalBytes))
		return false
	}
	if err := file.Sync(); err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
		return false
	}
	if err := file.Close(); err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, err.Error())
		return false
	}
	file = nil
	return m.finishCrossTransferDownloadSuccess(ctx, taskID, downloaded, totalBytes)
}

func (m *Manager) finishCrossTransferDownloadSuccess(ctx context.Context, taskID string, downloaded, totalBytes int64) bool {
	folderID, displayPath, err := m.resolveCrossTransferTarget(ctx, taskID)
	if err != nil {
		m.finishCrossTransferDownloadError(ctx, taskID, translateError(err.Error()))
		return false
	}
	m.patch(taskID, func(st *taskState) {
		if ctx.Err() != nil {
			return
		}
		st.Status = StatusPending
		st.Phase = PhaseUploading
		st.Progress = 0
		st.DownloadedBytes = downloaded
		st.TotalBytes = totalBytes
		st.TargetPath = folderID
		st.TargetDisplayPath = displayPath
		st.SpeedBytesPerSecond = 0
		st.Message = "源盘下载完成，等待上传"
		st.Error = ""
		st.speed.Reset()
	})
	return true
}

func (m *Manager) executeUpload(ctx context.Context, taskID string) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	resume := cloneMap(st.resumeData)
	resuming := len(resume) > 0
	progress, uploaded := resumedProgress(st)
	accountID := st.AccountID
	localPath := st.localPath
	cleanupLocalMode := st.CleanupLocalMode
	cleanupLocalPath := st.CleanupLocalPath
	fileName := st.FileName
	targetPath := st.TargetPath
	conflictPolicy := st.conflictPolicy
	sourceType := st.SourceType
	m.mu.Unlock()

	msg := "正在上传到网盘"
	if resuming {
		msg = "正在继续上传到网盘"
	}
	if sourceType == SourceTypeCrossTransfer {
		msg = "正在上传到目标网盘"
		if resuming {
			msg = "正在继续上传到目标网盘"
		}
	}
	started := false
	m.patch(taskID, func(st *taskState) {
		if ctx.Err() != nil || st.Status != StatusPending {
			return
		}
		started = true
		st.Status = StatusRunning
		st.Phase = PhaseUploading
		st.Progress = progress
		st.UploadedBytes = uploaded
		st.SpeedBytesPerSecond = 0
		st.Message = msg
		st.Error = ""
		st.speed.Reset()
	})
	if !started {
		return
	}

	entryName := uploadEntryName(fileName)

	result, err := m.runLocalUpload(ctx, accountID, driver.LocalUploadRequest{
		LocalPath:      localPath,
		FileName:       entryName,
		ParentID:       targetPath,
		ConflictPolicy: conflictPolicy,
		ResumeState:    resume,
		OnResumeState: func(state map[string]any) {
			m.applyResumeState(taskID, state)
		},
		OnProgress: func(uploaded, total int64, message string) {
			m.updateProgress(taskID, uploaded, total, message)
		},
	})

	m.mu.Lock()
	st, ok = m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	mode := st.cancelMode
	m.mu.Unlock()

	if err != nil {
		if ctx.Err() != nil {
			if mode == "pause" {
				m.patch(taskID, func(st *taskState) {
					st.Status = StatusPaused
					st.SpeedBytesPerSecond = 0
					st.Message = pausedMessage(st)
				})
				return
			}
			m.patch(taskID, func(st *taskState) {
				st.Status = StatusCanceled
				st.SpeedBytesPerSecond = 0
				st.Message = "上传任务已取消"
				st.Error = "上传任务已取消"
			})
			return
		}
		if shouldResetResumeState(err.Error()) {
			m.patch(taskID, func(st *taskState) {
				st.Status = StatusFailed
				st.SpeedBytesPerSecond = 0
				st.Message = "上传失败"
				st.Error = translateError(err.Error())
				st.resumeData = nil
				st.UploadedBytes = 0
				st.Progress = 0
			})
			return
		}
		m.failTask(taskID, err.Error())
		return
	}

	status := StatusSuccess
	msg = result.Message
	if result.Skipped {
		status = StatusSkipped
	}
	m.cleanupLocalSource(localPath, cleanupLocalPath, cleanupLocalMode)
	m.patch(taskID, func(st *taskState) {
		st.Status = status
		st.Phase = PhaseUploading
		st.Progress = 100
		st.DownloadedBytes = st.TotalBytes
		st.UploadedBytes = st.TotalBytes
		st.SpeedBytesPerSecond = 0
		st.Message = msg
		st.Error = ""
		st.resumeData = nil
		resultMeta := retainBatchRootMetadata(st.Result)
		st.Result = map[string]any{
			"file_id":   result.FileID,
			"parent_id": result.ParentID,
			"file_name": result.FileName,
			"size":      result.Size,
		}
		for key, value := range resultMeta {
			st.Result[key] = value
		}
	})
	if m.files == nil && m.bus != nil {
		parentID := result.ParentID
		if parentID == "" {
			parentID = targetPath
		}
		m.bus.Publish(context.Background(), eventbus.FileMutated{
			AccountID: accountID,
			Op:        "upload",
			ParentID:  parentID,
			FileID:    result.FileID,
		})
	}
	m.publishOfflineHandoffCompleted(taskID)
}

func shouldResetResumeState(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	return strings.Contains(lower, "invalidpartorder") || strings.Contains(lower, "previous part hash context")
}

const downloadPersistInterval = 2 * time.Second

func (m *Manager) updateDownloadProgress(taskID string, downloaded, total int64, message string, speed float64) {
	if total <= 0 {
		total = 1
	}
	progress := calcProgress(downloaded, total)
	now := time.Now()
	var snap *taskState
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if isCompletedUploadStatus(st.Status) {
		m.mu.Unlock()
		return
	}
	st.Status = StatusRunning
	st.Phase = PhaseDownloading
	st.Progress = progress
	st.DownloadedBytes = downloaded
	st.TotalBytes = total
	st.SpeedBytesPerSecond = speed
	st.Message = message
	st.Error = ""
	st.UpdatedAt = timeutil.UnixFloat(now)
	if st.lastEmit.IsZero() || now.Sub(st.lastEmit) >= downloadPersistInterval || downloaded >= total {
		st.lastEmit = now
		snap = st
	}
	m.mu.Unlock()
	if snap != nil {
		_ = m.persistTask(snap)
	}
	m.broadcast(taskID)
}

func (m *Manager) finishCrossTransferDownloadError(ctx context.Context, taskID, errMsg string) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	mode := st.cancelMode
	m.mu.Unlock()

	if ctx.Err() != nil {
		if mode == "pause" {
			m.patch(taskID, func(st *taskState) {
				st.Status = StatusPaused
				st.Phase = PhaseDownloading
				st.SpeedBytesPerSecond = 0
				st.Message = "源盘下载已暂停"
				st.Error = ""
			})
			return
		}
		m.patch(taskID, func(st *taskState) {
			st.Status = StatusCanceled
			st.Phase = PhaseDownloading
			st.SpeedBytesPerSecond = 0
			st.Message = "跨盘任务已取消"
			st.Error = "跨盘任务已取消"
		})
		return
	}
	m.patch(taskID, func(st *taskState) {
		st.Status = StatusFailed
		st.Phase = PhaseDownloading
		st.SpeedBytesPerSecond = 0
		st.Message = "源盘下载失败"
		st.Error = errMsg
	})
}

func (m *Manager) resolveCrossTransferTarget(ctx context.Context, taskID string) (string, string, error) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return "", "", domain.Errorf(domain.CodeNotFound, "上传任务不存在")
	}
	accountID := st.AccountID
	rootID := st.TargetPath
	relDir := st.RelDir
	displayPath := st.TargetDisplayPath
	m.mu.Unlock()

	folderID, err := ensureUploadTargetDir(ctx, m.files, m.targetDirCache, accountID, rootID, relDir)
	if err != nil {
		return "", "", err
	}
	return folderID, joinUploadDisplayPath(displayPath, relDir), nil
}

func openCrossTransferTempFile(localPath string, resume bool) (*os.File, error) {
	if resume {
		return os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE, 0o644)
	}
	return os.Create(localPath)
}

type crossTransferProgressWriter struct {
	writer  io.Writer
	onWrite func(int64)
}

func (w *crossTransferProgressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 && w.onWrite != nil {
		w.onWrite(int64(n))
	}
	return n, err
}

func (m *Manager) deleteUploadedFile(ctx context.Context, st *taskState) error {
	if st.Result == nil {
		return nil
	}
	raw, _ := st.Result["file_id"].(string)
	if raw == "" {
		return nil
	}
	if err := m.exec.Check(ctx, st.AccountID); err != nil {
		return err
	}
	err := m.exec.Run(ctx, st.AccountID, func(drv driver.Driver) error {
		deleter, err := driverexec.Require[driver.Deleter](drv)
		if err != nil {
			return err
		}
		return deleter.DeleteFiles(ctx, []string{raw})
	})
	if err != nil {
		return err
	}
	m.publishUploadedFileDeleted(st, raw)
	return nil
}

// deleteUploadedFiles 批量删除已上传的网盘文件（一次请求，驱动支持批量删除）。
func (m *Manager) deleteUploadedFiles(ctx context.Context, accountID int64, fileIDs []string) error {
	if len(fileIDs) == 0 {
		return nil
	}
	if err := m.exec.Check(ctx, accountID); err != nil {
		return err
	}
	return m.exec.Run(ctx, accountID, func(drv driver.Driver) error {
		deleter, err := driverexec.Require[driver.Deleter](drv)
		if err != nil {
			return err
		}
		return deleter.DeleteFiles(ctx, fileIDs)
	})
}

func (m *Manager) publishUploadedFileDeleted(st *taskState, fileID string) {
	if m.bus == nil {
		return
	}
	parentID, _ := st.Result["parent_id"].(string)
	if parentID == "" {
		parentID = st.TargetPath
	}
	m.bus.Publish(context.Background(), eventbus.FileMutated{
		AccountID: st.AccountID,
		Op:        "delete",
		ParentID:  parentID,
		FileIDs:   []string{fileID},
	})
}
