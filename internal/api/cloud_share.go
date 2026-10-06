package api

import (
	"net/http"
	"strconv"
	"strings"

	"litepan/internal/driver"
)

type createCloudShareReq struct {
	AccountID          int64    `json:"account_id"`
	Kind               string   `json:"kind"`
	Name               string   `json:"name"`
	FileIDs            []string `json:"file_ids"`
	ExpireDays         int      `json:"expire_days"`
	Password           string   `json:"password"`
	PayAmount          int64    `json:"pay_amount"`
	RewardEnabled      bool     `json:"reward_enabled"`
	ResourceDesc       string   `json:"resource_desc"`
	TrafficSwitch      int      `json:"traffic_switch"`
	TrafficLimitSwitch int      `json:"traffic_limit_switch"`
	TrafficLimit       int64    `json:"traffic_limit"`
}

type updateCloudShareReq struct {
	AccountID          int64    `json:"account_id"`
	Kind               string   `json:"kind"`
	ShareIDs           []string `json:"share_ids"`
	TrafficSwitch      int      `json:"traffic_switch"`
	TrafficLimitSwitch int      `json:"traffic_limit_switch"`
	TrafficLimit       int64    `json:"traffic_limit"`
}

type cancelCloudSharesReq struct {
	AccountID int64    `json:"account_id"`
	ShareIDs  []string `json:"share_ids"`
}

func (h *Handler) cloudShareCapabilities(w http.ResponseWriter, r *http.Request) {
	accountID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.cloudShares.Capabilities(r.Context(), accountID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取分享能力成功", Data: result})
}

func (h *Handler) createCloudShare(w http.ResponseWriter, r *http.Request) {
	var req createCloudShareReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.cloudShares.Create(r.Context(), req.AccountID, driver.CreateShareRequest{
		Kind: req.Kind, Name: req.Name, FileIDs: req.FileIDs, ExpireDays: req.ExpireDays,
		Password: req.Password, PayAmount: req.PayAmount, RewardEnabled: req.RewardEnabled,
		ResourceDesc: req.ResourceDesc, TrafficSwitch: req.TrafficSwitch,
		TrafficLimitSwitch: req.TrafficLimitSwitch, TrafficLimit: req.TrafficLimit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "分享创建成功", Data: result})
}

func (h *Handler) listCloudShares(w http.ResponseWriter, r *http.Request) {
	accountID, err := parseQueryInt64(r, "account_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	result, err := h.cloudShares.List(r.Context(), accountID, driver.ListSharesRequest{
		Kind: r.URL.Query().Get("kind"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "获取分享列表成功", Data: result})
}

func (h *Handler) updateCloudShares(w http.ResponseWriter, r *http.Request) {
	var req updateCloudShareReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	err := h.cloudShares.Update(r.Context(), req.AccountID, driver.UpdateShareRequest{
		Kind: req.Kind, ShareIDs: req.ShareIDs, TrafficSwitch: req.TrafficSwitch,
		TrafficLimitSwitch: req.TrafficLimitSwitch, TrafficLimit: req.TrafficLimit,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "分享设置已更新"})
}

func (h *Handler) cancelCloudShares(w http.ResponseWriter, r *http.Request) {
	var req cancelCloudSharesReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.cloudShares.Cancel(r.Context(), req.AccountID, req.ShareIDs); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, Resp{Success: true, Message: "分享已取消"})
}
