package fnosproxy

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"litepan/internal/domain"
	"litepan/internal/settings"
)

const (
	fnosManagementSecret = "NDzZTVxnRKP8Z0jXg1VAMonaG8akvh"
	fnosManagementAPIKey = "16CCEB3D-AB42-077D-36A1-F355324E4237"
)

type MediaLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type fnosResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (s *Service) UpdateManagement(ctx context.Context, in ManagementUpdateRequest) error {
	if s.settings == nil {
		return domain.Errf(domain.CodeNotImplement)
	}
	username := strings.TrimSpace(in.Username)
	password := in.Password
	if username == "" {
		password = ""
	} else if password == "" {
		password = s.settings.StringAllowEmpty(settings.KeyFnosAdminPassword)
	}
	if username != "" && password == "" {
		return domain.Errorf(domain.CodeValidation, "请输入飞牛影视管理员密码")
	}
	return s.settings.Update(ctx, map[string]string{
		settings.KeyFnosAdminUsername: username,
		settings.KeyFnosAdminPassword: password,
	})
}

func (s *Service) TestManagement(ctx context.Context, in ManagementUpdateRequest) error {
	_, _, err := s.managementCredentials(in)
	if err != nil {
		return err
	}
	_, err = s.listLibraries(ctx, in)
	return err
}

func (s *Service) ManagementConfigured() bool {
	return s.configFromSettings().ManagementReady
}

func (s *Service) ListLibraries(ctx context.Context) ([]MediaLibrary, error) {
	return s.listLibraries(ctx, ManagementUpdateRequest{})
}

func (s *Service) listLibraries(ctx context.Context, in ManagementUpdateRequest) ([]MediaLibrary, error) {
	token, err := s.loginManagement(ctx, in)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		GUID  string `json:"guid"`
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if err := s.managementRequest(ctx, token, http.MethodGet, "/mdb/list", nil, &rows); err != nil {
		return nil, err
	}
	out := make([]MediaLibrary, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = strings.TrimSpace(row.Title)
		}
		if row.GUID != "" {
			out = append(out, MediaLibrary{ID: row.GUID, Name: name})
		}
	}
	return out, nil
}

func (s *Service) ScanLibrary(ctx context.Context, libraryID string) error {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return domain.Errorf(domain.CodeValidation, "请选择飞牛影视媒体库")
	}
	token, err := s.loginManagement(ctx, ManagementUpdateRequest{})
	if err != nil {
		return err
	}
	return s.managementRequest(ctx, token, http.MethodPost, "/mdb/scan/"+url.PathEscape(libraryID), map[string]any{"guid": libraryID}, nil)
}

func (s *Service) RefreshMetadata(ctx context.Context, libraryID string, refreshMode int) error {
	libraryID = strings.TrimSpace(libraryID)
	if libraryID == "" {
		return domain.Errorf(domain.CodeValidation, "请选择飞牛影视媒体库")
	}
	if refreshMode != 0 && refreshMode != 1 {
		return domain.Errorf(domain.CodeValidation, "飞牛影视元数据刷新方式无效")
	}
	token, err := s.loginManagement(ctx, ManagementUpdateRequest{})
	if err != nil {
		return err
	}
	return s.managementRequest(ctx, token, http.MethodPost, "/mdb/refresh", map[string]any{
		"mdb_guid":     libraryID,
		"refresh_mode": refreshMode,
	}, nil)
}

func (s *Service) loginManagement(ctx context.Context, in ManagementUpdateRequest) (string, error) {
	username, password, err := s.managementCredentials(in)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(password))
	var data struct {
		Token string `json:"token"`
	}
	err = s.managementRequestAt(ctx, "", http.MethodPost, "/v/api/v2/user/loginByPassword", map[string]any{
		"username": username,
		"password": hex.EncodeToString(hash[:]),
		"app_name": "trimemedia-web",
	}, &data)
	if err == nil && strings.TrimSpace(data.Token) != "" {
		return data.Token, nil
	}
	data.Token = ""
	err = s.managementRequestAt(ctx, "", http.MethodPost, "/v/api/v1/login", map[string]any{
		"username": username,
		"password": password,
		"app_name": "trimemedia-web",
	}, &data)
	if err != nil || strings.TrimSpace(data.Token) == "" {
		return "", domain.Errorf(domain.CodeAuthExpired, "飞牛影视管理员登录失败，请检查账号和密码")
	}
	return data.Token, nil
}

func (s *Service) managementCredentials(in ManagementUpdateRequest) (string, string, error) {
	username := strings.TrimSpace(in.Username)
	password := in.Password
	if s.settings != nil {
		if username == "" {
			username = strings.TrimSpace(s.settings.StringAllowEmpty(settings.KeyFnosAdminUsername))
		}
		if password == "" {
			password = s.settings.StringAllowEmpty(settings.KeyFnosAdminPassword)
		}
	}
	if username == "" || password == "" {
		return "", "", domain.Errorf(domain.CodeValidation, "请先配置飞牛影视管理员账号和密码")
	}
	return username, password, nil
}

func (s *Service) managementRequest(ctx context.Context, token, method, path string, body any, out any) error {
	return s.managementRequestAt(ctx, token, method, "/v/api/v1"+path, body, out)
}

func (s *Service) managementRequestAt(ctx context.Context, token, method, path string, body any, out any) error {
	cfg := s.configFromSettings()
	if cfg.FnosURL == "" {
		return domain.Errorf(domain.CodeValidation, "请先填写飞牛影视地址")
	}
	base := strings.TrimSuffix(strings.TrimRight(cfg.FnosURL, "/"), "/v")
	var reader io.Reader
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("authx", fnosAuthX(path, raw))
	resp, err := s.client.Do(req)
	if err != nil {
		return domain.Errorf(domain.CodeDriverError, "连接飞牛影视失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.Errorf(domain.CodeAuthExpired, "飞牛影视管理员认证无效")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.Errorf(domain.CodeDriverError, "飞牛影视接口返回 HTTP %d", resp.StatusCode)
	}
	var envelope fnosResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return domain.Errorf(domain.CodeDriverError, "飞牛影视接口响应无法解析")
	}
	if envelope.Code != 0 {
		message := strings.TrimSpace(envelope.Msg)
		if message == "" {
			message = fmt.Sprintf("错误码 %d", envelope.Code)
		}
		return domain.Errorf(domain.CodeDriverError, "飞牛影视接口失败：%s", message)
	}
	if out != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return domain.Errorf(domain.CodeDriverError, "飞牛影视接口数据无法解析")
		}
	}
	return nil
}

func fnosAuthX(path string, body []byte) string {
	var nonceBytes [4]byte
	_, _ = rand.Read(nonceBytes[:])
	nonceNumber := int(nonceBytes[0])<<16 | int(nonceBytes[1])<<8 | int(nonceBytes[2])
	nonce := strconv.Itoa(100000 + nonceNumber%900000)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	bodySum := md5.Sum(body)
	toSign := strings.Join([]string{
		fnosManagementSecret,
		path,
		nonce,
		timestamp,
		hex.EncodeToString(bodySum[:]),
		fnosManagementAPIKey,
	}, "_")
	signSum := md5.Sum([]byte(toSign))
	return "nonce=" + nonce + "&timestamp=" + timestamp + "&sign=" + hex.EncodeToString(signSum[:])
}
