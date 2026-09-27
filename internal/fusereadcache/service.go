package fusereadcache

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"time"

	"litepan/internal/settings"
)

type Service struct {
	settings       *settings.Service
	store          *storeLayer
	mu             sync.Mutex
	usedBytes      int64
	usageKnown     bool
	lastMaintainAt time.Time
}

type Options struct {
	DataDir  string
	Settings *settings.Service
}

func New(ctx context.Context, opts Options) (*Service, error) {
	root := filepath.Join(opts.DataDir, SubdirName)
	if err := validateRoot(root); err != nil {
		return nil, err
	}
	st, err := openStore(ctx, root)
	if err != nil {
		return nil, err
	}
	return &Service{settings: opts.Settings, store: st}, nil
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	return s.store.Close()
}

func (s *Service) Enabled(ctx context.Context) bool {
	return LoadConfig(ctx, s.settings).Enabled
}

func (s *Service) Config(ctx context.Context) Config {
	return LoadConfig(ctx, s.settings)
}

type Stats struct {
	UsedBytes  int64  `json:"used_bytes"`
	LimitBytes int64  `json:"limit_bytes"`
	BlockCount int64  `json:"block_count"`
	RootPath   string `json:"root_path"`
}

func (s *Service) Stats(ctx context.Context) (Stats, error) {
	if s == nil {
		cfg := LoadConfig(ctx, nil)
		return Stats{LimitBytes: cfg.MaxBytes}, nil
	}
	cfg := LoadConfig(ctx, s.settings)
	if s.store == nil {
		return Stats{LimitBytes: cfg.MaxBytes}, nil
	}
	used, blocks, err := s.store.stats()
	if err != nil {
		return Stats{}, err
	}
	return Stats{
		UsedBytes:  used,
		LimitBytes: cfg.MaxBytes,
		BlockCount: blocks,
		RootPath:   s.store.rootPath(),
	}, nil
}

func (s *Service) ReadAt(ctx context.Context, accountID int64, fileID string, dest []byte, off int64, fetch func([]byte, int64) (int, error)) (int, error) {
	if len(dest) == 0 {
		return 0, nil
	}
	if s == nil || !s.Enabled(ctx) {
		return fetch(dest, off)
	}

	written := 0
	for written < len(dest) {
		curOff := off + int64(written)
		blockIdx := curOff / BlockSize
		blockOff := curOff % BlockSize
		need := int64(len(dest) - written)
		remainInBlock := BlockSize - blockOff
		if need > remainInBlock {
			need = remainInBlock
		}

		blockDest := dest[written : written+int(need)]
		s.mu.Lock()
		n, ok, err := s.store.loadBlockRange(accountID, fileID, blockIdx, blockOff, blockDest)
		s.mu.Unlock()
		if err != nil && err != io.EOF {
			ok = false
		}
		if ok {
			written += n
			if n < len(blockDest) {
				return written, nil
			}
			continue
		}

		buf := make([]byte, BlockSize)
		fetchOff := blockIdx * BlockSize
		n, err = fetch(buf, fetchOff)
		if n <= 0 {
			if written > 0 {
				return written, nil
			}
			return 0, err
		}
		chunk := buf[:n]
		if int64(len(chunk)) <= blockOff {
			if err == io.EOF {
				return written, nil
			}
			return written, err
		}
		avail := int64(len(chunk)) - blockOff
		if need > avail {
			need = avail
		}
		copied := copy(dest[written:], chunk[blockOff:blockOff+need])
		written += copied
		s.mu.Lock()
		_ = s.putBlockWithPolicy(ctx, accountID, fileID, blockIdx, chunk)
		s.mu.Unlock()
		if err == io.EOF || copied == 0 {
			return written, err
		}
	}
	return written, nil
}

func (s *Service) InvalidateFile(_ context.Context, accountID int64, fileID string) error {
	if s == nil || s.store == nil || accountID <= 0 || fileID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.store.invalidateFile(accountID, fileID)
	if err == nil {
		s.usageKnown = false
	}
	return err
}

func (s *Service) InvalidateAccount(_ context.Context, accountID int64) error {
	if s == nil || s.store == nil || accountID <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.store.invalidateAccount(accountID)
	if err == nil {
		s.usageKnown = false
	}
	return err
}

func (s *Service) ClearAll(_ context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.store.clearAll()
	if err == nil {
		s.usedBytes = 0
		s.usageKnown = true
		s.lastMaintainAt = time.Now()
	}
	return err
}

func (s *Service) UpdateSettings(ctx context.Context, patch map[string]string) error {
	if s == nil || s.settings == nil {
		return nil
	}
	return s.settings.Update(ctx, patch)
}
