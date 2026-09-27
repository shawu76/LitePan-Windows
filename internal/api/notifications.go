package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"litepan/internal/domain"
)

// streamNotificationUnread 以 SSE 推送未读通知数，断线由前端退回轮询。
func (h *Handler) streamNotificationUnread(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	// 先订阅再读初值：中间产生的变更会进缓冲，不会漏。订阅通道带 1 个缓冲，
	// 这里先丢弃可能残留的历史值，紧接着读到的初值才是权威的。
	counts, unsubscribe := h.notifications.Subscribe()
	defer unsubscribe()
	select {
	case <-counts:
	default:
	}

	// 初值必须在开始写 SSE 之前取：响应一旦开始写就不能再回错误状态。
	count, err := h.notifications.UnreadCount(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	sse, err := newSSEWriter(w)
	if err != nil {
		writeErr(w, err)
		return
	}

	sse.writeEvent("unread", unreadCountPayload(count))
	ticker := time.NewTicker(defaultSSEPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case next, ok := <-counts:
			if !ok {
				return
			}
			sse.writeEvent("unread", unreadCountPayload(next))
		case <-ticker.C:
			sse.writeEvent("ping", "{}")
		}
	}
}

func unreadCountPayload(count int) string {
	return fmt.Sprintf(`{"count":%d}`, count)
}

func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := h.notifications.List(r.Context(), limit, offset)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"items": toNotificationDTOs(items)})
}

func (h *Handler) notificationUnreadCount(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	count, err := h.notifications.UnreadCount(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"count": count})
}

func (h *Handler) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法通知 id"))
		return
	}
	if err := h.notifications.MarkRead(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (h *Handler) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	n, err := h.notifications.MarkAllRead(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"marked": n})
}

func (h *Handler) deleteNotification(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if id <= 0 {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法通知 id"))
		return
	}
	if err := h.notifications.Delete(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{})
}

func (h *Handler) deleteAllNotifications(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.notifications != nil) {
		return
	}
	n, err := h.notifications.DeleteAll(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"deleted": n})
}

type notificationDTO struct {
	ID        int64  `json:"id"`
	Level     string `json:"level"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	AccountID int64  `json:"account_id,omitempty"`
	RefID     int64  `json:"ref_id,omitempty"`
	IsRead    bool   `json:"is_read"`
	CreatedAt string `json:"created_at"`
}

func toNotificationDTOs(items []*domain.Notification) []notificationDTO {
	out := make([]notificationDTO, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		created := FormatAPITime(it.CreatedAt)
		out = append(out, notificationDTO{
			ID:        it.ID,
			Level:     it.Level,
			Category:  it.Category,
			Title:     it.Title,
			Message:   it.Message,
			AccountID: it.AccountID,
			RefID:     it.RefID,
			IsRead:    it.IsRead,
			CreatedAt: created,
		})
	}
	return out
}
