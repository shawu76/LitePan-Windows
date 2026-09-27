package cache

import (
	"container/list"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// guardRecorder 收集后台任务兜底日志，用于断言崩溃确实被记下来。
type guardRecorder struct {
	mu     sync.Mutex
	comps  []string
	stacks []string
}

func (r *guardRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *guardRecorder) Handle(_ context.Context, rec slog.Record) error {
	var comp, stack string
	rec.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "component":
			comp = a.Value.String()
		case "stack":
			stack = a.Value.String()
		}
		return true
	})
	r.mu.Lock()
	r.comps = append(r.comps, comp)
	r.stacks = append(r.stacks, stack)
	r.mu.Unlock()
	return nil
}

func (r *guardRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *guardRecorder) WithGroup(string) slog.Handler      { return r }

func (r *guardRecorder) snapshot() (int, string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.comps) == 0 {
		return 0, "", ""
	}
	return len(r.comps), r.comps[0], r.stacks[0]
}

// TestGcLoopSurvivesPanickingSweep 用故障注入验证 gcLoop 的兜底接线真的生效。
//
// 往缓存里塞一个类型不对的值，让每一轮清理都在 *entry 类型断言处崩溃。
// 若兜底没接上，这个测试进程会直接崩掉；接上了则应看到循环继续跑、每轮记一条带堆栈的日志。
func TestGcLoopSurvivesPanickingSweep(t *testing.T) {
	rec := &guardRecorder{}
	svc := &Service{
		ll:    list.New(),
		items: make(map[string]*list.Element),
		log:   slog.New(rec),
		stop:  make(chan struct{}),
	}
	svc.items["坏数据"] = svc.ll.PushBack("这里本该是 *entry")

	go svc.gcLoop(5 * time.Millisecond)
	// 先让循环跑够几轮，再通知它退出。
	deadline := time.Now().Add(2 * time.Second)
	for {
		if n, _, _ := rec.snapshot(); n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(svc.stop)

	count, component, stack := rec.snapshot()
	if count < 3 {
		t.Fatalf("兜底未生效或循环在崩溃后停下了：只记录到 %d 次崩溃（期望 ≥3）", count)
	}
	if component != "cache.gc" {
		t.Errorf("兜底日志的组件名不对：%q", component)
	}
	if !strings.Contains(stack, "SweepExpired") {
		t.Errorf("兜底日志的堆栈没指向真正的崩溃现场：\n%s", stack)
	}
}
