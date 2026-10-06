package dav

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

const (
	webDAVAuthCacheTTL     = 30 * time.Minute
	webDAVAuthCacheMaxSize = 64
)

// authCache 只缓存已成功验证的凭据指纹，避免 WebDAV 客户端的连续请求重复计算 PBKDF2。
// 指纹包含当前存储的密码哈希，管理员账号或密码变更后，旧缓存会自然失效。
type authCache struct {
	mu      sync.Mutex
	secret  [32]byte
	entries map[[32]byte]time.Time
	ttl     time.Duration
	maxSize int
}

func newAuthCache() *authCache {
	c := &authCache{
		entries: make(map[[32]byte]time.Time),
		ttl:     webDAVAuthCacheTTL,
		maxSize: webDAVAuthCacheMaxSize,
	}
	_, _ = rand.Read(c.secret[:])
	return c
}

func (c *authCache) verify(storedUser, storedPass, username, password string, verifyPassword func(string, string) bool) bool {
	if c == nil || username != storedUser || password == "" || verifyPassword == nil {
		return false
	}
	key := c.fingerprint(storedUser, storedPass, password)
	now := time.Now()
	if c.valid(key, now) {
		return true
	}
	if !verifyPassword(storedPass, password) {
		return false
	}
	c.remember(key, now)
	return true
}

func (c *authCache) fingerprint(username, storedPass, password string) [32]byte {
	mac := hmac.New(sha256.New, c.secret[:])
	_, _ = mac.Write([]byte(username))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(storedPass))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(password))
	var key [32]byte
	copy(key[:], mac.Sum(nil))
	return key
}

func (c *authCache) valid(key [32]byte, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	expiresAt, ok := c.entries[key]
	if !ok {
		return false
	}
	if !now.Before(expiresAt) {
		delete(c.entries, key)
		return false
	}
	return true
}

func (c *authCache) remember(key [32]byte, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for cachedKey, expiresAt := range c.entries {
		if !now.Before(expiresAt) {
			delete(c.entries, cachedKey)
		}
	}
	if len(c.entries) >= c.maxSize {
		var oldestKey [32]byte
		var oldestExpiry time.Time
		for cachedKey, expiresAt := range c.entries {
			if oldestExpiry.IsZero() || expiresAt.Before(oldestExpiry) {
				oldestKey = cachedKey
				oldestExpiry = expiresAt
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = now.Add(c.ttl)
}
