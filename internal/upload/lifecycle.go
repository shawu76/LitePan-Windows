package upload

import (
	"context"
	"strings"
	"time"

	"litepan/pkg/timeutil"
)

func (m *Manager) BatchPause(_ context.Context, taskIDs []string) BatchControlResult {
	result := BatchControlResult{}
	seen := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		if _, ok := seen[taskID]; ok {
			continue
		}
		seen[taskID] = struct{}{}
		_, ok, changed := m.pause(taskID)
		if !ok {
			result.MissingTaskIDs = append(result.MissingTaskIDs, taskID)
			continue
		}
		if changed {
			result.UpdatedTaskIDs = append(result.UpdatedTaskIDs, taskID)
		}
	}
	return result
}

func (m *Manager) Pause(_ context.Context, taskID string) (*Task, bool) {
	task, found, _ := m.pause(taskID)
	return task, found
}

func (m *Manager) pause(taskID string) (*Task, bool, bool) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil, false, false
	}
	if m.stopping {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true, false
	}
	if !isActiveUploadStatus(st.Status) {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true, false
	}
	st.cancelMode = "pause"
	st.Status = StatusPaused
	st.resumePriority = false
	st.SpeedBytesPerSecond = 0
	st.Message = pausedMessage(st)
	st.Error = ""
	st.UpdatedAt = timeutil.UnixFloat(time.Now())
	cancel := st.cancel
	snap := st
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.runCond.Broadcast()
	_ = m.persistTask(snap)
	m.broadcast(taskID)
	task, found := m.Get(context.Background(), taskID)
	return task, found, true
}

func (m *Manager) Resume(ctx context.Context, taskID string) (*Task, bool) {
	m.mu.Lock()
	st, ok := m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	if !isResumableUploadStatus(st.Status) {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true
	}
	done := st.runDone
	m.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return m.Get(context.Background(), taskID)
		}
	}

	m.mu.Lock()
	st, ok = m.tasks[taskID]
	if !ok {
		m.mu.Unlock()
		return nil, false
	}
	if m.stopping {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true
	}
	if !isResumableUploadStatus(st.Status) {
		t := m.snapshot(st)
		m.mu.Unlock()
		return t, true
	}
	if requiredLocalFileMissing(st) {
		markMissingLocalFileFailed(st)
		snap := st
		task := m.snapshot(st)
		m.mu.Unlock()
		_ = m.persistTask(snap)
		m.broadcast(taskID)
		return task, true
	}
	m.queueOrder++
	st.QueueOrder = m.queueOrder
	st.Status = StatusPending
	st.resumePriority = true
	st.Error = ""
	st.Result = retainBatchRootMetadata(st.Result)
	st.cancelMode = ""
	st.runDone = make(chan struct{})
	progress, uploaded := resumedProgress(st)
	if len(st.resumeData) > 0 {
		st.Progress = progress
		st.UploadedBytes = uploaded
		st.Message = "准备继续上传"
	} else if isCrossTransferDownload(st) {
		st.Progress = calcProgress(st.DownloadedBytes, st.TotalBytes)
		st.UploadedBytes = 0
		st.Message = "准备继续源盘下载"
	} else {
		st.Progress = 0
		st.UploadedBytes = 0
		st.Message = "等待上传"
	}
	snap := st
	task := m.snapshot(st)
	m.mu.Unlock()
	_ = m.persistTask(snap)
	go m.runTask(taskID)
	m.broadcast(taskID)
	return task, true
}
