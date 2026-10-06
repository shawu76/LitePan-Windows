package uploadutil

import (
	"io"
	"sync"

	"litepan/internal/driver"
)

const DefaultReadProgressStep = 1024 * 1024

type ReadProgress struct {
	R          io.Reader
	Base       int64
	Total      int64
	Step       int64
	Sent       int64
	lastEmit   int64
	OnProgress func(uploaded int64)
}

func (p *ReadProgress) Read(b []byte) (int, error) {
	n, err := p.R.Read(b)
	if n <= 0 {
		return n, err
	}
	p.Sent += int64(n)
	if p.OnProgress != nil {
		step := p.Step
		if step <= 0 {
			step = DefaultReadProgressStep
		}
		if p.Sent-p.lastEmit >= step || err == io.EOF {
			p.lastEmit = p.Sent
			p.OnProgress(p.Base + p.Sent)
		}
	}
	return n, err
}

// ParallelProgress 汇总已完成与在途分片；重试时保持已上报进度单调。
// 调用 Complete 前，调用方必须停止该分片的进度回调。
type ParallelProgress struct {
	mu         sync.Mutex
	completed  int64
	active     map[int]int64
	reported   int64
	total      int64
	onProgress driver.UploadProgress
	message    string
}

func NewParallelProgress(completed, total int64, onProgress driver.UploadProgress, message string) *ParallelProgress {
	return &ParallelProgress{
		completed: completed, active: make(map[int]int64), reported: completed,
		total: total, onProgress: onProgress, message: message,
	}
}

func (p *ParallelProgress) Update(part int, sent, size int64) {
	if p.onProgress == nil {
		return
	}
	sent = max(0, min(sent, size))
	p.mu.Lock()
	defer p.mu.Unlock()
	if sent <= p.active[part] {
		return
	}
	p.active[part] = sent
	p.notifyLocked()
}

func (p *ParallelProgress) Complete(part int, size int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.active, part)
	p.completed += size
	p.notifyLocked()
}

func (p *ParallelProgress) notifyLocked() {
	uploaded := p.completed
	for _, sent := range p.active {
		uploaded += sent
	}
	uploaded = min(p.total, max(p.reported, uploaded))
	if uploaded == p.reported {
		return
	}
	p.reported = uploaded
	NotifyProgress(p.onProgress, uploaded, p.total, p.message)
}
