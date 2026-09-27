package notification

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

type Options struct {
	Repo     domain.NotificationRepository
	Accounts domain.AccountRepository
	Log      *slog.Logger
}

type Service struct {
	repo     domain.NotificationRepository
	accounts domain.AccountRepository
	log      *slog.Logger

	// subs 是未读数订阅者（前端 SSE 长连接）。每个通道带 1 个缓冲，
	// 推送采用非阻塞写：慢客户端只保留最新值，不会拖住发布方。
	subMu   sync.Mutex
	subs    map[int64]chan int
	nextSub int64
}

func NewService(opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{repo: opts.Repo, accounts: opts.Accounts, log: log, subs: map[int64]chan int{}}
}

// Subscribe 订阅未读数变化，返回只读通道和退订函数；退订可重复调用。
func (s *Service) Subscribe() (<-chan int, func()) {
	if s == nil {
		closed := make(chan int)
		close(closed)
		return closed, func() {}
	}
	ch := make(chan int, 1)
	s.subMu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	s.subMu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.subMu.Lock()
			if current, ok := s.subs[id]; ok {
				delete(s.subs, id)
				close(current)
			}
			s.subMu.Unlock()
		})
	}
}

// publishUnread 重算未读数并推给所有订阅者。通知已落库后才调用，因此用独立上下文，
// 避免发布方请求结束时上下文已取消导致推送丢失。
func (s *Service) publishUnread() {
	if s == nil || s.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	count, err := s.repo.UnreadCount(ctx)
	if err != nil {
		return
	}
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- count:
		default:
			// 缓冲中已有旧值时用最新值替换，避免突发通知后铃铛停留在旧数量。
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- count:
			default:
			}
		}
	}
}

func (s *Service) Register(bus *eventbus.Bus) {
	if s == nil || bus == nil {
		return
	}
	eventbus.Subscribe(bus, s.onAuthFailed)
	eventbus.Subscribe(bus, s.onAuthRecovered)
	eventbus.Subscribe(bus, s.onCreated)
}

func (s *Service) List(ctx context.Context, limit, offset int) ([]*domain.Notification, error) {
	if s.repo == nil {
		return nil, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.List(ctx, limit, offset)
}

func (s *Service) UnreadCount(ctx context.Context) (int, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	return s.repo.UnreadCount(ctx)
}

func (s *Service) MarkRead(ctx context.Context, id int64) error {
	if s.repo == nil {
		return domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	if err := s.repo.MarkRead(ctx, id); err != nil {
		return err
	}
	s.publishUnread()
	return nil
}

func (s *Service) MarkAllRead(ctx context.Context) (int64, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	n, err := s.repo.MarkAllRead(ctx)
	if err != nil {
		return 0, err
	}
	s.publishUnread()
	return n, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if s.repo == nil {
		return domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.publishUnread()
	return nil
}

func (s *Service) DeleteAll(ctx context.Context) (int64, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	n, err := s.repo.DeleteAll(ctx)
	if err != nil {
		return 0, err
	}
	s.publishUnread()
	return n, nil
}

func (s *Service) DeleteByRef(ctx context.Context, category string, refID int64) (int64, error) {
	if s.repo == nil {
		return 0, domain.Errorf(domain.CodeInternal, "通知仓储未就绪")
	}
	n, err := s.repo.DeleteByRef(ctx, category, refID)
	if err != nil {
		return 0, err
	}
	s.publishUnread()
	return n, nil
}

func (s *Service) Notify(ctx context.Context, level, category, title, message string, accountID, refID int64) {
	if s == nil {
		return
	}
	s.persist(ctx, level, category, title, message, accountID, refID)
}

func (s *Service) onAuthFailed(ctx context.Context, e eventbus.AccountAuthFailed) {
	if !e.Fatal {
		return
	}
	msg := e.Reason
	if name := s.accountName(ctx, e.AccountID); name != "" {
		msg = name + "：" + e.Reason
	}
	s.persist(ctx, "error", "auth", "存储账号认证已失效", msg, e.AccountID, 0)
}

func (s *Service) onAuthRecovered(ctx context.Context, e eventbus.AccountAuthRecovered) {
	name := s.accountName(ctx, e.AccountID)
	if name == "" {
		name = fmt.Sprintf("账号 #%d", e.AccountID)
	}
	s.persist(ctx, "success", "auth", "存储账号认证已恢复", name+" 认证已恢复正常", e.AccountID, 0)
}

func (s *Service) onCreated(ctx context.Context, e eventbus.NotificationCreated) {
	category := e.Category
	if category == "" {
		category = "system"
	}
	level := e.Level
	if level == "" {
		level = "info"
	}
	s.persist(ctx, level, category, e.Title, e.Message, e.AccountID, e.RefID)
}

func (s *Service) persist(ctx context.Context, level, category, title, message string, accountID, refID int64) {
	if s.repo == nil {
		return
	}
	_, err := s.repo.Create(ctx, &domain.Notification{
		Level:     level,
		Category:  category,
		Title:     title,
		Message:   message,
		AccountID: accountID,
		RefID:     refID,
	})
	if err != nil {
		s.log.Warn("写入通知失败", "title", title, "err", err)
		return
	}
	// 新通知落库成功即推送一次未读数，前端铃铛无需再靠轮询发现。
	s.publishUnread()
}

func (s *Service) accountName(ctx context.Context, accountID int64) string {
	if s.accounts == nil || accountID <= 0 {
		return ""
	}
	acc, err := s.accounts.Get(ctx, accountID)
	if err != nil || acc == nil {
		return ""
	}
	return acc.Name
}
