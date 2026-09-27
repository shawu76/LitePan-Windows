package auth

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"litepan/internal/domain"
	"litepan/internal/driver"
)

func TestScheduleLogIgnoresSmallRecalculationDrift(t *testing.T) {
	sch := NewScheduler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	next := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	sch.logNextWaitIfChanged(next, time.Hour, "", nil)
	sch.logNextWaitIfChanged(next.Add(10*time.Second), time.Hour, "Cookie 回写", nil)
	if !sch.lastLoggedNext.Equal(next) {
		t.Fatalf("小幅重算不应重复记录：%v", sch.lastLoggedNext)
	}
	sch.logNextWaitIfChanged(next.Add(time.Minute), time.Hour, "认证刷新完成", nil)
	if !sch.lastLoggedNext.Equal(next.Add(time.Minute)) {
		t.Fatalf("明显变化应记录新时间：%v", sch.lastLoggedNext)
	}
}

func TestSeedInitialScheduleTokenDriver(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	st := &domain.AuthState{AccountID: 1, Status: domain.AuthActive, AccessToken: "t", RefreshToken: "r"}
	SeedInitialSchedule(st, "refresh_test", now)
	if st.LastRefreshAt != now {
		t.Fatalf("last_refresh_at=%v", st.LastRefreshAt)
	}
	want := now.Add(30 * 24 * time.Hour)
	if !st.TokenExpires.Equal(want) {
		t.Fatalf("token_expires=%v want %v", st.TokenExpires, want)
	}
}

func TestCalcNextCheckUsesTokenExpiresDespiteFirstBoot(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	svc, repo, _, bus := newTestService(now, driver.RefreshSuccess)
	defer bus.Close(context.Background())
	repo.states[1] = &domain.AuthState{
		AccountID:     1,
		Status:        domain.AuthActive,
		LastRefreshAt: now,
		TokenExpires:  now.Add(30 * 24 * time.Hour),
	}
	next := svc.calcNextCheck(context.Background(), 1, now, true)
	want := now.Add(30*24*time.Hour - 10*time.Hour)
	if !next.Equal(want) {
		t.Fatalf("next=%v want %v", next, want)
	}
}
