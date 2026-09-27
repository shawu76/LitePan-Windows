package api

import (
	"net/http"
	"strings"

	"litepan/internal/domain"
)

// devUnlockCode 开发模式解锁码；仅为隐藏实验性驱动的门槛，不是安全机制。
const devUnlockCode = "litepan666"

type devStateResp struct {
	Unlocked bool `json:"unlocked"`
}

type devUnlockReq struct {
	Code string `json:"code"`
}

func (h *Handler) getDevState(w http.ResponseWriter, _ *http.Request) {
	h.devMu.Lock()
	unlocked := h.devUnlocked
	h.devMu.Unlock()
	writeOK(w, devStateResp{Unlocked: unlocked})
}

func (h *Handler) unlockDevMode(w http.ResponseWriter, r *http.Request) {
	var in devUnlockReq
	if err := decodeJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if strings.TrimSpace(in.Code) != devUnlockCode {
		writeErr(w, domain.Errorf(domain.CodeValidation, "解锁码错误"))
		return
	}
	h.devMu.Lock()
	h.devUnlocked = true
	h.devMu.Unlock()
	writeOK(w, devStateResp{Unlocked: true})
}
