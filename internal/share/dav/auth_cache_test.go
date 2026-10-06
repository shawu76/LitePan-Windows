package dav

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthCacheReusesSuccessfulVerification(t *testing.T) {
	cache := newAuthCache()
	var calls atomic.Int32
	verify := func(stored, password string) bool {
		calls.Add(1)
		return stored == "hash-v1" && password == "correct"
	}

	if !cache.verify("admin", "hash-v1", "admin", "correct", verify) {
		t.Fatal("首次正确凭据应验证成功")
	}
	if !cache.verify("admin", "hash-v1", "admin", "correct", verify) {
		t.Fatal("相同凭据应命中成功缓存")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("密码校验调用了 %d 次，期望 1 次", got)
	}
}

func TestAuthCacheDoesNotCacheFailuresAndInvalidatesOnCredentialChange(t *testing.T) {
	cache := newAuthCache()
	var calls atomic.Int32
	verify := func(stored, password string) bool {
		calls.Add(1)
		return (stored == "hash-v1" && password == "old-password") ||
			(stored == "hash-v2" && password == "new-password")
	}

	if cache.verify("admin", "hash-v1", "admin", "wrong", verify) {
		t.Fatal("错误密码不应验证成功")
	}
	if cache.verify("admin", "hash-v1", "admin", "wrong", verify) {
		t.Fatal("错误密码不应被缓存")
	}
	if !cache.verify("admin", "hash-v1", "admin", "old-password", verify) {
		t.Fatal("旧密码首次校验应成功")
	}
	if cache.verify("admin", "hash-v2", "admin", "old-password", verify) {
		t.Fatal("密码哈希变更后不应命中旧缓存")
	}
	if cache.verify("admin", "hash-v2", "renamed", "new-password", verify) {
		t.Fatal("管理员用户名变更后旧用户名不应通过")
	}
}

func TestAuthCacheExpiresAndStaysBounded(t *testing.T) {
	cache := newAuthCache()
	cache.ttl = time.Millisecond
	cache.maxSize = 2
	verify := func(_, _ string) bool { return true }

	if !cache.verify("admin", "hash", "admin", "one", verify) {
		t.Fatal("凭据 one 验证失败")
	}
	time.Sleep(2 * time.Millisecond)
	if !cache.verify("admin", "hash", "admin", "two", verify) {
		t.Fatal("凭据 two 验证失败")
	}
	cache.ttl = time.Minute
	for _, password := range []string{"three", "four", "five"} {
		if !cache.verify("admin", "hash", "admin", password, verify) {
			t.Fatalf("凭据 %s 验证失败", password)
		}
	}
	cache.mu.Lock()
	size := len(cache.entries)
	cache.mu.Unlock()
	if size > cache.maxSize {
		t.Fatalf("缓存条目数 = %d，超过上限 %d", size, cache.maxSize)
	}
}
