package pan123open

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	pathShareCreate     = "/api/v1/share/create"
	pathPaidShareCreate = "/api/v1/share/content-payment/create"
	pathShareList       = "/api/v1/share/list"
	pathPaidShareList   = "/api/v1/share/payment/list"
	pathShareUpdate     = "/api/v1/share/list/info"
	pathPaidShareUpdate = "/api/v1/share/payment/list/info"
)

type shareListResponse struct {
	LastShareID json.Number     `json:"lastShareId"`
	ShareList   []shareListItem `json:"shareList"`
}

type shareListItem struct {
	ShareID           json.Number `json:"shareId"`
	ShareKey          string      `json:"shareKey"`
	ShareName         string      `json:"shareName"`
	Expiration        string      `json:"expiration"`
	Expired           int         `json:"expired"`
	SharePassword     string      `json:"sharePwd"`
	PayAmount         float64     `json:"payAmount"`
	Amount            float64     `json:"amount"`
	OrderCount        int64       `json:"orderCnt"`
	TrafficSwitch     int         `json:"trafficSwitch"`
	TrafficLimitState int         `json:"trafficLimitSwitch"`
	TrafficLimit      int64       `json:"trafficLimit"`
	BytesCharge       int64       `json:"bytesCharge"`
	PreviewCount      int64       `json:"previewCount"`
	DownloadCount     int64       `json:"downloadCount"`
	SaveCount         int64       `json:"saveCount"`
}

func (d *Driver) ShareCapabilities() driver.ShareCapabilities {
	return driver.ShareCapabilities{
		Supported: true, SupportsFree: true, SupportsPaid: true, SupportsManage: true,
		SupportsPassword: true, SupportsTraffic: true, SupportsCancel: false,
		MaxItems: 100, ExpireDays: []int{1, 7, 30, 0},
	}
}

func (d *Driver) CreateShare(ctx context.Context, req driver.CreateShareRequest) (*driver.ShareItem, error) {
	if len(req.FileIDs) == 0 || len(req.FileIDs) > 100 {
		return nil, domain.Errorf(domain.CodeValidation, "123 一次最多分享 100 个项目")
	}
	if len([]rune(req.Name)) > 35 {
		return nil, domain.Errorf(domain.CodeValidation, "123 分享名称不能超过 35 个字符")
	}
	// 先取 UID 再创建，避免分享已创建但拼接链接失败时用户重试出重复分享。
	uid, err := d.shareUID(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"shareName":          req.Name,
		"fileIDList":         strings.Join(req.FileIDs, ","),
		"trafficSwitch":      req.TrafficSwitch,
		"trafficLimitSwitch": req.TrafficLimitSwitch,
		"trafficLimit":       req.TrafficLimit,
	}
	path := pathShareCreate
	if req.Kind == driver.ShareKindPaid {
		if req.PayAmount < 1 || req.PayAmount > 1000 {
			return nil, domain.Errorf(domain.CodeValidation, "付费分享金额必须在 1～1000 元之间")
		}
		path = pathPaidShareCreate
		body["payAmount"] = req.PayAmount
		body["resourceDesc"] = req.ResourceDesc
		if req.RewardEnabled {
			body["isReward"] = 1
		} else {
			body["isReward"] = 0
		}
	} else {
		if !validShareExpire(req.ExpireDays) {
			return nil, domain.Errorf(domain.CodeValidation, "123 分享有效期只支持 1、7、30 天或永久")
		}
		if req.Password != "" && !validSharePassword(req.Password) {
			return nil, domain.Errorf(domain.CodeValidation, "123 自定义提取码必须是 4 位字母或数字")
		}
		body["shareExpire"] = req.ExpireDays
		if req.Password != "" {
			body["sharePwd"] = req.Password
		}
	}
	var result struct {
		ShareID  json.Number `json:"shareID"`
		ShareID2 json.Number `json:"shareId"`
		ShareKey string      `json:"shareKey"`
	}
	if err := d.apiCall(ctx, http.MethodPost, path, nil, body, &result); err != nil {
		return nil, err
	}
	id := firstNonEmptyNumber(result.ShareID, result.ShareID2)
	return &driver.ShareItem{
		ID: id, Kind: req.Kind, Key: result.ShareKey, URL: buildShareURL(uid, result.ShareKey),
		Name: req.Name, Password: req.Password, PayAmount: float64(req.PayAmount),
		TrafficSwitch: req.TrafficSwitch, TrafficLimitSwitch: req.TrafficLimitSwitch, TrafficLimit: req.TrafficLimit,
	}, nil
}

func (d *Driver) ListShares(ctx context.Context, req driver.ListSharesRequest) (*driver.SharePage, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(req.Limit))
	if strings.TrimSpace(req.Cursor) != "" {
		params.Set("lastShareId", strings.TrimSpace(req.Cursor))
	} else {
		params.Set("lastShareId", "0")
	}
	path := pathShareList
	if req.Kind == driver.ShareKindPaid {
		path = pathPaidShareList
	}
	var result shareListResponse
	if err := d.apiCall(ctx, http.MethodGet, path, params, nil, &result); err != nil {
		return nil, err
	}
	uid, err := d.shareUID(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]driver.ShareItem, 0, len(result.ShareList))
	for _, item := range result.ShareList {
		items = append(items, driver.ShareItem{
			ID: item.ShareID.String(), Kind: req.Kind, Key: item.ShareKey, URL: buildShareURL(uid, item.ShareKey),
			Name: item.ShareName, Expiration: item.Expiration, Expired: item.Expired != 0, Password: item.SharePassword,
			PayAmount: item.PayAmount, Income: item.Amount, OrderCount: item.OrderCount,
			TrafficSwitch: item.TrafficSwitch, TrafficLimitSwitch: item.TrafficLimitState, TrafficLimit: item.TrafficLimit,
			UsedBytes: item.BytesCharge, PreviewCount: item.PreviewCount, SaveCount: item.SaveCount, DownloadCount: item.DownloadCount,
		})
	}
	return &driver.SharePage{Items: items, NextCursor: result.LastShareID.String()}, nil
}

func (d *Driver) UpdateShares(ctx context.Context, req driver.UpdateShareRequest) error {
	if len(req.ShareIDs) == 0 || len(req.ShareIDs) > 100 {
		return domain.Errorf(domain.CodeValidation, "123 一次最多修改 100 个分享")
	}
	ids := make([]uint64, 0, len(req.ShareIDs))
	for _, raw := range req.ShareIDs {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return domain.Errorf(domain.CodeValidation, "非法分享 ID：%s", raw)
		}
		ids = append(ids, id)
	}
	path := pathShareUpdate
	if req.Kind == driver.ShareKindPaid {
		path = pathPaidShareUpdate
	}
	return d.apiCall(ctx, http.MethodPut, path, nil, map[string]any{
		"shareIdList": ids, "trafficSwitch": req.TrafficSwitch,
		"trafficLimitSwitch": req.TrafficLimitSwitch, "trafficLimit": req.TrafficLimit,
	}, nil)
}

func (d *Driver) shareUID(ctx context.Context) (string, error) {
	d.mu.Lock()
	uid := d.uid
	d.mu.Unlock()
	if uid != "" && uid != "0" {
		return uid, nil
	}
	var result struct {
		UID json.Number `json:"uid"`
	}
	if err := d.apiCall(ctx, http.MethodGet, pathUserInfo, nil, nil, &result); err != nil {
		return "", err
	}
	uid = result.UID.String()
	if uid == "" || uid == "0" {
		return "", domain.Errorf(domain.CodeDriverError, "123 用户信息缺少 UID")
	}
	d.mu.Lock()
	d.uid = uid
	d.mu.Unlock()
	return uid, nil
}

func buildShareURL(uid, key string) string {
	return fmt.Sprintf("https://%s.share.123pan.cn/123pan/%s", uid, key)
}

func validShareExpire(days int) bool {
	return days == 0 || days == 1 || days == 7 || days == 30
}

func validSharePassword(password string) bool {
	if len(password) != 4 {
		return false
	}
	for _, ch := range password {
		if (ch < '0' || ch > '9') && (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z') {
			return false
		}
	}
	return true
}
