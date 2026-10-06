package uploadutil

import (
	"slices"
	"sync"
	"testing"
)

func TestParallelProgressRetryAndCompletion(t *testing.T) {
	var got []int64
	p := NewParallelProgress(100, 300, func(uploaded, total int64, message string) {
		if total != 300 || message != "上传中" {
			t.Errorf("进度上下文丢失: total=%d message=%q", total, message)
		}
		got = append(got, uploaded)
	}, "上传中")
	p.Update(1, 60, 100)
	p.Update(2, 20, 100)
	p.Update(1, 10, 100) // 分片重试，不回退或重复累计。
	p.Update(1, 100, 100)
	p.Complete(1, 100)
	p.Update(2, 200, 100) // 在途字节不得超过分片大小。
	p.Complete(2, 100)
	if want := []int64{160, 180, 220, 300}; !slices.Equal(got, want) {
		t.Fatalf("进度=%v，期望=%v", got, want)
	}
}

func TestParallelProgressConcurrentParts(t *testing.T) {
	const count = 3
	var last int64
	p := NewParallelProgress(0, count*100, func(uploaded, total int64, _ string) {
		if uploaded <= last || uploaded > total {
			t.Errorf("并发进度异常: last=%d uploaded=%d total=%d", last, uploaded, total)
		}
		last = uploaded
	}, "")
	var workers sync.WaitGroup
	for part := range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for sent := int64(1); sent <= 100; sent++ {
				p.Update(part, sent, 100)
			}
			p.Complete(part, 100)
		}()
	}
	workers.Wait()
	if last != count*100 {
		t.Fatalf("最终进度=%d，期望=%d", last, count*100)
	}
}
