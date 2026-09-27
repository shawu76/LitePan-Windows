package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	slowDashboardRequestThreshold = time.Second
	slowDashboardLogInterval      = 30 * time.Minute
)

type slowRequestLogEntry struct {
	lastLogged time.Time
	suppressed int
}

type slowRequestLogs struct {
	mu      sync.Mutex
	entries map[string]slowRequestLogEntry
}

var dashboardOverviewPaths = map[string]struct{}{
	"/api/admin/accounts":                   {},
	"/api/admin/cache-retention/configs":    {},
	"/api/admin/cache-retention/stats":      {},
	"/api/admin/cache/stats":                {},
	"/api/admin/fuse/mounts":                {},
	"/api/admin/media-organize/tasks":       {},
	"/api/admin/notifications":              {},
	"/api/admin/notifications/unread-count": {},
	"/api/admin/strm/tasks":                 {},
	"/api/logs/stats":                       {},
}

// logSlowDashboardRequests 记录后台概况相关接口里偏慢的请求，仅写入 Debug 级别：
// 这些接口慢一点通常不影响使用，属于排查信息，不该出现在用户日常看的日志里。
func (h *Handler) logSlowDashboardRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !isDashboardOverviewPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		startedAt := time.Now()
		next.ServeHTTP(w, r)
		elapsed := time.Since(startedAt)
		if elapsed < slowDashboardRequestThreshold {
			h.slowLogs.recovered(r.URL.Path)
			return
		}
		suppressed, ok := h.slowLogs.shouldLog(r.URL.Path, time.Now())
		if !ok {
			return
		}
		// 降为 Debug：这是给排查用的诊断信息，不是用户该关心的状态。
		// 接口慢一点但页面照常加载时，报在常规日志里只会让人焦虑；
		// 把日志级别调到 Debug 就能看到是哪个接口、慢了多少。
		requestLogger(r.Context()).Debug(
			slowRequestLogMessage(r.URL.Path, elapsed),
			"method", r.Method,
			"path", r.URL.Path,
			"duration_ms", elapsed.Milliseconds(),
			"suppressed_count", suppressed,
		)
	})
}

func (s *slowRequestLogs) shouldLog(path string, now time.Time) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]slowRequestLogEntry)
	}
	entry := s.entries[path]
	if !entry.lastLogged.IsZero() && now.Sub(entry.lastLogged) < slowDashboardLogInterval {
		entry.suppressed++
		s.entries[path] = entry
		return 0, false
	}
	suppressed := entry.suppressed
	s.entries[path] = slowRequestLogEntry{lastLogged: now}
	return suppressed, true
}

func (s *slowRequestLogs) recovered(path string) {
	s.mu.Lock()
	delete(s.entries, path)
	s.mu.Unlock()
}

// slowRequestLogMessage 里必须带上接口路径：只写“概况接口慢”会让人
// 误以为是运行概况页，实际可能是日志页、账号页或通知小红点的轮询。
func slowRequestLogMessage(path string, elapsed time.Duration) string {
	return fmt.Sprintf("后台接口响应较慢：%s（%dms）", path, elapsed.Milliseconds())
}

func isDashboardOverviewPath(path string) bool {
	_, ok := dashboardOverviewPaths[path]
	return ok
}
