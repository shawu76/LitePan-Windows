package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"litepan/internal/domain"
	"litepan/internal/playback"
	"litepan/internal/strm"
)

func (h *Handler) strmPlay(w http.ResponseWriter, r *http.Request) {
	if h.strm == nil || h.playback == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	h.logSTRMPlayEntry(r, "strm_play")
	accountID, err := parsePathInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	fileID, err := strm.DecodeFileKey(chi.URLParam(r, "file_key"))
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 file_key"))
		return
	}
	if err := h.authorizeSTRMPlay(r); err != nil {
		h.logSTRMPlayDenied(r, "strm_play", err)
		writeErr(w, err)
		return
	}
	fileName, _ := url.PathUnescape(chi.URLParam(r, "filename"))
	if err := h.playback.ServeHTTP(w, r, playback.Request{
		AccountID: accountID,
		FileID:    fileID,
	}, playback.Intent{FileName: fileName}); err != nil {
		writeErr(w, err)
	}
}

func (h *Handler) strmPathPlay(w http.ResponseWriter, r *http.Request) {
	if h.strm == nil || h.playback == nil || h.files == nil {
		writeErr(w, domain.Errf(domain.CodeNotImplement))
		return
	}
	h.logSTRMPlayEntry(r, "strm_path_play")
	accountID, err := parsePathInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	rootID, err := strm.DecodePathKey(chi.URLParam(r, "root_key"))
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 root_key"))
		return
	}
	relativePath, err := strm.DecodePathKey(chi.URLParam(r, "path_key"))
	if err != nil {
		writeErr(w, domain.Errorf(domain.CodeValidation, "非法 path_key"))
		return
	}
	if err := h.authorizeSTRMPlay(r); err != nil {
		h.logSTRMPlayDenied(r, "strm_path_play", err)
		writeErr(w, err)
		return
	}
	item, err := h.files.ResolvePath(r.Context(), accountID, rootID, relativePath)
	if err != nil {
		writeErr(w, err)
		return
	}
	fileName, _ := url.PathUnescape(chi.URLParam(r, "filename"))
	if fileName == "" {
		fileName = item.Name
	}
	if err := h.playback.ServeHTTP(w, r, playback.Request{AccountID: accountID, FileID: item.ID}, playback.Intent{FileName: fileName}); err != nil {
		writeErr(w, err)
	}
}

// logSTRMPlayEntry 在 debug 级别记录 STRM 播放入口请求。
// 这是「客户端回来取流」的必经入口，且日志打在 token/签名校验之前，
// 因此能把「压根没来取」和「来了但被鉴权挡掉」分开——排查直读类播放问题时这是关键分界。
// 只记 token/签名的有无，不记它们的值（凭据不能进日志）。
func (h *Handler) logSTRMPlayEntry(r *http.Request, route string) {
	if h.log == nil {
		return
	}
	h.log.Debug("STRM 播放入口",
		"route", route,
		"user_agent", r.UserAgent(),
		"has_token", strings.TrimSpace(chi.URLParam(r, "token")) != "",
		"has_signature", strings.TrimSpace(chi.URLParam(r, "signature")) != "",
		"signature_required", h.strm != nil && h.strm.SignatureEnabled(),
	)
}

// logSTRMPlayDenied 记录鉴权失败的取流请求，用来区分「回来了但被拒」和「压根没回来」。
func (h *Handler) logSTRMPlayDenied(r *http.Request, route string, err error) {
	if h.log == nil {
		return
	}
	h.log.Debug("STRM 播放鉴权失败", "route", route, "user_agent", r.UserAgent(), "error", err)
}

func (h *Handler) authorizeSTRMPlay(r *http.Request) error {
	ok, err := h.strm.MatchToken(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		return err
	}
	if !ok {
		return domain.Errf(domain.CodePermissionDenied)
	}
	signature := chi.URLParam(r, "signature")
	if h.strm.SignatureEnabled() {
		if signature == "" {
			return domain.Errf(domain.CodePermissionDenied)
		}
		unsignedPath := strings.TrimSuffix(r.URL.EscapedPath(), "/s/"+signature)
		if !h.strm.VerifySignature(unsignedPath, signature) {
			return domain.Errf(domain.CodePermissionDenied)
		}
	}
	return nil
}
