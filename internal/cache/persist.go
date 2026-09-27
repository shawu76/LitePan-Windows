package cache

import (
	"time"

	"litepan/pkg/safego"
)

func (s *Service) ConfigurePersistence(enabled bool, dir string, interval time.Duration) {
	if interval < time.Minute {
		interval = time.Minute
	}

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	changed := s.persistDir != dir || s.persistInterval != interval
	s.persistDir = dir
	s.persistEnabled = enabled
	s.persistInterval = interval

	if !enabled {
		s.stopPersistenceLocked()
		return
	}
	if s.persistStop != nil {
		if !changed {
			return
		}
		s.stopPersistenceLocked()
	}
	stop := make(chan struct{})
	s.persistStop = stop
	go s.persistLoop(stop)
}

func (s *Service) stopPersistenceLocked() {
	if s.persistStop == nil {
		return
	}
	close(s.persistStop)
	s.persistStop = nil
}

func (s *Service) persistLoop(stop chan struct{}) {
	s.persistMu.Lock()
	interval := s.persistInterval
	s.persistMu.Unlock()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			// 兜住单轮崩溃：落盘出错只跳过这一轮，不能让整个服务下线。
			safego.Guard(s.log, "cache.persist", func() {
				s.persistMu.Lock()
				dir := s.persistDir
				enabled := s.persistEnabled
				s.persistMu.Unlock()
				if enabled && dir != "" {
					_ = s.SaveSnapshot(dir)
				}
			})
		case <-stop:
			return
		}
	}
}

func (s *Service) stopPersistence() {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.stopPersistenceLocked()
}
