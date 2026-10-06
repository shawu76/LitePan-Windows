package quark

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

const (
	pathShareCreate   = "/share"
	pathShareList     = "/share/mypage/detail"
	pathSharePassword = "/share/password"
	pathShareDelete   = "/share/delete"
)

type quarkShareItem struct {
	ShareID     json.RawMessage `json:"share_id"`
	PwdID       json.RawMessage `json:"pwd_id"`
	Title       string          `json:"title"`
	ShareURL    string          `json:"share_url"`
	Passcode    string          `json:"passcode"`
	ExpiredType int             `json:"expired_type"`
	ExpiredAt   int64           `json:"expired_at"`
	Status      int             `json:"status"`
	AuditStatus int             `json:"audit_status"`
}

type quarkShareListData struct {
	List []quarkShareItem `json:"list"`
}

type quarkShareCreateData struct {
	TaskID string `json:"task_id"`
}

type quarkShareTaskData struct {
	Status  int             `json:"status"`
	ShareID json.RawMessage `json:"share_id"`
}

type quarkTaskMetadata struct {
	TQGap int `json:"tq_gap"`
}

func (d *Driver) ShareCapabilities() driver.ShareCapabilities {
	return driver.ShareCapabilities{
		Supported: true, SupportsFree: true, SupportsManage: true,
		SupportsPassword: true, SupportsCancel: true,
		MaxItems: 100, ExpireDays: []int{1, 7, 30, 0},
	}
}

func (d *Driver) CreateShare(ctx context.Context, req driver.CreateShareRequest) (*driver.ShareItem, error) {
	if len(req.FileIDs) == 0 || len(req.FileIDs) > 100 {
		return nil, domain.Errorf(domain.CodeValidation, "夸克一次最多分享 100 个项目")
	}
	expiredType, ok := quarkExpiredType(req.ExpireDays)
	if !ok {
		return nil, domain.Errorf(domain.CodeValidation, "夸克分享有效期只支持 1、7、30 天或永久")
	}
	password := strings.TrimSpace(req.Password)
	if password != "" && !validQuarkSharePassword(password) {
		return nil, domain.Errorf(domain.CodeValidation, "夸克提取码必须是 4 位字母或数字")
	}
	body := map[string]any{
		"fid_list":           req.FileIDs,
		"title":              req.Name,
		"url_type":           1,
		"passcode":           "",
		"expired_type":       expiredType,
		"support_error_code": []string{"41060"},
	}
	if password != "" {
		body["url_type"] = 2
		body["passcode"] = password
	}
	var createResult quarkShareCreateData
	env, err := d.apiRequest(ctx, http.MethodPost, pathShareCreate, nil, body, &createResult)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(createResult.TaskID) == "" {
		return nil, domain.Errorf(domain.CodeDriverError, "夸克创建分享未返回 task_id")
	}
	interval := time.Second
	var metadata quarkTaskMetadata
	if json.Unmarshal(env.Metadata, &metadata) == nil && metadata.TQGap > 0 {
		interval = time.Duration(metadata.TQGap) * time.Millisecond
	}
	shareID, err := d.waitShareTask(ctx, createResult.TaskID, interval)
	if err != nil {
		return nil, err
	}
	result, err := d.getShareLink(ctx, shareID)
	if err != nil {
		return nil, err
	}
	if len(result.ShareID) == 0 {
		result.ShareID = json.RawMessage(strconv.Quote(shareID))
	}
	return quarkShareToDriver(result, req.Name, password), nil
}

func (d *Driver) waitShareTask(ctx context.Context, taskID string, interval time.Duration) (string, error) {
	for attempt := 0; attempt < 30; attempt++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
		query := url.Values{"task_id": {taskID}, "retry_index": {strconv.Itoa(attempt)}}
		var result quarkShareTaskData
		if _, err := d.apiRequest(ctx, http.MethodGet, pathTask, query, nil, &result); err != nil {
			return "", err
		}
		switch result.Status {
		case 2:
			shareID := quarkShareJSONID(result.ShareID)
			if shareID == "" {
				return "", domain.Errorf(domain.CodeDriverError, "夸克分享任务未返回 share_id")
			}
			return shareID, nil
		case 3:
			return "", domain.Errorf(domain.CodeDriverError, "夸克创建分享任务失败")
		}
	}
	return "", domain.Errorf(domain.CodeDriverError, "夸克创建分享任务超时")
}

func (d *Driver) ListShares(ctx context.Context, req driver.ListSharesRequest) (*driver.SharePage, error) {
	page := 1
	if raw := strings.TrimSpace(req.Cursor); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return nil, domain.Errorf(domain.CodeValidation, "非法的分享列表页码")
		}
		page = parsed
	}
	query := url.Values{
		"_page":                {strconv.Itoa(page)},
		"_size":                {strconv.Itoa(req.Limit)},
		"_order_field":         {"created_at"},
		"_order_type":          {"desc"},
		"_fetch_total":         {"1"},
		"_fetch_notify_follow": {"1"},
		"_filter":              {"all"},
		"_share_type":          {"0,1"},
	}
	var result quarkShareListData
	if _, err := d.apiRequest(ctx, http.MethodGet, pathShareList, query, nil, &result); err != nil {
		return nil, err
	}
	items := make([]driver.ShareItem, 0, len(result.List))
	for _, item := range result.List {
		items = append(items, *quarkShareToDriver(item, item.Title, item.Passcode))
	}
	next := ""
	if len(result.List) == req.Limit {
		next = strconv.Itoa(page + 1)
	}
	return &driver.SharePage{Items: items, NextCursor: next}, nil
}

func (d *Driver) CancelShares(ctx context.Context, shareIDs []string) error {
	if len(shareIDs) == 0 {
		return domain.Errorf(domain.CodeValidation, "请选择要取消的夸克分享")
	}
	_, err := d.apiRequest(ctx, http.MethodPost, pathShareDelete, nil, map[string]any{"share_ids": shareIDs}, nil)
	return err
}

func (d *Driver) getShareLink(ctx context.Context, shareID string) (quarkShareItem, error) {
	var result quarkShareItem
	_, err := d.apiRequest(ctx, http.MethodPost, pathSharePassword, nil, map[string]any{"share_id": shareID}, &result)
	return result, err
}

func quarkShareToDriver(item quarkShareItem, fallbackName, fallbackPassword string) *driver.ShareItem {
	name := strings.TrimSpace(item.Title)
	if name == "" {
		name = fallbackName
	}
	password := strings.TrimSpace(item.Passcode)
	if password == "" {
		password = fallbackPassword
	}
	expired := item.Status == 3 || item.AuditStatus == 2 || item.AuditStatus == 3
	if item.ExpiredType != 1 && item.ExpiredAt > 0 && quarkShareTime(item.ExpiredAt).Before(time.Now()) {
		expired = true
	}
	return &driver.ShareItem{
		ID: firstQuarkShareID(item), Kind: driver.ShareKindFree, Key: quarkShareJSONID(item.PwdID),
		URL: quarkShareURL(item), Name: name, Expiration: quarkShareExpiration(item),
		Expired: expired, Password: password,
	}
}

func quarkShareURL(item quarkShareItem) string {
	if shareURL := strings.TrimSpace(item.ShareURL); shareURL != "" {
		return shareURL
	}
	if pwdID := quarkShareJSONID(item.PwdID); pwdID != "" {
		return "https://pan.quark.cn/s/" + url.PathEscape(pwdID)
	}
	return ""
}

func quarkShareExpiration(item quarkShareItem) string {
	if item.ExpiredType == 1 || item.ExpiredAt <= 0 {
		return "永久有效"
	}
	return quarkShareTime(item.ExpiredAt).Format("2006-01-02 15:04:05")
}

func quarkShareTime(value int64) time.Time {
	if value < 1_000_000_000_000 {
		return time.Unix(value, 0)
	}
	return time.UnixMilli(value)
}

func quarkExpiredType(days int) (int, bool) {
	switch days {
	case 0:
		return 1, true
	case 1:
		return 2, true
	case 7:
		return 3, true
	case 30:
		return 4, true
	default:
		return 0, false
	}
}

func validQuarkSharePassword(password string) bool {
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

func firstQuarkShareID(item quarkShareItem) string {
	if id := quarkShareJSONID(item.ShareID); id != "" {
		return id
	}
	return quarkShareJSONID(item.PwdID)
}

func quarkShareJSONID(raw json.RawMessage) string {
	id := jsonID(raw)
	if id == "null" {
		return ""
	}
	return id
}
