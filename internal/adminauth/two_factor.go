package adminauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- TOTP 标准要求使用 HMAC-SHA1。
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"litepan/internal/domain"
	"litepan/pkg/security"
)

const (
	KeyAdminTwoFactorEnabled       = "admin_2fa_enabled"
	KeyAdminTwoFactorSecret        = "admin_2fa_secret"
	KeyAdminTwoFactorRecoveryCodes = "admin_2fa_recovery_codes"

	twoFactorChallengeTTL = 5 * 60
	twoFactorSetupTTL     = 10 * 60
)

type twoFactorAttempt struct {
	Count       int
	WindowStart time.Time
}

type loginChallenge struct {
	Username             string `json:"username"`
	Generation           string `json:"generation,omitempty"`
	Remember             bool   `json:"remember"`
	MustChangePassword   bool   `json:"must_change_password"`
	PasswordChangeReason string `json:"password_change_reason,omitempty"`
}

type setupToken struct {
	Username string `json:"username"`
	Secret   string `json:"secret"`
}

type TwoFactorSetupRequest struct {
	Password         string `json:"password"`
	VerificationCode string `json:"verification_code"`
}

type TwoFactorSetupResult struct {
	SetupToken string `json:"setup_token"`
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
	QRCode     string `json:"qr_code"`
}

type TwoFactorConfirmRequest struct {
	SetupToken string `json:"setup_token"`
	Code       string `json:"code"`
}

type TwoFactorVerifyRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

type TwoFactorRecoveryResult struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func (s *Service) twoFactorEnabled(ctx context.Context) bool {
	return s.configBool(ctx, KeyAdminTwoFactorEnabled, false) && s.configString(ctx, KeyAdminTwoFactorSecret, "") != ""
}

func (s *Service) allowTwoFactorAttempt(key string) bool {
	now := time.Now()
	s.twoFactorTryMu.Lock()
	defer s.twoFactorTryMu.Unlock()
	attempt := s.twoFactorTries[key]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) >= 5*time.Minute {
		attempt = twoFactorAttempt{WindowStart: now}
	}
	if attempt.Count >= 8 {
		s.twoFactorTries[key] = attempt
		return false
	}
	attempt.Count++
	s.twoFactorTries[key] = attempt
	return true
}

func (s *Service) clearTwoFactorAttempts(key string) {
	s.twoFactorTryMu.Lock()
	delete(s.twoFactorTries, key)
	s.twoFactorTryMu.Unlock()
}

func (s *Service) BeginTwoFactorSetup(ctx context.Context, req TwoFactorSetupRequest) (*TwoFactorSetupResult, error) {
	if err := s.verifyCurrentPassword(ctx, req.Password); err != nil {
		return nil, err
	}
	if s.twoFactorEnabled(ctx) {
		if err := s.verifyTwoFactorCode(ctx, req.VerificationCode); err != nil {
			return nil, domain.Errorf(domain.CodeValidation, "重新绑定前请输入当前动态码或恢复码")
		}
	}

	secret, err := randomBase32Secret()
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	username, _ := s.adminCredentials(ctx)
	otpURL := buildOTPAuthURL(username, secret)
	png, err := qrcode.Encode(otpURL, qrcode.Medium, 256)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	raw, _ := json.Marshal(setupToken{Username: username, Secret: secret})
	token, err := s.encryptValue(raw)
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	return &TwoFactorSetupResult{
		SetupToken: token,
		Secret:     secret,
		OTPAuthURL: otpURL,
		QRCode:     "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	}, nil
}

func (s *Service) ConfirmTwoFactorSetup(ctx context.Context, req TwoFactorConfirmRequest) (*TwoFactorRecoveryResult, error) {
	raw, err := s.decryptValue(strings.TrimSpace(req.SetupToken), twoFactorSetupTTL)
	if err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "绑定信息已过期，请重新开始")
	}
	var setup setupToken
	if err := json.Unmarshal(raw, &setup); err != nil {
		return nil, domain.Errorf(domain.CodeValidation, "绑定信息无效")
	}
	username, _ := s.adminCredentials(ctx)
	if setup.Username != username || !validateTOTP(setup.Secret, req.Code, time.Now()) {
		return nil, domain.Errorf(domain.CodeValidation, "动态验证码不正确")
	}
	encryptedSecret, err := s.encryptValue([]byte(setup.Secret))
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	codes, hashes, err := s.generateRecoveryCodes()
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	hashesJSON, _ := json.Marshal(hashes)

	s.twoFactorMu.Lock()
	defer s.twoFactorMu.Unlock()
	for _, update := range []configUpdate{
		{key: KeyAdminTwoFactorSecret, value: encryptedSecret},
		{key: KeyAdminTwoFactorRecoveryCodes, value: string(hashesJSON)},
		{key: KeyAdminTwoFactorEnabled, value: "true"},
	} {
		if err := s.setConfig(ctx, update.key, update.value); err != nil {
			return nil, domain.Wrap(domain.CodeInternal, err)
		}
	}
	s.log.Info("管理员两步验证已开启")
	return &TwoFactorRecoveryResult{RecoveryCodes: codes}, nil
}

func (s *Service) DisableTwoFactor(ctx context.Context, req TwoFactorVerifyRequest) error {
	if err := s.verifyCurrentPassword(ctx, req.Password); err != nil {
		return err
	}
	if !s.twoFactorEnabled(ctx) {
		return nil
	}
	if err := s.verifyTwoFactorCode(ctx, req.Code); err != nil {
		return err
	}
	s.twoFactorMu.Lock()
	defer s.twoFactorMu.Unlock()
	for _, key := range []string{KeyAdminTwoFactorEnabled, KeyAdminTwoFactorSecret, KeyAdminTwoFactorRecoveryCodes} {
		if err := s.setConfig(ctx, key, ""); err != nil {
			return domain.Wrap(domain.CodeInternal, err)
		}
	}
	s.log.Warn("管理员两步验证已关闭")
	return nil
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, req TwoFactorVerifyRequest) (*TwoFactorRecoveryResult, error) {
	if err := s.verifyCurrentPassword(ctx, req.Password); err != nil {
		return nil, err
	}
	if !s.twoFactorEnabled(ctx) {
		return nil, domain.Errorf(domain.CodeValidation, "尚未开启两步验证")
	}
	if err := s.verifyTwoFactorCode(ctx, req.Code); err != nil {
		return nil, err
	}
	codes, hashes, err := s.generateRecoveryCodes()
	if err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	raw, _ := json.Marshal(hashes)
	if err := s.setConfig(ctx, KeyAdminTwoFactorRecoveryCodes, string(raw)); err != nil {
		return nil, domain.Wrap(domain.CodeInternal, err)
	}
	s.log.Info("管理员两步验证恢复码已重新生成")
	return &TwoFactorRecoveryResult{RecoveryCodes: codes}, nil
}

func (s *Service) verifyCurrentPassword(ctx context.Context, password string) error {
	_, stored := s.adminCredentials(ctx)
	if !security.VerifyAdminPassword(stored, password) {
		// 使用 400，避免前端将输错密码当作登录失效。
		return domain.Errorf(domain.CodeValidation, "当前管理员密码不正确")
	}
	return nil
}

func (s *Service) verifyTwoFactorCode(ctx context.Context, code string) error {
	code = normalizeVerificationCode(code)
	if code == "" {
		return domain.Errorf(domain.CodeValidation, "请输入动态验证码或恢复码")
	}
	encrypted := s.configString(ctx, KeyAdminTwoFactorSecret, "")
	secret, err := s.decryptValue(encrypted, 0)
	if err != nil {
		return domain.Errorf(domain.CodeInternal, "两步验证配置无法读取")
	}
	if validateTOTP(string(secret), code, time.Now()) {
		return nil
	}
	if s.consumeRecoveryCode(ctx, code) {
		return nil
	}
	// 验证失败不应触发前端退出登录。
	return domain.Errorf(domain.CodeValidation, "动态验证码或恢复码不正确；恢复码用过一次即失效")
}

func (s *Service) consumeRecoveryCode(ctx context.Context, code string) bool {
	s.twoFactorMu.Lock()
	defer s.twoFactorMu.Unlock()
	var hashes []string
	if err := json.Unmarshal([]byte(s.configString(ctx, KeyAdminTwoFactorRecoveryCodes, "[]")), &hashes); err != nil {
		return false
	}
	wanted := s.recoveryCodeHash(code)
	for i, hash := range hashes {
		if hmac.Equal([]byte(hash), []byte(wanted)) {
			hashes = append(hashes[:i], hashes[i+1:]...)
			raw, _ := json.Marshal(hashes)
			if err := s.setConfig(ctx, KeyAdminTwoFactorRecoveryCodes, string(raw)); err != nil {
				return false
			}
			return true
		}
	}
	return false
}

func (s *Service) generateRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		raw, err := randomCharacters(10, "ABCDEFGHJKLMNPQRSTUVWXYZ23456789")
		if err != nil {
			return nil, nil, err
		}
		codes[i] = raw[:5] + "-" + raw[5:]
		hashes[i] = s.recoveryCodeHash(codes[i])
	}
	return codes, hashes, nil
}

func (s *Service) recoveryCodeHash(code string) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("litepan-admin-2fa-recovery:" + normalizeVerificationCode(code)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) encryptValue(plain []byte) (string, error) {
	key := sha256.Sum256(append(append([]byte(nil), s.secret...), []byte(":admin-2fa")...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	payload := append([]byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), plain...)
	sealed := gcm.Seal(nonce, nonce, payload, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Service) decryptValue(value string, maxAge int) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(append(append([]byte(nil), s.secret...), []byte(":admin-2fa")...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("密文无效")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(string(plain), "\n", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("密文无效")
	}
	createdAt, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || (maxAge > 0 && time.Now().Unix()-createdAt > int64(maxAge)) {
		return nil, fmt.Errorf("密文已过期")
	}
	return []byte(parts[1]), nil
}

func randomBase32Secret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func randomCharacters(length int, alphabet string) (string, error) {
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(buf), nil
}

func buildOTPAuthURL(username, secret string) string {
	label := "LitePan:" + username
	query := url.Values{
		"secret":    {secret},
		"issuer":    {"LitePan"},
		"algorithm": {"SHA1"},
		"digits":    {"6"},
		"period":    {"30"},
	}
	return "otpauth://totp/" + url.PathEscape(label) + "?" + query.Encode()
}

func normalizeVerificationCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", ""))
}

func validateTOTP(secret, code string, now time.Time) bool {
	code = normalizeVerificationCode(code)
	if len(code) != 6 {
		return false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false
	}
	counter := now.Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		candidate := totpCode(decoded, counter+offset)
		if hmac.Equal([]byte(candidate), []byte(code)) {
			return true
		}
	}
	return false
}

func totpCode(secret []byte, counter int64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(counter))
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	index := sum[len(sum)-1] & 0x0f
	binaryCode := (uint32(sum[index])&0x7f)<<24 |
		(uint32(sum[index+1])&0xff)<<16 |
		(uint32(sum[index+2])&0xff)<<8 |
		(uint32(sum[index+3]) & 0xff)
	return fmt.Sprintf("%06d", binaryCode%1_000_000)
}
