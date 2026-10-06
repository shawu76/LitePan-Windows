package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "litepan/drivers/189Cloud"
	"litepan/internal/domain"
	"litepan/internal/driver"
)

func TestCloud189RefreshResponseAndCooldown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   domain.ErrorCode
	}{
		{"无状态码正常返回", 200, `{"accessToken":"mock-new-access","refreshToken":"mock-new-refresh"}`, ""},
		{"数字成功码", 200, `{"res_code":0,"access_token":"mock-new-access"}`, ""},
		{"字符串成功码", 200, `{"res_code":"0","accessToken":"mock-new-access"}`, ""},
		{"只有error字段", 200, `{"error":"invalid_grant","error_description":"mock-secret-echo"}`, domain.CodeAuthExpired},
		{"HTTP400失效", 400, `{"error":"invalid_token"}`, domain.CodeAuthExpired},
		{"替代错误码字段", 200, `{"errorCode":"UserInvalidOpenToken","errorMsg":"mock-secret-echo"}`, domain.CodeAuthExpired},
		{"业务限流", 200, `{"res_code":-1,"res_message":"Too many requests: mock-secret-echo"}`, domain.CodeRateLimited},
		{"HTTP401", 401, `{"message":"mock-secret-echo"}`, domain.CodeAuthExpired},
		{"HTTP403不触发续期", 403, `{"error":"invalid_token"}`, domain.CodePermissionDenied},
		{"HTTP429优先", 429, `{"error":"invalid_token"}`, domain.CodeRateLimited},
		{"HTTP503不误判认证", 503, `{"error":"invalid_grant"}`, domain.CodeDriverError},
		{"未知业务错误", 200, `{"res_code":-12345,"res_message":"请求编号401: mock-secret-echo"}`, domain.CodeDriverError},
		{"未知代码不回显", 200, `{"error":"mock-secret-echo","message":"mock-secret-echo"}`, domain.CodeDriverError},
		{"成功码缺少Token", 200, `{"res_code":0}`, domain.CodeDriverError},
		{"空对象", 200, `{}`, domain.CodeDriverError},
		{"空Token", 200, `{"accessToken":"   "}`, domain.CodeDriverError},
		{"Token类型错误", 200, `{"accessToken":{"value":"mock-secret-echo"}}`, domain.CodeDriverError},
		{"错误响应即使含Token也拒绝", 200, `{"res_code":-1,"accessToken":"mock-secret-echo","refreshToken":"mock-secret-echo"}`, domain.CodeDriverError},
		{"显式失败", 200, `{"success":false,"accessToken":"mock-secret-echo"}`, domain.CodeDriverError},
		{"非JSON", 200, `<html>mock-secret-echo</html>`, domain.CodeDriverError},
		{"JSON数组", 200, `["mock-secret-echo"]`, domain.CodeDriverError},
		{"JSON空值", 200, `null`, domain.CodeDriverError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refreshes, sessions atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/oauth2/refreshToken.do":
					refreshes.Add(1)
					if r.Method != http.MethodPost || r.FormValue("refreshToken") != "mock-refresh" {
						t.Error("刷新请求方法或凭据不正确")
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				case "/getSessionForPC.action":
					sessions.Add(1)
					_, _ = io.WriteString(w, `{"res_code":0,"sessionKey":"mock-session","sessionSecret":"mock-session-secret"}`)
				default:
					t.Errorf("意外请求路径：%s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			// 真实驱动的固定域名只拨到本机模拟服务，禁止访问真实网盘。
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "open.e.189.cn:443" && addr != "api.cloud.189.cn:443" {
					return nil, fmt.Errorf("禁止模拟测试访问地址 %s", addr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
			ctx := context.Background()
			accounts := &fakeAccountRepo{accounts: map[int64]*domain.Account{1: {ID: 1, DriverType: "189_cloud", IsActive: true}}}
			repo := &fakeAuthRepo{states: map[int64]*domain.AuthState{1: {AccountID: 1, Status: domain.AuthActive, RefreshToken: "mock-refresh"}}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			mgr := driver.NewManager(accounts, repo, nil, log)
			defer mgr.Close(ctx)
			now := time.Now()
			svc := NewService(Options{Accounts: accounts, AuthStates: repo, Drivers: mgr, Log: log, Now: func() time.Time { return now }})
			_, err := mgr.Get(ctx, 1)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				ae, ok := domain.AsAppError(err)
				if !ok || ae.Code != tc.code {
					t.Fatalf("错误=%v，期望 %s", err, tc.code)
				}
				payload, _ := json.Marshal(ae)
				if strings.Contains(string(payload), "mock-secret-echo") || strings.Contains(ae.Message, "成功") {
					t.Fatalf("错误信息泄露响应或误报成功：%s", payload)
				}
				if ae.Details["http_status"] != tc.status {
					t.Fatalf("缺少状态码诊断：%+v", ae.Details)
				}
			}
			for i := 0; i < 8; i++ {
				_, _ = mgr.Get(ctx, 1)
				_, _ = svc.Refresh(ctx, 1, driver.CallerActive)
				_ = svc.Gate().HandlePassiveError(ctx, 1)
			}
			st, err := repo.Get(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			if refreshes.Load() != 1 {
				t.Fatalf("刷新未被合并/冷却：%d", refreshes.Load())
			}
			if tc.code == "" {
				if st.Status != domain.AuthActive || st.AccessToken != "mock-new-access" || sessions.Load() != 1 {
					t.Fatalf("正常响应处理错误：%+v", st)
				}
			} else {
				wantState, delay := domain.AuthCooldown, time.Minute
				if tc.code == domain.CodeAuthExpired {
					wantState, delay = domain.AuthTokenExpired, 24*time.Hour
				}
				if st.Status != wantState || st.NextRetryAt.Sub(now) != delay || st.RefreshToken != "mock-refresh" || sessions.Load() != 0 {
					t.Fatalf("错误响应未正确冷却或覆盖了凭据：%+v", st)
				}
			}
		})
	}
}

// 会话初始化仅在认证失效时刷新 Token，网络及服务端错误直接返回。
func TestCloud189InitSessionFailureTriggersTokenRefreshOnlyOnAuthExpiry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sessionStatus int
		wantRefreshes int32
		wantSessions  int32
		wantOK        bool
	}{
		{"503会话错误不换Token", http.StatusServiceUnavailable, 0, 1, false},
		{"401会话失效换一次Token", http.StatusUnauthorized, 1, 2, true},
		{"400会话失效换一次Token", http.StatusBadRequest, 1, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refreshes, sessions atomic.Int32
			var hits []string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/oauth2/refreshToken.do":
					n := refreshes.Add(1)
					hits = append(hits, fmt.Sprintf("refresh#%d", n))
					_, _ = io.WriteString(w, `{"accessToken":"new-access","refreshToken":"new-refresh"}`)
				case "/getSessionForPC.action":
					n := sessions.Add(1)
					hits = append(hits, fmt.Sprintf("session#%d", n))
					// 仅首次按场景返回错误；换 Token 后的会话请求应成功。
					if n == 1 && tc.sessionStatus != http.StatusOK {
						w.WriteHeader(tc.sessionStatus)
						_, _ = io.WriteString(w, `{"errorCode":"UserInvalidOpenToken","errorMsg":"unifyAccountInfo is null"}`)
						return
					}
					_, _ = io.WriteString(w, `{"res_code":0,"sessionKey":"mock-session","sessionSecret":"mock-session-secret"}`)
				default:
					t.Errorf("意外请求路径：%s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "open.e.189.cn:443" && addr != "api.cloud.189.cn:443" {
					return nil, fmt.Errorf("禁止模拟测试访问地址 %s", addr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
			ctx := context.Background()
			accounts := &fakeAccountRepo{accounts: map[int64]*domain.Account{1: {ID: 1, DriverType: "189_cloud", IsActive: true}}}
			repo := &fakeAuthRepo{states: map[int64]*domain.AuthState{1: {AccountID: 1, Status: domain.AuthActive, AccessToken: "old-access", RefreshToken: "mock-refresh"}}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			mgr := driver.NewManager(accounts, repo, nil, log)
			defer mgr.Close(ctx)
			now := time.Now()
			NewService(Options{Accounts: accounts, AuthStates: repo, Drivers: mgr, Log: log, Now: func() time.Time { return now }})
			_, err := mgr.Get(ctx, 1)
			if tc.wantOK != (err == nil) {
				t.Fatalf("Get 错误不符合预期：%v (refreshes=%d sessions=%d hits=%v)", err, refreshes.Load(), sessions.Load(), hits)
			}
			if got := refreshes.Load(); got != tc.wantRefreshes {
				t.Fatalf("Token 刷新次数=%d，期望 %d", got, tc.wantRefreshes)
			}
			if got := sessions.Load(); got != tc.wantSessions {
				t.Fatalf("会话请求次数=%d，期望 %d", got, tc.wantSessions)
			}
		})
	}
}

func TestCloud189ExpiredSessionRecovery(t *testing.T) {
	const expired = `{"errorCode":"InvalidSessionKey","errorMsg":"userSessionBO is null or fail to get sessionsecret by sessionkey","success":null}`
	for _, tc := range []struct {
		name, space, endpoint string
		status                int
		body                  string
		code                  domain.ErrorCode
		concurrency           int
	}{
		{"个人云列目录", "personal", "/listFiles.action", 400, expired, "", 1},
		{"家庭云并发列目录", "family", "/family/file/listFiles.action", 400, expired, "", 6},
		{"家庭云表单提交", "family", "/batch/createBatchTask.action", 400, expired, "", 1},
		{"家庭云任务查询使用个人会话", "family", "/batch/checkBatchTask.action", 400, expired, "", 1},
		{"普通400不刷新", "family", "/family/file/listFiles.action", 400, `{"errorCode":"InvalidArgument"}`, domain.CodeDriverError, 1},
		{"403不刷新", "family", "/family/file/listFiles.action", 403, expired, domain.CodePermissionDenied, 1},
		{"429不刷新", "family", "/family/file/listFiles.action", 429, expired, domain.CodeRateLimited, 1},
		{"503不刷新", "family", "/family/file/listFiles.action", 503, expired, domain.CodeDriverError, 1},
		{"表单403不刷新", "family", "/batch/createBatchTask.action", 403, expired, domain.CodePermissionDenied, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sessions, refreshes, rejected atomic.Int32
			var armed atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/oauth2/refreshToken.do" {
					refreshes.Add(1)
					if r.FormValue("refreshToken") != "old-refresh" {
						t.Error("未使用原刷新凭据")
					}
					_, _ = io.WriteString(w, `{"accessToken":"new-access","refreshToken":"new-refresh"}`)
					return
				}
				if r.URL.Path == "/getSessionForPC.action" {
					n := sessions.Add(1)
					_, _ = fmt.Fprintf(w, `{"sessionKey":"personal-%d","sessionSecret":"secret","familySessionKey":"family-%d","familySessionSecret":"secret"}`, n, n)
					return
				}
				prefix := tc.space
				if r.URL.Path == "/batch/checkBatchTask.action" {
					prefix = "personal"
				}
				key := r.Header.Get("SessionKey")
				if !strings.HasPrefix(key, prefix+"-") {
					t.Errorf("%s 使用了错误的会话类型 %q", r.URL.Path, key)
				}
				if armed.Load() && r.URL.Path == tc.endpoint && key == prefix+"-1" {
					rejected.Add(1)
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				switch r.URL.Path {
				case "/family/manage/getFamilyList.action":
					_, _ = io.WriteString(w, `{"familyInfoResp":[{"familyId":"123","useFlag":1}]}`)
				case "/listFiles.action", "/family/file/listFiles.action":
					_, _ = io.WriteString(w, `{"fileListAO":{"fileList":[{"id":"42","name":"测试.txt","size":100}]}}`)
				case "/batch/createBatchTask.action":
					_, _ = io.WriteString(w, `{"taskId":"mock-task"}`)
				case "/batch/checkBatchTask.action":
					_, _ = io.WriteString(w, `{"taskStatus":4,"failedCount":0}`)
				default:
					t.Errorf("意外请求路径：%s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "open.e.189.cn:443" && addr != "api.cloud.189.cn:443" {
					return nil, fmt.Errorf("禁止模拟测试访问地址 %s", addr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = original; transport.CloseIdleConnections() }()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			accounts := &fakeAccountRepo{accounts: map[int64]*domain.Account{1: {
				ID: 1, DriverType: "189_cloud", IsActive: true, Config: fmt.Sprintf(`{"space_type":%q}`, tc.space),
			}}}
			repo := &fakeAuthRepo{states: map[int64]*domain.AuthState{1: {
				AccountID: 1, Status: domain.AuthActive, AccessToken: "old-access", RefreshToken: "old-refresh",
			}}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			mgr := driver.NewManager(accounts, repo, nil, log)
			defer mgr.Close(ctx)
			NewService(Options{Accounts: accounts, AuthStates: repo, Drivers: mgr, Log: log})
			d, err := mgr.Get(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			// 先正常列目录，再模拟会话到期；所有写操作仅发往本机 mock。
			if _, err := d.ListFiles(ctx, "0"); err != nil {
				t.Fatal(err)
			}
			armed.Store(true)
			results := make(chan error, tc.concurrency)
			for i := 0; i < tc.concurrency; i++ {
				go func() {
					if strings.HasPrefix(tc.endpoint, "/batch/") {
						results <- d.(driver.Copier).CopyFiles(ctx, []string{"42"}, "456")
						return
					}
					items, err := d.ListFiles(ctx, "0")
					if err == nil && (len(items) != 1 || items[0].ID != "42") {
						err = fmt.Errorf("恢复后文件列表不正确：%v", items)
					}
					results <- err
				}()
			}
			for i := 0; i < tc.concurrency; i++ {
				if err := <-results; tc.code == "" {
					if err != nil {
						t.Error(err)
					}
				} else if ae, ok := domain.AsAppError(err); !ok || ae.Code != tc.code {
					t.Errorf("错误=%v，期望 %s", err, tc.code)
				}
			}
			st, err := repo.Get(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			wantRefreshes := int32(0)
			wantToken := "old-refresh"
			if tc.code == "" {
				wantRefreshes, wantToken = 1, "new-refresh"
			}
			if rejected.Load() == 0 || refreshes.Load() != wantRefreshes || sessions.Load() != 1+wantRefreshes || st.RefreshToken != wantToken || st.Status != domain.AuthActive {
				t.Fatalf("恢复/刷新合并不符合预期：拒绝%d次，刷新%d次，会话%d次，状态%s", rejected.Load(), refreshes.Load(), sessions.Load(), st.Status)
			}
		})
	}
}
