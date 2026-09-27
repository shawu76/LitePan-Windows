package safego_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"litepan/pkg/safego"
)

// captureHandler 收集日志记录，用来断言崩溃确实被记下来了。
type captureHandler struct {
	mu      sync.Mutex
	records []map[string]any
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	fields := map[string]any{"msg": r.Message, "level": r.Level}
	r.Attrs(func(a slog.Attr) bool {
		fields[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.records = append(h.records, fields)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) first(t *testing.T) map[string]any {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.records) == 0 {
		t.Fatal("没有任何日志记录，崩溃没有被记下来")
	}
	return h.records[0]
}

func newLogger() (*slog.Logger, *captureHandler) {
	h := &captureHandler{}
	return slog.New(h), h
}

func TestGuardRunsNormally(t *testing.T) {
	log, handler := newLogger()
	ran := false

	if completed := safego.Guard(log, "test.normal", func() { ran = true }); !completed {
		t.Fatal("正常执行的函数不应被报告为未完成")
	}
	if !ran {
		t.Fatal("函数没有被执行")
	}
	if len(handler.records) != 0 {
		t.Fatalf("正常路径不应产生任何日志，实际 %d 条", len(handler.records))
	}
}

// 这个用例本身就是"兜底有效"的证明：若 Guard 没有拦住 panic，测试进程会直接崩掉。
func TestGuardCatchesPanic(t *testing.T) {
	log, handler := newLogger()

	completed := safego.Guard(log, "cache.gc", func() { panic("故意制造的崩溃") })

	if completed {
		t.Fatal("崩溃的任务不应被报告为已完成")
	}
	rec := handler.first(t)
	if rec["level"] != slog.LevelError {
		t.Errorf("崩溃应记 Error 级，实际 %v", rec["level"])
	}
	if rec["component"] != "cache.gc" {
		t.Errorf("日志缺少组件名，实际 %v", rec["component"])
	}
	if !strings.Contains(rec["msg"].(string), "已拦截") {
		t.Errorf("日志文案未说明已拦截：%v", rec["msg"])
	}
	if got, _ := rec["panic"].(string); got != "故意制造的崩溃" {
		t.Errorf("日志未记录崩溃原因，实际 %v", rec["panic"])
	}
}

// 兜底必须带堆栈，否则等于把 bug 藏起来。
func TestGuardLogsStack(t *testing.T) {
	log, handler := newLogger()

	safego.Guard(log, "test.stack", func() { panic("带堆栈") })

	stack, _ := handler.first(t)["stack"].(string)
	if stack == "" {
		t.Fatal("日志没有堆栈，无法定位问题")
	}
	if !strings.Contains(stack, "TestGuardLogsStack") {
		t.Errorf("堆栈里没有崩溃现场的函数名：\n%s", stack)
	}
}

// 用真实用法验证：兜住的是"一轮"，一轮崩了后面的轮次照常执行。
func TestGuardKeepsLoopRunningAfterPanic(t *testing.T) {
	log, handler := newLogger()
	var ran []int

	for round := 1; round <= 3; round++ {
		round := round
		safego.Guard(log, "test.loop", func() {
			if round == 2 {
				panic("第 2 轮崩溃")
			}
			ran = append(ran, round)
		})
	}

	if len(ran) != 2 || ran[0] != 1 || ran[1] != 3 {
		t.Fatalf("循环未能在崩溃后继续，实际执行的轮次 %v", ran)
	}
	if len(handler.records) != 1 {
		t.Fatalf("应恰好记录 1 条崩溃日志，实际 %d 条", len(handler.records))
	}
}

// 漏传 logger 时不能把故障记录整个丢掉。
func TestGuardWithoutLogger(t *testing.T) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(prev)

	if completed := safego.Guard(nil, "test.nil-log", func() { panic("无 logger") }); completed {
		t.Fatal("崩溃的任务不应被报告为已完成")
	}
}
