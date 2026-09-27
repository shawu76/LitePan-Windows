package notification

import (
	"context"
	"sync"
	"testing"
	"time"

	"litepan/internal/domain"
)

type fakeRepo struct {
	mu     sync.Mutex
	items  map[int64]*domain.Notification
	nextID int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{items: map[int64]*domain.Notification{}}
}

func (r *fakeRepo) Create(_ context.Context, n *domain.Notification) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	n.ID = r.nextID
	clone := *n
	r.items[n.ID] = &clone
	return n.ID, nil
}

func (r *fakeRepo) List(_ context.Context, _, _ int) ([]*domain.Notification, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.Notification, 0, len(r.items))
	for _, it := range r.items {
		clone := *it
		out = append(out, &clone)
	}
	return out, nil
}

func (r *fakeRepo) UnreadCount(_ context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, it := range r.items {
		if !it.IsRead {
			count++
		}
	}
	return count, nil
}

func (r *fakeRepo) MarkRead(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if it, ok := r.items[id]; ok {
		it.IsRead = true
	}
	return nil
}

func (r *fakeRepo) MarkAllRead(_ context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for _, it := range r.items {
		if !it.IsRead {
			it.IsRead = true
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) Delete(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, id)
	return nil
}

func (r *fakeRepo) DeleteAll(_ context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := int64(len(r.items))
	r.items = map[int64]*domain.Notification{}
	return n, nil
}

func (r *fakeRepo) DeleteByRef(_ context.Context, category string, refID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for id, it := range r.items {
		if it.Category == category && it.RefID == refID {
			delete(r.items, id)
			n++
		}
	}
	return n, nil
}

var _ domain.NotificationRepository = (*fakeRepo)(nil)

// waitCount 等待订阅通道推出期望的未读数；中途出现其它值直接跳过（推送只保证最终一致）。
func waitCount(t *testing.T, ch <-chan int, want int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case got, ok := <-ch:
			if !ok {
				t.Fatalf("订阅通道已关闭，未等到未读数 %d", want)
			}
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("超时未收到未读数 %d", want)
		}
	}
}

func TestNotifyPublishesUnreadCount(t *testing.T) {
	svc := NewService(Options{Repo: newFakeRepo()})
	ch, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	svc.Notify(context.Background(), "info", "system", "标题", "内容", 0, 0)
	waitCount(t, ch, 1)

	svc.Notify(context.Background(), "info", "system", "标题2", "内容", 0, 0)
	waitCount(t, ch, 2)

	if _, err := svc.MarkAllRead(context.Background()); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	waitCount(t, ch, 0)
}

// 已读 / 删除不发 eventbus 事件，必须同样推送未读数，否则多端不会同步。
func TestReadAndDeletePublishUnreadCount(t *testing.T) {
	svc := NewService(Options{Repo: newFakeRepo()})
	ch, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	id, err := svc.repo.Create(context.Background(), &domain.Notification{Level: "info", Category: "system"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.MarkRead(context.Background(), id); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	waitCount(t, ch, 0)

	svc.Notify(context.Background(), "info", "system", "标题", "内容", 0, 0)
	waitCount(t, ch, 1)

	if _, err := svc.DeleteAll(context.Background()); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	waitCount(t, ch, 0)
}

func TestUnsubscribeClosesChannelAndStopsPush(t *testing.T) {
	svc := NewService(Options{Repo: newFakeRepo()})
	ch, unsubscribe := svc.Subscribe()
	unsubscribe()

	if _, ok := <-ch; ok {
		t.Fatal("退订后通道应已关闭")
	}
	// 退订后继续变更不应 panic，也不应再推送。
	svc.Notify(context.Background(), "info", "system", "标题", "内容", 0, 0)

	// 重复退订必须安全。
	unsubscribe()
}

// 慢客户端不能拖住发布方：通知写入不能被一个不消费的订阅者阻塞。
func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	svc := NewService(Options{Repo: newFakeRepo()})
	_, unsubscribe := svc.Subscribe() // 故意不消费
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			svc.Notify(context.Background(), "info", "system", "标题", "内容", 0, 0)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("慢订阅者拖住了发布方")
	}
}

func TestSlowSubscriberKeepsLatestCount(t *testing.T) {
	svc := NewService(Options{Repo: newFakeRepo()})
	ch, unsubscribe := svc.Subscribe()
	defer unsubscribe()

	// 不消费第一次推送，让缓冲保持占满；第二次推送必须替换旧值。
	svc.Notify(context.Background(), "info", "system", "标题1", "内容", 0, 0)
	svc.Notify(context.Background(), "info", "system", "标题2", "内容", 0, 0)
	waitCount(t, ch, 2)
}

// 并发订阅/退订与推送交错，配合 -race 校验通道关闭与发送不会互相踩踏。
func TestSubscribeUnsubscribeUnderPublish(t *testing.T) {
	svc := NewService(Options{Repo: newFakeRepo()})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			svc.Notify(context.Background(), "info", "system", "标题", "内容", 0, 0)
		}
	}()

	for i := 0; i < 50; i++ {
		ch, unsubscribe := svc.Subscribe()
		go func() {
			for range ch {
			}
		}()
		unsubscribe()
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("并发订阅/退订下发布阻塞")
	}
}
