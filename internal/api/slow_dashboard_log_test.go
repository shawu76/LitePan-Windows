package api

import (
	"strings"
	"testing"
	"time"
)

func TestSlowRequestLogsSuppressAndRecover(t *testing.T) {
	var logs slowRequestLogs
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.Local)

	if suppressed, ok := logs.shouldLog("/api/logs/stats", now); !ok || suppressed != 0 {
		t.Fatalf("首次慢请求应立即记录: suppressed=%d ok=%v", suppressed, ok)
	}
	for i := 0; i < 3; i++ {
		if _, ok := logs.shouldLog("/api/logs/stats", now.Add(time.Duration(i+1)*time.Minute)); ok {
			t.Fatal("限频周期内不应重复记录")
		}
	}
	if suppressed, ok := logs.shouldLog("/api/logs/stats", now.Add(slowDashboardLogInterval)); !ok || suppressed != 3 {
		t.Fatalf("限频周期后应汇总被抑制次数: suppressed=%d ok=%v", suppressed, ok)
	}

	logs.recovered("/api/logs/stats")
	if suppressed, ok := logs.shouldLog("/api/logs/stats", now.Add(slowDashboardLogInterval+time.Minute)); !ok || suppressed != 0 {
		t.Fatalf("恢复后再次变慢应立即记录: suppressed=%d ok=%v", suppressed, ok)
	}
}

func TestSlowRequestLogsAreIsolatedByPath(t *testing.T) {
	var logs slowRequestLogs
	now := time.Now()
	if _, ok := logs.shouldLog("/api/admin/accounts", now); !ok {
		t.Fatal("第一个接口首次慢请求应记录")
	}
	if _, ok := logs.shouldLog("/api/admin/notifications/unread-count", now); !ok {
		t.Fatal("不同接口不应共享限频状态")
	}
}

// 文案必须带接口路径：只写“概况接口慢”会让人误以为是运行概况页。
func TestSlowRequestLogMessageNamesPath(t *testing.T) {
	got := slowRequestLogMessage("/api/admin/notifications/unread-count", 1500*time.Millisecond)
	if !strings.Contains(got, "/api/admin/notifications/unread-count") {
		t.Fatalf("文案应包含接口路径，实际 %q", got)
	}
	if !strings.Contains(got, "1500ms") {
		t.Fatalf("文案应包含耗时，实际 %q", got)
	}
}
