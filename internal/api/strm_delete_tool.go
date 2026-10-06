package api

import (
	"net/http"

	"litepan/internal/strmdelete"
)

func (h *Handler) getStrmDeleteTool(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmDelete != nil) {
		return
	}
	status, err := h.strmDelete.Status(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, status)
}

func (h *Handler) updateStrmDeleteTool(w http.ResponseWriter, r *http.Request) {
	if !ensureServiceReady(w, h.strmDelete != nil) {
		return
	}
	var cfg strmdelete.Config
	if err := decodeJSON(r, &cfg); err != nil {
		writeErr(w, err)
		return
	}
	status, err := h.strmDelete.UpdateConfig(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, status)
}

func (h *Handler) confirmStrmDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err = h.strmDelete.Confirm(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	if h.notifications != nil {
		_, _ = h.notifications.DeleteByRef(r.Context(), "strm_delete_confirm", id)
	}
	writeOK(w, map[string]bool{"deleted": true})
}

func (h *Handler) cancelStrmDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err = h.strmDelete.Cancel(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	if h.notifications != nil {
		_, _ = h.notifications.DeleteByRef(r.Context(), "strm_delete_confirm", id)
	}
	writeOK(w, map[string]bool{"cancelled": true})
}
