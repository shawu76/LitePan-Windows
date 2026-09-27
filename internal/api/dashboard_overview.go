package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"litepan/internal/domain"
	"litepan/internal/logx"
	"litepan/internal/settings"
)

type dashboardCacheStats struct {
	TotalKeys   int     `json:"total_keys"`
	TotalSize   int64   `json:"total_size_bytes"`
	Hits        int64   `json:"hits"`
	Misses      int64   `json:"misses"`
	Evictions   int64   `json:"evictions"`
	Expirations int64   `json:"expirations"`
	HitRate     float64 `json:"hit_rate"`
}

type dashboardOverviewDTO struct {
	Accounts            []accountDTO           `json:"accounts"`
	CacheStats          dashboardCacheStats    `json:"cache_stats"`
	CacheRetentionTasks []retentionTaskDTO     `json:"cache_retention_tasks"`
	CacheRetentionStats map[string]any         `json:"cache_retention_stats"`
	FuseMounts          []fuseMountDTO         `json:"fuse_mounts"`
	StrmTasks           []strmTaskDTO          `json:"strm_tasks"`
	OrganizeTasks       []mediaOrganizeTaskDTO `json:"organize_tasks"`
	Notifications       []notificationDTO      `json:"notifications"`
	UnreadCount         int                    `json:"unread_count"`
	LogStats            logx.Stats             `json:"log_stats"`
}

// degradeOverviewSection 单个分块失败时记警告并保留空数据，避免整个仪表盘变成 500。
func degradeOverviewSection(ctx context.Context, section string, err error) {
	if err == nil {
		return
	}
	// 请求已被取消（客户端提前断开）：非服务端故障，静默不记。
	if ctx != nil && ctx.Err() != nil {
		return
	}
	requestLogger(ctx).Warn("仪表盘概况分块加载失败", "section", section, "error", err.Error())
}

func (h *Handler) dashboardOverview(w http.ResponseWriter, r *http.Request) {
	out := dashboardOverviewDTO{
		Accounts:            []accountDTO{},
		CacheRetentionTasks: []retentionTaskDTO{},
		CacheRetentionStats: map[string]any{"total": 0, "running": 0, "paused": 0},
		FuseMounts:          []fuseMountDTO{},
		StrmTasks:           []strmTaskDTO{},
		OrganizeTasks:       []mediaOrganizeTaskDTO{},
		Notifications:       []notificationDTO{},
		LogStats:            logx.Stats{ByLevel: map[string]int{}, ByModule: map[string]int{}},
	}

	group, ctx := errgroup.WithContext(r.Context())
	group.Go(func() error {
		views, err := h.accountSvc.List(ctx)
		if err != nil {
			degradeOverviewSection(ctx, "accounts", err)
			return nil
		}
		items := make([]accountDTO, 0, len(views))
		for _, view := range views {
			dto := viewToDTO(view)
			dto.Config = dashboardAccountConfig(view.Config)
			items = append(items, dto)
		}
		out.Accounts = items
		return nil
	})
	group.Go(func() error {
		if h.cacheRetention == nil {
			return nil
		}
		tasks, err := h.cacheRetention.ListTasks(ctx)
		if err != nil {
			degradeOverviewSection(ctx, "cache_retention", err)
			return nil
		}
		items := make([]retentionTaskDTO, 0, len(tasks))
		running := 0
		for _, task := range tasks {
			if task.Status == domain.RetentionStatusRunning {
				running++
			}
			items = append(items, retentionTaskDTO{Status: task.Status, LastRefresh: formatOptionalTime(task.LastRefresh)})
		}
		out.CacheRetentionTasks = items
		out.CacheRetentionStats = map[string]any{"total": len(tasks), "running": running, "paused": len(tasks) - running}
		return nil
	})
	group.Go(func() error {
		if h.strm == nil {
			return nil
		}
		tasks, err := h.strm.ListTasks(ctx)
		if err != nil {
			degradeOverviewSection(ctx, "strm", err)
			return nil
		}
		items := make([]strmTaskDTO, 0, len(tasks))
		for _, task := range tasks {
			items = append(items, strmTaskDTO{
				Status: task.Status, GeneratedCount: task.GeneratedCount,
				LastScan: FormatAPITime(task.LastScan),
			})
		}
		out.StrmTasks = items
		return nil
	})
	group.Go(func() error {
		if h.mediaOrganize == nil {
			return nil
		}
		tasks, err := h.mediaOrganize.ListTasks(ctx)
		if err != nil {
			degradeOverviewSection(ctx, "media_organize", err)
			return nil
		}
		items := make([]mediaOrganizeTaskDTO, 0, len(tasks))
		for _, task := range tasks {
			var lastResult any
			if len(task.LastRunResult) > 0 {
				_ = json.Unmarshal(task.LastRunResult, &lastResult)
			}
			items = append(items, mediaOrganizeTaskDTO{
				ID: task.ID, TaskName: task.TaskName, Status: task.Status,
				LastRunAt: FormatAPITime(task.LastRunAt), LastRunResult: lastResult,
			})
		}
		out.OrganizeTasks = items
		return nil
	})
	group.Go(func() error {
		if h.fuse == nil {
			return nil
		}
		mounts, err := h.fuse.List(ctx)
		if err != nil {
			degradeOverviewSection(ctx, "fuse", err)
			return nil
		}
		items := make([]fuseMountDTO, 0, len(mounts))
		for _, mount := range mounts {
			items = append(items, fuseMountDTO{ID: mount.ID, Name: mount.Name, State: mount.State})
		}
		out.FuseMounts = items
		return nil
	})
	group.Go(func() error {
		if h.notifications == nil {
			return nil
		}
		items, err := h.notifications.List(ctx, 1, 0)
		if err != nil {
			degradeOverviewSection(ctx, "notifications", err)
			return nil
		}
		count, err := h.notifications.UnreadCount(ctx)
		if err != nil {
			degradeOverviewSection(ctx, "notifications_unread", err)
			return nil
		}
		out.Notifications = toNotificationDTOs(items)
		out.UnreadCount = count
		return nil
	})

	if h.cache != nil {
		stats := h.cache.Stats()
		out.CacheStats = dashboardCacheStats{
			TotalKeys: stats.Items, TotalSize: stats.Bytes, Hits: stats.Hits, Misses: stats.Misses,
			Evictions: stats.Evictions, Expirations: stats.Expirations,
			HitRate: roundHitRate(hitRateFrom(h.listHits)),
		}
	}
	if h.logs != nil && h.logs.Storage() != nil {
		ackAt := ""
		if h.settings != nil {
			ackAt = strings.TrimSpace(h.settings.String(settings.KeyLogErrorAckAt))
		}
		// StatsRecent 只读最近两天的日志文件，StatsFiltered 遍历全部历史文件会拖死接口。
		statsStart := time.Now()
		out.LogStats = h.logs.Storage().StatsRecent(logx.LevelInfo, ackAt)
		if elapsed := time.Since(statsStart); elapsed >= slowOverviewStatsThreshold {
			requestLogger(r.Context()).Warn("仪表盘日志统计耗时过长",
				"elapsed", elapsed.String(), "total", out.LogStats.Total)
		}
	}
	// 各分块失败已在内部降级，Wait 不会返回错误。
	group.Wait()
	writeOK(w, out)
}

// slowOverviewStatsThreshold 超过该阈值就把日志统计耗时记进日志，用于定位仪表盘请求超时。
const slowOverviewStatsThreshold = 2 * time.Second

func dashboardAccountConfig(raw string) string {
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &values) != nil {
		return `{}`
	}
	safe := make(map[string]json.RawMessage, 2)
	for _, key := range []string{"download_mode", "downloadMode"} {
		if value, ok := values[key]; ok {
			safe[key] = value
		}
	}
	encoded, _ := json.Marshal(safe)
	return string(encoded)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return FormatAPITime(*value)
}
