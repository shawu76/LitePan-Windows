package adminauth

import (
	"context"
	"encoding/base32"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/store"
	"litepan/pkg/security"
)

type countingConfigRepo struct {
	domain.ConfigRepository
	gets atomic.Int64
	alls atomic.Int64
}

func (r *countingConfigRepo) Get(ctx context.Context, key string) (string, bool, error) {
	r.gets.Add(1)
	return r.ConfigRepository.Get(ctx, key)
}

func (r *countingConfigRepo) All(ctx context.Context) (map[string]string, error) {
	r.alls.Add(1)
	return r.ConfigRepository.All(ctx)
}

func newTestAuth(t *testing.T) (*Service, domain.ConfigRepository, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)
	_ = st.Configs.Set(ctx, KeyAdminUsername, "admin")
	_ = st.Configs.Set(ctx, KeyAdminPassword, security.HashPassword("changed-secret"))
	return New(st.Configs, []byte("test-secret-key-min-16b"), nil), st.Configs, ctx
}

func newBareTestAuth(t *testing.T) (*Service, domain.ConfigRepository, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(db)
	return New(st.Configs, []byte("test-secret-key-min-16b"), nil), st.Configs, ctx
}

func TestAdminRequestsReuseConfigSnapshot(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Memory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	base := store.New(db).Configs
	if err := base.Set(ctx, KeyAdminUsername, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := base.Set(ctx, KeyAdminPassword, security.HashPassword("changed-secret")); err != nil {
		t.Fatal(err)
	}
	repo := &countingConfigRepo{ConfigRepository: base}
	svc := New(repo, []byte("test-secret-key-min-16b"), nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
	if err := svc.WriteSession(rec, req, Session{IsAdmin: true, Username: "admin"}, true); err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	for range 20 {
		checkReq := httptest.NewRequest(http.MethodGet, "/api/admin/accounts", nil)
		checkReq.AddCookie(cookie)
		sess, ok := svc.ReadSession(checkReq)
		if !ok {
			t.Fatal("session should remain valid")
		}
		if err := svc.EnsureAdminAccess(ctx, checkReq, sess); err != nil {
			t.Fatal(err)
		}
	}
	if got := repo.alls.Load(); got != 1 {
		t.Fatalf("config snapshot loads = %d, want 1", got)
	}
	if got := repo.gets.Load(); got != 0 {
		t.Fatalf("per-key config reads = %d, want 0", got)
	}
}

func TestDefaultAdminPasswordIsInitializedAsHashAndMustChange(t *testing.T) {
	svc, configs, ctx := newBareTestAuth(t)

	username, storedPassword := svc.adminCredentials(ctx)
	if username != defaultAdminUsername {
		t.Fatalf("username = %q, want %q", username, defaultAdminUsername)
	}
	if !security.IsPasswordHash(storedPassword) {
		t.Fatalf("default password should be stored as hash, got %q", storedPassword)
	}
	if !security.VerifyAdminPassword(storedPassword, defaultAdminPassword) {
		t.Fatal("hashed default password should verify admin password")
	}
	persisted, ok, err := configs.Get(ctx, KeyAdminPassword)
	if err != nil {
		t.Fatalf("get persisted password: %v", err)
	}
	if !ok || persisted != storedPassword {
		t.Fatalf("persisted password = %q, ok=%v, want stored hash %q", persisted, ok, storedPassword)
	}

	state := svc.credentialState(ctx)
	if !state.MustChangePassword {
		t.Fatal("default admin should still require password change")
	}
	if state.PasswordChangeReason != "default_credentials" {
		t.Fatalf("reason = %q, want default_credentials", state.PasswordChangeReason)
	}
}

func TestPublicIndexIsDisabledByDefault(t *testing.T) {
	svc, _, ctx := newBareTestAuth(t)
	if svc.publicIndexEnabled(ctx) {
		t.Fatal("public index should be disabled by default")
	}
	if err := svc.setConfig(ctx, KeyPublicIndexEnabled, "true"); err != nil {
		t.Fatalf("enable public index: %v", err)
	}
	if !svc.publicIndexEnabled(ctx) {
		t.Fatal("saved public index setting should override the default")
	}
}

func TestTwoFactorLoginDoesNotCreateSessionBeforeCodeVerification(t *testing.T) {
	svc, _, ctx := newTestAuth(t)
	setup, err := svc.BeginTwoFactorSetup(ctx, TwoFactorSetupRequest{Password: "changed-secret"})
	if err != nil {
		t.Fatalf("begin setup: %v", err)
	}
	code := totpCodeForTest(t, setup.Secret, time.Now())
	recovery, err := svc.ConfirmTwoFactorSetup(ctx, TwoFactorConfirmRequest{SetupToken: setup.SetupToken, Code: code})
	if err != nil {
		t.Fatalf("confirm setup: %v", err)
	}
	if len(recovery.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes = %d, want 10", len(recovery.RecoveryCodes))
	}

	firstRec := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	first, err := svc.Login(ctx, firstReq, firstRec, "admin", "changed-secret", true, "", "")
	if err != nil {
		t.Fatalf("first factor login: %v", err)
	}
	if !first.TwoFactorRequired || first.Challenge == "" {
		t.Fatalf("expected two-factor challenge, got %+v", first)
	}
	if len(firstRec.Result().Cookies()) != 0 {
		t.Fatal("password-only step must not create an admin session")
	}

	secondRec := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	second, err := svc.Login(ctx, secondReq, secondRec, "", "", false, totpCodeForTest(t, setup.Secret, time.Now()), first.Challenge)
	if err != nil {
		t.Fatalf("second factor login: %v", err)
	}
	if !second.IsAdmin || len(secondRec.Result().Cookies()) != 1 {
		t.Fatalf("verified login did not create session: result=%+v cookies=%d", second, len(secondRec.Result().Cookies()))
	}
}

func TestTwoFactorRecoveryCodeCanOnlyBeUsedOnce(t *testing.T) {
	svc, _, ctx := newTestAuth(t)
	setup, err := svc.BeginTwoFactorSetup(ctx, TwoFactorSetupRequest{Password: "changed-secret"})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := svc.ConfirmTwoFactorSetup(ctx, TwoFactorConfirmRequest{
		SetupToken: setup.SetupToken,
		Code:       totpCodeForTest(t, setup.Secret, time.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	code := recovery.RecoveryCodes[0]
	if err := svc.verifyTwoFactorCode(ctx, code); err != nil {
		t.Fatalf("first recovery code use: %v", err)
	}
	err = svc.verifyTwoFactorCode(ctx, code)
	if err == nil {
		t.Fatal("consumed recovery code was accepted again")
	}
	// 用错码必须是 VALIDATION(400)：401 + ADMIN_AUTH_REQUIRED 会让前端当成登录态失效直接跳登录页
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeValidation {
		t.Fatalf("reused recovery code error = %v, want %v", err, domain.CodeValidation)
	}
}

func TestTwoFactorWrongPasswordIsValidationError(t *testing.T) {
	svc, _, ctx := newTestAuth(t)
	if _, err := svc.BeginTwoFactorSetup(ctx, TwoFactorSetupRequest{Password: "changed-secret"}); err != nil {
		t.Fatal(err)
	}
	err := svc.DisableTwoFactor(ctx, TwoFactorVerifyRequest{Password: "wrong-password", Code: "123456"})
	if err == nil {
		t.Fatal("wrong password was accepted")
	}
	if ae, ok := domain.AsAppError(err); !ok || ae.Code != domain.CodeValidation {
		t.Fatalf("wrong password error = %v, want %v", err, domain.CodeValidation)
	}
}

func TestTOTPMatchesRFC6238SHA1Vector(t *testing.T) {
	secret := []byte("12345678901234567890")
	if got := totpCode(secret, 59/30); got != "287082" {
		t.Fatalf("TOTP code = %q, want 287082", got)
	}
}

func TestTwoFactorAttemptLimit(t *testing.T) {
	svc, _, _ := newTestAuth(t)
	for i := 0; i < 8; i++ {
		if !svc.allowTwoFactorAttempt("admin|127.0.0.1") {
			t.Fatalf("attempt %d was blocked too early", i+1)
		}
	}
	if svc.allowTwoFactorAttempt("admin|127.0.0.1") {
		t.Fatal("ninth attempt should be rate limited")
	}
	svc.clearTwoFactorAttempts("admin|127.0.0.1")
	if !svc.allowTwoFactorAttempt("admin|127.0.0.1") {
		t.Fatal("successful verification should clear the attempt window")
	}
}

func totpCodeForTest(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return totpCode(decoded, now.Unix()/30)
}

func TestReadSessionExpiresAfterTimeout(t *testing.T) {
	svc, configs, ctx := newTestAuth(t)
	_ = configs.Set(ctx, KeySessionTimeout, "1800")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := svc.WriteSession(rec, req, Session{IsAdmin: true, Username: "admin"}, false); err != nil {
		t.Fatalf("write session: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	checkReq := httptest.NewRequest(http.MethodGet, "/", nil)
	checkReq.AddCookie(cookie)
	if _, ok := svc.ReadSession(checkReq); !ok {
		t.Fatal("session should be valid immediately after login")
	}

	sess, ok := svc.ReadSession(checkReq)
	if !ok || sess == nil {
		t.Fatal("expected session payload")
	}
	sess.CreatedAt = time.Now().Add(-31 * time.Minute).Format(time.RFC3339)
	rec2 := httptest.NewRecorder()
	if err := svc.WriteSession(rec2, checkReq, *sess, false); err != nil {
		t.Fatalf("rewrite session: %v", err)
	}

	expiredReq := httptest.NewRequest(http.MethodGet, "/", nil)
	expiredReq.AddCookie(rec2.Result().Cookies()[0])
	if _, ok := svc.ReadSession(expiredReq); ok {
		t.Fatal("session should expire after configured timeout")
	}
}

func TestUpdateCredentialsPreservesSessionCreatedAt(t *testing.T) {
	svc, configs, ctx := newTestAuth(t)
	_ = configs.Set(ctx, KeySessionTimeout, "1800")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	createdAt := time.Now().Add(-20 * time.Minute).Format(time.RFC3339)
	if err := svc.WriteSession(rec, req, Session{
		IsAdmin:   true,
		Username:  "admin",
		CreatedAt: createdAt,
	}, false); err != nil {
		t.Fatalf("write session: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	updateReq := httptest.NewRequest(http.MethodPost, "/api/admin/update-credentials", nil)
	updateReq.AddCookie(cookie)
	sess, ok := svc.ReadSession(updateReq)
	if !ok {
		t.Fatal("expected active session before update")
	}
	timeout := 0.5
	updateRec := httptest.NewRecorder()
	if err := svc.UpdateCredentials(ctx, updateReq, updateRec, UpdateCredentialsRequest{
		AdminUsername:  "admin",
		SessionTimeout: &timeout,
	}, sess); err != nil {
		t.Fatalf("update credentials: %v", err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/", nil)
	afterReq.AddCookie(updateRec.Result().Cookies()[0])
	got, ok := svc.ReadSession(afterReq)
	if !ok || got == nil {
		t.Fatal("session should remain valid after settings save")
	}
	if got.CreatedAt != createdAt {
		t.Fatalf("CreatedAt changed after settings save: got %q want %q", got.CreatedAt, createdAt)
	}
}

func TestUpdateCredentialsClearsMustChangePassword(t *testing.T) {
	svc, _, ctx := newTestAuth(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := svc.WriteSession(rec, req, Session{
		IsAdmin:              true,
		Username:             "admin",
		MustChangePassword:   true,
		PasswordChangeReason: "default_credentials",
	}, false); err != nil {
		t.Fatalf("write session: %v", err)
	}
	cookie := rec.Result().Cookies()[0]

	updateReq := httptest.NewRequest(http.MethodPost, "/api/admin/update-credentials", nil)
	updateReq.AddCookie(cookie)
	sess, ok := svc.ReadSession(updateReq)
	if !ok {
		t.Fatal("expected active session before update")
	}
	updateRec := httptest.NewRecorder()
	if err := svc.UpdateCredentials(ctx, updateReq, updateRec, UpdateCredentialsRequest{
		AdminUsername: "admin",
		AdminPassword: "new-secret",
	}, sess); err != nil {
		t.Fatalf("update credentials: %v", err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/", nil)
	afterReq.AddCookie(updateRec.Result().Cookies()[0])
	got, ok := svc.ReadSession(afterReq)
	if !ok || got == nil {
		t.Fatal("session should remain valid after password change")
	}
	if got.MustChangePassword {
		t.Fatal("session must_change_password should be cleared after password upgrade")
	}
	st := svc.Status(ctx, afterReq)
	if st.MustChangePassword {
		t.Fatal("status must_change_password should be false after password upgrade")
	}
}

func TestUpdateCredentialsValidationFailureDoesNotPersistAnySetting(t *testing.T) {
	svc, configs, ctx := newTestAuth(t)
	if err := configs.Set(ctx, KeyPublicIndexEnabled, "true"); err != nil {
		t.Fatalf("set public index: %v", err)
	}

	publicIndexEnabled := false
	invalidConcurrency := 6
	err := svc.UpdateCredentials(
		ctx,
		httptest.NewRequest(http.MethodPost, "/api/admin/update-credentials", nil),
		httptest.NewRecorder(),
		UpdateCredentialsRequest{
			AdminUsername:         "changed_admin",
			PublicIndexEnabled:    &publicIndexEnabled,
			UploadTaskConcurrency: &invalidConcurrency,
		},
		nil,
	)
	if err == nil {
		t.Fatal("expected invalid concurrency to reject the whole update")
	}

	username, ok, getErr := configs.Get(ctx, KeyAdminUsername)
	if getErr != nil {
		t.Fatalf("get username: %v", getErr)
	}
	if !ok || username != "admin" {
		t.Fatalf("username changed after rejected update: got %q, ok=%v", username, ok)
	}
	publicIndex, ok, getErr := configs.Get(ctx, KeyPublicIndexEnabled)
	if getErr != nil {
		t.Fatalf("get public index: %v", getErr)
	}
	if !ok || publicIndex != "true" {
		t.Fatalf("public index changed after rejected update: got %q, ok=%v", publicIndex, ok)
	}
}

func TestDefaultCredentialsMayOnlyUseBootstrapRestoreEndpoints(t *testing.T) {
	svc, _, ctx := newBareTestAuth(t)
	sess := &Session{
		IsAdmin:              true,
		Username:             "admin",
		MustChangePassword:   true,
		PasswordChangeReason: "default_credentials",
	}

	allowed := []string{
		"/api/admin/backups/import",
		"/api/admin/backups/11111111-1111-1111-1111-111111111111/restore",
		"/api/admin/backups/restart",
	}
	for _, path := range allowed {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if err := svc.EnsureAdminAccess(ctx, req, sess); err != nil {
			t.Fatalf("bootstrap restore path %q rejected: %v", path, err)
		}
	}

	blocked := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/backups/"},
		{http.MethodPost, "/api/admin/backups/"},
		{http.MethodDelete, "/api/admin/backups/pending"},
		{http.MethodPost, "/api/admin/accounts"},
		{http.MethodGet, "/api/admin/backups/import"},
	}
	for _, item := range blocked {
		req := httptest.NewRequest(item.method, item.path, nil)
		if err := svc.EnsureAdminAccess(ctx, req, sess); err == nil {
			t.Fatalf("locked default session unexpectedly accessed %s %s", item.method, item.path)
		}
	}
}

func TestTemporaryPasswordCannotUseBootstrapRestoreEndpoints(t *testing.T) {
	svc, _, ctx := newTestAuth(t)
	sess := &Session{
		IsAdmin:              true,
		Username:             "admin",
		MustChangePassword:   true,
		PasswordChangeReason: "temporary_password",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/backups/import", nil)
	if err := svc.EnsureAdminAccess(ctx, req, sess); err == nil {
		t.Fatal("temporary password session must not use bootstrap restore endpoints")
	}
}
