package cache

import (
	"testing"
	"time"
)

func TestSweepExpiredRemovesOnlyExpiredEntries(t *testing.T) {
	s := NewService(Options{})
	t.Cleanup(s.Close)

	s.Set("expired1", "v1", 10*time.Millisecond)
	s.Set("expired2", "v2", 10*time.Millisecond)
	s.Set("alive", "v3", time.Hour)
	s.Set("forever", "v4", 0) // 零 TTL = 永不过期
	if len(s.items) != 4 {
		t.Fatalf("初始条目数 = %d, want 4", len(s.items))
	}

	time.Sleep(30 * time.Millisecond)

	s.sweepExpired()

	if len(s.items) != 2 {
		t.Fatalf("清理后条目数 = %d, want 2（只应删掉两个过期项）", len(s.items))
	}
	for _, k := range []string{"alive", "forever"} {
		if _, ok := s.Get(k); !ok {
			t.Fatalf("%s 不应被清理", k)
		}
	}
	for _, k := range []string{"expired1", "expired2"} {
		if _, ok := s.Get(k); ok {
			t.Fatalf("%s 应已被清理", k)
		}
	}
}

func TestSweepExpiredReturnsCountAndBytes(t *testing.T) {
	s := NewService(Options{})
	t.Cleanup(s.Close)

	s.Set("keep", "v", 0)
	s.Set("drop", "0123456789", 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)

	count, bytes := s.SweepExpired()
	if count != 1 {
		t.Fatalf("SweepExpired 清理数 = %d, want 1", count)
	}
	if bytes <= 0 {
		t.Fatalf("SweepExpired 释放字节 = %d, want > 0", bytes)
	}
	if len(s.items) != 1 {
		t.Fatalf("剩余条目数 = %d, want 1", len(s.items))
	}
}
