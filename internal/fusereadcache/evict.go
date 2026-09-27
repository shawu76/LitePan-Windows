package fusereadcache

import (
	"context"
	"time"
)

const maintenanceInterval = 30 * time.Minute

func (s *Service) maintain(cfg Config) error {
	if s == nil || s.store == nil {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour).Unix()
	return s.store.expireBefore(cutoff)
}

func (s *Service) ensureSpace(cfg Config, incoming int64) error {
	if !s.usageKnown {
		used, _, err := s.store.stats()
		if err != nil {
			return err
		}
		s.usedBytes = used
		s.usageKnown = true
	}
	if s.usedBytes+incoming <= cfg.MaxBytes {
		return nil
	}
	for s.usedBytes+incoming > cfg.MaxBytes {
		meta, ok, err := s.pickEvict(cfg)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := s.store.deleteBlock(meta); err != nil {
			return err
		}
		s.usedBytes -= meta.ByteLen
		if s.usedBytes < 0 {
			s.usedBytes = 0
		}
	}
	return nil
}

func (s *Service) pickEvict(cfg Config) (blockMeta, bool, error) {
	if cfg.EvictionPolicy == PolicyLargeFile {
		return s.store.pickEvictLargeFile()
	}
	return s.store.pickEvictLRU()
}

func (s *Service) putBlockWithPolicy(ctx context.Context, accountID int64, fileID string, blockIdx int64, data []byte) error {
	cfg := LoadConfig(ctx, s.settings)
	if s.lastMaintainAt.IsZero() || time.Since(s.lastMaintainAt) >= maintenanceInterval {
		if err := s.maintain(cfg); err != nil {
			return err
		}
		s.usageKnown = false
		s.lastMaintainAt = time.Now()
	}
	if err := s.ensureSpace(cfg, int64(len(data))); err != nil {
		return err
	}
	delta, err := s.store.putBlock(accountID, fileID, blockIdx, data)
	if err != nil {
		s.usageKnown = false
		return err
	}
	s.usedBytes += delta
	return nil
}
